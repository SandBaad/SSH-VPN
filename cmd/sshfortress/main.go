// Package sshfortress provides the CLI entry point for SSH-VPN.
package sshfortress

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"sshfortress/internal/buildinfo"
	"sshfortress/internal/config"
	"sshfortress/internal/store"
	"sshfortress/tui"
)

// Version is kept for compatibility; the real value lives in buildinfo.
var Version = buildinfo.Version

var (
	cfgFile string
	jsonOut bool
)

var rootCmd = &cobra.Command{
	Use:   "sshfortress",
	Short: "SSH-VPN — fast, stable SSH VPN server manager",
	Long: `SSH-VPN turns a Linux VPS into a fast, stable SSH VPN server:
users with expiry/limits/quotas, a built-in UDP gateway for voice & video
calls, a WebSocket/TLS/CDN gateway, BBR tuning and a terminal UI.

Run without arguments to open the terminal UI.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		return tui.Run(e.cfg, e.db)
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("SSH-VPN v%s\n", buildinfo.Version)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default "+config.DefaultPath+")")
	rootCmd.PersistentFlags().Bool("debug", false, "unused, kept for compatibility")
	_ = rootCmd.PersistentFlags().MarkHidden("debug")
	rootCmd.AddCommand(versionCmd)
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

type env struct {
	cfg *config.Config
	db  *store.DB
}

func load() (*env, error) {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		if os.IsPermission(err) || strings.Contains(err.Error(), "permission denied") {
			return nil, fmt.Errorf("permission denied — run with sudo")
		}
		return nil, err
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "sshfortress.db"))
	if err != nil {
		if strings.Contains(err.Error(), "permission denied") {
			return nil, fmt.Errorf("permission denied — run with sudo")
		}
		return nil, err
	}
	return &env{cfg: cfg, db: db}, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func table() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
}

// parsePorts parses "80,443 8080".
func parsePorts(s string) ([]int, error) {
	var out []int
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid port %q", f)
		}
		out = append(out, n)
	}
	return out, nil
}

const (
	green  = "\033[32m"
	yellow = "\033[33m"
	red    = "\033[31m"
	dim    = "\033[2m"
	bold   = "\033[1m"
	reset  = "\033[0m"
)

func color(c, s string) string {
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
		return s // not a terminal
	}
	return c + s + reset
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	var a string
	fmt.Scanln(&a)
	a = strings.ToLower(strings.TrimSpace(a))
	return a == "y" || a == "yes"
}
