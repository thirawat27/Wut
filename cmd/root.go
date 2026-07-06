// Package cmd provides CLI commands for WUT
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"wut/internal/config"
	"wut/internal/health"
	"wut/internal/logger"
	"wut/internal/metrics"
	"wut/internal/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

var (
	// Version is set during build
	Version = "0.3.0"
	// BuildTime is set during build
	BuildTime = "unknown"
	// Commit is set during build
	Commit = "unknown"

	cfgFile       string
	debug         bool
	didInitialize bool

	// rootCmd represents the base command
	rootCmd = &cobra.Command{
		Use:   "wut",
		Short: "Command Helper",
		Long: `The Smart Command Line Assistant That Actually Understands You
`,
		Version: "", // Will be set in init()
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if shouldSkipInitialization(cmd) {
				return nil
			}

			if err := initialize(cmd.Context()); err != nil {
				return err
			}

			// Check if WUT has been initialized
			if !config.IsInitialized() {
				fmt.Println()
				banner := lipgloss.NewStyle().
					Bold(true).
					Foreground(lipgloss.Color("#FFFFFF")).
					Background(lipgloss.Color("#EF4444")).
					Padding(0, 2).
					Render("⚠  WUT has not been initialized yet!")
				fmt.Println(banner)
				fmt.Println()
				fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF")).Render("  Please run the setup wizard first:"))
				fmt.Println()
				fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981")).Bold(true).Render("    wut init"))
				fmt.Println()
				fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Render("  This will configure your settings, optionally set up shell integration,"))
				fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Render("  and download the command database — all in one step."))
				fmt.Println()
				os.Exit(1)
			}

			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			cleanup()
		},
	}
)

// applyPremiumHelpRecursively applies the premium UI help styling to all commands
func applyPremiumHelpRecursively(c *cobra.Command) {
	setupPremiumHelp(c)
	for _, sub := range c.Commands() {
		applyPremiumHelpRecursively(sub)
	}
}

func shouldSkipInitialization(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}

	if help, err := cmd.Flags().GetBool("help"); err == nil && help {
		return true
	}

	switch cmd.Name() {
	case "init", "help", "version", "bug-report":
		return true
	default:
		return false
	}
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Info("received shutdown signal, shutting down gracefully...")
		cancel()
	}()

	// Set context for root command
	rootCmd.SetContext(ctx)

	// Apply modern UI scheme to all registered commands
	applyPremiumHelpRecursively(rootCmd)

	if err := rootCmd.Execute(); err != nil {
		logger.Error("command execution failed", "error", err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/wut/config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "enable debug mode")
}

func setupPremiumHelp(cmd *cobra.Command) {
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if c.Name() == "wut" {
			termWidth := 80
			if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
				termWidth = w
			}

			padX := 4
			if termWidth < 70 {
				padX = 1
			}

			bannerStyle := lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.Color("#8B5CF6")). // Electric Blue background
				Padding(1, padX).                      // Dynamic left/right padding
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("#8B5CF6")). // Violet border
				MarginBottom(1)

			if termWidth < 70 {
				bannerStyle = bannerStyle.Width(termWidth - 2)
			}

			desc := "⚡ WUT (What?)\nThe smart command line assistant that understands what you mean"
			fmt.Printf("\n%s\n", bannerStyle.Render(desc))
			fmt.Println(ui.Mascot())
		} else {
			fmt.Printf("\n%s\n", ui.Title(fmt.Sprintf("%s - %s", c.CommandPath(), c.Short)))
			if c.Long != "" && c.Long != c.Short {
				fmt.Printf("%s\n\n", ui.Secondary(c.Long))
			} else {
				fmt.Println()
			}
		}

		fmt.Printf("%s\n", ui.Title("Usage:"))
		if c.Runnable() {
			fmt.Printf("  %s %s\n", ui.Primary(c.UseLine()), ui.Warning("[flags]"))
		}
		if c.HasAvailableSubCommands() {
			fmt.Printf("  %s %s\n", ui.Primary(c.CommandPath()), ui.Success("[command]"))
		}
		fmt.Println()

		if len(c.Example) > 0 {
			fmt.Printf("%s\n", ui.Title("Examples:"))
			fmt.Printf("%s\n\n", ui.Accent(c.Example))
		}

		if c.HasAvailableSubCommands() {
			var coreCmds []*cobra.Command
			var shortcuts []*cobra.Command

			for _, sub := range c.Commands() {
				if sub.IsAvailableCommand() {
					if len(sub.Name()) <= 1 {
						shortcuts = append(shortcuts, sub)
					} else {
						coreCmds = append(coreCmds, sub)
					}
				}
			}

			if len(coreCmds) > 0 {
				fmt.Printf("%s\n", ui.Title("Core Commands:"))
				for _, sub := range coreCmds {
					pad := 20 - len(sub.Name())
					if pad < 2 {
						pad = 2
					}
					fmt.Printf("  %s%s%s\n", ui.Success(sub.Name()), strings.Repeat(" ", pad), ui.Muted(sub.Short))
				}
				fmt.Println()
			}

			if len(shortcuts) > 0 {
				fmt.Printf("%s\n", ui.Title("Shortcuts:"))
				for _, sub := range shortcuts {
					pad := 20 - len(sub.Name())
					if pad < 2 {
						pad = 2
					}
					fmt.Printf("  %s%s%s\n", ui.Success(sub.Name()), strings.Repeat(" ", pad), ui.Muted(sub.Short))
				}
				fmt.Println()
			}
		}

		printFlagsGroup := func(title string, flags *pflag.FlagSet) {
			visibleCount := 0
			flags.VisitAll(func(f *pflag.Flag) {
				if !f.Hidden {
					visibleCount++
				}
			})

			if visibleCount > 0 {
				fmt.Printf("%s\n", ui.Title(title))
				flags.VisitAll(func(f *pflag.Flag) {
					if f.Hidden {
						return
					}
					name := fmt.Sprintf("      --%s", f.Name)
					if f.Shorthand != "" {
						name = fmt.Sprintf("  -%s, --%s", f.Shorthand, f.Name)
					}
					if f.Value.Type() != "bool" {
						if f.Value.Type() == "string" {
							name += " string"
						} else {
							name += " " + f.Value.Type()
						}
					}
					pad := 28 - len(name)
					if pad < 2 {
						pad = 2
					}
					fmt.Printf("%s%s%s\n", ui.Warning(name), strings.Repeat(" ", pad), ui.Muted(f.Usage))
				})
				fmt.Println()
			}
		}

		if c.HasAvailableLocalFlags() {
			printFlagsGroup("Flags:", c.LocalFlags())
		}

		if c.HasAvailableInheritedFlags() {
			printFlagsGroup("Global Flags:", c.InheritedFlags())
		}

		if c.HasAvailableSubCommands() {
			part1 := ui.Primary(fmt.Sprintf("\"%s ", c.CommandPath()))
			part2 := ui.Success("[command]")
			part3 := ui.Warning(" --help\"")

			fmt.Printf("%s%s%s%s%s\n", ui.Muted("Use "), part1, part2, part3, ui.Muted(" for more information about a command."))
		}
	})
}

// SetVersionInfo updates the version string after variables are set
func SetVersionInfo() {
	rootCmd.Version = Version
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	// Configuration will be loaded in initialize()
}

// initialize performs initialization before command execution
func initialize(ctx context.Context) error {
	didInitialize = false

	// Initialize logger first
	logCfg := logger.DefaultConfig()
	if debug {
		logCfg.Level = "debug"
	}

	if err := logger.Initialize(logCfg); err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}

	log := logger.With("init")
	log.Info("starting WUT", "version", Version, "commit", Commit, "build_time", BuildTime)

	// Load configuration
	cfg, err := config.Load(cfgFile)
	if err != nil {
		log.Error("failed to load configuration", "error", err)
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Override debug from flag
	if debug {
		cfg.App.Debug = true
	}

	// Ensure directories exist
	if err := config.EnsureDirs(); err != nil {
		log.Error("failed to create directories", "error", err)
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Initialize metrics
	metrics.Initialize(Version, Commit)

	// Initialize health checker
	healthChecker := health.NewChecker(Version)
	healthChecker.RegisterDefaultChecks()

	// Log startup information
	log.Info("initialization complete",
		"config_file", cfgFile,
		"debug", cfg.App.Debug,
	)

	didInitialize = true
	return nil
}

// cleanup performs cleanup after command execution
func cleanup() {
	if !didInitialize {
		return
	}
	didInitialize = false

	log := logger.With("cleanup")
	log.Info("performing cleanup")

	// Flush logger
	if err := logger.Get().Sync(); err != nil {
		// Ignore sync errors
		_ = err
	}

	log.Info("cleanup complete")
}
