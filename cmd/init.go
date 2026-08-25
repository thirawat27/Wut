package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
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

// stdinReader is shared by every prompt in the wizard.
//
// A fresh bufio.Reader/Scanner per prompt reads ahead and keeps whatever it
// buffered, so with piped input the first prompt swallowed the answers meant for
// the later ones and every prompt after it silently fell back to its default.
// One reader for the whole wizard keeps the buffer intact between questions.
var (
	stdinReader     *bufio.Reader
	stdinReaderOnce sync.Once
)

func promptReader() *bufio.Reader {
	stdinReaderOnce.Do(func() {
		stdinReader = bufio.NewReader(os.Stdin)
	})
	return stdinReader
}

// readPromptLine returns the next line of input and whether one was available.
func readPromptLine() (string, bool) {
	line, err := promptReader().ReadString('\n')
	if line == "" && err != nil {
		return "", false // EOF with nothing buffered
	}
	return strings.TrimSpace(line), true
}

// Helper methods for prompts
func askYN(prompt string, defaultYes bool) bool {
	q := lipgloss.NewStyle().Foreground(cPink).Bold(true).Render("?")
	p := lipgloss.NewStyle().Foreground(cWhite).Render(prompt)
	fmt.Printf("    %s  %s ", q, p)

	answer, ok := readPromptLine()
	if !ok {
		return defaultYes // Exits gracefully on EOF / Windows bug
	}
	answer = strings.ToLower(answer)
	if answer == "" {
		return defaultYes
	}
	return answer == "y" || answer == "yes" // accept any positive string
}

func askChoice(prompt string, defaultVal string) string {
	q := lipgloss.NewStyle().Foreground(cPink).Bold(true).Render("?")
	p := lipgloss.NewStyle().Foreground(cWhite).Render(prompt)
	fmt.Printf("    %s  %s ", q, p)

	answer, ok := readPromptLine()
	if !ok || answer == "" {
		return defaultVal
	}
	return answer
}

// initWizard carries the state that every setup step needs: the config being
// built, the presentation helpers, and the step counter.
//
// runInit used to be a single 327-line function holding all of this in local
// variables and closures, which made it impossible to read one step without
// reading the whole flow.
type initWizard struct {
	log        *logger.Logger
	cfg        *config.Config
	termWidth  int
	totalSteps int
	stepNum    int
}

func newInitWizard(log *logger.Logger, cfg *config.Config) *initWizard {
	termWidth := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		termWidth = w
	}

	totalSteps := 5
	if initSkipShell {
		totalSteps--
	}
	if initSkipTLDR {
		totalSteps--
	}

	return &initWizard{
		log:        log,
		cfg:        cfg,
		termWidth:  termWidth,
		totalSteps: totalSteps,
	}
}

// ─── Presentation helpers ────────────────────────────────────────────────────

func (w *initWizard) printStep(icon, title string) {
	if initQuick {
		return
	}
	w.stepNum++

	separatorLen := 50
	if w.termWidth < 60 {
		separatorLen = w.termWidth - 8
	}
	if separatorLen < 20 {
		separatorLen = 20
	}

	badge := lipgloss.NewStyle().Bold(true).Foreground(cBlue).Render(fmt.Sprintf("[%d/%d]", w.stepNum, w.totalSteps))
	heading := lipgloss.NewStyle().Bold(true).Foreground(cWhite).Render(icon + "  " + title)
	fmt.Printf("\n  %s  %s\n", badge, heading)
	fmt.Println(lipgloss.NewStyle().Foreground(cDarkGray).Render("  " + strings.Repeat("━", separatorLen)))
}

func (w *initWizard) printOK(s string) {
	fmt.Printf("    %s  %s\n", lipgloss.NewStyle().Foreground(cGreen).Render("✓"), lipgloss.NewStyle().Foreground(cGray).Render(s))
	time.Sleep(300 * time.Millisecond) // Add slight premium delay
}

func (w *initWizard) printWarn(s string) {
	fmt.Printf("    %s  %s\n", lipgloss.NewStyle().Foreground(cAmber).Render("⚠"), lipgloss.NewStyle().Foreground(cGray).Render(s))
}

func (w *initWizard) value(s string) string {
	return lipgloss.NewStyle().Foreground(cCyan).Render(s)
}

// noteBox renders an indented explanatory paragraph beside a step.
func (w *initWizard) noteBox(text string) string {
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(cDarkGray).
		PaddingLeft(2).
		MarginLeft(4).
		Foreground(cGray).
		Render(text)
}

func (w *initWizard) printBanner() {
	if initQuick {
		return
	}

	heroWidth := 54
	if w.termWidth < 60 {
		heroWidth = w.termWidth - 4
	}
	if heroWidth < 30 {
		heroWidth = 30
	}

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

// ─── Steps ───────────────────────────────────────────────────────────────────

func (w *initWizard) stepDirectories() error {
	w.printStep("📁", "Directories Setup")

	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	if !initQuick {
		w.printOK("Configuration folders verified")
	}
	return nil
}

func (w *initWizard) stepPreferences() error {
	if initQuick {
		w.cfg.UI.Theme = "auto"
		w.cfg.Fuzzy.Enabled = true
		w.cfg.History.Enabled = true
		w.cfg.Context.Enabled = true
		if err := config.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		return nil
	}

	w.printStep("⚙️ ", "Terminal Preferences")

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
	switch askChoice("Selection [1]:", "1") {
	case "2":
		w.cfg.UI.Theme = "dark"
	case "3":
		w.cfg.UI.Theme = "light"
	default:
		w.cfg.UI.Theme = "auto"
	}
	w.printOK("Theme profile set to " + w.value(w.cfg.UI.Theme))
	fmt.Println()

	w.cfg.History.Enabled = askYN("Enable command history productivity tracking? [Y/n]:", true)
	w.printOK("History tracking " + boolToEnabled(w.cfg.History.Enabled))
	fmt.Println()

	w.cfg.Context.Enabled = askYN("Enable project context analysis to get smarter suggestions? [Y/n]:", true)
	w.printOK("Context analysis " + boolToEnabled(w.cfg.Context.Enabled))

	if err := config.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	return nil
}

func (w *initWizard) stepShellIntegration() {
	if initSkipShell {
		return
	}

	if initQuick {
		// Quick setup never touches shell configs automatically.
		fmt.Println("Shell integration skipped in quick mode. Run 'wut install --shell <shell>' to enable it.")
		return
	}

	w.printStep("🐚", "Shell Integration")

	activeShell := shell.DetectCurrentShell()
	shellTargets := detectShellsForInit(initShell)

	displayShell := activeShell
	if displayShell == "" && len(shellTargets) > 0 {
		displayShell = shellTargets[0]
	}
	fmt.Printf("    Detected active shell: %s\n", w.value(displayShell))
	fmt.Println()
	fmt.Println(w.noteBox("WUT can add key bindings (Ctrl+Space, Ctrl+G) and\n" +
		"safe helper commands (oops, again) to your shell config.\n" +
		"It will not replace command-not-found handlers or your prompt."))
	fmt.Println()

	if !askYN("Install shell integration now? [y/N]:", false) {
		w.printOK("Shell integration skipped — run 'wut install --shell <shell>' later")
		return
	}
	if len(shellTargets) == 0 {
		w.printWarn("No installable shells detected")
		return
	}

	w.installShellHooks(shellTargets)
}

func (w *initWizard) installShellHooks(shellTargets []string) {
	installer := shell.NewInstaller()
	installed := 0

	for _, shellType := range shellTargets {
		if _, err := installer.Install(shellType); err != nil {
			if err.Error() == "already installed" {
				w.printOK(fmt.Sprintf("%s hooks already installed", shellType))
			} else {
				w.printWarn(fmt.Sprintf("%s integration: %v", shellType, err))
			}
			continue
		}

		installed++
		w.printOK(fmt.Sprintf("%s hooks installed successfully", shellType))

		reloadCmd := shell.GetReloadCommand(shellType, getShellRcFile(shellType))
		if reloadCmd == "" {
			reloadCmd = "restart your shell"
		}
		fmt.Printf("      %s Type %s to apply immediately.\n",
			lipgloss.NewStyle().Foreground(cPink).Render("→"),
			lipgloss.NewStyle().Foreground(cWhite).Render(reloadCmd),
		)
	}

	if installed == 0 {
		w.printWarn("Shell integration was not installed for any shell")
	}
}

func (w *initWizard) stepHistoryImport(ctx context.Context) {
	w.printStep("🕘", "History Import")

	if !w.cfg.History.Enabled {
		if !initQuick {
			w.printWarn("History tracking disabled; shell history import skipped")
		}
		return
	}

	importCtx, cancel := context.WithTimeout(ctx, historyImportTimeout)
	defer cancel()

	summary, err := bootstrapShellHistoryImport(importCtx)
	if err != nil {
		if initQuick {
			fmt.Printf("Shell history import skipped: %v\n", err)
		} else {
			w.printWarn("Shell history import: " + err.Error())
		}
		return
	}

	if initQuick {
		if summary.imported > 0 {
			fmt.Printf("Imported %d shell history entries\n", summary.imported)
		}
		return
	}

	switch {
	case summary.imported > 0:
		w.printOK(fmt.Sprintf("Imported %d history entries from %d shell sources", summary.imported, len(summary.sources)))
	case len(summary.sources) > 0:
		w.printOK(fmt.Sprintf("Scanned %d shell history sources; no new commands to import", len(summary.sources)))
	default:
		w.printOK("No shell history sources detected on this machine")
	}
}

func (w *initWizard) stepTLDRDatabase() {
	if initSkipTLDR {
		return
	}

	if initQuick {
		fmt.Println("Download TLDR pages: wut db sync")
		return
	}

	w.printStep("📚", "Offline Knowledge Base")

	fmt.Println()
	fmt.Println(w.noteBox("TLDR pages provide instant offline cheat sheets\n" +
		"for almost any CLI tool on your system."))
	fmt.Println()

	if !askYN("Download TLDR database now? (Highly Recommended) [Y/n]:", true) {
		w.printOK("Skipped — run 'wut db sync' to execute later")
		return
	}

	fmt.Printf("    %s\n", lipgloss.NewStyle().Foreground(cGray).Render("Syncing... please wait a moment."))
	if err := runDBSync(dbSyncCmd, []string{}); err != nil {
		w.printWarn("Sync encountered an issue: " + err.Error())
		return
	}
	w.printOK("Documentation is now offline")
}

func (w *initWizard) finish() {
	w.cfg.App.Initialized = true
	if err := config.Save(); err != nil {
		w.log.Error("failed to mark as initialized", "error", err)
	}

	if initQuick {
		fmt.Println(lipgloss.NewStyle().Foreground(cGreen).Bold(true).Render("✅ Quick setup complete!"))
		fmt.Println(ui.Accent("wut s git") + " — try it!")
		return
	}

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
}

// historyImportTimeout bounds the shell-history scan during setup so a huge or
// unreadable history file cannot stall the wizard.
const historyImportTimeout = 15 * time.Second

// watchForCancel prints a friendly notice and exits when the user interrupts
// the wizard. The returned function stops the watcher.
//
// The prompts block on stdin, so context cancellation cannot unwind them;
// exiting is the honest option here. The handler is torn down on return so it
// does not outlive the command.
func watchForCancel() func() {
	osSig := make(chan os.Signal, 1)
	signal.Notify(osSig, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		select {
		case <-osSig:
			fmt.Println()
			fmt.Println(lipgloss.NewStyle().Foreground(cAmber).Bold(true).Render("\n  ⚠ Setup cancelled — you can re-run 'wut init' any time.\n"))
			os.Exit(1)
		case <-done:
		}
	}()

	return func() {
		signal.Stop(osSig)
		close(done)
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	log := logger.With("init")
	log.Info("starting initialization wizard")

	if _, err := config.Load(cfgFile); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	stopWatching := watchForCancel()
	defer stopWatching()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	wizard := newInitWizard(log, config.Get())
	wizard.printBanner()

	if err := wizard.stepDirectories(); err != nil {
		return err
	}
	if err := wizard.stepPreferences(); err != nil {
		return err
	}
	wizard.stepShellIntegration()
	wizard.stepHistoryImport(ctx)
	wizard.stepTLDRDatabase()
	wizard.finish()

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
