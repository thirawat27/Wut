package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"wut/internal/config"
	"wut/internal/corrector"
	"wut/internal/db"
	"wut/internal/ui"
)

// fixCmd corrects typos in commands
var fixCmd = &cobra.Command{
	Use:   "fix [command]",
	Short: "Fix typos in your commands",
	Long: `Correct common typos and suggest the right command.
WUT will detect typos, dangerous commands, and suggest alternatives.`,
	Example: `  wut fix "gti status"
  wut fix "doker ps"
  wut fix "rm -rf /"`,
	RunE: runFix,
}

var (
	fixCopy      bool
	fixList      bool
	fixShellMode bool
)

func init() {
	rootCmd.AddCommand(fixCmd)

	fixCmd.Flags().BoolVarP(&fixCopy, "copy", "c", false, "copy corrected command to clipboard")
	fixCmd.Flags().BoolVarP(&fixList, "list", "l", false, "list common typos")
	fixCmd.Flags().BoolVar(&fixShellMode, "shell", false, "output corrected command only for shell integration")
	_ = fixCmd.Flags().MarkHidden("shell")
}

func runFix(cmd *cobra.Command, args []string) error {
	// 1. Setup storage and corrector
	store, err := db.NewStorage(config.GetDatabasePath())
	if err == nil {
		defer store.Close()
		hydrateHistoryFromShell(context.Background(), store)
	}

	c := corrector.New()

	// Populate corrector with history for better fuzzy matching
	if store != nil {
		if history, err := store.GetHistory(context.Background(), 100); err == nil {
			var historyCmds []string
			for _, h := range history {
				historyCmds = append(historyCmds, h.Command)
			}
			c.SetHistoryCommands(historyCmds)
		}
	}

	// 2. Handle --list flag
	if fixList {
		return listCommonTypos()
	}

	// 3. Get input: either from args or last history command
	input := ""
	if len(args) > 0 {
		input = strings.Join(args, " ")
	} else if store != nil {
		// Fetch last command from history (skipping 'wut' commands)
		history, err := store.GetHistory(context.Background(), 10)
		if err == nil {
			for _, entry := range history {
				cmdStr := strings.TrimSpace(entry.Command)
				if cmdStr != "" && !strings.HasPrefix(cmdStr, "wut") {
					input = cmdStr
					break
				}
			}
		}
	}

	if input == "" {
		if store == nil {
			return fmt.Errorf("no command provided and history database is unavailable")
		}
		return fmt.Errorf("no command provided and no recent history found to fix")
	}

	// 4a. Detect if input looks like natural language → run semantic engine
	if looksLikeNaturalLanguage(input) {
		if fixShellMode {
			best, err := bestSemanticMatch(input)
			if err != nil {
				return err
			}
			fmt.Println(best)
			return nil
		}
		return runSemanticSearch(input)
	}

	// 4b. Perform typo/flag correction
	correction, err := c.Correct(input)
	if err != nil {
		return err
	}

	if correction == nil {
		if fixShellMode {
			return fmt.Errorf("no correction needed")
		}

		// No correction needed
		successStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981")).
			Render("✓")
		fmt.Printf("%s %s\n", successStyle, "This command looks correct!")

		// Suggest alternatives
		alternatives := c.SuggestAlternative(input)
		if len(alternatives) > 0 {
			fmt.Println()
			fmt.Println("Modern alternatives:")
			for _, alt := range alternatives {
				fmt.Printf("  • %s\n", ui.Cyan(alt))
			}
		}

		return nil
	}

	if correction.IsDangerous {
		if fixShellMode {
			return fmt.Errorf("dangerous command")
		}
		displayCorrection(correction)
		return nil
	}

	if fixShellMode {
		fmt.Println(strings.TrimSpace(correction.Corrected))
		return nil
	}

	// Display correction
	displayCorrection(correction)

	// Copy to clipboard if requested
	if fixCopy && correction.Corrected != "" {
		if err := clipboard.WriteAll(correction.Corrected); err != nil {
			return fmt.Errorf("failed to copy to clipboard: %w", err)
		}
		fmt.Printf("%s Copied to clipboard\n", ui.Success("✓"))
	}

	return nil
}

// looksLikeNaturalLanguage returns true when the input appears to be a
// human-language description rather than a shell command.
// Heuristic: it contains ≥ 2 "natural" words AND the first word is NOT a
// known root command.
func looksLikeNaturalLanguage(input string) bool {
	// Use a set for O(1) lookups instead of O(n) slice scans
	naturalTriggers := map[string]bool{
		"how": true, "list": true, "show": true, "what": true,
		"find": true, "get": true, "display": true, "where": true,
		"which": true, "delete": true, "remove": true, "stop": true,
		"restart": true, "enter": true, "open": true, "create": true,
		"check": true, "view": true, "search": true, "compress": true,
		"extract": true, "kill": true, "count": true, "run": true,
		"build": true, "print": true, "clean": true, "logs": true,
	}

	knownCommands := map[string]bool{
		"git": true, "docker": true, "kubectl": true, "npm": true,
		"go": true, "python": true, "pip": true, "curl": true,
		"ssh": true, "tar": true, "find": true, "grep": true,
		"ls": true, "rm": true, "cp": true, "mv": true, "cat": true,
		"systemctl": true, "apt": true, "brew": true, "cargo": true,
		"terraform": true, "aws": true, "gcloud": true, "helm": true,
		"wut": true,
	}

	words := strings.Fields(strings.ToLower(input))
	if len(words) < 2 {
		return false
	}

	// If first word is a known command, it's a shell command
	if knownCommands[words[0]] {
		return false
	}

	// O(1) lookup per word instead of O(n) inner loop
	for _, w := range words {
		if naturalTriggers[w] {
			return true
		}
	}
	return false
}

// runSemanticSearch uses the semantic engine to translate natural language
// into shell commands and displays ranked results.
func runSemanticSearch(query string) error {
	results, err := semanticMatches(query)
	if err != nil {
		fmt.Println()
		fmt.Println(ui.Yellow("🤔 No matching commands found for: ") + lipgloss.NewStyle().Bold(true).Render(query))
		fmt.Println("Try rephrasing, e.g: \"list running containers\" or \"undo last commit\"")
		return nil
	}

	fmt.Println()
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED"))
	fmt.Println(headerStyle.Render("🧠 Semantic Match: " + "\"" + query + "\""))
	fmt.Println()

	for i, match := range results {
		confColor := "#10B981"
		if match.Confidence < 0.7 {
			confColor = "#F59E0B"
		}
		if match.Confidence < 0.4 {
			confColor = "#6B7280"
		}

		numStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8B5CF6")).Bold(true)
		cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981")).Bold(true)
		descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))
		confStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(confColor))
		catStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6366F1"))

		fmt.Printf("  %s  %s\n",
			numStyle.Render(fmt.Sprintf("[%d]", i+1)),
			cmdStyle.Render(match.Intent.Command))
		fmt.Printf("     %s\n", descStyle.Render(match.Intent.Description))
		fmt.Printf("     %s  %s\n",
			catStyle.Render("#"+match.Intent.Category),
			confStyle.Render(fmt.Sprintf("%.0f%% match", match.Confidence*100)))
		fmt.Println()
	}

	return nil
}

func bestSemanticMatch(query string) (string, error) {
	results, err := semanticMatches(query)
	if err != nil {
		return "", err
	}
	return results[0].Intent.Command, nil
}

func semanticMatches(query string) ([]corrector.IntentMatch, error) {
	results := corrector.QuerySemantic(query, 5)
	if len(results) == 0 {
		return nil, fmt.Errorf("no semantic matches found")
	}
	return results, nil
}

func displayCorrection(c *corrector.Correction) {
	if c.IsDangerous {
		dangerStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#DC2626")).
			Padding(0, 1)

		fmt.Println()
		fmt.Println(dangerStyle.Render(" ⚠️  DANGEROUS COMMAND DETECTED "))
		fmt.Println()
		fmt.Println(c.Explanation)
		fmt.Println()

		warningBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#F59E0B")).
			Padding(1).
			Render("Never run this command unless you absolutely know what you're doing!")
		fmt.Println(warningBox)
		fmt.Println()
		return
	}

	// Normal correction
	fmt.Println()
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7C3AED"))
	fmt.Println(headerStyle.Render("🤔 Did you mean:"))
	fmt.Println()

	// Show original
	fmt.Printf("  Original:  %s\n", ui.Red(c.Original))

	// Show corrected
	if c.Corrected != "" {
		fmt.Printf("  Corrected: %s\n", ui.Green(c.Corrected))
	}

	// Show explanation
	if c.Explanation != "" {
		fmt.Println()
		infoStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))
		fmt.Printf("  %s\n", infoStyle.Render(c.Explanation))
	}

	// Show confidence
	fmt.Println()
	confidenceStr := fmt.Sprintf("Confidence: %.0f%%", c.Confidence*100)
	var confidenceColor lipgloss.Color
	switch {
	case c.Confidence >= 0.9:
		confidenceColor = lipgloss.Color("#10B981")
	case c.Confidence >= 0.7:
		confidenceColor = lipgloss.Color("#F59E0B")
	default:
		confidenceColor = lipgloss.Color("#6B7280")
	}
	confidenceStyle := lipgloss.NewStyle().
		Foreground(confidenceColor)
	fmt.Printf("  %s\n", confidenceStyle.Render(confidenceStr))

	fmt.Println()
}

func listCommonTypos() error {
	// Use a slice of examples since the new corrector uses a dynamic corpus
	examples := []struct {
		Typo    string
		Correct string
	}{
		{"gti comit", "git commit"},
		{"dockr buld", "docker build"},
		{"kubctl dpoly", "kubectl deploy"},
		{"terrform applay", "terraform apply"},
		{"npn isntall", "npm install"},
		{"systemtcl strat", "systemctl start"},
		{"cd..", "cd .."},
		{"grpe", "grep"},
	}

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7C3AED"))

	fmt.Println()
	fmt.Println(headerStyle.Render("📋 Core Typo Correction Patterns"))
	fmt.Println()

	for _, ex := range examples {
		fmt.Printf("  %s → %s\n",
			ui.Red(ex.Typo),
			ui.Green(ex.Correct))
	}

	fmt.Println()
	fmt.Println("WUT will automatically correct these when you use 'wut fix'")

	return nil
}
