package sshfortress

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"sshfortress/internal/card"
	"sshfortress/internal/config"
	"sshfortress/internal/user"
)

func init() {
	userCmd := &cobra.Command{Use: "user", Aliases: []string{"users"}, Short: "Manage VPN users"}

	var (
		pass   string
		days   int
		conns  int
		gb     float64
		note   string
		yes    bool
		reset  bool
		limGB  float64
		purgeD int
	)

	add := &cobra.Command{
		Use:   "add NAME",
		Short: "Create a user (prints a connection card)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("traffic") {
				gb = 0
			} else if gb == 0 {
				gb = -1 // explicit 0 = unlimited
			}
			rec, pw, err := user.NewManager(e.cfg, e.db).Create(user.CreateUserOpts{
				Username: args[0], Password: pass, ExpirationDays: days,
				MaxConnections: conns, TrafficGB: gb, Notes: note,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(map[string]any{"user": rec, "password": pw, "host": card.Host(e.cfg)})
			}
			fmt.Print(card.Render(e.cfg, rec, pw))
			return nil
		},
	}
	add.Flags().StringVarP(&pass, "password", "p", "", "password (random if omitted)")
	add.Flags().IntVarP(&days, "days", "d", 0, "days until expiry (default from config)")
	add.Flags().IntVarP(&conns, "limit", "l", 0, "max simultaneous connections/devices")
	add.Flags().Float64VarP(&gb, "traffic", "t", 0, "traffic quota in GB (0 = unlimited)")
	add.Flags().StringVarP(&note, "note", "n", "", "free-text note")
	add.Flags().BoolVar(&jsonOut, "json", false, "JSON output")

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List users",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			users, err := user.NewManager(e.cfg, e.db).List()
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(users)
			}
			if len(users) == 0 {
				fmt.Println("No users yet. Create one: sshfortress user add NAME")
				return nil
			}
			w := table()
			fmt.Fprintln(w, "USER\tSTATUS\tONLINE\tEXPIRES\tTRAFFIC\tNOTE")
			for _, u := range users {
				st := u.Status
				switch st {
				case user.StatusActive:
					st = color(green, st)
				case user.StatusExpired, user.StatusQuota:
					st = color(red, st)
				default:
					st = color(yellow, st)
				}
				fmt.Fprintf(w, "%s\t%s\t%d/%d\t%s (%dd)\t%s\t%s\n", u.Username, st, u.ActiveSessions,
					u.MaxConnections, u.ExpiresAt.Format("2006-01-02"), u.DaysLeft, card.Traffic(&u.UserRecord), u.Notes)
			}
			return w.Flush()
		},
	}
	list.Flags().BoolVar(&jsonOut, "json", false, "JSON output")

	info := &cobra.Command{
		Use:   "info NAME",
		Short: "Show a user's details, sessions and connection card",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			u, err := user.NewManager(e.cfg, e.db).Get(args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(u)
			}
			fmt.Print(card.Render(e.cfg, &u.UserRecord, ""))
			fmt.Printf("  Status: %s   Created: %s   Last seen: %s\n", u.Status, u.CreatedAt.Format("2006-01-02"), ago(u.LastSeen))
			for _, s := range u.Sessions {
				fmt.Printf("  • %s via %s since %s (%s)\n", s.ClientIP, s.Via, s.StartedAt.Format("15:04:05"), card.FormatBytes(int64(s.Bytes)))
			}
			return nil
		},
	}
	info.Flags().BoolVar(&jsonOut, "json", false, "JSON output")

	del := &cobra.Command{
		Use:     "del NAME",
		Aliases: []string{"delete", "rm"},
		Short:   "Delete a user (disconnects them)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			if !yes && !confirm(fmt.Sprintf("Delete %s?", args[0])) {
				return nil
			}
			if err := user.NewManager(e.cfg, e.db).Delete(args[0]); err != nil {
				return err
			}
			fmt.Println("deleted", args[0])
			return nil
		},
	}
	del.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")

	renew := &cobra.Command{
		Use:   "renew NAME DAYS",
		Short: "Extend a user by DAYS (from today if already expired) and unlock",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("DAYS must be a number")
			}
			u, err := user.NewManager(e.cfg, e.db).Renew(args[0], n)
			if err != nil {
				return err
			}
			if reset {
				if u, err = user.NewManager(e.cfg, e.db).ResetTraffic(args[0]); err != nil {
					return err
				}
			}
			fmt.Printf("%s now expires %s\n", u.Username, u.ExpiresAt.Format("2006-01-02 15:04"))
			return nil
		},
	}
	renew.Flags().BoolVar(&reset, "reset-traffic", false, "also reset the traffic counter")

	passwd := &cobra.Command{
		Use:   "passwd NAME [PASSWORD]",
		Short: "Set a new password (random if omitted) and print the card",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			pw := ""
			if len(args) == 2 {
				pw = args[1]
			}
			m := user.NewManager(e.cfg, e.db)
			pw, err = m.SetPassword(args[0], pw)
			if err != nil {
				return err
			}
			u, err := m.Get(args[0])
			if err != nil {
				return err
			}
			fmt.Print(card.Render(e.cfg, &u.UserRecord, pw))
			return nil
		},
	}

	lock := &cobra.Command{
		Use: "lock NAME", Short: "Disable a user and disconnect them", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			if err := user.NewManager(e.cfg, e.db).Lock(args[0], ""); err != nil {
				return err
			}
			fmt.Println("locked", args[0])
			return nil
		},
	}
	unlock := &cobra.Command{
		Use: "unlock NAME", Short: "Re-enable a locked user", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			if err := user.NewManager(e.cfg, e.db).Unlock(args[0]); err != nil {
				return err
			}
			fmt.Println("unlocked", args[0])
			return nil
		},
	}
	limit := &cobra.Command{
		Use: "limit NAME N", Short: "Set max simultaneous connections", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("N must be a number")
			}
			if err := user.NewManager(e.cfg, e.db).SetMaxConnections(args[0], n); err != nil {
				return err
			}
			fmt.Printf("%s: max %d connections\n", args[0], n)
			return nil
		},
	}
	traffic := &cobra.Command{
		Use:   "traffic NAME",
		Short: "Show, limit or reset a user's traffic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			m := user.NewManager(e.cfg, e.db)
			if cmd.Flags().Changed("set") {
				if _, err := m.SetTrafficLimit(args[0], config.GB(limGB)); err != nil {
					return err
				}
			}
			if reset {
				if _, err := m.ResetTraffic(args[0]); err != nil {
					return err
				}
			}
			u, err := m.Get(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("%s: %s\n", u.Username, card.Traffic(&u.UserRecord))
			return nil
		},
	}
	traffic.Flags().Float64Var(&limGB, "set", 0, "set quota in GB (0 = unlimited)")
	traffic.Flags().BoolVar(&reset, "reset", false, "reset the counter (and lift a quota lock)")

	kick := &cobra.Command{
		Use: "kick NAME", Short: "Disconnect all of a user's sessions", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			n, err := user.NewManager(e.cfg, e.db).Kick(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("disconnected %d session(s)\n", n)
			return nil
		},
	}
	purge := &cobra.Command{
		Use:   "purge",
		Short: "Delete users that expired more than --days ago",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			if !yes && !confirm(fmt.Sprintf("Delete all users expired more than %d days ago?", purgeD)) {
				return nil
			}
			removed, err := user.NewManager(e.cfg, e.db).PurgeExpired(purgeD)
			fmt.Printf("deleted %d user(s) %v\n", len(removed), removed)
			return err
		},
	}
	purge.Flags().IntVar(&purgeD, "days", 7, "grace period in days")
	purge.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")

	expiry := &cobra.Command{
		Use:   "expiry NAME DATE",
		Short: "Set an exact expiry: 2026-12-31 or \"2026-12-31 18:00\" (server time)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := load()
			if err != nil {
				return err
			}
			var t time.Time
			for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
				if t, err = time.ParseInLocation(layout, args[1], time.Local); err == nil {
					break
				}
			}
			if err != nil {
				return fmt.Errorf("date must look like 2026-12-31 or \"2026-12-31 18:00\"")
			}
			u, err := user.NewManager(e.cfg, e.db).SetExpiry(args[0], t)
			if err != nil {
				return err
			}
			fmt.Printf("%s expires %s\n", u.Username, u.ExpiresAt.Format("2006-01-02 15:04"))
			return nil
		},
	}

	userCmd.AddCommand(add, list, info, del, renew, passwd, lock, unlock, limit, traffic, kick, purge, expiry)
	rootCmd.AddCommand(userCmd)
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
