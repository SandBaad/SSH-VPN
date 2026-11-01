// Package netopt tunes the Linux network stack for SSH tunnelling.
//
// The profile focuses on what matters for a tunnel that mixes bulk
// downloads with real-time traffic (calls) over a long-RTT path:
//   - BBR + fq pacing: high throughput on lossy mobile/international paths.
//   - tcp_notsent_lowat: keeps the kernel send queue short, so a voice
//     packet isn't stuck behind megabytes of a download.
//   - tcp_slow_start_after_idle=0: an SSH tunnel is one long-lived TCP
//     connection with bursts; the default resets its speed after every pause.
//   - buffers sized from the server's RAM, larger port range and backlogs.
//
// Original values are saved before the first change, so Reset really
// restores the system instead of leaving half the settings behind.
package netopt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"sshfortress/internal/config"
	"sshfortress/internal/sysx"
)

// Paths (variables so tests can redirect them).
var (
	ProcSys     = "/proc/sys"
	SysctlFile  = "/etc/sysctl.d/99-sshfortress.conf"
	ModulesFile = "/etc/modules-load.d/sshfortress.conf"
	LimitsFile  = "/etc/security/limits.d/99-sshfortress.conf"
)

// Param is one kernel setting.
type Param struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Current   string `json:"current"`
	Desc      string `json:"desc"`
	Applied   bool   `json:"applied"`
	Supported bool   `json:"supported"`
}

func memTotalBytes() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				kb, _ := strconv.ParseUint(f[1], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}

// bufferMax picks socket buffer limits from RAM: big enough for a
// 100+ Mbit/s link across continents, small enough for a 512 MB VPS.
func bufferMax() int {
	ram := memTotalBytes()
	switch {
	case ram >= 4<<30:
		return 64 << 20
	case ram >= 1<<30:
		return 32 << 20
	default:
		return 16 << 20
	}
}

// Profile returns the recommended parameters (without current values).
func Profile(cfg *config.Config) []Param {
	bmax := strconv.Itoa(bufferMax())
	cc := cfg.Network.CongestionControl
	if cc == "" {
		cc = "bbr"
	}
	p := []Param{
		{Key: "net.core.default_qdisc", Value: "fq", Desc: "Fair-queue pacing (pairs with BBR)"},
		{Key: "net.ipv4.tcp_congestion_control", Value: cc, Desc: "Congestion control"},
		{Key: "net.ipv4.tcp_notsent_lowat", Value: "16384", Desc: "Short send queue → low latency for calls"},
		{Key: "net.ipv4.tcp_slow_start_after_idle", Value: "0", Desc: "Keep tunnel speed after pauses"},
		{Key: "net.ipv4.tcp_mtu_probing", Value: "1", Desc: "Recover from broken path MTU"},
		{Key: "net.ipv4.tcp_fastopen", Value: "3", Desc: "TCP Fast Open (client + server)"},
		{Key: "net.ipv4.tcp_window_scaling", Value: "1", Desc: "Large TCP windows"},
		{Key: "net.ipv4.tcp_sack", Value: "1", Desc: "Selective ACKs"},
		{Key: "net.ipv4.tcp_timestamps", Value: "1", Desc: "RTT measurement"},
		{Key: "net.ipv4.tcp_no_metrics_save", Value: "1", Desc: "Don't reuse stale path metrics"},
		{Key: "net.core.rmem_max", Value: bmax, Desc: "Max receive buffer"},
		{Key: "net.core.wmem_max", Value: bmax, Desc: "Max send buffer"},
		{Key: "net.core.rmem_default", Value: "262144", Desc: "Default receive buffer (UDP flows)"},
		{Key: "net.core.wmem_default", Value: "262144", Desc: "Default send buffer (UDP flows)"},
		{Key: "net.ipv4.tcp_rmem", Value: "4096 131072 " + bmax, Desc: "TCP receive buffer min/default/max"},
		{Key: "net.ipv4.tcp_wmem", Value: "4096 65536 " + bmax, Desc: "TCP send buffer min/default/max"},
		{Key: "net.ipv4.udp_rmem_min", Value: "16384", Desc: "UDP receive buffer floor"},
		{Key: "net.ipv4.udp_wmem_min", Value: "16384", Desc: "UDP send buffer floor"},
		{Key: "net.core.netdev_max_backlog", Value: "16384", Desc: "Packet backlog per CPU"},
		{Key: "net.core.somaxconn", Value: "8192", Desc: "Listen backlog"},
		{Key: "net.ipv4.tcp_max_syn_backlog", Value: "8192", Desc: "SYN backlog"},
		{Key: "net.ipv4.ip_local_port_range", Value: "10240 65535", Desc: "Ephemeral ports for tunnels/UDP flows"},
		{Key: "net.ipv4.tcp_tw_reuse", Value: "1", Desc: "Reuse TIME_WAIT sockets"},
		{Key: "net.ipv4.tcp_fin_timeout", Value: "15", Desc: "FIN timeout (s)"},
		{Key: "net.ipv4.tcp_keepalive_time", Value: "60", Desc: "Keepalive start (s)"},
		{Key: "net.ipv4.tcp_keepalive_intvl", Value: "10", Desc: "Keepalive interval (s)"},
		{Key: "net.ipv4.tcp_keepalive_probes", Value: "6", Desc: "Keepalive probes"},
		{Key: "net.ipv4.tcp_retries2", Value: "8", Desc: "Give up on dead peers in ~100s, not 15 min"},
		{Key: "net.ipv4.tcp_rfc1337", Value: "1", Desc: "TIME_WAIT assassination protection"},
		{Key: "fs.file-max", Value: "2097152", Desc: "System-wide open files"},
		{Key: "vm.swappiness", Value: "10", Desc: "Prefer RAM over swap"},
		{Key: "net.netfilter.nf_conntrack_max", Value: "262144", Desc: "Connection tracking table"},
	}
	keys := make([]string, 0, len(cfg.Network.CustomSysctls))
	for k := range cfg.Network.CustomSysctls {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p = append(p, Param{Key: k, Value: cfg.Network.CustomSysctls[k], Desc: "Custom"})
	}
	return p
}

func sysPath(key string) string {
	return filepath.Join(ProcSys, strings.ReplaceAll(key, ".", "/"))
}

// Read returns a sysctl value with whitespace normalized.
func Read(key string) (string, bool) {
	data, err := os.ReadFile(sysPath(key))
	if err != nil {
		return "", false
	}
	return strings.Join(strings.Fields(string(data)), " "), true
}

func write(key, val string) error {
	return os.WriteFile(sysPath(key), []byte(val+"\n"), 0o644)
}

func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

// Status returns the profile with current values.
func Status(cfg *config.Config) []Param {
	ps := Profile(cfg)
	for i := range ps {
		ps[i].Current, ps[i].Supported = Read(ps[i].Key)
		ps[i].Applied = ps[i].Supported && norm(ps[i].Current) == norm(ps[i].Value)
	}
	return ps
}

// Score returns applied/total over supported parameters.
func Score(ps []Param) (applied, total int) {
	for _, p := range ps {
		if !p.Supported {
			continue
		}
		total++
		if p.Applied {
			applied++
		}
	}
	return
}

// BBRActive reports whether BBR is the active congestion control.
func BBRActive() bool {
	v, _ := Read("net.ipv4.tcp_congestion_control")
	return v == "bbr" || v == "bbr2"
}

func ccAvailable(cc string) bool {
	v, _ := Read("net.ipv4.tcp_available_congestion_control")
	for _, a := range strings.Fields(v) {
		if a == cc {
			return true
		}
	}
	return false
}

// Result summarizes Apply.
type Result struct {
	Applied  int      `json:"applied"`
	Total    int      `json:"total"`
	Skipped  []string `json:"skipped,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func backupPath(cfg *config.Config) string {
	return filepath.Join(cfg.DataDir, "sysctl-backup.json")
}

// Apply applies the profile now and persists it for reboots.
func Apply(cfg *config.Config) (*Result, error) {
	if err := sysx.RequireRoot(); err != nil {
		return nil, err
	}
	res := &Result{}
	ps := Profile(cfg)

	// BBR needs the tcp_bbr module on many kernels.
	for i := range ps {
		if ps[i].Key != "net.ipv4.tcp_congestion_control" {
			continue
		}
		cc := ps[i].Value
		if !ccAvailable(cc) {
			_, _ = sysx.Run("modprobe", "tcp_"+cc)
		}
		if !ccAvailable(cc) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s is not available in this kernel — kept cubic", cc))
			ps[i].Value = "cubic"
		} else if strings.HasPrefix(cc, "bbr") {
			_, _ = sysx.WriteIfChanged(ModulesFile, []byte("# SSH-VPN: load BBR at boot\ntcp_"+cc+"\n"), 0o644)
		}
	}

	// Save originals once, so Reset can restore them.
	bk := backupPath(cfg)
	if !sysx.FileExists(bk) {
		orig := map[string]string{}
		for _, p := range ps {
			if v, ok := Read(p.Key); ok {
				orig[p.Key] = v
			}
		}
		if data, err := json.MarshalIndent(orig, "", "  "); err == nil {
			_ = os.MkdirAll(filepath.Dir(bk), 0o750)
			_ = os.WriteFile(bk, data, 0o600)
		}
	}

	var lines []string
	lines = append(lines, "# SSH-VPN network profile — generated by `sshfortress optimize`.",
		"# Remove with `sshfortress optimize reset`.", "")
	for _, p := range ps {
		cur, ok := Read(p.Key)
		if !ok {
			res.Skipped = append(res.Skipped, p.Key)
			continue
		}
		res.Total++
		lines = append(lines, "# "+p.Desc, p.Key+" = "+p.Value)
		if norm(cur) == norm(p.Value) {
			res.Applied++
			continue
		}
		err := write(p.Key, p.Value)
		if err != nil && p.Key == "net.core.default_qdisc" {
			_, _ = sysx.Run("modprobe", "sch_"+p.Value)
			err = write(p.Key, p.Value)
		}
		if err != nil {
			if p.Key == "net.core.default_qdisc" {
				res.Warnings = append(res.Warnings, "the "+p.Value+" qdisc isn't available in this kernel (BBR still works without it)")
			} else {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v", p.Key, err))
			}
			continue
		}
		res.Applied++
	}
	if _, err := sysx.WriteIfChanged(SysctlFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		res.Warnings = append(res.Warnings, "persist: "+err.Error())
	}
	limits := "# SSH-VPN: many tunnels need many open files\n* soft nofile 1048576\n* hard nofile 1048576\nroot soft nofile 1048576\nroot hard nofile 1048576\n"
	if _, err := sysx.WriteIfChanged(LimitsFile, []byte(limits), 0o644); err != nil {
		res.Warnings = append(res.Warnings, "limits: "+err.Error())
	}
	return res, nil
}

// Reset removes the profile and restores the values saved before the
// first Apply.
func Reset(cfg *config.Config) error {
	if err := sysx.RequireRoot(); err != nil {
		return err
	}
	_ = os.Remove(SysctlFile)
	_ = os.Remove(ModulesFile)
	_ = os.Remove(LimitsFile)
	bk := backupPath(cfg)
	if data, err := os.ReadFile(bk); err == nil {
		var orig map[string]string
		if json.Unmarshal(data, &orig) == nil {
			for k, v := range orig {
				_ = write(k, v)
			}
		}
		_ = os.Remove(bk)
	}
	// Re-apply the distro's own sysctl files. Best effort: some keys are
	// read-only in containers, and the originals were already restored.
	_, _ = sysx.Run("sysctl", "--system")
	return nil
}
