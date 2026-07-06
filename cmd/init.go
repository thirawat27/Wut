package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"wut/internal/config"
	"wut/internal/logger"
	"wut/internal/shell"
	"wut/internal/ui"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize WUT for first-time use",
	Long: `Interactive setup wizard for WUT configuration.

This command will:
  • Create configuration directory structure
  • Detect your shell and recommend integrations
  • Set up default preferences
  • Optionally sync TLDR pages

Run this when you first install WUT or want to reconfigure.`,
	Example: `  wut init              # Interactive setup
  wut init --quick      # Quick setup with defaults
  wut init --shell zsh  # Setup for specific shell`,
	RunE: runInit,
}

var (
	initQuick     bool
	initShell     string
	initSkipTLDR  bool
	initSkipShell bool
	initNonTUI    bool
)

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().BoolVarP(&initQuick, "quick", "q", false, "quick setup with defaults (non-interactive)")
	initCmd.Flags().StringVarP(&initShell, "shell", "s", "", "shell type")
	initCmd.Flags().BoolVar(&initSkipTLDR, "skip-tldr", false, "skip TLDR pages setup")
	initCmd.Flags().BoolVar(&initSkipShell, "skip-shell", false, "skip shell integration setup")
	initCmd.Flags().BoolVar(&initNonTUI, "no-tui", false, "use simple text interface (no fancy UI)")
}

// Global UI colors
var (
	cBlue     = lipgloss.Color("#8B5CF6") // Changed to Purple/Violet for UI
	cCyan     = lipgloss.Color("#C4B5FD") // Light Purple
	cGreen    = lipgloss.Color("#10B981")
	cAmber    = lipgloss.Color("#F59E0B")
	cPink     = lipgloss.Color("#EC4899")
	cGray     = lipgloss.Color("#6B7280")
	cDarkGray = lipgloss.Color("#374151")
	cWhite    = lipgloss.Color("#F8F9FA")
)

// Helper methods for prompts
func askYN(prompt string, defaultYes bool) bool {
	q := lipgloss.NewStyle().Foreground(cPink).Bold(true).Render("?")
	p := lipgloss.NewStyle().Foreground(cWhite).Render(prompt)
	fmt.Printf("    %s  %s ", q, p)

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if answer == "" {
			return defaultYes
		}
		return answer == "y" || answer == "yes" // accept any positive string
	}
	return defaultYes // Exits gracefully on EOF / Windows bug
}

func askChoice(prompt string, defaultVal string) string {
	q := lipgloss.NewStyle().Foreground(cPink).Bold(true).Render("?")
	p := lipgloss.NewStyle().Foreground(cWhite).Render(prompt)
	fmt.Printf("    %s  %s ", q, p)

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.TrimSpace(scanner.Text())
		if answer == "" {
			return defaultVal
		}
		return answer
	}
	return defaultVal
}

func runInit(cmd *cobra.Command, args []string) error {
	log := logger.With("init")
	log.Info("starting initialization wizard")

	if _, err := config.Load(cfgFile); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// ─── Interrupt handling ────────────────────────────────────────────────────
	osSig := make(chan os.Signal, 1)
	signal.Notify(osSig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-osSig
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Foreground(cAmber).Bold(true).Render("\n  ⚠ Setup cancelled — you can re-run 'wut init' any time.\n"))
		os.Exit(1)
	}()

	// ─── Get dynamic terminal width ──────────────────────────────────────────
	termWidth := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		termWidth = w
	}

	heroWidth := 54
	if termWidth < 60 {
		heroWidth = termWidth - 4
	}
	if heroWidth < 30 {
		heroWidth = 30
	}

	// ─── Hero Banner ───────────────────────────────────────────────────────────
	if !initQuick {
		panelBorder := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cBlue).
			Padding(1, 3)

		heroLogo := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(cBlue).
			Padding(0, 2).
			Render(" 🚀 WUT SETUP ")

		heroDesc := lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Foreground(cCyan).Render("Supercharge your terminal workflow."),
			lipgloss.NewStyle().Foreground(cGray).Render("Press ")+
				lipgloss.NewStyle().Foreground(cPink).Render("Ctrl+C")+
				lipgloss.NewStyle().Foreground(cGray).Render(" anytime to abort."),
		)

		heroContent := lipgloss.JoinVertical(lipgloss.Left, heroLogo, "", heroDesc)
		fmt.Println()
		fmt.Println(panelBorder.Width(heroWidth).Render(heroContent))
	}

	totalSteps := 5
	if initSkipShell {
		totalSteps--
	}
	if initSkipTLDR {
		totalSteps--
	}
	stepNum := 0

	printStep := func(icon, title string) {
		stepNum++

		separatorLen := 50
		if termWidth < 60 {
			separatorLen = termWidth - 8
		}
		if separatorLen < 20 {
			separatorLen = 20
		}

		badge := lipgloss.NewStyle().Bold(true).Foreground(cBlue).Render(fmt.Sprintf("[%d/%d]", stepNum, totalSteps))
		heading := lipgloss.NewStyle().Bold(true).Foreground(cWhite).Render(icon + "  " + title)
		fmt.Printf("\n  %s  %s\n", badge, heading)
		fmt.Println(lipgloss.NewStyle().Foreground(cDarkGray).Render("  " + strings.Repeat("━", separatorLen)))
	}
	printOK := func(s string) {
		fmt.Printf("    %s  %s\n", lipgloss.NewStyle().Foreground(cGreen).Render("✓"), lipgloss.NewStyle().Foreground(cGray).Render(s))
		time.Sleep(300 * time.Millisecond) // Add slight premium delay
	}
	printWarn := func(s string) {
		fmt.Printf("    %s  %s\n", lipgloss.NewStyle().Foreground(cAmber).Render("⚠"), lipgloss.NewStyle().Foreground(cGray).Render(s))
	}
	valFmt := func(s string) string { return lipgloss.NewStyle().Foreground(cCyan).Render(s) }

	cfg := config.Get()

	// ─── Step 1: Directories ───────────────────────────────────────────────────
	if !initQuick {
		printStep("📁", "Directories Setup")
	}
	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}
	if !initQuick {
		printOK("Configuration folders verified")
	}

	// ─── Step 2: Configuration ─────────────────────────────────────────────────
	if initQuick {
		cfg.UI.Theme = "auto"
		cfg.Fuzzy.Enabled = true
		cfg.History.Enabled = true
		cfg.Context.Enabled = true
		if err := config.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
	} else {
		printStep("⚙️ ", "Terminal Preferences")

		lbl := lipgloss.NewStyle().Foreground(cGray).Render
		opt := lipgloss.NewStyle().Foreground(cWhite).Render
		num := lipgloss.NewStyle().Foreground(cBlue).Bold(true).Render

		themeMenu := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(cDarkGray).
			PaddingLeft(2).
			MarginLeft(4).
			Render(
				lipgloss.JoinVertical(lipgloss.Left,
					lbl("Choose your preferred theme:"),
					fmt.Sprintf(" %s %s", num("1"), opt("Auto-detect (Recommended)")),
					fmt.Sprintf(" %s %s", num("2"), opt("Dark mode")),
					fmt.Sprintf(" %s %s", num("3"), opt("Light mode")),
				),
			)
		fmt.Println()
		fmt.Println(themeMenu)

		fmt.Println()
		choice := askChoice("Selection [1]:", "1")
		switch choice {
		case "2":
			cfg.UI.Theme = "dark"
		case "3":
			cfg.UI.Theme = "light"
		default:
			cfg.UI.Theme = "auto"
		}
		printOK("Theme profile set to " + valFmt(cfg.UI.Theme))
		fmt.Println()

		cfg.History.Enabled = askYN("Enable command history productivity tracking? [Y/n]:", true)
		printOK("History tracking " + boolToEnabled(cfg.History.Enabled))
		fmt.Println()

		cfg.Context.Enabled = askYN("Enable project context analysis to get smarter suggestions? [Y/n]:", true)
		printOK("Context analysis " + boolToEnabled(cfg.Context.Enabled))

		if err := config.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
	}

	// ─── Step 3: Shell Integration (opt-in only) ─────────────────────────────
	if !initSkipShell {
		activeShell := shell.DetectCurrentShell()
		shellTargets := detectShellsForInit(initShell)

		if initQuick {
			// Quick setup never touches shell configs automatically.
			fmt.Println("Shell integration skipped in quick mode. Run 'wut install --shell <shell>' to enable it.")
		} else {
			printStep("🐚", "Shell Integration")
			displayShell := activeShell
			if displayShell == "" && len(shellTargets) > 0 {
				displayShell = shellTargets[0]
			}
			fmt.Printf("    Detected active shell: %s\n", valFmt(displayShell))
			fmt.Println()
			descBox := lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(cDarkGray).
				PaddingLeft(2).
				MarginLeft(4).
				Foreground(cGray).
				Render("WUT can add key bindings (Ctrl+Space, Ctrl+G) and\nsafe helper commands (oops, again) to your shell config.\nIt will not replace command-not-found handlers or your prompt.")
			fmt.Println(descBox)
			fmt.Println()

			if !askYN("Install shell integration now? [y/N]:", false) {
				printOK("Shell integration skipped — run 'wut install --shell <shell>' later")
			} else if len(shellTargets) == 0 {
				printWarn("No installable shells detected")
			} else {
				installer := shell.NewInstaller()
				installedShells := 0
				for _, shellType := range shellTargets {
					if _, err := installer.Install(shellType); err != nil {
						if err.Error() == "already installed" {
							printOK(fmt.Sprintf("%s hooks already installed", shellType))
						} else {
							printWarn(fmt.Sprintf("%s integration: %v", shellType, err))
						}
						continue
					}

					installedShells++
					printOK(fmt.Sprintf("%s hooks installed successfully", shellType))
					reloadCmd := shell.GetReloadCommand(shellType, getShellRcFile(shellType))
					if reloadCmd == "" {
						reloadCmd = "restart your shell"
					}
					fmt.Printf("      %s Type %s to apply immediately.\n",
						lipgloss.NewStyle().Foreground(cPink).Render("→"),
						lipgloss.NewStyle().Foreground(cWhite).Render(reloadCmd),
					)
				}
				if installedShells == 0 {
					printWarn("Shell integration was not installed for any shell")
				}
			}
		}
	}

	// ─── Step 4: Shell History Import ─────────────────────────────────────────
	if !initQuick {
		printStep("🕘", "History Import")
	}
	if cfg.History.Enabled {
		importCtx, importCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer importCancel()

		summary, err := bootstrapShellHistoryImport(importCtx)
		if err != nil {
			if initQuick {
				fmt.Printf("Shell history import skipped: %v\n", err)
			} else {
				printWarn("Shell history import: " + err.Error())
			}
		} else if !initQuick {
			switch {
			case summary.imported > 0:
				printOK(fmt.Sprintf("Imported %d history entries from %d shell sources", summary.imported, len(summary.sources)))
			case len(summary.sources) > 0:
				printOK(fmt.Sprintf("Scanned %d shell history sources; no new commands to import", len(summary.sources)))
			default:
				printOK("No shell history sources detected on this machine")
			}
		} else if summary.imported > 0 {
			fmt.Printf("Imported %d shell history entries\n", summary.imported)
		}
	} else if !initQuick {
		printWarn("History tracking disabled; shell history import skipped")
	}

	// ─── Step 5: TLDR Pages ────────────────────────────────────────────────────
	if !initSkipTLDR {
		if !initQuick {
			printStep("📚", "Offline Knowledge Base")

			descBox := lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(cDarkGray).
				PaddingLeft(2).
				MarginLeft(4).
				Foreground(cGray).
				Render("TLDR pages provide instant offline cheat sheets\nfor almost any CLI tool on your system.")

			fmt.Println()
			fmt.Println(descBox)
			fmt.Println()

			if askYN("Download TLDR database now? (Highly Recommended) [Y/n]:", true) {
				fmt.Printf("    %s\n", lipgloss.NewStyle().Foreground(cGray).Render("Syncing... please wait a moment."))
				if err := runDBSync(dbSyncCmd, []string{}); err != nil {
					printWarn("Sync encountered an issue: " + err.Error())
				} else {
					printOK("Documentation is now offline")
				}
			} else {
				printOK("Skipped — run 'wut db sync' to execute later")
			}
		} else {
			fmt.Println("Download TLDR pages: wut db sync")
		}
	}

	// ─── Mark as initialized ──────────────────────────────────────────────────
	cfg.App.Initialized = true
	if err := config.Save(); err != nil {
		log.Error("failed to mark as initialized", "error", err)
	}

	// ─── Done Card ─────────────────────────────────────────────────────────────
	if !initQuick {
		fmt.Println()

		cmdCol := func(s string) string { return lipgloss.NewStyle().Foreground(cCyan).Bold(true).Render(s) }
		descCol := func(s string) string { return lipgloss.NewStyle().Foreground(cGray).Render(s) }

		doneBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cGreen).
			Padding(1, 3).
			Render(lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Foreground(cGreen).Bold(true).Render("🎉 Setup Complete!"),
				"",
				ui.Mascot(),
				"",
				lipgloss.NewStyle().Foreground(cWhite).Render("Pro tips to get started:"),
				fmt.Sprintf("  %s        %s", cmdCol("wut s <cmd>"), descCol("Search instant AI cheat sheets")),
				fmt.Sprintf("  %s               %s", cmdCol("wut h"), descCol("Interactive timeline history")),
				fmt.Sprintf("  %s           %s", cmdCol("wut stats"), descCol("Productivity metric dashboard")),
				fmt.Sprintf("  %s        %s", cmdCol("wut bookmark"), descCol("Pin your favorite commands")),
			))

		fmt.Println(doneBox)
		fmt.Println()
	} else {
		fmt.Println(lipgloss.NewStyle().Foreground(cGreen).Bold(true).Render("✅ Quick setup complete!"))
		fmt.Println(ui.Accent("wut s git") + " — try it!")
	}

	return nil
}

// OS / Shell helpers

func detectShellsForInit(explicit string) []string {
	if explicit = shell.CanonicalName(explicit); explicit != "" {
		return []string{explicit}
	}

	// Only target the active/preferred shell by default to avoid touching
	// every installed shell without explicit consent.
	if preferred := shell.DetectPreferredInstallShell(); preferred != "" {
		return []string{preferred}
	}

	return nil
}

func getShellRcFile(shellType string) string {
	if rcFile, err := shell.GetConfigFile(shellType); err == nil && rcFile != "" {
		return rcFile
	}
	home, _ := os.UserHomeDir()
	return home + "/.bashrc"
}

func boolToEnabled(b bool) string {
	if b {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#2DC653")).Render("enabled")
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF70A6")).Render("disabled")
}
