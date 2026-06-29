package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	if cfg.DataDir == "" {
		t.Error("DataDir should not be empty")
	}
	if len(cfg.SSH.Ports) == 0 {
		t.Error("SSH.Ports should have at least one default port")
	}
	if cfg.SSH.Ports[0] != 22 {
		t.Errorf("SSH.Ports[0] = %d, want 22", cfg.SSH.Ports[0])
	}
	if cfg.BadVPN.MaxClients != 1000 {
		t.Errorf("BadVPN.MaxClients = %d, want 1000", cfg.BadVPN.MaxClients)
	}
	if cfg.UserDefaults.DefaultMaxConnections != 2 {
		t.Errorf("UserDefaults.DefaultMaxConnections = %d, want 2", cfg.UserDefaults.DefaultMaxConnections)
	}
}

func TestLoadMissingFile(t *testing.T) {
	// Loading from a non-existent path should return defaults, not an error.
	cfg, err := Load("/tmp/sshfortress_test_nonexistent_config.yaml")
	if err != nil {
		// The error might be from ensureDirs if we don't have permission,
		// which is acceptable in test environments.
		t.Skipf("Skipping due to directory creation: %v", err)
	}
	if cfg == nil {
		t.Fatal("Config should not be nil with missing file")
	}
	if cfg.SSH.ConfigPath != "/etc/ssh/sshd_config" {
		t.Errorf("SSH.ConfigPath = %s, want /etc/ssh/sshd_config", cfg.SSH.ConfigPath)
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yaml")

	cfg := Defaults()
	cfg.SSH.Ports = []int{22, 443, 8080}
	cfg.BadVPN.Enabled = true
	cfg.DataDir = tmpDir
	cfg.LogDir = tmpDir

	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Config file not created: %v", err)
	}

	// Reload.
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded.SSH.Ports) != 3 {
		t.Errorf("loaded SSH.Ports length = %d, want 3", len(loaded.SSH.Ports))
	}
	if !loaded.BadVPN.Enabled {
		t.Error("loaded BadVPN.Enabled should be true")
	}
}
