package sshd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sshfortress/internal/config"
)

func TestCommentAndRestorePorts(t *testing.T) {
	in := []byte("Port 22\n#Port 23\nListenAddress 0.0.0.0\n  Port 2222\n")
	out := commentPorts(in)
	if strings.Contains(string(out), "\nPort 22") || !strings.HasPrefix(string(out), portComment+"Port 22") {
		t.Fatalf("not commented: %q", out)
	}
	if got := string(uncommentPorts(out)); got != string(in) {
		t.Fatalf("restore mismatch:\n%q\n%q", got, in)
	}
}

func TestInlineBlocksRoundTrip(t *testing.T) {
	orig := []byte("PermitRootLogin yes\n")
	with := withInlineBlocks(orig, []string{"Port 22"}, []string{"Match Group sshvpn", "    PasswordAuthentication yes"})
	s := string(with)
	if !strings.HasPrefix(s, "# >>> SSH-VPN managed (global)") || !strings.Contains(s, "Match Group sshvpn") {
		t.Fatalf("blocks missing: %s", s)
	}
	if strings.Index(s, "Match Group") < strings.Index(s, "PermitRootLogin") {
		t.Fatal("Match block must come after the admin's settings")
	}
	if got := strings.TrimSpace(string(stripBlocks(with))); got != "PermitRootLogin yes" {
		t.Fatalf("strip left %q", got)
	}
}

func TestVersionCompare(t *testing.T) {
	v := Version{Major: 7, Minor: 4}
	if v.AtLeast(7, 8) || !v.AtLeast(7, 4) || !v.AtLeast(6, 9) {
		t.Fatal("AtLeast wrong")
	}
	if !(Version{}).AtLeast(99, 0) {
		t.Fatal("unknown version should count as new")
	}
}

// Validates generated configs with a real sshd when SSHD_BIN is set.
func TestApplyWithRealSSHD(t *testing.T) {
	bin := os.Getenv("SSHD_BIN")
	if bin == "" || os.Geteuid() != 0 {
		t.Skip("set SSHD_BIN and run as root to validate against a real sshd")
	}
	for _, withInclude := range []bool{true, false} {
		dir := t.TempDir()
		key := filepath.Join(dir, "host_ed25519")
		keygen := filepath.Join(filepath.Dir(filepath.Dir(bin)), "bin", "ssh-keygen")
		if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
			t.Fatalf("keygen: %v %s", err, out)
		}
		main := "HostKey " + key + "\nPort 22\nPidFile " + filepath.Join(dir, "pid") + "\n"
		if withInclude {
			os.MkdirAll(filepath.Join(dir, "sshd_config.d"), 0o755)
			// cloud-init style override that breaks password logins
			os.WriteFile(filepath.Join(dir, "sshd_config.d", "50-cloud-init.conf"), []byte("PasswordAuthentication no\n"), 0o644)
			main = "Include " + filepath.Join(dir, "sshd_config.d", "*.conf") + "\n" + main
		}
		mainPath := filepath.Join(dir, "sshd_config")
		os.WriteFile(mainPath, []byte(main), 0o644)

		cfg := config.Defaults()
		cfg.SSH.SSHDPath = bin
		cfg.SSH.ConfigPath = mainPath
		cfg.SSH.Ports = []int{22, 2222}
		cfg.SSH.Banner = "Welcome to SSH-VPN"
		m := &Manager{cfg: cfg, NoSystemd: true}
		res, err := m.Apply()
		if err != nil {
			t.Fatalf("include=%v: %v", withInclude, err)
		}
		if res.Level != 0 {
			t.Errorf("include=%v: expected full config on modern sshd, got level %d %v", withInclude, res.Level, res.Warnings)
		}
		eff, err := m.Effective("")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(eff["port"], ","); got != "22,2222" {
			t.Errorf("include=%v: ports = %s", withInclude, got)
		}
		if eff["compression"][0] != "no" || eff["clientaliveinterval"][0] != "20" || eff["maxstartups"][0] != "100:30:1000" {
			t.Errorf("include=%v: tuning not effective: %v %v %v", withInclude, eff["compression"], eff["clientaliveinterval"], eff["maxstartups"])
		}
		if err := m.Remove(); err != nil {
			t.Fatalf("remove: %v", err)
		}
		after, _ := os.ReadFile(mainPath)
		if string(after) != main {
			t.Errorf("include=%v: Remove didn't restore the original:\n%s", withInclude, after)
		}
	}
}
