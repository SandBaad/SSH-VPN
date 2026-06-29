package system

import (
	"fmt"
	"os/exec"
	"strings"
)

// ServiceStatus holds the state of a systemd service.
type ServiceStatus struct {
	Name    string
	Active  bool
	Enabled bool
	Status  string // "active", "inactive", "failed", etc.
}

// GetServiceStatus checks the status of a systemd service.
func GetServiceStatus(name string) ServiceStatus {
	s := ServiceStatus{Name: name}

	out, err := exec.Command("systemctl", "is-active", name).Output()
	if err == nil {
		s.Status = strings.TrimSpace(string(out))
		s.Active = s.Status == "active"
	} else {
		s.Status = "inactive"
	}

	out, err = exec.Command("systemctl", "is-enabled", name).Output()
	s.Enabled = err == nil && strings.TrimSpace(string(out)) == "enabled"

	return s
}

// RestartService restarts a systemd service.
func RestartService(name string) error {
	cmd := exec.Command("systemctl", "restart", name)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restart %s: %s: %w", name, strings.TrimSpace(string(output)), err)
	}
	return nil
}

// StopService stops a systemd service.
func StopService(name string) error {
	cmd := exec.Command("systemctl", "stop", name)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stop %s: %s: %w", name, strings.TrimSpace(string(output)), err)
	}
	return nil
}

// StartService starts a systemd service.
func StartService(name string) error {
	cmd := exec.Command("systemctl", "start", name)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start %s: %s: %w", name, strings.TrimSpace(string(output)), err)
	}
	return nil
}

// GetManagedServices returns the status of all SSH Fortress managed services.
func GetManagedServices() []ServiceStatus {
	services := []string{"ssh", "sshd", "badvpn-udpgw"}
	var result []ServiceStatus

	for _, name := range services {
		s := GetServiceStatus(name)
		// Only include services that exist.
		if s.Status != "" {
			result = append(result, s)
		}
	}
	return result
}
