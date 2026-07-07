// Package cmd provides smart command suggestions
package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"wut/internal/config"
	appctx "wut/internal/context"
	"wut/internal/corrector"
	"wut/internal/db"
	"wut/internal/logger"
	"wut/internal/smart"
	"wut/internal/ui"
)

// smartCmd provides intelligent, context-aware command suggestions
var smartCmd = &cobra.Command{
	Use:   "smart [query]",
	Short: "Smart command suggestions based on context",
	Long: `Get intelligent command suggestions based on your project context,
command history, and current directory. WUT will detect your project type
and suggest the most relevant commands.`,
	Example: `  wut smart
  wut smart git
  wut smart "docker build"`,
	RunE: runSmart,
}

var (
	smartLimit   int
	smartCorrect bool
)

func init() {
	rootCmd.AddCommand(smartCmd)

	smartCmd.Flags().IntVarP(&smartLimit, "limit", "l", 0, "maximum suggestions to show (0 = unlimited)")
	smartCmd.Flags().BoolVarP(&smartCorrect, "correct", "c", true, "auto-correct typos")
}

func runSmart(cmd *cobra.Command, args []string) error {
	// Use shorter timeout to ensure responsiveness
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	log := logger.With("smart")

	// Get query from args
	query := ""
	if len(args) > 0 {
		query = strings.Join(args, " ")
	}

	// Detect context with timeout
	analyzer := appctx.NewAnalyzer()
	appCtx, err := analyzer.Analyze(ctx)
	if err != nil {
		log.Warn("failed to detect context", "error", err)
		appCtx = &appctx.Context{
			WorkingDir:  ".",
			ProjectType: "unknown",
		}
	}

	// Initialize storage once and reuse it for correction, ranking and history.
	storage := openSmartStorage(log)
	if storage != nil {
		defer storage.Close()
		hydrateHistoryFromShell(context.Background(), storage)
	}

	// Check for typos if enabled
	if smartCorrect && query != "" {
		c := corrector.New()

		// Optional: supply history to corrector for better matching
		if storage != nil {
			if history, err := storage.GetHistory(context.Background(), 100); err == nil {
				historyCmds := make([]string, 0, len(history))
				for _, h := range history {
					if strings.HasPrefix(strings.ToLower(strings.TrimSpace(h.Command)), "wut ") {
						continue
					}
					historyCmds = append(historyCmds, h.Command)
				}
				c.SetHistoryCommands(historyCmds)
			}
		}

		if correction, err := c.Correct(query); err == nil && correction != nil {
			if correction.IsDangerous {
				printCorrection(correction)
				return nil // Don't proceed with dangerous commands
			}
			if shouldApplySmartCorrection(query, correction) {
				printCorrection(correction)
				query = correction.Corrected
			}
		}
	}

	// Create smart engine
	engine := smart.NewEngine(storage)
	fetchLimit := smartLimit
	if fetchLimit > 0 && fetchLimit < 120 {
		fetchLimit = 120
	}

	// Get intelligent suggestions with timeout
	suggestionsCh := make(chan []smart.Suggestion, 1)
	var suggestErr error

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic in suggest", "recover", r)
			}
		}()
		sugs, err := engine.Suggest(ctx, query, appCtx, fetchLimit)
		if err != nil {
			suggestErr = err
			return
		}
		// Use select so the goroutine exits even if the caller already timed out.
		select {
		case suggestionsCh <- sugs:
		case <-ctx.Done():
		}
	}()

	var suggestions []smart.Suggestion
	select {
	case suggestions = <-suggestionsCh:
		// Got suggestions
	case <-ctx.Done():
		log.Warn("suggestion timeout, using fallback")
		suggestions = engine.GetFallbackSuggestions(appCtx, smartLimit)
	}

	if suggestErr != nil {
		log.Error("failed to get suggestions", "error", suggestErr)
		// Try fallback
		suggestions = engine.GetFallbackSuggestions(appCtx, smartLimit)
	}

	// Display suggestions
	if len(suggestions) == 0 {
		// Always show fallback suggestions instead of empty
		suggestions = engine.GetFallbackSuggestions(appCtx, smartLimit)
	}

	return showSmartSuggestions(query, appCtx, suggestions)
}

func openSmartStorage(log *logger.Logger) *db.Storage {
	storageCh := make(chan *db.Storage, 1)
	storageErrCh := make(chan error, 1)

	go func() {
		s, err := db.NewStorage(config.GetDatabasePath())
		if err != nil {
			storageErrCh <- err
			return
		}
		storageCh <- s
	}()

	select {
	case storage := <-storageCh:
		return storage
	case err := <-storageErrCh:
		log.Warn("failed to initialize storage, continuing without history", "error", err)
	case <-time.After(500 * time.Millisecond):
		log.Warn("storage initialization timeout, continuing without history")
	}

	return nil
}

func printCorrection(c *corrector.Correction) {
	if c.IsDangerous {
		warningStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#EF4444")).
			Background(lipgloss.Color("#FEE2E2"))
		fmt.Println(warningStyle.Render(" " + c.Explanation + " "))
		fmt.Println()
		return
	}

	if c.Corrected != "" && c.Corrected != c.Original {
		correctionStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B"))
		fmt.Printf("%s %s → %s\n\n",
			correctionStyle.Render("🤔 Did you mean:"),
			c.Original,
			ui.Green(c.Corrected))
	}
}

func shouldApplySmartCorrection(original string, correction *corrector.Correction) bool {
	if correction == nil {
		return false
	}

	corrected := strings.TrimSpace(correction.Corrected)
	original = strings.TrimSpace(original)
	if corrected == "" || corrected == original {
		return false
	}

	originalRoot := strings.ToLower(strings.TrimSpace(firstToken(original)))
	correctedRoot := strings.ToLower(strings.TrimSpace(firstToken(corrected)))

	if originalRoot != "wut" && correctedRoot == "wut" {
		return false
	}

	return true
}

func firstToken(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
