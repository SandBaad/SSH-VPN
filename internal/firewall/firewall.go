// Package firewall adds egress rules for VPN users using iptables' owner
// match. With OpenSSH privilege separation every forwarded connection is
// opened by an sshd process running as the VPN user, so rules on the
// user's group apply exactly to tunnel traffic — nothing else on the
// server is touched.
//
// Rules (chain SSHVPN, jumped to from OUTPUT for the VPN group):
//   - block_local: VPN users can't reach services on this server
//     (databases, admin panels on 127.0.0.1, the server's own public IP),
//     except the udpgw ports and DNS.
//   - block_smtp: no outbound port 25 — spam from a VPN user is the
//     fastest way to get a VPS suspended or its IP blacklisted.
package firewall

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"sshfortress/internal/config"
	"sshfortress/internal/sysx"
)

const chain = "SSHVPN"

// GroupID looks up a group's gid in /etc/group.
func GroupID(name string) (int, error) {
	data, err := os.ReadFile("/etc/group")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 3 && f[0] == name {
			return strconv.Atoi(f[2])
		}
	}
	return 0, fmt.Errorf("group %q not found", name)
}

type tool struct {
	bin      string
	loopback string
}

func tools() []tool {
	var out []tool
	if b := sysx.Which("iptables"); b != "" {
		out = append(out, tool{b, "127.0.0.0/8"})
	}
	if b := sysx.Which("ip6tables"); b != "" {
		out = append(out, tool{b, "::1/128"})
	}
	return out
}

func (t tool) run(args ...string) error {
	_, err := sysx.Run(t.bin, append([]string{"-w", "5"}, args...)...)
	return err
}

// Rules returns the rule specs (without "-A SSHVPN") for the chain.
func Rules(cfg *config.Config) [][]string {
	var rules [][]string
	fw := cfg.Firewall
	if fw.BlockLocal {
		allowed := append([]int{}, fw.AllowLocalPorts...)
		if cfg.UDPGW.Enabled {
			allowed = append(allowed, cfg.UDPGW.Ports...)
		}
		// multiport accepts at most 15 ports per rule
		for i := 0; i < len(allowed); i += 15 {
			end := min(i+15, len(allowed))
			ps := make([]string, 0, end-i)
			for _, p := range allowed[i:end] {
				ps = append(ps, strconv.Itoa(p))
			}
			rules = append(rules, []string{"-o", "lo", "-p", "tcp", "-m", "multiport", "--dports", strings.Join(ps, ","), "-j", "RETURN"})
		}
		rules = append(rules,
			[]string{"-o", "lo", "-p", "udp", "--dport", "53", "-j", "RETURN"},
			[]string{"-o", "lo", "-p", "tcp", "--dport", "53", "-j", "RETURN"},
			[]string{"-o", "lo", "-j", "REJECT"},
		)
	}
	if fw.BlockSMTP {
		rules = append(rules, []string{"-p", "tcp", "--dport", "25", "-j", "REJECT"})
	}
	return append(rules, []string{"-j", "RETURN"})
}

// Apply (re)builds the chain. Idempotent; safe to call on every start.
func Apply(cfg *config.Config) error {
	if !cfg.Firewall.Enabled {
		return Remove()
	}
	ts := tools()
	if len(ts) == 0 {
		return fmt.Errorf("iptables not found — install the iptables package")
	}
	gid, err := GroupID(cfg.UserDefaults.Group)
	if err != nil {
		return err
	}
	jump := []string{"OUTPUT", "-m", "owner", "--gid-owner", strconv.Itoa(gid), "-j", chain}
	var errs []string
	for _, t := range ts {
		_ = t.run("-N", chain) // exists already → error, fine
		if err := t.run("-F", chain); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", t.bin, err))
			continue
		}
		for _, r := range Rules(cfg) {
			if err := t.run(append([]string{"-A", chain}, r...)...); err != nil {
				errs = append(errs, fmt.Sprintf("%s %s: %v", t.bin, strings.Join(r, " "), err))
			}
		}
		if t.run(append([]string{"-C"}, jump...)...) != nil {
			if err := t.run(append([]string{"-I"}, jump...)...); err != nil {
				errs = append(errs, fmt.Sprintf("%s jump: %v", t.bin, err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("firewall: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Remove deletes the chain and every jump to it.
func Remove() error {
	for _, t := range tools() {
		out, err := sysx.Run(t.bin, "-w", "5", "-S", "OUTPUT")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "-A OUTPUT ") && strings.HasSuffix(line, "-j "+chain) {
				spec := strings.Fields(strings.TrimPrefix(line, "-A OUTPUT "))
				_ = t.run(append([]string{"-D", "OUTPUT"}, spec...)...)
			}
		}
		_ = t.run("-F", chain)
		_ = t.run("-X", chain)
	}
	return nil
}

// Active reports whether the jump rule is installed (IPv4).
func Active() bool {
	b := sysx.Which("iptables")
	if b == "" {
		return false
	}
	out, err := sysx.Run(b, "-w", "5", "-S", "OUTPUT")
	return err == nil && strings.Contains(out, "-j "+chain)
}
