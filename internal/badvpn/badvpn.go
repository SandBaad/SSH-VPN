// Package badvpn runs the external badvpn-udpgw binary under systemd. It is
// the alternative to the built-in UDP gateway (udpgw.mode: badvpn).
package badvpn

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"sshfortress/internal/config"
	"sshfortress/internal/sysx"
)

// Unit is the systemd unit name (same as v1, so upgrades replace it).
const Unit = "badvpn-udpgw"

const unitPath = "/etc/systemd/system/" + Unit + ".service"

// Status of the external gateway.
type Status struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Enabled   bool   `json:"enabled"`
	PID       int    `json:"pid"`
	Binary    string `json:"binary"`
}

// GetStatus inspects the binary and the unit.
func GetStatus(cfg *config.Config) Status {
	s := Status{Binary: cfg.BadVPN.BinaryPath}
	s.Installed = sysx.FileExists(cfg.BadVPN.BinaryPath)
	if !sysx.HasSystemd() {
		return s
	}
	s.Running = sysx.UnitActive(Unit)
	s.Enabled = sysx.UnitEnabled(Unit)
	if s.Running {
		out, _ := sysx.Run("systemctl", "show", Unit, "--property=MainPID", "--value")
		s.PID, _ = strconv.Atoi(strings.TrimSpace(out))
	}
	return s
}

// UnitFile renders the systemd unit for the configured ports and limits.
func UnitFile(cfg *config.Config) string {
	args := []string{cfg.BadVPN.BinaryPath, "--logger", "syslog", "--loglevel", "warning"}
	ports := cfg.UDPGW.Ports
	if len(ports) > 16 { // badvpn's MAX_LISTEN_ADDRS
		ports = ports[:16]
	}
	for _, p := range ports {
		args = append(args, "--listen-addr", fmt.Sprintf("%s:%d", cfg.UDPGW.Listen, p))
	}
	args = append(args,
		"--max-clients", strconv.Itoa(cfg.UDPGW.MaxClients),
		"--max-connections-for-client", strconv.Itoa(cfg.UDPGW.MaxConnectionsPerClient))
	return fmt.Sprintf(`[Unit]
Description=BadVPN UDP Gateway for SSH-VPN (voice/video calls, games, DNS)
After=network.target

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=2
DynamicUser=yes
NoNewPrivileges=yes
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`, strings.Join(args, " "))
}

// Apply writes the unit and (re)starts the service.
func Apply(cfg *config.Config) error {
	if err := sysx.RequireRoot(); err != nil {
		return err
	}
	if !sysx.FileExists(cfg.BadVPN.BinaryPath) {
		return fmt.Errorf("badvpn-udpgw not found at %s — install it, or use udpgw.mode: builtin", cfg.BadVPN.BinaryPath)
	}
	changed, err := sysx.WriteIfChanged(unitPath, []byte(UnitFile(cfg)), 0o644)
	if err != nil {
		return err
	}
	_ = sysx.Systemctl("daemon-reload")
	if err := sysx.Systemctl("enable", Unit); err != nil {
		return err
	}
	if changed || !sysx.UnitActive(Unit) {
		return sysx.Systemctl("restart", Unit)
	}
	return nil
}

// Disable stops and disables the unit (used when switching to builtin).
func Disable() {
	if !sysx.HasSystemd() || !sysx.UnitExists(Unit+".service") {
		return
	}
	_ = sysx.Systemctl("disable", "--now", Unit)
}

// Remove disables the unit and deletes its file.
func Remove() {
	Disable()
	_ = os.Remove(unitPath)
	_ = sysx.Systemctl("daemon-reload")
}
