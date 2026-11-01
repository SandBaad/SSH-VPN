// Package setup provisions and removes SSH-VPN's system integration.
// Every step is idempotent: `sshfortress apply` can run any number of times.
package setup

import (
	"fmt"
	"os"
	"strings"

	"sshfortress/internal/badvpn"
	"sshfortress/internal/config"
	"sshfortress/internal/firewall"
	"sshfortress/internal/netopt"
	"sshfortress/internal/sshd"
	"sshfortress/internal/store"
	"sshfortress/internal/sysx"
	"sshfortress/internal/user"
)

// UnitPath is the daemon's systemd unit.
const UnitPath = "/etc/systemd/system/sshfortress.service"

// Step is one provisioning action.
type Step struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// DaemonUnit renders the daemon's systemd unit.
func DaemonUnit(bin, cfgPath string) string {
	return fmt.Sprintf(`[Unit]
Description=SSH-VPN daemon (limits, quotas, UDP gateway, WebSocket/TLS gateway)
Documentation=https://github.com/SandBaad/SSH-VPN
After=network-online.target ssh.service sshd.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s daemon --config %s
Restart=always
RestartSec=3
# Sessions opened through the gateway are child sshd processes: keep them
# alive when the daemon restarts or is upgraded.
KillMode=process
LimitNOFILE=1048576
TasksMax=infinity

[Install]
WantedBy=multi-user.target
`, bin, cfgPath)
}

// Apply provisions everything from cfg and (re)starts the daemon.
func Apply(cfg *config.Config, db *store.DB) []Step {
	var steps []Step
	add := func(name string, err error, detail string) {
		s := Step{Name: name, OK: err == nil, Detail: detail}
		if err != nil {
			s.Detail = err.Error()
		}
		steps = append(steps, s)
	}
	if err := sysx.RequireRoot(); err != nil {
		add("root", err, "")
		return steps
	}
	add("directories", cfg.EnsureDirs(), cfg.DataDir)

	um := user.NewManager(cfg, db)
	if err := um.EnsureGroup(); err != nil {
		add("VPN group", err, "")
	} else {
		n, err := um.MigrateGroups()
		add("VPN group", err, fmt.Sprintf("group %s (%d users moved from v1)", cfg.UserDefaults.Group, n))
	}

	res, err := sshd.New(cfg).Apply()
	if err == nil {
		d := fmt.Sprintf("%s via %s", res.SSHVersion, res.File)
		if res.SocketFix {
			d += "; switched ssh.socket → ssh.service"
		}
		if len(res.Warnings) > 0 {
			d += "; " + strings.Join(res.Warnings, "; ")
		}
		add("sshd tuning", nil, d)
	} else {
		add("sshd tuning", err, "")
	}

	if cfg.Network.AutoOptimize {
		r, err := netopt.Apply(cfg)
		if err == nil {
			d := fmt.Sprintf("%d/%d kernel settings", r.Applied, r.Total)
			if netopt.BBRActive() {
				d += ", BBR on"
			}
			if len(r.Warnings) > 0 {
				d += "; " + strings.Join(r.Warnings, "; ")
			}
			add("network optimization", nil, d)
		} else {
			add("network optimization", err, "")
		}
	}

	if cfg.Firewall.Enabled {
		add("firewall", firewall.Apply(cfg), "egress rules for VPN users")
	} else {
		_ = firewall.Remove()
	}

	if cfg.UDPGW.Enabled && cfg.UDPGW.Mode == "badvpn" {
		add("badvpn-udpgw", badvpn.Apply(cfg), "external binary")
	} else {
		badvpn.Disable()
	}

	if sysx.HasSystemd() {
		bin, _ := os.Executable()
		if p := sysx.Which("sshfortress"); p != "" {
			bin = p
		}
		_, err := sysx.WriteIfChanged(UnitPath, []byte(DaemonUnit(bin, cfg.Path())), 0o644)
		if err == nil {
			_ = sysx.Systemctl("daemon-reload")
			err = sysx.Systemctl("enable", "sshfortress")
		}
		if err == nil {
			err = sysx.Systemctl("restart", "sshfortress")
		}
		add("daemon", err, "sshfortress.service (restarted; sessions kept)")
	} else {
		add("daemon", fmt.Errorf("systemd not found"), "run `sshfortress daemon` under your init system")
	}
	return steps
}

// Uninstall removes system integration. Users and data are kept unless
// purge is true.
func Uninstall(cfg *config.Config, keepOptimizations bool) []Step {
	var steps []Step
	add := func(name string, err error) {
		s := Step{Name: name, OK: err == nil}
		if err != nil {
			s.Detail = err.Error()
		}
		steps = append(steps, s)
	}
	if sysx.HasSystemd() {
		_ = sysx.Systemctl("disable", "--now", "sshfortress")
		_ = os.Remove(UnitPath)
		_ = sysx.Systemctl("daemon-reload")
		add("daemon removed", nil)
	}
	badvpn.Remove()
	add("firewall rules removed", firewall.Remove())
	add("sshd settings removed", sshd.New(cfg).Remove())
	if !keepOptimizations {
		add("network profile reset", netopt.Reset(cfg))
	}
	return steps
}
