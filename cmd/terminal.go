// Package cmd provides CLI commands for WUT
package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"wut/internal/config"
	"wut/internal/db"
	"wut/internal/logger"
)

// terminalCmd opens WUT as a standalone terminal application.
// It launches the same interactive TUI as 'wut suggest' without requiring
// shell integration.
var terminalCmd = &cobra.Command{
	Use:   "terminal",
	Short: "Open WUT as a standalone terminal",
	Long: `Launch WUT in its own interactive terminal interface.

This is the standalone way to use WUT: no shell hooks, no key bindings, and no
modifications to your shell config. You can search commands and fix typos
directly from the TUI.

If you prefer tighter integration, use 'wut install' to add optional shell
key bindings after running 'wut init'.`,
	Example: `  wut terminal
  wut t`,
	RunE: runTerminal,
}

func init() {
	rootCmd.AddCommand(terminalCmd)
}

func runTerminal(cmd *cobra.Command, args []string) error {
	log := logger.With("terminal")
	start := time.Now()

	defer func() {
		log.Debug("terminal completed", "duration", time.Since(start))
	}()

	dbPath := config.GetTLDRDatabasePath()

	var storage *db.Storage
	var err error
	if _, statErr := os.Stat(dbPath); statErr == nil {
		storage, err = db.NewStorage(dbPath)
		if err != nil {
			log.Warn("failed to open local storage", "error", err)
		}
	}
	if storage != nil {
		defer storage.Close()
	}

	clientOpts := []db.ClientOption{
		db.WithAutoDetect(true),
	}
	if storage != nil {
		clientOpts = append(clientOpts, db.WithStorage(storage))
	}

	client := db.NewClient(clientOpts...)

	ctx := cmd.Context()
	online := client.IsOnline(ctx)
	if !online {
		fmt.Println("📴 Offline mode - using local database")
		fmt.Println("   Run 'wut db sync' to download more commands")
		fmt.Println()
	}

	model := db.NewModel()
	if storage != nil {
		model.SetStorage(storage)
	}

	program := tea.NewProgram(model, tea.WithAltScreen())

	finalModel, err := program.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	if m, ok := finalModel.(*db.Model); ok {
		selected := m.Selected()
		if selected != "" {
			fmt.Println(selected)
		}
	}

	return nil
}
