// Package config provides YAML-based configuration for SSH-VPN.
//
// Old (v1) config files keep working: unknown keys are ignored and every
// section that is missing falls back to the defaults below.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPath is where the installer writes the config file.
const DefaultPath = "/etc/sshfortress/config.yaml"

const (
	defaultDataDir = "/var/lib/sshfortress"
	defaultLogDir  = "/var/log/sshfortress"
	// RunDir holds runtime state shared between the daemon and the TUI/CLI.
	RunDir = "/run/sshfortress"
)

// Config holds all application configuration.
type Config struct {
	DataDir string `yaml:"data_dir"`
	LogDir  string `yaml:"log_dir"`

	Server       ServerConfig       `yaml:"server"`
	SSH          SSHConfig          `yaml:"ssh"`
	UDPGW        UDPGWConfig        `yaml:"udpgw"`
	BadVPN       BadVPNConfig       `yaml:"badvpn"`
	Gateway      GatewayConfig      `yaml:"gateway"`
	Limiter      LimiterConfig      `yaml:"limiter"`
	Network      NetworkConfig      `yaml:"network"`
	Firewall     FirewallConfig     `yaml:"firewall"`
	UserDefaults UserDefaultsConfig `yaml:"user_defaults"`
	Monitor      MonitorConfig      `yaml:"monitor"`

	path string // file the config was loaded from (for Save)
}

// ServerConfig describes how clients reach this server.
type ServerConfig struct {
	// Host is the domain or IP printed on user connection cards.
	// Empty means "auto-detect the public IP".
	Host string `yaml:"host"`
}

// SSHConfig holds sshd-related configuration.
type SSHConfig struct {
	// Ports sshd listens on directly.
	Ports []int `yaml:"ports"`
	// ConfigPath is the main sshd_config.
	ConfigPath string `yaml:"config_path"`
	// SSHDPath is the sshd binary (auto-detected when empty).
	SSHDPath string `yaml:"sshd_path"`
	// MaxAuthTries per connection.
	MaxAuthTries int `yaml:"max_auth_tries"`
	// PasswordAuth turns on password auth globally. VPN users always get
	// password auth through a "Match Group" block, so this can stay false.
	PasswordAuth bool `yaml:"password_auth"`
	// Tuning writes the speed/stability directives (keepalive, MaxStartups…).
	Tuning bool `yaml:"tuning"`
	// Compression: compressing already-encrypted traffic (HTTPS, video,
	// calls) only costs CPU and adds latency, so the default is off.
	Compression bool `yaml:"compression"`
	// ClientAliveInterval/CountMax detect dead phones quickly so their
	// stale sessions don't block reconnects under the connection limit.
	ClientAliveInterval int `yaml:"client_alive_interval"`
	ClientAliveCountMax int `yaml:"client_alive_count_max"`
	// MaxStartups is sshd's "start:rate:full" for concurrent handshakes.
	MaxStartups string `yaml:"max_startups"`
	// LoginGraceTime in seconds.
	LoginGraceTime int `yaml:"login_grace_time"`
	// Banner text shown to VPN clients (many apps display it). Empty = none.
	Banner string `yaml:"banner"`
}

// UDPGWConfig configures the UDP gateway used for voice/video calls, games
// and DNS over SSH (the protocol of badvpn-udpgw).
type UDPGWConfig struct {
	Enabled bool `yaml:"enabled"`
	// Mode is "builtin" (fast Go implementation in the daemon) or "badvpn"
	// (external badvpn-udpgw binary under systemd).
	Mode string `yaml:"mode"`
	// Listen address; clients reach it through their SSH tunnel.
	Listen string `yaml:"listen"`
	// Ports to listen on. Apps use different defaults (7300 is the most
	// common, some use 7100/7200), so all three are open by default.
	Ports []int `yaml:"ports"`
	// MaxClients is the max simultaneous udpgw clients (≈ tunnels).
	MaxClients int `yaml:"max_clients"`
	// MaxConnectionsPerClient is the max UDP flows per client. When it is
	// reached the least-recently-used flow is closed, which is what used to
	// break calls with the old limit of 10.
	MaxConnectionsPerClient int `yaml:"max_connections_per_client"`
	// FlowIdleSecs closes UDP flows with no traffic in either direction.
	FlowIdleSecs int `yaml:"flow_idle_secs"`
	// DNS server for packets flagged as DNS by the client ("" = system resolver).
	DNS string `yaml:"dns"`
}

// BadVPNConfig holds settings for the external badvpn-udpgw binary
// (used when udpgw.mode is "badvpn").
type BadVPNConfig struct {
	BinaryPath string `yaml:"binary_path"`
}

// GatewayConfig configures the multi-protocol front door. Each port
// auto-detects raw SSH, HTTP payload / WebSocket (CDN-friendly) and TLS/SSL,
// then hands the connection straight to sshd with TCP_NODELAY set.
type GatewayConfig struct {
	Enabled bool `yaml:"enabled"`
	// Listen address ("" = all interfaces, IPv4 and IPv6).
	Listen string `yaml:"listen"`
	// Ports to listen on, e.g. [80, 443, 8080].
	Ports []int `yaml:"ports"`
	// TLSCert/TLSKey: PEM files. Empty = auto-generated self-signed cert.
	TLSCert string `yaml:"tls_cert"`
	TLSKey  string `yaml:"tls_key"`
	// MaxConnections caps concurrent gateway connections.
	MaxConnections int `yaml:"max_connections"`
	// HandshakeTimeoutSecs bounds how long a client may take to say what it is.
	HandshakeTimeoutSecs int `yaml:"handshake_timeout_secs"`
}

// LimiterConfig controls simultaneous-connection enforcement.
type LimiterConfig struct {
	Enabled bool `yaml:"enabled"`
	// Policy is "oldest" (kick the oldest session — best for phones that
	// reconnect after switching networks) or "newest" (refuse newcomers).
	Policy string `yaml:"policy"`
	// IntervalSecs between enforcement/accounting passes.
	IntervalSecs int `yaml:"interval_secs"`
}

// NetworkConfig holds kernel network optimization parameters.
type NetworkConfig struct {
	// AutoOptimize applies the sysctl profile when the daemon starts.
	AutoOptimize bool `yaml:"auto_optimize"`
	// CongestionControl, e.g. "bbr".
	CongestionControl string `yaml:"congestion_control"`
	// CustomSysctls are extra key/value pairs added to the profile.
	CustomSysctls map[string]string `yaml:"custom_sysctls"`
}

// FirewallConfig controls per-VPN-user egress rules (iptables owner match).
type FirewallConfig struct {
	Enabled bool `yaml:"enabled"`
	// BlockLocal stops VPN users from reaching services on this server
	// (databases, panels on 127.0.0.1…) except udpgw and DNS.
	BlockLocal bool `yaml:"block_local"`
	// BlockSMTP blocks outbound port 25 so nobody can spam from your VPS.
	BlockSMTP bool `yaml:"block_smtp"`
	// AllowLocalPorts are extra local TCP ports VPN users may reach.
	AllowLocalPorts []int `yaml:"allow_local_ports"`
}

// UserDefaultsConfig holds default values for new users.
type UserDefaultsConfig struct {
	DefaultExpirationDays int     `yaml:"default_expiration_days"`
	DefaultMaxConnections int     `yaml:"default_max_connections"`
	DefaultTrafficGB      float64 `yaml:"default_traffic_gb"`
	Shell                 string  `yaml:"shell"`
	// Group every VPN user belongs to (used by sshd Match and the firewall).
	Group string `yaml:"group"`
}

// MonitorConfig holds monitoring settings.
type MonitorConfig struct {
	RefreshIntervalSecs int    `yaml:"refresh_interval_secs"`
	AuthLogPath         string `yaml:"auth_log_path"`
}

// Defaults returns a Config populated with sensible defaults.
func Defaults() *Config {
	return &Config{
		DataDir: defaultDataDir,
		LogDir:  defaultLogDir,
		SSH: SSHConfig{
			Ports:               []int{22},
			ConfigPath:          "/etc/ssh/sshd_config",
			MaxAuthTries:        4,
			PasswordAuth:        false,
			Tuning:              true,
			Compression:         false,
			ClientAliveInterval: 20,
			ClientAliveCountMax: 3,
			MaxStartups:         "100:30:1000",
			LoginGraceTime:      30,
		},
		UDPGW: UDPGWConfig{
			Enabled:                 true,
			Mode:                    "builtin",
			Listen:                  "127.0.0.1",
			Ports:                   []int{7100, 7200, 7300},
			MaxClients:              2048,
			MaxConnectionsPerClient: 512,
			FlowIdleSecs:            120,
		},
		BadVPN: BadVPNConfig{
			BinaryPath: "/usr/local/bin/badvpn-udpgw",
		},
		Gateway: GatewayConfig{
			Enabled:              false,
			Ports:                []int{},
			MaxConnections:       4096,
			HandshakeTimeoutSecs: 15,
		},
		Limiter: LimiterConfig{
			Enabled:      true,
			Policy:       "oldest",
			IntervalSecs: 5,
		},
		Network: NetworkConfig{
			AutoOptimize:      true,
			CongestionControl: "bbr",
			CustomSysctls:     map[string]string{},
		},
		Firewall: FirewallConfig{
			Enabled:    true,
			BlockLocal: true,
			BlockSMTP:  true,
		},
		UserDefaults: UserDefaultsConfig{
			DefaultExpirationDays: 30,
			DefaultMaxConnections: 2,
			DefaultTrafficGB:      0,
			Shell:                 "/bin/false",
			Group:                 "sshvpn",
		},
		Monitor: MonitorConfig{
			RefreshIntervalSecs: 2,
			AuthLogPath:         "/var/log/auth.log",
		},
	}
}

// Load reads the config from path (DefaultPath when empty).
// A missing file is not an error — defaults are used instead.
func Load(path string) (*Config, error) {
	cfg := Defaults()
	if path == "" {
		path = DefaultPath
	}
	cfg.path = path

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.normalize()
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.path = path
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// Path returns the file this config was loaded from.
func (c *Config) Path() string {
	if c.path == "" {
		return DefaultPath
	}
	return c.path
}

// SetPath changes the file Save writes to.
func (c *Config) SetPath(p string) { c.path = p }

// EnsureDirs creates the data and log directories.
func (c *Config) EnsureDirs() error {
	for _, dir := range []string{c.DataDir, c.LogDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	return nil
}

// normalize fills zero values left by partial (e.g. v1) config files.
func (c *Config) normalize() {
	d := Defaults()
	if c.DataDir == "" {
		c.DataDir = d.DataDir
	}
	if c.LogDir == "" {
		c.LogDir = d.LogDir
	}
	if len(c.SSH.Ports) == 0 {
		c.SSH.Ports = d.SSH.Ports
	}
	if c.SSH.ConfigPath == "" {
		c.SSH.ConfigPath = d.SSH.ConfigPath
	}
	if c.SSH.MaxAuthTries <= 0 {
		c.SSH.MaxAuthTries = d.SSH.MaxAuthTries
	}
	if c.SSH.ClientAliveInterval <= 0 {
		c.SSH.ClientAliveInterval = d.SSH.ClientAliveInterval
	}
	if c.SSH.ClientAliveCountMax <= 0 {
		c.SSH.ClientAliveCountMax = d.SSH.ClientAliveCountMax
	}
	if c.SSH.MaxStartups == "" {
		c.SSH.MaxStartups = d.SSH.MaxStartups
	}
	if c.SSH.LoginGraceTime <= 0 {
		c.SSH.LoginGraceTime = d.SSH.LoginGraceTime
	}
	c.UDPGW.Mode = strings.ToLower(strings.TrimSpace(c.UDPGW.Mode))
	if c.UDPGW.Mode == "" {
		c.UDPGW.Mode = d.UDPGW.Mode
	}
	if c.UDPGW.Listen == "" {
		c.UDPGW.Listen = d.UDPGW.Listen
	}
	if len(c.UDPGW.Ports) == 0 {
		c.UDPGW.Ports = d.UDPGW.Ports
	}
	if c.UDPGW.MaxClients <= 0 {
		c.UDPGW.MaxClients = d.UDPGW.MaxClients
	}
	if c.UDPGW.MaxConnectionsPerClient <= 0 {
		c.UDPGW.MaxConnectionsPerClient = d.UDPGW.MaxConnectionsPerClient
	}
	if c.UDPGW.FlowIdleSecs <= 0 {
		c.UDPGW.FlowIdleSecs = d.UDPGW.FlowIdleSecs
	}
	if c.BadVPN.BinaryPath == "" {
		c.BadVPN.BinaryPath = d.BadVPN.BinaryPath
	}
	if c.Gateway.MaxConnections <= 0 {
		c.Gateway.MaxConnections = d.Gateway.MaxConnections
	}
	if c.Gateway.HandshakeTimeoutSecs <= 0 {
		c.Gateway.HandshakeTimeoutSecs = d.Gateway.HandshakeTimeoutSecs
	}
	c.Limiter.Policy = strings.ToLower(strings.TrimSpace(c.Limiter.Policy))
	if c.Limiter.Policy == "" {
		c.Limiter.Policy = d.Limiter.Policy
	}
	if c.Limiter.IntervalSecs <= 0 {
		c.Limiter.IntervalSecs = d.Limiter.IntervalSecs
	}
	if c.Network.CongestionControl == "" {
		c.Network.CongestionControl = d.Network.CongestionControl
	}
	if c.Network.CustomSysctls == nil {
		c.Network.CustomSysctls = map[string]string{}
	}
	if c.UserDefaults.DefaultExpirationDays <= 0 {
		c.UserDefaults.DefaultExpirationDays = d.UserDefaults.DefaultExpirationDays
	}
	if c.UserDefaults.DefaultMaxConnections <= 0 {
		c.UserDefaults.DefaultMaxConnections = d.UserDefaults.DefaultMaxConnections
	}
	if c.UserDefaults.Shell == "" {
		c.UserDefaults.Shell = d.UserDefaults.Shell
	}
	if c.UserDefaults.Group == "" {
		c.UserDefaults.Group = d.UserDefaults.Group
	}
	if c.Monitor.RefreshIntervalSecs <= 0 {
		c.Monitor.RefreshIntervalSecs = d.Monitor.RefreshIntervalSecs
	}
	c.SSH.Ports = uniqSorted(c.SSH.Ports)
	c.UDPGW.Ports = uniqSorted(c.UDPGW.Ports)
	c.Gateway.Ports = uniqSorted(c.Gateway.Ports)
}

// Validate checks the config for mistakes that would break the server.
func (c *Config) Validate() error {
	check := func(section string, ports []int) error {
		for _, p := range ports {
			if p < 1 || p > 65535 {
				return fmt.Errorf("%s: port %d is out of range (1-65535)", section, p)
			}
		}
		return nil
	}
	if err := check("ssh.ports", c.SSH.Ports); err != nil {
		return err
	}
	if err := check("udpgw.ports", c.UDPGW.Ports); err != nil {
		return err
	}
	if err := check("gateway.ports", c.Gateway.Ports); err != nil {
		return err
	}
	if c.UDPGW.Mode != "builtin" && c.UDPGW.Mode != "badvpn" {
		return fmt.Errorf("udpgw.mode must be \"builtin\" or \"badvpn\", got %q", c.UDPGW.Mode)
	}
	if c.Limiter.Policy != "oldest" && c.Limiter.Policy != "newest" {
		return fmt.Errorf("limiter.policy must be \"oldest\" or \"newest\", got %q", c.Limiter.Policy)
	}
	if strings.Count(c.SSH.MaxStartups, ":") != 2 && strings.Count(c.SSH.MaxStartups, ":") != 0 {
		return fmt.Errorf("ssh.max_startups must look like 100:30:1000, got %q", c.SSH.MaxStartups)
	}
	// The same port can't be owned by two components.
	owner := map[int]string{}
	claim := func(section string, ports []int) error {
		for _, p := range ports {
			if prev, ok := owner[p]; ok {
				return fmt.Errorf("port %d is used by both %s and %s", p, prev, section)
			}
			owner[p] = section
		}
		return nil
	}
	if err := claim("ssh.ports", c.SSH.Ports); err != nil {
		return err
	}
	if c.Gateway.Enabled {
		if err := claim("gateway.ports", c.Gateway.Ports); err != nil {
			return err
		}
	}
	if c.UDPGW.Enabled {
		if err := claim("udpgw.ports", c.UDPGW.Ports); err != nil {
			return err
		}
	}
	return nil
}

// Save writes the config to its path (or DefaultPath).
func Save(cfg *Config, path string) error {
	if path == "" {
		path = cfg.Path()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	header := "# SSH-VPN configuration — https://github.com/SandBaad/SSH-VPN\n" +
		"# After editing run: sshfortress apply\n\n"
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(header), data...), 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func uniqSorted(in []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

// GB converts gigabytes to bytes.
func GB(gb float64) int64 { return int64(gb * 1024 * 1024 * 1024) }
