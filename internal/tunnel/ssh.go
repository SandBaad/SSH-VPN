// Package tunnel provides SSH tunnel and BadVPN udpgw lifecycle management.
package tunnel

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"sshfortress/internal/config"
	"sshfortress/internal/security"
)

// SSHManager manages sshd configuration and multi-port listening.
type SSHManager struct {
	cfg *config.Config
}

// NewSSHManager creates a new SSH tunnel manager.
func NewSSHManager(cfg *config.Config) *SSHManager {
	return &SSHManager{cfg: cfg}
}

// SSHStatus holds the current state of the SSH service.
type SSHStatus struct {
	Running       bool
	ListeningPorts []int
	ActiveConns   int
	ConfigPath    string
}

// GetStatus returns the current SSH service status.
func (m *SSHManager) GetStatus() SSHStatus {
	status := SSHStatus{
		ConfigPath: m.cfg.SSH.ConfigPath,
	}

	// Check if sshd is running.
	out, err := exec.Command("systemctl", "is-active", "sshd").Output()
	if err != nil {
		out, err = exec.Command("systemctl", "is-active", "ssh").Output()
	}
	status.Running = err == nil && strings.TrimSpace(string(out)) == "active"

	// Get listening ports.
	status.ListeningPorts = m.getListeningPorts()

	// Count active connections.
	out, _ = exec.Command("ss", "-tn", "state", "established", "sport", "= :ssh").Output()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 1 {
		status.ActiveConns = len(lines) - 1
	}

	return status
}

// GetConfiguredPorts reads the current ports from sshd_config.
func (m *SSHManager) GetConfiguredPorts() ([]int, error) {
	data, err := os.ReadFile(m.cfg.SSH.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read sshd_config: %w", err)
	}

	var ports []int
	portRegex := regexp.MustCompile(`(?m)^Port\s+(\d+)`)
	matches := portRegex.FindAllStringSubmatch(string(data), -1)
	for _, match := range matches {
		if port, err := strconv.Atoi(match[1]); err == nil {
			ports = append(ports, port)
		}
	}

	if len(ports) == 0 {
		ports = []int{22} // Default if no Port directive found.
	}
	return ports, nil
}

// SetPorts updates the SSH listening ports in sshd_config.
func (m *SSHManager) SetPorts(ports []int) error {
	if len(ports) == 0 {
		return fmt.Errorf("at least one port is required")
	}
	for _, p := range ports {
		if err := security.ValidatePort(p); err != nil {
			return err
		}
	}

	data, err := os.ReadFile(m.cfg.SSH.ConfigPath)
	if err != nil {
		return fmt.Errorf("read sshd_config: %w", err)
	}

	content := string(data)

	// Remove all existing Port lines.
	portRegex := regexp.MustCompile(`(?m)^#?\s*Port\s+\d+\s*\n?`)
	content = portRegex.ReplaceAllString(content, "")

	// Build new Port directives.
	var portLines strings.Builder
	for _, p := range ports {
		portLines.WriteString(fmt.Sprintf("Port %d\n", p))
	}

	// Insert at the top of the file (after any leading comments).
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) == 2 {
		content = lines[0] + "\n" + portLines.String() + lines[1]
	} else {
		content = portLines.String() + content
	}

	if err := os.WriteFile(m.cfg.SSH.ConfigPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write sshd_config: %w", err)
	}

	return nil
}

// SetPasswordAuth enables or disables password authentication.
func (m *SSHManager) SetPasswordAuth(enabled bool) error {
	value := "yes"
	if !enabled {
		value = "no"
	}
	return m.setConfigDirective("PasswordAuthentication", value)
}

// SetMaxAuthTries sets the maximum authentication attempts.
func (m *SSHManager) SetMaxAuthTries(tries int) error {
	if tries < 1 || tries > 10 {
		return fmt.Errorf("max auth tries must be between 1 and 10")
	}
	return m.setConfigDirective("MaxAuthTries", strconv.Itoa(tries))
}

// RestartService restarts the SSH daemon.
func (m *SSHManager) RestartService() error {
	// Try sshd first, then ssh (different distro naming).
	if err := exec.Command("systemctl", "restart", "sshd").Run(); err != nil {
		if err2 := exec.Command("systemctl", "restart", "ssh").Run(); err2 != nil {
			return fmt.Errorf("restart ssh service failed: %w", err2)
		}
	}
	return nil
}

// TestConfig validates the sshd config without applying it.
func (m *SSHManager) TestConfig() error {
	cmd := exec.Command("sshd", "-t", "-f", m.cfg.SSH.ConfigPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sshd config test failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func (m *SSHManager) getListeningPorts() []int {
	out, err := exec.Command("ss", "-tlnp").Output()
	if err != nil {
		return nil
	}

	var ports []int
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "sshd") {
			continue
		}
		// Parse local address field to extract port.
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			addr := fields[3]
			if idx := strings.LastIndex(addr, ":"); idx >= 0 {
				if port, err := strconv.Atoi(addr[idx+1:]); err == nil {
					ports = append(ports, port)
				}
			}
		}
	}
	return ports
}

func (m *SSHManager) setConfigDirective(key, value string) error {
	data, err := os.ReadFile(m.cfg.SSH.ConfigPath)
	if err != nil {
		return fmt.Errorf("read sshd_config: %w", err)
	}

	content := string(data)
	directiveRegex := regexp.MustCompile(fmt.Sprintf(`(?m)^#?\s*%s\s+.*$`, regexp.QuoteMeta(key)))

	newLine := fmt.Sprintf("%s %s", key, value)
	if directiveRegex.MatchString(content) {
		content = directiveRegex.ReplaceAllString(content, newLine)
	} else {
		content += "\n" + newLine + "\n"
	}

	return os.WriteFile(m.cfg.SSH.ConfigPath, []byte(content), 0644)
}
