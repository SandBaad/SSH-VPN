package views

import (
	"fmt"
	"strings"
	"time"

	"sshfortress/internal/card"
	"sshfortress/internal/firewall"
	"sshfortress/internal/netopt"
	"sshfortress/internal/procfs"
	"sshfortress/internal/state"
	"sshfortress/internal/store"
	"sshfortress/internal/system"
	apptheme "sshfortress/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

// DashboardState holds data for the dashboard.
type DashboardState struct {
	Loaded    bool
	SysInfo   system.Info
	Daemon    *state.State
	DaemonOK  bool
	BBR       bool
	CC        string
	Firewall  bool
	Users     int
	Active    int
	Expired   int
	Locked    int
	Traffic   int64
	SSHActive bool
}

func loadDashboard(a *App) DashboardState {
	d := DashboardState{Loaded: true, SysInfo: system.GetInfo()}
	if st, err := state.Read(); err == nil {
		d.Daemon = st
		d.DaemonOK = st.Fresh()
	}
	d.BBR = netopt.BBRActive()
	d.CC, _ = netopt.Read("net.ipv4.tcp_congestion_control")
	d.Firewall = a.cfg.Firewall.Enabled && firewall.Active()
	if tcp, err := procfs.ReadTCP(); err == nil {
		for _, p := range a.cfg.SSH.Ports {
			if tcp.ListenPorts[uint16(p)] {
				d.SSHActive = true
			}
		}
	}
	if us, err := a.db.ListUsers(); err == nil {
		now := time.Now()
		d.Users = len(us)
		for _, u := range us {
			d.Traffic += u.TrafficUsed
			switch {
			case u.IsExpired(now) || u.LockReason == store.LockExpired:
				d.Expired++
			case u.Disabled:
				d.Locked++
			default:
				d.Active++
			}
		}
	}
	return d
}

func (a *App) renderDashboard(w int) string {
	var b strings.Builder
	b.WriteString(title("📊 DASHBOARD"))
	d := a.dash
	if !d.Loaded {
		return b.String() + "  loading…"
	}
	cw := cardWidth(w)
	si := d.SysInfo
	server := a.renderCard("🖥  Server", []cardRow{
		{"Host", card.Host(a.cfg)},
		{"OS", si.OS},
		{"Kernel", si.Kernel},
		{"CPU", fmt.Sprintf("%d cores · load %s", si.CPUCores, strings.Fields(si.LoadAvg + " -")[0])},
		{"Uptime", system.FormatUptime(si.Uptime)},
		{"Memory", fmt.Sprintf("%s %.0f%%", renderProgressBar(si.MemPercent, 12), si.MemPercent)},
		{"Disk", fmt.Sprintf("%s %.0f%%", renderProgressBar(si.DiskPercent, 12), si.DiskPercent)},
	}, cw)

	online, speed, udpTxt, gwTxt := "-", "-", apptheme.StyleDim.Render("off"), apptheme.StyleDim.Render("off")
	if d.DaemonOK {
		st := d.Daemon
		online = fmt.Sprintf("%d sessions", len(st.Sessions))
		speed = system.FormatBytes(st.TotalBps) + "/s"
		if st.UDPGW != nil && st.UDPGW.Mode != "off" {
			udpTxt = ok(st.UDPGW.Healthy, st.UDPGW.Mode, "down")
			if st.UDPGW.Stats != nil {
				udpTxt += apptheme.StyleDim.Render(fmt.Sprintf(" · %d flows", st.UDPGW.Stats.Flows))
			}
		}
		if st.Gateway != nil {
			gwTxt = ok(len(st.Gateway.Ports) > 0, fmt.Sprintf("%v", st.Gateway.Ports), "no ports")
			if len(st.Gateway.Failed) > 0 {
				gwTxt += apptheme.StyleWarning.Render(fmt.Sprintf(" (%d failed)", len(st.Gateway.Failed)))
			}
		}
	}
	bbr := ok(d.BBR, "BBR", d.CC)
	services := a.renderCard("🔌  Services", []cardRow{
		{"Daemon", ok(d.DaemonOK, "running", "not running")},
		{"SSH server", ok(d.SSHActive, "listening "+ints(a.cfg.SSH.Ports), "not listening")},
		{"UDP gateway", udpTxt},
		{"WS/TLS gateway", gwTxt},
		{"Congestion", bbr},
		{"Firewall", ok(d.Firewall, "active", "off")},
	}, cw)

	stats := a.renderCard("👥  Users", []cardRow{
		{"Total", fmt.Sprintf("%d", d.Users)},
		{"Active", apptheme.StyleSuccess.Render(fmt.Sprintf("%d", d.Active))},
		{"Expired", apptheme.StyleDanger.Render(fmt.Sprintf("%d", d.Expired))},
		{"Locked", apptheme.StyleWarning.Render(fmt.Sprintf("%d", d.Locked))},
		{"Online", online},
		{"Throughput", speed},
		{"Traffic (all)", card.FormatBytes(d.Traffic)},
	}, cw)

	var ev []cardRow
	if d.Daemon != nil {
		events := d.Daemon.Events
		if len(events) > 6 {
			events = events[len(events)-6:]
		}
		for i := len(events) - 1; i >= 0; i-- {
			ev = append(ev, cardRow{events[i].Time.Format("01-02 15:04"), trunc(events[i].Text, cw-22)})
		}
	}
	if len(ev) == 0 {
		ev = []cardRow{{"", apptheme.StyleDim.Render("no events yet")}}
	}
	events := a.renderCard("📜  Recent activity", ev, cw)

	if twoColumns(w) {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, server, "  ", services))
		b.WriteString("\n")
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, stats, "  ", events))
	} else {
		b.WriteString(lipgloss.JoinVertical(lipgloss.Left, services, stats, server))
	}
	if !d.DaemonOK {
		b.WriteString("\n" + apptheme.StyleWarning.Render("  ! The daemon isn't running: limits, quotas and the UDP gateway are off.  Fix: systemctl enable --now sshfortress"))
	}
	return b.String()
}

func trunc(s string, n int) string {
	r := []rune(s)
	if n < 4 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
