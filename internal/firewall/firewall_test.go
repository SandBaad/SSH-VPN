package firewall

import (
	"strings"
	"testing"

	"sshfortress/internal/config"
)

func TestRules(t *testing.T) {
	cfg := config.Defaults()
	cfg.Firewall.AllowLocalPorts = []int{8443}
	var lines []string
	for _, r := range Rules(cfg) {
		lines = append(lines, strings.Join(r, " "))
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{
		"--dports 8443,7100,7200,7300 -j RETURN", // udpgw stays reachable
		"-o lo -p udp --dport 53 -j RETURN",      // DNS via local resolver
		"-o lo -j REJECT",                        // everything else local is blocked
		"--dport 25 -j REJECT",                   // no spam
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing rule %q in:\n%s", want, all)
		}
	}
	if !strings.HasSuffix(all, "-j RETURN") {
		t.Error("chain must end with RETURN")
	}
	if strings.Index(all, "7300 -j RETURN") > strings.Index(all, "-o lo -j REJECT") {
		t.Error("allow rules must come before the reject")
	}

	cfg.Firewall.BlockLocal, cfg.Firewall.BlockSMTP = false, false
	if n := len(Rules(cfg)); n != 1 {
		t.Errorf("expected only RETURN, got %d rules", n)
	}
}
