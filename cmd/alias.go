// Package cmd provides alias management commands
package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"wut/internal/alias"
	appctx "wut/internal/context"
	"wut/internal/ui"
)

// aliasCmd manages shell aliases
var aliasCmd = &cobra.Command{
	Use:   "alias",
	Short: "Manage shell aliases",
	Long:  `View, add, and generate smart aliases for your shell.`,
	RunE:  runAlias,
}

var (
	aliasList     bool
	aliasAdd      bool
	aliasName     string
	aliasCommand  string
	aliasDesc     string
	aliasGenerate bool
	aliasApply    bool
	aliasShell    string
)

func init() {
	rootCmd.AddCommand(aliasCmd)

	aliasCmd.Flags().BoolVarP(&aliasList, "list", "l", false, "list all aliases")
	aliasCmd.Flags().BoolVarP(&aliasAdd, "add", "a", false, "add a new alias")
	aliasCmd.Flags().StringVarP(&aliasName, "name", "n", "", "alias name")
	aliasCmd.Flags().StringVarP(&aliasCommand, "command", "c", "", "alias command")
	aliasCmd.Flags().StringVar(&aliasDesc, "description", "", "alias description")
	aliasCmd.Flags().BoolVarP(&aliasGenerate, "generate", "g", false, "generate smart aliases for current project")
	aliasCmd.Flags().BoolVar(&aliasApply, "apply", false, "apply aliases to shell config")
	aliasCmd.Flags().StringVarP(&aliasShell, "shell", "s", "", "shell type (bash, zsh, fish)")
}

func runAlias(cmd *cobra.Command, args []string) error {
	// Detect shell
	shell := aliasShell
	if shell == "" {
		shell = detectShellForAlias()
	}

	manager := alias.NewManager(shell)
	_ = manager.Load() // Non-fatal, might be first run

	// Generate smart aliases
	if aliasGenerate {
		return generateAliases(manager)
	}

	// Apply aliases to shell
	if aliasApply {
		fmt.Printf("This will add WUT-managed aliases to your %s shell config.\n", shell)
		fmt.Print("Continue? [y/N]: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
			if answer != "y" && answer != "yes" {
				fmt.Println("Cancelled.")
				return nil
			}
		}
		backupPath, err := manager.ApplyToShell()
		if err != nil {
			return err
		}
		if backupPath != "" {
			fmt.Printf("✓ Backup created: %s\n", backupPath)
		}
		fmt.Println("✓ Aliases applied to shell config")
		return nil
	}

	// Add alias
	if aliasAdd {
		if aliasName == "" || aliasCommand == "" {
			return fmt.Errorf("--name and --command are required for adding aliases")
		}
		return manager.Add(aliasName, aliasCommand, aliasDesc, "custom")
	}

	// Default: list aliases
	return listAliases(manager)
}

func generateAliases(manager *alias.Manager) error {
	cmdCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	analyzer := appctx.NewAnalyzer()
	ctx, err := analyzer.Analyze(cmdCtx)
	if err != nil {
		return err
	}

	// Generate suggestions
	suggestions := manager.GenerateSmartAliases(ctx)

	if len(suggestions) == 0 {
		fmt.Println("No new alias suggestions for this project.")
		return nil
	}

	// Display suggestions
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7C3AED"))

	fmt.Println()
	fmt.Println(headerStyle.Render("✨ Suggested Aliases for Your Project"))
	fmt.Println()

	for _, a := range suggestions {
		nameStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#10B981")).
			Render(a.Name)

		fmt.Printf("  %s = %s\n", nameStyle, a.Command)
		fmt.Printf("     %s\n", a.Description)
		fmt.Println()
	}

	// Also show popular aliases
	fmt.Println(headerStyle.Render("📌 Popular General Aliases"))
	fmt.Println()

	popular := alias.GetPopularAliases()
	existing := manager.GetAll()

	var newPopular []alias.Alias
	for _, p := range popular {
		if _, exists := existing[p.Name]; !exists {
			newPopular = append(newPopular, p)
		}
	}

	for _, a := range newPopular[:min(5, len(newPopular))] {
		nameStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#3B82F6")).
			Render(a.Name)

		fmt.Printf("  %s = %s\n", nameStyle, a.Command)
		fmt.Printf("     %s\n", a.Description)
		fmt.Println()
	}

	fmt.Println("Use --add to add an alias, or --apply to apply all to your shell config.")

	return nil
}

func listAliases(manager *alias.Manager) error {
	aliases := manager.GetAll()

	if len(aliases) == 0 {
		fmt.Println("No aliases configured. Use --generate to create smart aliases.")
		return nil
	}

	// Group by category
	byCategory := make(map[string][]*alias.Alias)
	for _, a := range aliases {
		byCategory[a.Category] = append(byCategory[a.Category], a)
	}

	// Sort categories
	var categories []string
	for cat := range byCategory {
		categories = append(categories, cat)
	}
	sort.Strings(categories)

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7C3AED"))

	fmt.Println()

	for _, cat := range categories {
		catAliases := byCategory[cat]

		// Sort aliases by name
		sort.Slice(catAliases, func(i, j int) bool {
			return catAliases[i].Name < catAliases[j].Name
		})

		fmt.Println(headerStyle.Render(cases.Title(language.English).String(cat)))
		fmt.Println()

		for _, a := range catAliases {
			nameStyle := ui.Green(a.Name)
			fmt.Printf("  %s = %s\n", nameStyle, a.Command)
			if a.Description != "" {
				fmt.Printf("     %s\n", a.Description)
			}
		}
		fmt.Println()
	}

	return nil
}

func detectShellForAlias() string {
	// Check SHELL environment variable
	shell := os.Getenv("SHELL")
	if shell != "" {
		switch {
		case strings.Contains(shell, "bash"):
			return "bash"
		case strings.Contains(shell, "zsh"):
			return "zsh"
		case strings.Contains(shell, "fish"):
			return "fish"
		}
	}

	// Check platform
	if runtime.GOOS == "windows" {
		return "powershell"
	}

	// Default to bash
	return "bash"
}
