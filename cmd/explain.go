// Package cmd provides CLI commands for WUT
package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"wut/internal/config"
	"wut/internal/logger"
	"wut/internal/metrics"
	"wut/internal/ui"
)

// explainCmd represents the explain command
var explainCmd = &cobra.Command{
	Use:   "explain [command]",
	Short: "Explain a command",
	Long:  `Get a detailed explanation of what a command does, its flags, and potential risks.`,
	Example: `  wut explain "git rebase -i"
  wut explain "docker-compose up -d"
  wut explain "rm -rf /"`,
	RunE: runExplain,
}

var (
	explainVerbose   bool
	explainDangerous bool
)

func init() {
	rootCmd.AddCommand(explainCmd)

	explainCmd.Flags().BoolVarP(&explainVerbose, "verbose", "v", false, "show detailed explanation")
	explainCmd.Flags().BoolVar(&explainDangerous, "dangerous", false, "show dangerous command warnings")
}

func runExplain(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	log := logger.With("explain")

	if len(args) == 0 {
		return fmt.Errorf("please provide a command to explain")
	}

	command := strings.Join(args, " ")
	log.Debug("explaining command", "command", command)

	cfg := config.Get()

	// Parse the command
	parsed := parseCommand(command)

	// Generate explanation
	explanation, err := generateExplanation(ctx, parsed, cfg)
	if err != nil {
		log.Error("failed to generate explanation", "error", err)
		return fmt.Errorf("failed to explain command: %w", err)
	}

	// Display explanation
	if err := displayExplanation(explanation, cfg); err != nil {
		return err
	}

	// Record metrics
	metrics.RecordCommandExplained()

	return nil
}

// Explanation holds command explanation
type Explanation struct {
	Command      string
	Summary      string
	Description  string
	Arguments    []Argument
	Flags        []Flag
	Examples     []Example
	Warnings     []string
	Tips         []string
	IsDangerous  bool
	DangerLevel  string
	Alternatives []string
}

// Argument represents a command argument
type Argument struct {
	Name        string
	Description string
	Required    bool
	Default     string
}

// Flag represents a command flag
type Flag struct {
	Name        string
	Value       string
	Description string
	HasValue    bool
	IsShort     bool
}

// Example represents a usage example
type Example struct {
	Command     string
	Description string
}

// ParsedCommand represents a parsed command
type ParsedCommand struct {
	Command string
	Args    []string
	Flags   []ParsedFlag
	Raw     string
}

// ParsedFlag represents a parsed flag
type ParsedFlag struct {
	Name    string
	Value   string
	IsShort bool
}

func generateExplanation(ctx context.Context, parsed *ParsedCommand, cfg *config.Config) (*Explanation, error) {
	// This is a simplified implementation
	// In production, this would use a comprehensive command database

	explanation := &Explanation{
		Command:      parsed.Raw,
		Summary:      generateSummary(parsed),
		Description:  generateDescription(parsed),
		Arguments:    extractArguments(parsed),
		Flags:        extractFlagsV2(parsed),
		Examples:     generateExamples(parsed),
		Warnings:     generateWarnings(parsed),
		Tips:         generateTips(parsed),
		IsDangerous:  checkIfDangerous(parsed),
		DangerLevel:  calculateDangerLevel(parsed),
		Alternatives: generateAlternatives(parsed),
	}

	return explanation, nil
}

func displayExplanation(exp *Explanation, cfg *config.Config) error {
	// Use UI package for styled output
	uiRenderer := ui.NewRenderer(cfg.UI)

	// Print header
	uiRenderer.PrintHeader("Command Explanation")
	fmt.Println()

	// Print command
	fmt.Printf("Command: %s\n\n", ui.Cyan(exp.Command))

	// Print summary
	fmt.Printf("Summary: %s\n\n", exp.Summary)

	// Print description
	if exp.Description != "" {
		fmt.Printf("Description:\n%s\n\n", exp.Description)
	}

	// Print warnings for dangerous commands
	if exp.IsDangerous && (explainDangerous || cfg.UI.ShowExplanations) {
		fmt.Println(ui.Red("⚠️  WARNING: This command can be dangerous!"))
		fmt.Printf("Danger Level: %s\n\n", exp.DangerLevel)

		for _, warning := range exp.Warnings {
			fmt.Printf("  • %s\n", warning)
		}
		fmt.Println()
	}

	// Print arguments
	if len(exp.Arguments) > 0 {
		fmt.Println("Arguments:")
		for _, arg := range exp.Arguments {
			required := ""
			if arg.Required {
				required = ui.Red(" (required)")
			}
			fmt.Printf("  %s%s - %s\n", ui.Yellow(arg.Name), required, arg.Description)
		}
		fmt.Println()
	}

	// Print flags
	if len(exp.Flags) > 0 {
		fmt.Println("Flags:")
		for _, flag := range exp.Flags {
			flagStr := ""
			if flag.IsShort {
				flagStr += ui.Greenf("-%s", flag.Name)
			} else {
				flagStr += ui.Greenf("--%s", flag.Name)
			}
			fmt.Printf("  %s - %s\n", flagStr, flag.Description)
		}
		fmt.Println()
	}

	// Print examples
	if len(exp.Examples) > 0 {
		fmt.Println("Examples:")
		for _, ex := range exp.Examples {
			fmt.Printf("  $ %s\n", ui.Cyan(ex.Command))
			fmt.Printf("    %s\n", ex.Description)
		}
		fmt.Println()
	}

	// Print tips
	if len(exp.Tips) > 0 && cfg.UI.ShowExplanations {
		fmt.Println("Tips:")
		for _, tip := range exp.Tips {
			fmt.Printf("  💡 %s\n", tip)
		}
		fmt.Println()
	}

	// Print alternatives
	if len(exp.Alternatives) > 0 {
		fmt.Println("Alternatives:")
		for _, alt := range exp.Alternatives {
			fmt.Printf("  • %s\n", alt)
		}
		fmt.Println()
	}

	return nil
}

// Helper functions for explanation generation

func parseCommand(command string) *ParsedCommand {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return &ParsedCommand{Raw: command}
	}

	parsed := &ParsedCommand{
		Command: parts[0],
		Raw:     command,
	}

	for i := 1; i < len(parts); i++ {
		part := parts[i]
		if strings.HasPrefix(part, "--") {
			// Long flag
			flagParts := strings.SplitN(part, "=", 2)
			flag := ParsedFlag{
				Name:    strings.TrimPrefix(flagParts[0], "--"),
				IsShort: false,
			}
			if len(flagParts) > 1 {
				flag.Value = flagParts[1]
			}
			parsed.Flags = append(parsed.Flags, flag)
		} else if strings.HasPrefix(part, "-") && len(part) > 1 {
			// Short flag(s)
			if len(part) > 2 && part[2] != '=' {
				// Multiple short flags like -rf
				for j := 1; j < len(part); j++ {
					parsed.Flags = append(parsed.Flags, ParsedFlag{
						Name:    string(part[j]),
						IsShort: true,
					})
				}
			} else {
				// Single short flag with optional value
				flagParts := strings.SplitN(part, "=", 2)
				flag := ParsedFlag{
					Name:    strings.TrimPrefix(flagParts[0], "-"),
					IsShort: true,
				}
				if len(flagParts) > 1 {
					flag.Value = flagParts[1]
				}
				parsed.Flags = append(parsed.Flags, flag)
			}
		} else {
			// Regular argument
			parsed.Args = append(parsed.Args, part)
		}
	}

	return parsed
}

func generateSummary(parsed *ParsedCommand) string {
	if parsed.Command == "" {
		return "Unknown command"
	}

	// Build summary based on command
	return fmt.Sprintf("Executes %s", parsed.Command)
}

func generateDescription(parsed *ParsedCommand) string {
	// In production, this would look up from a command database
	return fmt.Sprintf("The %s command is used to perform operations.", parsed.Command)
}

func extractArguments(parsed *ParsedCommand) []Argument {
	var args []Argument
	for _, arg := range parsed.Args {
		args = append(args, Argument{
			Name:        arg,
			Description: "Command argument",
			Required:    true,
		})
	}
	return args
}

func extractFlagsV2(parsed *ParsedCommand) []Flag {
	var flags []Flag
	for _, f := range parsed.Flags {
		flags = append(flags, Flag{
			Name:        f.Name,
			Value:       f.Value,
			Description: "Command flag",
			HasValue:    f.Value != "",
			IsShort:     f.IsShort,
		})
	}
	return flags
}

func generateExamples(parsed *ParsedCommand) []Example {
	return []Example{
		{
			Command:     parsed.Raw,
			Description: "Basic usage",
		},
	}
}

func generateWarnings(parsed *ParsedCommand) []string {
	var warnings []string

	// Check for dangerous patterns
	cmd := strings.ToLower(parsed.Raw)

	if strings.Contains(cmd, "rm -rf") || strings.Contains(cmd, "rm -r -f") {
		warnings = append(warnings, "This will recursively and forcefully delete files")
		warnings = append(warnings, "Deleted files cannot be easily recovered")
	}

	if strings.Contains(cmd, "> /dev/") || strings.Contains(cmd, "> /") {
		warnings = append(warnings, "This may overwrite system files")
	}

	if strings.Contains(cmd, "chmod -R 777") || strings.Contains(cmd, "chmod -R 666") {
		warnings = append(warnings, "This gives everyone full permissions to files")
	}

	if strings.Contains(cmd, "mkfs") || strings.Contains(cmd, "dd if=") {
		warnings = append(warnings, "This can destroy data on storage devices")
	}

	return warnings
}

func generateTips(parsed *ParsedCommand) []string {
	var tips []string

	cmd := strings.ToLower(parsed.Command)

	if cmd == "rm" {
		tips = append(tips, "Use 'rm -i' for interactive mode to confirm each deletion")
		tips = append(tips, "Consider using 'trash' command instead for safer deletion")
	}

	if cmd == "git" {
		tips = append(tips, "Use 'git status' before committing to review changes")
	}

	return tips
}

func checkIfDangerous(parsed *ParsedCommand) bool {
	cmd := strings.ToLower(parsed.Raw)

	dangerousPatterns := []string{
		"rm -rf /",
		"rm -rf *",
		"mkfs",
		"dd if=/dev/zero",
		"> /dev/",
		":(){ :|:& };:",
		"chmod -R 777 /",
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(cmd, pattern) {
			return true
		}
	}

	return false
}

func calculateDangerLevel(parsed *ParsedCommand) string {
	if !checkIfDangerous(parsed) {
		return "safe"
	}

	cmd := strings.ToLower(parsed.Raw)

	if strings.Contains(cmd, "rm -rf /") ||
		strings.Contains(cmd, "mkfs") ||
		strings.Contains(cmd, ":(){ :|:& }:") {
		return "critical"
	}

	if strings.Contains(cmd, "rm -rf") {
		return "high"
	}

	return "medium"
}

func generateAlternatives(parsed *ParsedCommand) []string {
	cmd := strings.ToLower(parsed.Command)

	alternatives := map[string][]string{
		"rm": {
			"Use 'trash' command to move files to trash instead of deleting",
			"Use 'rm -i' for interactive deletion",
		},
		"cp": {
			"Use 'rsync' for better progress and resume capability",
		},
	}

	if alts, ok := alternatives[cmd]; ok {
		return alts
	}

	return nil
}
