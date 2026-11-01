package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.DataDir == "" {
		t.Error("DataDir should not be empty")
	}
	if len(cfg.SSH.Ports) == 0 || cfg.SSH.Ports[0] != 22 {
		t.Errorf("SSH.Ports = %v, want [22]", cfg.SSH.Ports)
	}
	if cfg.UDPGW.MaxConnectionsPerClient < 256 {
		t.Errorf("udpgw flows per client = %d; values this low drop call streams", cfg.UDPGW.MaxConnectionsPerClient)
	}
	if cfg.UserDefaults.DefaultMaxConnections != 2 {
		t.Errorf("DefaultMaxConnections = %d, want 2", cfg.UserDefaults.DefaultMaxConnections)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("defaults must validate: %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if cfg.SSH.ConfigPath != "/etc/ssh/sshd_config" {
		t.Errorf("SSH.ConfigPath = %s", cfg.SSH.ConfigPath)
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yaml")

	cfg := Defaults()
	cfg.SSH.Ports = []int{22, 2222}
	cfg.Gateway.Enabled = true
	cfg.Gateway.Ports = []int{80, 443}
	cfg.DataDir = tmpDir
	cfg.LogDir = tmpDir

	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded.SSH.Ports) != 2 || len(loaded.Gateway.Ports) != 2 || !loaded.Gateway.Enabled {
		t.Errorf("round trip mismatch: %+v %+v", loaded.SSH, loaded.Gateway)
	}
	if loaded.Path() != path {
		t.Errorf("Path() = %s", loaded.Path())
	}
}

// A v1 config file must keep loading after the upgrade.
func TestLoadV1Config(t *testing.T) {
	v1 := `
data_dir: /var/lib/sshfortress
ssh:
  ports: [22, 443]
  config_path: /etc/ssh/sshd_config
  max_auth_tries: 3
  password_auth: true
badvpn:
  enabled: true
  binary_path: /usr/local/bin/badvpn-udpgw
  listen_addr: "127.0.0.1:7300"
  max_clients: 1000
  max_connections_per_client: 10
network:
  auto_optimize: false
  congestion_control: bbr
user_defaults:
  default_expiration_days: 30
  default_max_connections: 2
  shell: /bin/false
monitor:
  refresh_interval_secs: 5
  auth_log_path: /var/log/auth.log
`
	path := filepath.Join(t.TempDir(), "v1.yaml")
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("v1 config must load: %v", err)
	}
	if !cfg.SSH.PasswordAuth || len(cfg.SSH.Ports) != 2 {
		t.Errorf("v1 ssh section lost: %+v", cfg.SSH)
	}
	if cfg.UDPGW.Mode != "builtin" || len(cfg.UDPGW.Ports) == 0 {
		t.Errorf("udpgw defaults not applied: %+v", cfg.UDPGW)
	}
	if cfg.UserDefaults.Group != "sshvpn" {
		t.Errorf("group default not applied")
	}
}

func TestValidatePortConflicts(t *testing.T) {
	cfg := Defaults()
	cfg.Gateway.Enabled = true
	cfg.Gateway.Ports = []int{22}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "port 22") {
		t.Fatalf("expected conflict error, got %v", err)
	}

	cfg = Defaults()
	cfg.UDPGW.Mode = "magic"
	if cfg.Validate() == nil {
		t.Fatal("bad udpgw mode accepted")
	}
	cfg = Defaults()
	cfg.SSH.Ports = []int{70000}
	if cfg.Validate() == nil {
		t.Fatal("bad port accepted")
	}
}
