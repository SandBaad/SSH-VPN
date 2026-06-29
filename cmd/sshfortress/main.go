// Package sshfortress provides the CLI entry point for SSH Fortress.
package sshfortress

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"sshfortress/internal/config"
	"sshfortress/internal/store"
	"sshfortress/tui"
)

var (
	// Version is set at build time via ldflags.
	Version   = "dev"
	cfgFile   string
	debugMode bool
)

var rootCmd = &cobra.Command{
	Use:   "sshfortress",
	Short: "SSH Fortress — Enterprise-Grade SSH Manager",
	Long: `SSH Fortress is a modern, high-performance SSH tunnel and user
management system built in Go. It provides a beautiful terminal UI
for managing SSH users, tunnels, BadVPN, and network optimization.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		db, err := store.Open(cfg.DataDir + "/sshfortress.db")
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
		defer db.Close()

		return tui.Run(cfg, db)
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("SSH Fortress v%s\n", Version)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file path (default: /etc/sshfortress/config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&debugMode, "debug", "d", false, "enable debug logging")
	rootCmd.AddCommand(versionCmd)
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
