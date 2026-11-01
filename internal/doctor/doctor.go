// Package doctor diagnoses everything that commonly breaks an SSH VPN and
// says how to fix it.
package doctor

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"sshfortress/internal/config"
	"sshfortress/internal/firewall"
	"sshfortress/internal/netopt"
	"sshfortress/internal/procfs"
	"sshfortress/internal/sshd"
	"sshfortress/internal/state"
	"sshfortress/internal/store"
	"sshfortress/internal/sysx"
)

// Status of a check.
const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
	Skip = "skip"
)

// Check is one diagnostic result.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// Run executes all checks.
func Run(cfg *config.Config, db *store.DB) []Check {
	var out []Check
	add := func(name, status, detail, fix string) {
		out = append(out, Check{Name: name, Status: status, Detail: detail, Fix: fix})
	}

	if os.Geteuid() != 0 {
		add("Root", Warn, "not running as root: many checks are incomplete", "run: sudo sshfortress doctor")
	}

	// ── sshd ──────────────────────────────────────────────────────────────
	sm := sshd.New(cfg)
	bin := sm.Binary()
	if bin == "" {
		add("OpenSSH server", Fail, "sshd not found", "apt install openssh-server  (or dnf install openssh-server)")
		return out
	}
	v := sm.Version()
	add("OpenSSH server", OK, fmt.Sprintf("%s (%s)", orUnknown(v.Raw), bin), "")
	if outp, err := sysx.Run(bin, "-t", "-f", cfg.SSH.ConfigPath); err != nil {
		add("sshd config", Fail, firstLine(outp), "check "+cfg.SSH.ConfigPath+" then run: sshfortress apply")
	} else {
		add("sshd config", OK, "valid", "")
	}

	probe := ""
	if users, err := db.ListUsers(); err == nil && len(users) > 0 {
		probe = users[0].Username
	}
	if eff, err := sm.Effective(probe); err == nil {
		bad := []string{}
		want := map[string]string{"usedns": "no", "tcpkeepalive": "yes"}
		if probe != "" {
			want["passwordauthentication"] = "yes"
		}
		if !cfg.SSH.Compression {
			want["compression"] = "no"
		}
		for k, w := range want {
			if got := first(eff[k]); got != w {
				bad = append(bad, fmt.Sprintf("%s=%s (want %s)", k, got, w))
			}
		}
		if ci, _ := strconv.Atoi(first(eff["clientaliveinterval"])); ci == 0 {
			bad = append(bad, "clientaliveinterval=0 (dead phones keep their slot)")
		}
		if fw := first(eff["allowtcpforwarding"]); probe != "" && fw != "yes" && fw != "local" && fw != "all" {
			bad = append(bad, "allowtcpforwarding="+fw+" (tunnels can't work)")
		}
		if ms := first(eff["maxstartups"]); strings.HasPrefix(ms, "10:") {
			bad = append(bad, "maxstartups="+ms+" (refuses users when many reconnect at once)")
		}
		if len(bad) == 0 {
			detail := "tuned for VPN"
			if probe != "" {
				detail += fmt.Sprintf(" (checked as VPN user %q)", probe)
			}
			add("sshd settings", OK, detail, "")
		} else {
			add("sshd settings", Warn, strings.Join(bad, "; "), "run: sshfortress apply")
		}
	} else {
		add("sshd settings", Skip, err.Error(), "")
	}

	if sysx.HasSystemd() && sysx.UnitExists("ssh.socket") && (sysx.UnitActive("ssh.socket") || sysx.UnitEnabled("ssh.socket")) {
		add("ssh.socket", Warn, "socket activation is on: extra SSH ports are ignored", "run: sshfortress apply (switches to ssh.service)")
	}

	tcp, _ := procfs.ReadTCP()
	listening := func(p int) bool { return tcp != nil && tcp.ListenPorts[uint16(p)] }
	var missing []string
	for _, p := range cfg.SSH.Ports {
		if !listening(p) {
			missing = append(missing, strconv.Itoa(p))
		}
	}
	if len(missing) == 0 {
		add("SSH ports", OK, "listening on "+joinInts(cfg.SSH.Ports), "")
	} else {
		add("SSH ports", Fail, "not listening on "+strings.Join(missing, ", "), "run: sshfortress apply; check firewall/other services on these ports")
	}

	// ── daemon / components ────────────────────────────────────────────────
	st, stErr := state.Read()
	daemonOK := stErr == nil && st.Fresh()
	if sysx.HasSystemd() && !sysx.UnitActive("sshfortress") {
		add("Daemon", Fail, "sshfortress service is not running: no limits, quotas or UDP gateway", "systemctl enable --now sshfortress")
	} else if !daemonOK {
		add("Daemon", Warn, "no fresh state from the daemon", "journalctl -u sshfortress -n 50")
	} else {
		add("Daemon", OK, fmt.Sprintf("up since %s, %d online", st.StartedAt.Format("2006-01-02 15:04"), len(st.Sessions)), "")
		for _, w := range lastN(st.Warnings, 3) {
			add("Daemon warning", Warn, w, "")
		}
	}

	if cfg.UDPGW.Enabled {
		var up, down []string
		for _, p := range cfg.UDPGW.Ports {
			c, err := net.DialTimeout("tcp", net.JoinHostPort(cfg.UDPGW.Listen, strconv.Itoa(p)), 2*time.Second)
			if err == nil {
				c.Close()
				up = append(up, strconv.Itoa(p))
			} else {
				down = append(down, strconv.Itoa(p))
			}
		}
		switch {
		case len(down) == 0:
			add("UDP gateway (calls)", OK, fmt.Sprintf("%s mode, ports %s", cfg.UDPGW.Mode, strings.Join(up, ", ")), "")
		case len(up) > 0:
			add("UDP gateway (calls)", Warn, "down on "+strings.Join(down, ", "), "journalctl -u sshfortress -n 50")
		default:
			add("UDP gateway (calls)", Fail, "not reachable: voice/video calls and games won't work", "systemctl restart sshfortress")
		}
	} else {
		add("UDP gateway (calls)", Warn, "disabled: UDP (calls, games, QUIC) won't work through SSH", "set udpgw.enabled: true and run: sshfortress apply")
	}

	if cfg.Gateway.Enabled {
		if daemonOK && st.Gateway != nil {
			if len(st.Gateway.Failed) > 0 {
				var f []string
				for p, e := range st.Gateway.Failed {
					f = append(f, p+": "+shorten(e))
				}
				add("Gateway", Warn, "some ports failed — "+strings.Join(f, "; "), "free those ports or change gateway.ports")
			} else {
				add("Gateway", OK, "SSH/WebSocket/TLS on "+joinInts(st.Gateway.Ports), "")
			}
		} else {
			add("Gateway", Warn, "enabled but no state from the daemon", "systemctl restart sshfortress")
		}
	} else {
		add("Gateway", Skip, "disabled (WebSocket/CDN/TLS access is off)", "sshfortress gateway enable --ports 80,443")
	}

	// ── kernel ─────────────────────────────────────────────────────────────
	if netopt.BBRActive() {
		q, _ := netopt.Read("net.core.default_qdisc")
		add("BBR", OK, "active, qdisc "+q, "")
	} else {
		cc, _ := netopt.Read("net.ipv4.tcp_congestion_control")
		add("BBR", Warn, "congestion control is "+orUnknown(cc), "run: sshfortress optimize")
	}
	a, t := netopt.Score(netopt.Status(cfg))
	if t > 0 && a*100/t < 80 {
		add("Network profile", Warn, fmt.Sprintf("%d/%d settings applied", a, t), "run: sshfortress optimize")
	} else if t > 0 {
		add("Network profile", OK, fmt.Sprintf("%d/%d settings applied", a, t), "")
	}

	if cfg.Firewall.Enabled {
		if firewall.Active() {
			add("Firewall", OK, "VPN users can't reach local services or send spam", "")
		} else {
			add("Firewall", Warn, "egress rules not installed", "apt install iptables; systemctl restart sshfortress")
		}
	}

	// ── resources ──────────────────────────────────────────────────────────
	if ram, swap := memInfo(); ram > 0 {
		detail := fmt.Sprintf("RAM %d MB, swap %d MB", ram>>20, swap>>20)
		if ram < 900<<20 && swap == 0 {
			add("Memory", Warn, detail+" — each SSH session uses ~5 MB; small servers can run out", "add a 1-2 GB swap file")
		} else {
			add("Memory", OK, detail, "")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := net.DefaultResolver.LookupHost(ctx, "telegram.org"); err != nil {
		add("DNS", Fail, "cannot resolve telegram.org: "+shorten(err.Error()), "check /etc/resolv.conf")
	} else if el := time.Since(start); el > 500*time.Millisecond {
		add("DNS", Warn, fmt.Sprintf("slow lookups (%s) slow down every new site", el.Round(time.Millisecond)), "use a faster resolver, e.g. 1.1.1.1")
	} else {
		add("DNS", OK, fmt.Sprintf("resolves in %s", time.Since(start).Round(time.Millisecond)), "")
	}

	// ── users ──────────────────────────────────────────────────────────────
	if users, err := db.ListUsers(); err == nil {
		now := time.Now()
		expiredActive := 0
		for _, u := range users {
			if u.IsExpired(now) && !u.Disabled {
				expiredActive++
			}
		}
		if expiredActive > 0 && !daemonOK {
			add("Users", Warn, fmt.Sprintf("%d expired users still enabled (daemon not running)", expiredActive), "systemctl enable --now sshfortress")
		} else {
			add("Users", OK, fmt.Sprintf("%d managed users", len(users)), "")
		}
	}
	return out
}

// Summary counts statuses.
func Summary(cs []Check) (ok, warn, fail int) {
	for _, c := range cs {
		switch c.Status {
		case OK:
			ok++
		case Warn:
			warn++
		case Fail:
			fail++
		}
	}
	return
}

func memInfo() (ram, swap uint64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			ram = n * 1024
		case "SwapTotal:":
			swap = n * 1024
		}
	}
	return
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func shorten(s string) string {
	if len(s) > 90 {
		return s[:87] + "..."
	}
	return s
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

func lastN(v []string, n int) []string {
	if len(v) > n {
		return v[len(v)-n:]
	}
	return v
}
