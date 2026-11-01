// Package sshd manages OpenSSH server settings for VPN use.
//
// Settings live in one managed drop-in, /etc/ssh/sshd_config.d/00-sshvpn.conf.
// sshd uses the FIRST value it sees for each option, and the "00-" prefix
// makes ours win over e.g. cloud-init's 50-cloud-init.conf (which sets
// PasswordAuthentication no on most VPS images and silently breaks VPN
// logins). On systems without an Include line, managed blocks are written
// into sshd_config itself.
//
// Every change is validated with `sshd -t`; if sshd rejects it, simpler
// variants are tried for older OpenSSH versions, and if all fail the old
// files are restored — sshd is never left unable to start.
package sshd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"sshfortress/internal/config"
	"sshfortress/internal/sysx"
)

const (
	dropInName  = "00-sshvpn.conf"
	bannerPath  = "/etc/sshfortress/banner.txt"
	markBegin   = "# >>> SSH-VPN managed (%s) — do not edit >>>"
	markEnd     = "# <<< SSH-VPN managed (%s) <<<"
	portComment = "#sshvpn# "
)

// Manager applies sshd settings.
type Manager struct {
	cfg *config.Config
	// NoSystemd skips service operations (tests, containers).
	NoSystemd bool
}

// New returns a manager.
func New(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg, NoSystemd: !sysx.HasSystemd()}
}

// Binary returns the sshd path.
func (m *Manager) Binary() string {
	if m.cfg.SSH.SSHDPath != "" {
		return m.cfg.SSH.SSHDPath
	}
	return sysx.Which("sshd")
}

// Version is an OpenSSH version.
type Version struct {
	Major, Minor int
	Raw          string
}

// AtLeast reports whether v >= major.minor (unknown versions count as new).
func (v Version) AtLeast(major, minor int) bool {
	if v.Major == 0 {
		return true
	}
	return v.Major > major || (v.Major == major && v.Minor >= minor)
}

var verRe = regexp.MustCompile(`OpenSSH_(\d+)\.(\d+)[A-Za-z0-9.]*`)

// Version detects the OpenSSH version. `sshd -V` prints it on new versions;
// older ones print it in the usage message for the unknown flag.
func (m *Manager) Version() Version {
	bin := m.Binary()
	if bin == "" {
		return Version{}
	}
	out, _ := sysx.Run(bin, "-V")
	if !verRe.MatchString(out) {
		if ssh := sysx.Which("ssh"); ssh != "" {
			out, _ = sysx.Run(ssh, "-V")
		}
	}
	mm := verRe.FindStringSubmatch(out)
	if mm == nil {
		return Version{}
	}
	maj, _ := strconv.Atoi(mm[1])
	min, _ := strconv.Atoi(mm[2])
	return Version{Major: maj, Minor: min, Raw: mm[0]}
}

// ServiceName returns the systemd unit of the SSH daemon.
func (m *Manager) ServiceName() string {
	if sysx.UnitExists("ssh.service") {
		return "ssh"
	}
	return "sshd"
}

var includeRe = regexp.MustCompile(`(?mi)^\s*Include\s+(\S*sshd_config\.d\S*)`)

// dropInDir returns the directory sshd includes, if any.
func dropInDir(mainConfig string) (string, bool) {
	data, err := os.ReadFile(mainConfig)
	if err != nil {
		return "", false
	}
	mm := includeRe.FindStringSubmatch(string(data))
	if mm == nil {
		return "", false
	}
	pattern := mm[1]
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(filepath.Dir(mainConfig), pattern)
	}
	return filepath.Dir(pattern), true
}

// variant levels: 0 = full, 1 = compatible, 2 = minimal.
func (m *Manager) render(v Version, level int) (global, match []string) {
	c := m.cfg.SSH
	ports := append([]int(nil), c.Ports...)
	sort.Ints(ports)
	for _, p := range ports {
		global = append(global, fmt.Sprintf("Port %d", p))
	}
	if c.PasswordAuth {
		global = append(global, "PasswordAuthentication yes")
	}
	if c.Tuning && level < 2 {
		global = append(global,
			"# Faster logins: no reverse-DNS lookup of every client",
			"UseDNS no",
			"# Detect dead phones fast so stale sessions don't eat connection slots",
			"TCPKeepAlive yes",
			fmt.Sprintf("ClientAliveInterval %d", c.ClientAliveInterval),
			fmt.Sprintf("ClientAliveCountMax %d", c.ClientAliveCountMax),
			fmt.Sprintf("LoginGraceTime %d", c.LoginGraceTime),
			"# Many users reconnecting at once must not be refused (default 10:30:100)",
			fmt.Sprintf("MaxStartups %s", c.MaxStartups),
			fmt.Sprintf("MaxAuthTries %d", c.MaxAuthTries),
		)
		if !c.Compression {
			global = append(global,
				"# HTTPS/video/calls are already compressed: zlib only adds CPU and latency",
				"Compression no")
		}
		if level == 0 && v.AtLeast(9, 8) {
			global = append(global,
				"# Mobile users share carrier-NAT IPs; one typo must not lock out a whole operator",
				"PerSourcePenalties authfail:1s noauth:1s grace-exceeded:5s min:60s max:5m")
		}
	}

	group := m.cfg.UserDefaults.Group
	match = append(match, "Match Group "+group,
		"    PasswordAuthentication yes",
		"    AllowTcpForwarding "+map[bool]string{true: "local", false: "yes"}[level < 2],
		"    X11Forwarding no",
		"    AllowAgentForwarding no",
		"    PermitTunnel no",
		"    GatewayPorts no",
	)
	if level == 0 {
		match = append(match,
			"    KbdInteractiveAuthentication yes",
			"    AuthenticationMethods any",
			"    AllowStreamLocalForwarding no")
		if v.AtLeast(7, 8) {
			match = append(match, "    PermitListen none")
		}
	}
	if strings.TrimSpace(c.Banner) != "" {
		match = append(match, "    Banner "+bannerPath)
	}
	return global, match
}

func header() []string {
	return []string{
		"# Managed by SSH-VPN (sshfortress) — https://github.com/SandBaad/SSH-VPN",
		"# Edit /etc/sshfortress/config.yaml and run `sshfortress apply` instead.",
		"",
	}
}

// Result describes what Apply did.
type Result struct {
	Mode       string   `json:"mode"` // drop-in or inline
	File       string   `json:"file"`
	Level      int      `json:"level"`
	Changed    bool     `json:"changed"`
	Restarted  bool     `json:"restarted"`
	SocketFix  bool     `json:"socket_activation_disabled"`
	Warnings   []string `json:"warnings,omitempty"`
	SSHVersion string   `json:"ssh_version"`
}

// Apply writes the managed configuration, validates it and reloads sshd.
func (m *Manager) Apply() (*Result, error) {
	if err := sysx.RequireRoot(); err != nil {
		return nil, err
	}
	bin := m.Binary()
	if bin == "" {
		return nil, fmt.Errorf("sshd not found — install openssh-server first")
	}
	mainPath := m.cfg.SSH.ConfigPath
	mainOrig, err := os.ReadFile(mainPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", mainPath, err)
	}
	// One-time pristine backup for uninstall.
	if bk := mainPath + ".sshvpn-orig"; !sysx.FileExists(bk) {
		_ = os.WriteFile(bk, mainOrig, 0o600)
	}
	v := m.Version()
	res := &Result{SSHVersion: v.Raw}

	if err := m.writeBanner(); err != nil {
		res.Warnings = append(res.Warnings, "banner: "+err.Error())
	}

	dir, hasInclude := dropInDir(mainPath)
	dropIn := filepath.Join(dir, dropInName)
	var dropOrig []byte
	dropExisted := false
	if hasInclude {
		dropOrig, err = os.ReadFile(dropIn)
		dropExisted = err == nil
	}
	rollback := func() {
		_ = os.WriteFile(mainPath, mainOrig, 0o644)
		if hasInclude {
			if dropExisted {
				_ = os.WriteFile(dropIn, dropOrig, 0o644)
			} else {
				_ = os.Remove(dropIn)
			}
		}
	}

	var lastErr error
	for level := 0; level <= 2; level++ {
		global, match := m.render(v, level)
		var newMain []byte
		if hasInclude {
			res.Mode, res.File = "drop-in", dropIn
			body := strings.Join(append(append(append(header(), global...), ""), match...), "\n") + "\n"
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
			if err := sysx.WriteFileAtomic(dropIn, []byte(body), 0o644); err != nil {
				return nil, err
			}
			newMain = commentPorts(stripBlocks(mainOrig))
		} else {
			res.Mode, res.File = "inline", mainPath
			newMain = withInlineBlocks(commentPorts(stripBlocks(mainOrig)), global, match)
		}
		if err := sysx.WriteFileAtomic(mainPath, newMain, 0o644); err != nil {
			rollback()
			return nil, err
		}
		if out, err := sysx.Run(bin, "-t", "-f", mainPath); err != nil {
			lastErr = fmt.Errorf("sshd rejected the config: %s", out)
			continue
		}
		res.Level = level
		if level > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("old OpenSSH (%s): used compatibility level %d", v.Raw, level))
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		rollback()
		return nil, lastErr
	}
	nowMain, _ := os.ReadFile(mainPath)
	nowDrop, _ := os.ReadFile(dropIn)
	res.Changed = string(nowMain) != string(mainOrig) || (hasInclude && string(nowDrop) != string(dropOrig))

	if m.NoSystemd {
		return res, nil
	}
	svc := m.ServiceName()
	needRestart := false

	// Ubuntu 22.10+ starts sshd through ssh.socket, which ignores Port
	// lines in sshd_config. Switch to the classic always-running daemon.
	if sysx.UnitExists("ssh.socket") && (sysx.UnitEnabled("ssh.socket") || sysx.UnitActive("ssh.socket")) {
		_ = sysx.Systemctl("disable", "--now", "ssh.socket")
		_ = sysx.Systemctl("enable", svc+".service")
		res.SocketFix = true
		needRestart = true
	}
	// Each tunnel can hold many forwarded sockets: raise the fd limit.
	limits := "[Service]\nLimitNOFILE=1048576\n"
	if changed, err := sysx.WriteIfChanged(fmt.Sprintf("/etc/systemd/system/%s.service.d/sshvpn.conf", svc), []byte(limits), 0o644); err == nil && changed {
		needRestart = true
	}
	if needRestart {
		_ = sysx.Systemctl("daemon-reload")
		if err := sysx.Systemctl("restart", svc); err != nil {
			return res, fmt.Errorf("restart %s: %w", svc, err)
		}
		res.Restarted = true
	} else if res.Changed {
		// reload = SIGHUP: sshd re-execs with the new config and rebinds
		// ports; existing sessions are untouched.
		if err := sysx.Systemctl("reload-or-restart", svc); err != nil {
			return res, fmt.Errorf("reload %s: %w", svc, err)
		}
		res.Restarted = true
	}
	return res, nil
}

func (m *Manager) writeBanner() error {
	text := strings.TrimSpace(m.cfg.SSH.Banner)
	if text == "" {
		return nil
	}
	_, err := sysx.WriteIfChanged(bannerPath, []byte(text+"\n"), 0o644)
	return err
}

// Remove deletes everything Apply added (used by uninstall).
func (m *Manager) Remove() error {
	mainPath := m.cfg.SSH.ConfigPath
	data, err := os.ReadFile(mainPath)
	if err != nil {
		return err
	}
	if dir, ok := dropInDir(mainPath); ok {
		_ = os.Remove(filepath.Join(dir, dropInName))
	}
	restored := uncommentPorts(stripBlocks(data))
	if err := sysx.WriteFileAtomic(mainPath, restored, 0o644); err != nil {
		return err
	}
	if bin := m.Binary(); bin != "" {
		if out, err := sysx.Run(bin, "-t", "-f", mainPath); err != nil {
			_ = os.WriteFile(mainPath, data, 0o644)
			return fmt.Errorf("sshd rejected restored config, kept current: %s", out)
		}
	}
	if !m.NoSystemd {
		_ = sysx.Systemctl("reload-or-restart", m.ServiceName())
	}
	return nil
}

// Effective returns `sshd -T` output as key → values, evaluated for a
// connection by user (so the Match Group block is included when user is a
// VPN user).
func (m *Manager) Effective(user string) (map[string][]string, error) {
	bin := m.Binary()
	if bin == "" {
		return nil, fmt.Errorf("sshd not found")
	}
	args := []string{"-T", "-f", m.cfg.SSH.ConfigPath}
	if user != "" {
		args = append(args, "-C", "user="+user+",host=probe,addr=192.0.2.1")
	}
	out, err := sysx.Run(bin, args...)
	if err != nil {
		return nil, err
	}
	res := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok {
			res[strings.ToLower(k)] = append(res[strings.ToLower(k)], v)
		}
	}
	return res, nil
}

// ConfiguredPorts returns the ports sshd will listen on, per `sshd -T`.
func (m *Manager) ConfiguredPorts() []int {
	eff, err := m.Effective("")
	if err != nil {
		return append([]int(nil), m.cfg.SSH.Ports...)
	}
	var ports []int
	for _, p := range eff["port"] {
		if n, err := strconv.Atoi(p); err == nil {
			ports = append(ports, n)
		}
	}
	return ports
}

// ─── text helpers ───────────────────────────────────────────────────────────

var portLineRe = regexp.MustCompile(`(?m)^[ \t]*Port[ \t]+\d+[ \t]*$`)

// commentPorts disables Port lines in the main config (all ports now come
// from our managed settings; duplicates would make sshd log bind errors).
func commentPorts(b []byte) []byte {
	return portLineRe.ReplaceAllFunc(b, func(line []byte) []byte {
		return append([]byte(portComment), line...)
	})
}

var commentedPortRe = regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(portComment))

func uncommentPorts(b []byte) []byte {
	return commentedPortRe.ReplaceAll(b, nil)
}

// stripBlocks removes our inline managed blocks.
func stripBlocks(b []byte) []byte {
	s := string(b)
	for _, name := range []string{"global", "match"} {
		begin := fmt.Sprintf(markBegin, name)
		end := fmt.Sprintf(markEnd, name)
		for {
			i := strings.Index(s, begin)
			if i < 0 {
				break
			}
			j := strings.Index(s[i:], end)
			if j < 0 {
				break
			}
			k := i + j + len(end)
			if k < len(s) && s[k] == '\n' {
				k++
			}
			s = s[:i] + s[k:]
		}
	}
	return []byte(s)
}

// withInlineBlocks puts global settings at the very top (first value wins)
// and the Match block at the very end (Match runs to end of file).
func withInlineBlocks(b []byte, global, match []string) []byte {
	var out strings.Builder
	out.WriteString(fmt.Sprintf(markBegin, "global") + "\n")
	for _, l := range append(header(), global...) {
		out.WriteString(l + "\n")
	}
	out.WriteString(fmt.Sprintf(markEnd, "global") + "\n")
	body := string(b)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	out.WriteString(body)
	out.WriteString(fmt.Sprintf(markBegin, "match") + "\n")
	for _, l := range match {
		out.WriteString(l + "\n")
	}
	out.WriteString(fmt.Sprintf(markEnd, "match") + "\n")
	return []byte(out.String())
}
