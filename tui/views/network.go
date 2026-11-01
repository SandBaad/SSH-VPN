package views

import (
	"fmt"
	"sort"
	"strings"

	"sshfortress/internal/badvpn"
	"sshfortress/internal/state"
	"sshfortress/internal/system"
	apptheme "sshfortress/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

// NetworkState holds gateway and udpgw status.
type NetworkState struct {
	St     *state.State
	Fresh  bool
	BadVPN badvpn.Status
}

func loadNetwork(a *App) NetworkState {
	n := NetworkState{}
	if st, err := state.Read(); err == nil {
		n.St, n.Fresh = st, st.Fresh()
	}
	if a.cfg.UDPGW.Mode == "badvpn" {
		n.BadVPN = badvpn.GetStatus(a.cfg)
	}
	return n
}

func ints(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = fmt.Sprint(n)
	}
	return strings.Join(s, ", ")
}

func (a *App) renderNetwork(w int) string {
	var b strings.Builder
	b.WriteString(title("🌐 GATEWAY & UDP"))
	n := a.net
	cw := cardWidth(w)
	cfg := a.cfg

	// Gateway card
	var gw []cardRow
	if !cfg.Gateway.Enabled {
		gw = []cardRow{{"Status", apptheme.StyleDim.Render("disabled")}, {"Enable", "sshfortress gateway enable --ports 80,443"}}
	} else if n.Fresh && n.St.Gateway != nil {
		g := n.St.Gateway
		gw = []cardRow{
			{"Ports", ok(len(g.Ports) > 0, ints(g.Ports), "none bound")},
			{"Live sessions", fmt.Sprint(g.Sessions)},
			{"Handshaking", fmt.Sprint(g.Pending)},
			{"Total / rejected", fmt.Sprintf("%d / %d", g.Total, g.Rejected)},
		}
		keys := make([]string, 0, len(g.ByVia))
		for k := range g.ByVia {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			gw = append(gw, cardRow{"  via " + k, fmt.Sprint(g.ByVia[k])})
		}
		for p, e := range g.Failed {
			gw = append(gw, cardRow{"Port " + p, apptheme.StyleDanger.Render(trunc(e, cw-22))})
		}
	} else {
		gw = []cardRow{{"Status", apptheme.StyleWarning.Render("enabled, daemon not running")}}
	}
	gwCard := a.renderCard("🚪  Gateway (SSH · WebSocket · TLS)", gw, cw)

	// UDPGW card
	var u []cardRow
	switch {
	case !cfg.UDPGW.Enabled:
		u = []cardRow{{"Status", apptheme.StyleDanger.Render("disabled — calls won't work")}}
	case cfg.UDPGW.Mode == "badvpn":
		u = []cardRow{
			{"Mode", "external badvpn-udpgw"},
			{"Installed", ok(n.BadVPN.Installed, "yes", "no")},
			{"Running", ok(n.BadVPN.Running, fmt.Sprintf("pid %d", n.BadVPN.PID), "stopped")},
			{"Ports", ints(cfg.UDPGW.Ports)},
		}
	case n.Fresh && n.St.UDPGW != nil:
		x := n.St.UDPGW
		u = []cardRow{
			{"Mode", "built-in"},
			{"Health", ok(x.Healthy, "answering", "down")},
			{"Ports", cfg.UDPGW.Listen + ":" + ints(cfg.UDPGW.Ports)},
		}
		if s := x.Stats; s != nil {
			u = append(u,
				cardRow{"Clients / flows", fmt.Sprintf("%d / %d", s.Clients, s.Flows)},
				cardRow{"Up / down", system.FormatBytes(s.BytesUp) + " / " + system.FormatBytes(s.BytesDown)},
				cardRow{"Packets", fmt.Sprintf("↑%d ↓%d", s.PacketsUp, s.PacketsDown)},
				cardRow{"Dropped", fmt.Sprint(s.Dropped)},
			)
		}
		for addr, e := range x.Failed {
			u = append(u, cardRow{addr, apptheme.StyleDanger.Render(trunc(e, cw-22))})
		}
	default:
		u = []cardRow{{"Status", apptheme.StyleWarning.Render("daemon not running")}}
	}
	udpCard := a.renderCard("📞  UDP gateway (calls · games · DNS)", u, cw)

	sshCard := a.renderCard("🔐  SSH", []cardRow{
		{"sshd ports", ints(cfg.SSH.Ports)},
		{"Keepalive", fmt.Sprintf("%ds × %d", cfg.SSH.ClientAliveInterval, cfg.SSH.ClientAliveCountMax)},
		{"MaxStartups", cfg.SSH.MaxStartups},
		{"Compression", map[bool]string{true: "on", false: "off (faster)"}[cfg.SSH.Compression]},
		{"Limit policy", "kick " + cfg.Limiter.Policy},
	}, cw)

	tips := a.renderCard("💡  Client setup", []cardRow{
		{"UDPGW in app", "127.0.0.1:7300"},
		{"WS payload", "GET / HTTP/1.1[crlf]"},
		{"", "Host: <host>[crlf]"},
		{"", "Upgrade: websocket[crlf]"},
		{"", "[crlf]"},
		{"SSL/TLS", "gateway port + any SNI"},
	}, cw)

	if twoColumns(w) {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, gwCard, "  ", udpCard))
		b.WriteString("\n")
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, sshCard, "  ", tips))
	} else {
		b.WriteString(lipgloss.JoinVertical(lipgloss.Left, gwCard, udpCard, sshCard))
	}
	b.WriteString(keyHelp("change ports in /etc/sshfortress/config.yaml, then: sshfortress apply"))
	return b.String()
}
