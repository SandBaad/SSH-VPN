// Package config provides YAML-based configuration for SSH Fortress.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath = "/etc/sshfortress/config.yaml"
	defaultDataDir    = "/var/lib/sshfortress"
	defaultLogDir     = "/var/log/sshfortress"
)

// Config holds all application configuration.
type Config struct {
	// DataDir is the directory for persistent data (database, backups).
	DataDir string `yaml:"data_dir"`

	// LogDir is the directory for log files.
	LogDir string `yaml:"log_dir"`

	// SSH configuration.
	SSH SSHConfig `yaml:"ssh"`

	// BadVPN configuration.
	BadVPN BadVPNConfig `yaml:"badvpn"`

	// Network optimization settings.
	Network NetworkConfig `yaml:"network"`

	// User defaults.
	UserDefaults UserDefaultsConfig `yaml:"user_defaults"`

	// Monitor settings.
	Monitor MonitorConfig `yaml:"monitor"`
}

// SSHConfig holds SSH-specific configuration.
type SSHConfig struct {
	// Ports is the list of ports sshd should listen on.
	Ports []int `yaml:"ports"`

	// ConfigPath is the path to sshd_config.
	ConfigPath string `yaml:"config_path"`

	// MaxAuthTries is the maximum authentication attempts.
	MaxAuthTries int `yaml:"max_auth_tries"`

	// PasswordAuth enables/disables password authentication.
	PasswordAuth bool `yaml:"password_auth"`
}

// BadVPNConfig holds BadVPN udpgw configuration.
type BadVPNConfig struct {
	// Enabled toggles BadVPN management.
	Enabled bool `yaml:"enabled"`

	// BinaryPath is the path to the badvpn-udpgw binary.
	BinaryPath string `yaml:"binary_path"`

	// ListenAddr is the address BadVPN listens on.
	ListenAddr string `yaml:"listen_addr"`

	// MaxClients is the maximum number of udpgw clients.
	MaxClients int `yaml:"max_clients"`

	// MaxConnectionsPerClient is the max connections per single client.
	MaxConnectionsPerClient int `yaml:"max_connections_per_client"`
}

// NetworkConfig holds network optimization parameters.
type NetworkConfig struct {
	// AutoOptimize runs optimization on startup.
	AutoOptimize bool `yaml:"auto_optimize"`

	// CongestionControl sets the TCP congestion algorithm (e.g., bbr).
	CongestionControl string `yaml:"congestion_control"`

	// CustomSysctls are additional sysctl key-value pairs.
	CustomSysctls map[string]string `yaml:"custom_sysctls"`
}

// UserDefaultsConfig holds default values for new SSH users.
type UserDefaultsConfig struct {
	// DefaultExpirationDays is the default account expiration in days.
	DefaultExpirationDays int `yaml:"default_expiration_days"`

	// DefaultMaxConnections is the default max simultaneous connections.
	DefaultMaxConnections int `yaml:"default_max_connections"`

	// Shell is the default shell for new users.
	Shell string `yaml:"shell"`
}

// MonitorConfig holds monitoring settings.
type MonitorConfig struct {
	// RefreshIntervalSecs is the refresh interval for the monitor view.
	RefreshIntervalSecs int `yaml:"refresh_interval_secs"`

	// AuthLogPath is the path to the auth log.
	AuthLogPath string `yaml:"auth_log_path"`
}

// Defaults returns a Config populated with sensible defaults.
func Defaults() *Config {
	return &Config{
		DataDir: defaultDataDir,
		LogDir:  defaultLogDir,
		SSH: SSHConfig{
			Ports:        []int{22},
			ConfigPath:   "/etc/ssh/sshd_config",
			MaxAuthTries: 3,
			PasswordAuth: true,
		},
		BadVPN: BadVPNConfig{
			Enabled:                 false,
			BinaryPath:              "/usr/local/bin/badvpn-udpgw",
			ListenAddr:              "127.0.0.1:7300",
			MaxClients:              1000,
			MaxConnectionsPerClient: 10,
		},
		Network: NetworkConfig{
			AutoOptimize:      false,
			CongestionControl: "bbr",
			CustomSysctls:     make(map[string]string),
		},
		UserDefaults: UserDefaultsConfig{
			DefaultExpirationDays: 30,
			DefaultMaxConnections: 2,
			Shell:                 "/bin/false",
		},
		Monitor: MonitorConfig{
			RefreshIntervalSecs: 5,
			AuthLogPath:         "/var/log/auth.log",
		},
	}
}

// Load reads the config from the given path, falling back to default path.
// Missing file is not an error — defaults are used instead.
func Load(path string) (*Config, error) {
	cfg := Defaults()

	if path == "" {
		path = defaultConfigPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file — use defaults. Ensure data dir exists.
			return cfg, ensureDirs(cfg)
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	return cfg, ensureDirs(cfg)
}

// Save writes the config to the given path.
func Save(cfg *Config, path string) error {
	if path == "" {
		path = defaultConfigPath
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	return os.WriteFile(path, data, 0640)
}

func ensureDirs(cfg *Config) error {
	for _, dir := range []string{cfg.DataDir, cfg.LogDir} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	return nil
}
