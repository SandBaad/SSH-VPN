package sshfortress

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"sshfortress/internal/backup"
	"sshfortress/internal/buildinfo"
	"sshfortress/internal/card"
	"sshfortress/internal/config"
	"sshfortress/internal/daemon"
	"sshfortress/internal/doctor"
	"sshfortress/internal/netopt"
	"sshfortress/internal/session"
	"sshfortress/internal/setup"
	"sshfortress/internal/state"
	"sshfortress/internal/system"
	"sshfortress/internal/sysx"
)

func init() {
	rootCmd.AddCommand(daemonCmd(), onlineCmd(), statusCmd(), optimizeCmd(), applyCmd(),
		setupCmd(), gatewayCmd(), udpgwCmd(), doctorCmd(), backupCmd(), uninstallCmd())
}

func daemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "daemon",
		Short:  "Run the background service (used by systemd)",
		Hidden: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return daemon.New(e.cfg, e.db).Run(ctx)
		},
	}
}

// liveSessions prefers the daemon's snapshot (it has bandwidth figures).
func liveSessions(e *env) ([]state.Session, bool) {
	if st, err := state.Read(); err == nil && st.Fresh() {
		return st.Sessions, true
	}
	sc := &session.Scanner{UDPGWPorts: e.cfg.UDPGW.Ports}
	ss, _ := sc.Scan()
	out := make([]state.Session, len(ss))
	for i, s := range ss {
		out[i] = state.Session{Session: s}
	}
	return out, false
}

func rate(bps uint64) string {
	if bps == 0 {
		return "-"
	}
	return system.FormatBytes(bps) + "/s"
}

func onlineCmd() *cobra.Command {
	var watch bool
	c := &cobra.Command{
		Use:   "online",
		Short: "Show connected users",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			for {
				ss, _ := liveSessions(e)
				if jsonOut {
					return printJSON(ss)
				}
				if watch {
					fmt.Print("\033[H\033[2J")
				}
				w := table()
				fmt.Fprintln(w, "USER\tCLIENT\tVIA\tSINCE\tTRAFFIC\tSPEED\tUDP")
				for _, s := range ss {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n", s.Username, s.ClientIP, s.Via,
						s.StartedAt.Format("15:04:05"), card.FormatBytes(int64(s.Bytes)), rate(s.RateBps), s.UDPFlows)
				}
				w.Flush()
				fmt.Printf("%d session(s)\n", len(ss))
				if !watch {
					return nil
				}
				time.Sleep(2 * time.Second)
			}
		},
	}
	c.Flags().BoolVarP(&watch, "watch", "w", false, "refresh every 2s")
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return c
}

func statusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "status",
		Short: "Show a summary of all components",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			st, _ := state.Read()
			users, _ := e.db.ListUsers()
			cc, _ := netopt.Read("net.ipv4.tcp_congestion_control")
			if jsonOut {
				return printJSON(map[string]any{"version": buildinfo.Version, "users": len(users), "congestion_control": cc, "daemon": st})
			}
			fmt.Println(color(bold, "SSH-VPN v"+buildinfo.Version))
			if st != nil && st.Fresh() {
				fmt.Printf("  daemon     %s  up since %s\n", color(green, "running"), st.StartedAt.Format("2006-01-02 15:04"))
				fmt.Printf("  online     %d session(s), %s\n", len(st.Sessions), rate(st.TotalBps))
				if st.UDPGW != nil {
					h := color(green, "ok")
					if !st.UDPGW.Healthy {
						h = color(red, "down")
					}
					fmt.Printf("  udpgw      %s (%s) ports %v", h, st.UDPGW.Mode, st.UDPGW.Ports)
					if st.UDPGW.Stats != nil {
						fmt.Printf("  · %d clients, %d flows", st.UDPGW.Stats.Clients, st.UDPGW.Stats.Flows)
					}
					fmt.Println()
				}
				if st.Gateway != nil {
					fmt.Printf("  gateway    ports %v  · %d sessions, %d total", st.Gateway.Ports, st.Gateway.Sessions, st.Gateway.Total)
					if len(st.Gateway.Failed) > 0 {
						fmt.Printf("  %s", color(yellow, fmt.Sprintf("failed: %v", st.Gateway.Failed)))
					}
					fmt.Println()
				}
			} else {
				fmt.Printf("  daemon     %s (systemctl status sshfortress)\n", color(red, "not running"))
			}
			fmt.Printf("  users      %d\n", len(users))
			fmt.Printf("  tcp cc     %s\n", cc)
			if st != nil && len(st.Events) > 0 {
				fmt.Println("  recent:")
				for _, ev := range st.Events[max(0, len(st.Events)-5):] {
					fmt.Printf("    %s %s\n", ev.Time.Format("01-02 15:04"), ev.Text)
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return c
}

func optimizeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:       "optimize [apply|status|reset]",
		Short:     "Kernel network tuning (BBR, buffers, low latency)",
		ValidArgs: []string{"apply", "status", "reset"},
		Args:      cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			action := "apply"
			if len(args) == 1 {
				action = args[0]
			}
			switch action {
			case "status":
				ps := netopt.Status(e.cfg)
				if jsonOut {
					return printJSON(ps)
				}
				w := table()
				fmt.Fprintln(w, "\tSETTING\tCURRENT\tTARGET")
				for _, p := range ps {
					mark := color(red, "✗")
					if p.Applied {
						mark = color(green, "✓")
					} else if !p.Supported {
						mark = color(dim, "-")
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", mark, p.Key, p.Current, p.Value)
				}
				w.Flush()
				a, t := netopt.Score(ps)
				fmt.Printf("%d/%d applied\n", a, t)
			case "apply":
				r, err := netopt.Apply(e.cfg)
				if err != nil {
					return err
				}
				fmt.Printf("applied %d/%d settings (persisted to %s)\n", r.Applied, r.Total, netopt.SysctlFile)
				for _, w := range r.Warnings {
					fmt.Println(color(yellow, "  ! "+w))
				}
			case "reset":
				if err := netopt.Reset(e.cfg); err != nil {
					return err
				}
				fmt.Println("restored the original kernel settings")
			default:
				return fmt.Errorf("unknown action %q", action)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return c
}

func printSteps(steps []setup.Step) {
	for _, s := range steps {
		mark := color(green, "✓")
		if !s.OK {
			mark = color(red, "✗")
		}
		fmt.Printf("  %s %-22s %s\n", mark, s.Name, s.Detail)
	}
}

func applyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Apply config.yaml to the system (sshd, kernel, firewall, daemon)",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			printSteps(setup.Apply(e.cfg, e.db))
			return nil
		},
	}
}

func setupCmd() *cobra.Command {
	var (
		gwPorts, sshPorts, host string
		noGW, noOpt, noFW       bool
	)
	c := &cobra.Command{
		Use:   "setup",
		Short: "First-time setup (used by the installer); safe to re-run",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := sysx.RequireRoot(); err != nil {
				return err
			}
			path := cfgFile
			if path == "" {
				path = config.DefaultPath
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("gateway-ports") {
				ps, err := parsePorts(gwPorts)
				if err != nil {
					return err
				}
				cfg.Gateway.Ports = ps
				cfg.Gateway.Enabled = len(ps) > 0
			}
			if noGW {
				cfg.Gateway.Enabled = false
			}
			if cmd.Flags().Changed("ssh-ports") {
				ps, err := parsePorts(sshPorts)
				if err != nil {
					return err
				}
				cfg.SSH.Ports = ps
			}
			if host != "" {
				cfg.Server.Host = host
			}
			if noOpt {
				cfg.Network.AutoOptimize = false
			}
			if noFW {
				cfg.Firewall.Enabled = false
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			if err := config.Save(cfg, path); err != nil {
				return err
			}
			cfg.SetPath(path)
			cfgFile = path
			e, err := load()
			if err != nil {
				return err
			}
			fmt.Println(color(bold, "Setting up SSH-VPN"))
			printSteps(setup.Apply(e.cfg, e.db))
			return nil
		},
	}
	c.Flags().StringVar(&gwPorts, "gateway-ports", "", "gateway ports, e.g. 80,443 (empty = disabled)")
	c.Flags().StringVar(&sshPorts, "ssh-ports", "", "sshd ports, e.g. 22")
	c.Flags().StringVar(&host, "host", "", "domain or IP shown on connection cards")
	c.Flags().BoolVar(&noGW, "no-gateway", false, "disable the gateway")
	c.Flags().BoolVar(&noOpt, "no-optimize", false, "don't tune the kernel")
	c.Flags().BoolVar(&noFW, "no-firewall", false, "don't install egress firewall rules")
	return c
}

func saveAndRestart(cfg *config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := config.Save(cfg, cfg.Path()); err != nil {
		return err
	}
	if sysx.HasSystemd() && sysx.UnitExists("sshfortress.service") {
		if err := sysx.Systemctl("restart", "sshfortress"); err != nil {
			return err
		}
		fmt.Println("saved and restarted the daemon (existing sessions are kept)")
		return nil
	}
	fmt.Println("saved; restart the daemon to apply")
	return nil
}

func gatewayCmd() *cobra.Command {
	g := &cobra.Command{Use: "gateway", Short: "SSH / WebSocket / TLS gateway"}
	var ports string
	enable := &cobra.Command{
		Use:   "enable",
		Short: "Enable the gateway on --ports",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			if ports != "" {
				ps, err := parsePorts(ports)
				if err != nil {
					return err
				}
				e.cfg.Gateway.Ports = ps
			}
			if len(e.cfg.Gateway.Ports) == 0 {
				return fmt.Errorf("give ports, e.g. --ports 80,443")
			}
			e.cfg.Gateway.Enabled = true
			return saveAndRestart(e.cfg)
		},
	}
	enable.Flags().StringVar(&ports, "ports", "", "ports, e.g. 80,443,8080")
	disable := &cobra.Command{
		Use: "disable", Short: "Disable the gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			e.cfg.Gateway.Enabled = false
			return saveAndRestart(e.cfg)
		},
	}
	status := &cobra.Command{
		Use: "status", Short: "Show gateway status",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := state.Read()
			if err != nil || st.Gateway == nil {
				fmt.Println("gateway is not running")
				return nil
			}
			if jsonOut {
				return printJSON(st.Gateway)
			}
			fmt.Printf("ports %v · %d live sessions · %d total · %d rejected\n", st.Gateway.Ports, st.Gateway.Sessions, st.Gateway.Total, st.Gateway.Rejected)
			keys := make([]string, 0, len(st.Gateway.ByVia))
			for k := range st.Gateway.ByVia {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Printf("  %-9s %d\n", k, st.Gateway.ByVia[k])
			}
			for p, e := range st.Gateway.Failed {
				fmt.Println(color(yellow, "  port "+p+" failed: "+e))
			}
			return nil
		},
	}
	status.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	g.AddCommand(enable, disable, status)
	return g
}

func udpgwCmd() *cobra.Command {
	u := &cobra.Command{Use: "udpgw", Short: "UDP gateway for calls, games and DNS"}
	mode := &cobra.Command{
		Use:       "mode builtin|badvpn",
		Short:     "Choose the built-in gateway or the external badvpn-udpgw binary",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"builtin", "badvpn"},
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			e.cfg.UDPGW.Mode = strings.ToLower(args[0])
			e.cfg.UDPGW.Enabled = true
			return saveAndRestart(e.cfg)
		},
	}
	var ports string
	setPorts := &cobra.Command{
		Use:   "ports",
		Short: "Set udpgw ports (default 7100,7200,7300)",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			ps, err := parsePorts(ports)
			if err != nil {
				return err
			}
			e.cfg.UDPGW.Ports = ps
			return saveAndRestart(e.cfg)
		},
	}
	setPorts.Flags().StringVar(&ports, "set", "7100,7200,7300", "ports")
	u.AddCommand(mode, setPorts)
	return u
}

func doctorCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose problems and show how to fix them",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			checks := doctor.Run(e.cfg, e.db)
			if jsonOut {
				return printJSON(checks)
			}
			for _, c := range checks {
				mark := map[string]string{doctor.OK: color(green, "✓"), doctor.Warn: color(yellow, "!"),
					doctor.Fail: color(red, "✗"), doctor.Skip: color(dim, "-")}[c.Status]
				fmt.Printf(" %s %-20s %s\n", mark, c.Name, c.Detail)
				if c.Fix != "" && c.Status != doctor.OK {
					fmt.Printf("   %s\n", color(dim, "→ "+c.Fix))
				}
			}
			ok, warn, fail := doctor.Summary(checks)
			fmt.Printf("\n%d ok, %d warnings, %d problems\n", ok, warn, fail)
			if fail > 0 {
				os.Exit(2)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return c
}

func backupCmd() *cobra.Command {
	b := &cobra.Command{Use: "backup", Short: "Backup and restore users (with passwords)"}
	create := &cobra.Command{
		Use: "create", Short: "Create a backup",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			p, err := backup.NewEngine(e.cfg, e.db).Create()
			if err != nil {
				return err
			}
			fmt.Println("backup written:", p)
			return nil
		},
	}
	list := &cobra.Command{
		Use: "list", Short: "List backups",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			bs, err := backup.NewEngine(e.cfg, e.db).ListBackups()
			if err != nil {
				return err
			}
			w := table()
			fmt.Fprintln(w, "ID\tDATE\tUSERS\tSIZE\tFILE")
			for _, x := range bs {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", x.ID, x.CreatedAt.Format("2006-01-02 15:04"), x.UserCount, card.FormatBytes(x.SizeBytes), x.FilePath)
			}
			return w.Flush()
		},
	}
	restore := &cobra.Command{
		Use: "restore FILE", Short: "Restore users from a backup (also from another server)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			r, err := backup.NewEngine(e.cfg, e.db).Restore(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("restored %d users (%d accounts created, %d passwords)\n", r.Users, len(r.Created), r.Passwords)
			if r.ConfigSaved != "" {
				fmt.Println("the backed-up config was saved as", r.ConfigSaved, "(review it, then copy over config.yaml if you want it)")
			}
			for _, w := range r.Warnings {
				fmt.Println(color(yellow, "  ! "+w))
			}
			return nil
		},
	}
	del := &cobra.Command{
		Use: "delete ID", Short: "Delete a backup", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			return backup.NewEngine(e.cfg, e.db).DeleteBackup(args[0])
		},
	}
	b.AddCommand(create, list, restore, del)
	return b
}

func uninstallCmd() *cobra.Command {
	var keepOpt, yes bool
	c := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove system integration (keeps users and data)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := sysx.RequireRoot(); err != nil {
				return err
			}
			if !yes && !confirm("Remove SSH-VPN's sshd settings, firewall rules and service?") {
				return nil
			}
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return err
			}
			printSteps(setup.Uninstall(cfg, keepOpt))
			return nil
		},
	}
	c.Flags().BoolVar(&keepOpt, "keep-optimizations", false, "keep the kernel network profile")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return c
}
