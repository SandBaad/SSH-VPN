package tunnel

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"sshfortress/internal/config"
)

const systemdUnitPath = "/etc/systemd/system/badvpn-udpgw.service"

// BadVPNManager manages the badvpn-udpgw process lifecycle.
type BadVPNManager struct {
	cfg *config.Config
	mu  sync.Mutex
}

// NewBadVPNManager creates a new BadVPN manager.
func NewBadVPNManager(cfg *config.Config) *BadVPNManager {
	return &BadVPNManager{cfg: cfg}
}

// BadVPNStatus holds the current state of the BadVPN service.
type BadVPNStatus struct {
	Installed   bool
	Running     bool
	Enabled     bool // Systemd auto-start enabled
	ListenAddr  string
	MaxClients  int
	MaxConnsPC  int // Max connections per client
	BinaryPath  string
	PID         int
}

// GetStatus returns the current BadVPN status.
func (m *BadVPNManager) GetStatus() BadVPNStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	status := BadVPNStatus{
		ListenAddr: m.cfg.BadVPN.ListenAddr,
		MaxClients: m.cfg.BadVPN.MaxClients,
		MaxConnsPC: m.cfg.BadVPN.MaxConnectionsPerClient,
		BinaryPath: m.cfg.BadVPN.BinaryPath,
	}

	// Check if binary exists.
	if _, err := os.Stat(m.cfg.BadVPN.BinaryPath); err == nil {
		status.Installed = true
	}

	// Check if running via systemctl.
	out, err := exec.Command("systemctl", "is-active", "badvpn-udpgw").Output()
	status.Running = err == nil && strings.TrimSpace(string(out)) == "active"

	// Check if enabled.
	out, err = exec.Command("systemctl", "is-enabled", "badvpn-udpgw").Output()
	status.Enabled = err == nil && strings.TrimSpace(string(out)) == "enabled"

	// Get PID if running.
	if status.Running {
		out, err := exec.Command("systemctl", "show", "badvpn-udpgw", "--property=MainPID", "--value").Output()
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil {
				status.PID = pid
			}
		}
	}

	return status
}

// Start starts the BadVPN udpgw service.
func (m *BadVPNManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := os.Stat(m.cfg.BadVPN.BinaryPath); os.IsNotExist(err) {
		return fmt.Errorf("badvpn-udpgw binary not found at %s", m.cfg.BadVPN.BinaryPath)
	}

	// Ensure systemd unit exists.
	if err := m.ensureSystemdUnit(); err != nil {
		return fmt.Errorf("create systemd unit: %w", err)
	}

	// Reload systemd and start.
	exec.Command("systemctl", "daemon-reload").Run()

	cmd := exec.Command("systemctl", "start", "badvpn-udpgw")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start badvpn failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	return nil
}

// Stop stops the BadVPN udpgw service.
func (m *BadVPNManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := exec.Command("systemctl", "stop", "badvpn-udpgw")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stop badvpn failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// Enable sets BadVPN to start on boot.
func (m *BadVPNManager) Enable() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureSystemdUnit(); err != nil {
		return err
	}
	exec.Command("systemctl", "daemon-reload").Run()

	cmd := exec.Command("systemctl", "enable", "badvpn-udpgw")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("enable badvpn failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// Disable prevents BadVPN from starting on boot.
func (m *BadVPNManager) Disable() error {
	cmd := exec.Command("systemctl", "disable", "badvpn-udpgw")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("disable badvpn failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// Restart restarts the BadVPN service.
func (m *BadVPNManager) Restart() error {
	if err := m.Stop(); err != nil {
		// Ignore stop errors — service might not be running.
		_ = err
	}
	return m.Start()
}

// UpdateConfig updates the BadVPN configuration and restarts if running.
func (m *BadVPNManager) UpdateConfig(listenAddr string, maxClients, maxConnsPC int) error {
	m.cfg.BadVPN.ListenAddr = listenAddr
	m.cfg.BadVPN.MaxClients = maxClients
	m.cfg.BadVPN.MaxConnectionsPerClient = maxConnsPC

	// Regenerate systemd unit with new config.
	m.mu.Lock()
	err := m.ensureSystemdUnit()
	m.mu.Unlock()
	if err != nil {
		return err
	}

	exec.Command("systemctl", "daemon-reload").Run()

	// Restart if currently running.
	status := m.GetStatus()
	if status.Running {
		return m.Restart()
	}
	return nil
}

// ensureSystemdUnit creates or updates the systemd service unit file.
// Must be called with m.mu held.
func (m *BadVPNManager) ensureSystemdUnit() error {
	unit := fmt.Sprintf(`[Unit]
Description=BadVPN UDP Gateway
After=network.target

[Service]
Type=simple
ExecStart=%s --listen-addr %s --max-clients %d --max-connections-for-client %d
Restart=always
RestartSec=3
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`,
		m.cfg.BadVPN.BinaryPath,
		m.cfg.BadVPN.ListenAddr,
		m.cfg.BadVPN.MaxClients,
		m.cfg.BadVPN.MaxConnectionsPerClient,
	)

	return os.WriteFile(systemdUnitPath, []byte(unit), 0644)
}

// InstallBinary copies a BadVPN binary to the configured path.
func (m *BadVPNManager) InstallBinary(sourcePath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read source binary: %w", err)
	}

	if err := os.WriteFile(m.cfg.BadVPN.BinaryPath, data, 0755); err != nil {
		return fmt.Errorf("write binary: %w", err)
	}

	return nil
}
