package system

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"sshfortress/internal/config"
)

// SysctlParam represents a kernel parameter with its current and recommended values.
type SysctlParam struct {
	Key          string
	CurrentValue string
	OptimalValue string
	Description  string
	Applied      bool
}

// Optimizer handles network parameter tuning.
type Optimizer struct {
	cfg *config.Config
}

// NewOptimizer creates a new network optimizer.
func NewOptimizer(cfg *config.Config) *Optimizer {
	return &Optimizer{cfg: cfg}
}

// GetRecommendedParams returns the recommended sysctl parameters for SSH tunneling.
func (o *Optimizer) GetRecommendedParams() []SysctlParam {
	params := []SysctlParam{
		// TCP BBR Congestion Control
		{
			Key:          "net.core.default_qdisc",
			OptimalValue: "fq",
			Description:  "Fair Queue discipline for BBR",
		},
		{
			Key:          "net.ipv4.tcp_congestion_control",
			OptimalValue: o.cfg.Network.CongestionControl,
			Description:  "TCP congestion control algorithm",
		},

		// TCP Buffer Optimization
		{
			Key:          "net.core.rmem_max",
			OptimalValue: "16777216",
			Description:  "Max receive socket buffer (16MB)",
		},
		{
			Key:          "net.core.wmem_max",
			OptimalValue: "16777216",
			Description:  "Max send socket buffer (16MB)",
		},
		{
			Key:          "net.ipv4.tcp_rmem",
			OptimalValue: "4096 87380 16777216",
			Description:  "TCP receive buffer (min default max)",
		},
		{
			Key:          "net.ipv4.tcp_wmem",
			OptimalValue: "4096 65536 16777216",
			Description:  "TCP send buffer (min default max)",
		},

		// TCP Performance
		{
			Key:          "net.ipv4.tcp_window_scaling",
			OptimalValue: "1",
			Description:  "Enable TCP window scaling",
		},
		{
			Key:          "net.ipv4.tcp_fastopen",
			OptimalValue: "3",
			Description:  "Enable TCP Fast Open (client+server)",
		},
		{
			Key:          "net.ipv4.tcp_mtu_probing",
			OptimalValue: "1",
			Description:  "Enable MTU probing",
		},

		// Keepalive Settings
		{
			Key:          "net.ipv4.tcp_keepalive_time",
			OptimalValue: "120",
			Description:  "Keepalive probe interval (seconds)",
		},
		{
			Key:          "net.ipv4.tcp_keepalive_intvl",
			OptimalValue: "30",
			Description:  "Keepalive retry interval (seconds)",
		},
		{
			Key:          "net.ipv4.tcp_keepalive_probes",
			OptimalValue: "6",
			Description:  "Keepalive probe count before drop",
		},

		// Connection Tracking
		{
			Key:          "net.ipv4.tcp_max_syn_backlog",
			OptimalValue: "8192",
			Description:  "Max SYN backlog queue",
		},
		{
			Key:          "net.core.somaxconn",
			OptimalValue: "4096",
			Description:  "Max socket listen backlog",
		},
		{
			Key:          "net.ipv4.tcp_tw_reuse",
			OptimalValue: "1",
			Description:  "Reuse TIME_WAIT sockets",
		},
		{
			Key:          "net.ipv4.tcp_fin_timeout",
			OptimalValue: "15",
			Description:  "FIN timeout (seconds)",
		},

		// IP Forwarding (required for tunneling)
		{
			Key:          "net.ipv4.ip_forward",
			OptimalValue: "1",
			Description:  "Enable IP forwarding for tunnels",
		},
	}

	// Add any custom sysctls from config.
	for key, val := range o.cfg.Network.CustomSysctls {
		params = append(params, SysctlParam{
			Key:          key,
			OptimalValue: val,
			Description:  "Custom parameter",
		})
	}

	// Read current values.
	for i := range params {
		params[i].CurrentValue = readSysctl(params[i].Key)
		params[i].Applied = strings.TrimSpace(params[i].CurrentValue) == strings.TrimSpace(params[i].OptimalValue)
	}

	return params
}

// Apply applies all recommended sysctl parameters.
// Returns the count of successfully applied parameters.
func (o *Optimizer) Apply() (int, error) {
	params := o.GetRecommendedParams()
	applied := 0
	var errs []string

	for _, p := range params {
		if p.Applied {
			applied++
			continue
		}

		if err := writeSysctl(p.Key, p.OptimalValue); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p.Key, err))
		} else {
			applied++
		}
	}

	// Make changes persistent in /etc/sysctl.d/99-sshfortress.conf
	if err := o.writePersistentConfig(params); err != nil {
		errs = append(errs, fmt.Sprintf("persistent config: %v", err))
	}

	if len(errs) > 0 {
		return applied, fmt.Errorf("some parameters failed:\n%s", strings.Join(errs, "\n"))
	}
	return applied, nil
}

// Reset removes the persistent config and reloads system defaults.
func (o *Optimizer) Reset() error {
	os.Remove("/etc/sysctl.d/99-sshfortress.conf")
	cmd := exec.Command("sysctl", "--system")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sysctl reload failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func readSysctl(key string) string {
	out, err := exec.Command("sysctl", "-n", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func writeSysctl(key, value string) error {
	param := fmt.Sprintf("%s=%s", key, value)
	cmd := exec.Command("sysctl", "-w", param)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func (o *Optimizer) writePersistentConfig(params []SysctlParam) error {
	var lines []string
	lines = append(lines, "# SSH Fortress Network Optimization")
	lines = append(lines, "# Generated automatically — do not edit manually")
	lines = append(lines, "")

	for _, p := range params {
		lines = append(lines, fmt.Sprintf("# %s", p.Description))
		lines = append(lines, fmt.Sprintf("%s = %s", p.Key, p.OptimalValue))
		lines = append(lines, "")
	}

	content := strings.Join(lines, "\n")
	return os.WriteFile("/etc/sysctl.d/99-sshfortress.conf", []byte(content), 0644)
}
