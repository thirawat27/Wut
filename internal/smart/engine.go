// Package smart provides intelligent command suggestions
package smart

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"wut/internal/commandsearch"
	appctx "wut/internal/context"
	"wut/internal/db"
	"wut/internal/historyml"
	"wut/internal/performance"
	"wut/internal/shell"
)

// Engine provides intelligent command suggestions
type Engine struct {
	storage      *db.Storage
	matcher      *performance.FastMatcher
	cache        *performance.LRUCache[string, []Suggestion]
	ctxCache     *performance.LRUCache[string, *appctx.Context]
	index        *performance.InvertedIndex
	autocomplete *performance.Autocomplete

	// Scoring weights
	weights ScoringWeights

	mu sync.RWMutex
}

// ScoringWeights holds scoring weights for ranking
type ScoringWeights struct {
	ExactMatch       float64
	PrefixMatch      float64
	ContainsMatch    float64
	FuzzyMatch       float64
	HistoryFreq      float64
	Recency          float64
	ContextRelevance float64
}

// DefaultScoringWeights returns default weights
func DefaultScoringWeights() ScoringWeights {
	return ScoringWeights{
		ExactMatch:       1.0,
		PrefixMatch:      0.9,
		ContainsMatch:    0.7,
		FuzzyMatch:       0.5,
		HistoryFreq:      0.3,
		Recency:          0.2,
		ContextRelevance: 0.4,
	}
}

// Suggestion represents a command suggestion
type Suggestion struct {
	Command        string
	Description    string
	Score          float64
	Source         string
	Icon           string
	UsageCount     int
	LastUsed       time.Time
	ContextMatch   float64
	IsPerfectMatch bool
}

// NewEngine creates a new smart engine
func NewEngine(storage *db.Storage) *Engine {
	return &Engine{
		storage:      storage,
		matcher:      performance.NewFastMatcher(false, 0.3, 3),
		cache:        performance.NewLRUCache[string, []Suggestion](1000, 32),
		ctxCache:     performance.NewLRUCache[string, *appctx.Context](100, 8),
		index:        performance.NewInvertedIndex(),
		autocomplete: performance.NewAutocomplete(100),
		weights:      DefaultScoringWeights(),
	}
}

func safeGo(fn func()) {
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "WUT smart engine: recovered panic: %v\n%s", r, debug.Stack())
			}
		}()
		fn()
	}()
	_ = wg
}

// SetWeights sets custom scoring weights
func (e *Engine) SetWeights(weights ScoringWeights) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.weights = weights
}

// Suggest returns intelligent command suggestions
func (e *Engine) Suggest(ctx context.Context, query string, contextData *appctx.Context, limit int) ([]Suggestion, error) {
	if limit <= 0 {
		limit = 10
	}
	if contextData == nil {
		contextData = &appctx.Context{ProjectType: "unknown"}
	}

	// Check cache for exact query
	cacheKey := query + ":" + contextData.ProjectType
	if cached, ok := e.cache.Get(cacheKey); ok {
		return e.limitSuggestions(cached, limit), nil
	}

	// Every source runs concurrently and reports through one channel. Naming
	// each source keeps a panic traceable to the source that raised it.
	sources := []suggestionSource{
		{"history", func() []Suggestion { return e.getHistorySuggestions(ctx, query, limit) }},
		{"context", func() []Suggestion { return e.getContextSuggestions(contextData, query) }},
		{"workflow", func() []Suggestion { return e.getWorkflowSuggestions(contextData, query) }},
		{"fuzzy", func() []Suggestion { return e.getFuzzySuggestions(query, limit) }},
		{"catalog", func() []Suggestion { return e.getCatalogSuggestions(ctx, query, limit) }},
	}

	suggestionChan := make(chan []Suggestion, len(sources))
	var wg sync.WaitGroup

	for _, source := range sources {
		wg.Add(1)
		go func(src suggestionSource) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "WUT smart engine %s: recovered panic: %v\n%s", src.name, r, debug.Stack())
				}
			}()
			select {
			case suggestionChan <- src.collect():
			case <-ctx.Done():
			}
		}(source)
	}

	// Close channel when done
	go func() {
		wg.Wait()
		close(suggestionChan)
	}()

	// Collect and deduplicate with context check
	suggestionMap := make(map[string]Suggestion)
	complete := false
	for !complete {
		select {
		case suggestions, ok := <-suggestionChan:
			if !ok {
				// Channel closed, all sources reported
				complete = true
				break
			}
			for _, s := range suggestions {
				if existing, ok := suggestionMap[s.Command]; ok {
					suggestionMap[s.Command] = mergeSuggestion(existing, s)
				} else {
					suggestionMap[s.Command] = s
				}
			}
		case <-ctx.Done():
			// Context cancelled/timed out, return what we have
			return e.finishPartial(suggestionMap, query, contextData, limit), nil
		}
	}

	// Convert to slice and sort
	results := make([]Suggestion, 0, len(suggestionMap))
	for _, s := range suggestionMap {
		results = append(results, s)
	}

	// Score and sort
	results = e.scoreAndSort(results, query, contextData)

	// Only a complete result set is cached. Caching a set truncated by a
	// cancelled context would serve those degraded results for the next 30s,
	// long after the condition that caused the truncation had passed.
	e.cache.Set(cacheKey, results, 30*time.Second)

	return e.limitSuggestions(results, limit), nil
}

// suggestionSource pairs a source name with its collector so the fan-out loop
// stays one block instead of one copy-pasted goroutine per source.
type suggestionSource struct {
	name    string
	collect func() []Suggestion
}

// finishPartial sorts and limits whatever was collected before the context
// ended. The result is returned but deliberately not cached.
func (e *Engine) finishPartial(collected map[string]Suggestion, query string, contextData *appctx.Context, limit int) []Suggestion {
	results := make([]Suggestion, 0, len(collected))
	for _, s := range collected {
		results = append(results, s)
	}
	results = e.scoreAndSort(results, query, contextData)
	return e.limitSuggestions(results, limit)
}

// getHistorySuggestions gets suggestions from command history sequentially
func (e *Engine) getHistorySuggestions(ctx context.Context, query string, limit int) []Suggestion {
	if e.storage == nil {
		return nil
	}

	select {
	case <-ctx.Done():
		return nil
	default:
	}

	if strings.TrimSpace(query) != "" {
		return e.getHistoryLogSuggestions(ctx, query, limit)
	}

	return e.getHistorySummarySuggestions(ctx, limit)
}

func (e *Engine) getHistorySummarySuggestions(ctx context.Context, limit int) []Suggestion {
	scanLimit := 0
	if limit > 0 && limit < 100 {
		scanLimit = limit * 400
		if scanLimit < 800 {
			scanLimit = 800
		}
	}

	summaries, err := e.storage.GetHistoryCommandSummaries(ctx, scanLimit)
	if err != nil || len(summaries) == 0 {
		return nil
	}

	ranker := historyml.Train(historySummariesToSamples(summaries), time.Now())
	currentShell := shell.DetectCurrentShell()
	currentOS := runtime.GOOS

	suggestions := make([]Suggestion, 0, len(summaries))
	for _, summary := range summaries {
		profile := commandsearch.BuildProfile(summary.Command)

		score := historySummaryBoost(summary, ranker)
		score += historySummarySourceBoost(summary, currentOS, currentShell)

		description := historySummaryDescription(summary, profile)
		contextMatch := 0.0
		if summary.SourceOS == currentOS || summary.SourceShell == currentShell {
			contextMatch = 0.35
		}

		suggestions = append(suggestions, Suggestion{
			Command:      summary.Command,
			Description:  description,
			Score:        score,
			Source:       "🌌 Smart History",
			Icon:         "🕘",
			UsageCount:   summary.UsageCount,
			LastUsed:     summary.LastUsed,
			ContextMatch: contextMatch,
		})
	}

	return suggestions
}

func (e *Engine) getHistoryLogSuggestions(ctx context.Context, query string, limit int) []Suggestion {
	if e.storage == nil {
		return nil
	}
	searchLimit := 0
	if limit > 0 {
		searchLimit = limit * 50
		if searchLimit < 150 {
			searchLimit = 150
		}
		if searchLimit > 500 {
			searchLimit = 500
		}
	}

	matches, err := e.storage.SearchHistoryMatches(ctx, query, searchLimit)
	if err != nil || len(matches) == 0 {
		return nil
	}

	currentShell := shell.DetectCurrentShell()
	currentOS := runtime.GOOS
	queryProfile := commandsearch.ParseQuery(query)
	suggestionMap := make(map[string]Suggestion, len(matches))

	for idx, match := range matches {
		entry := match.Entry
		profile := commandsearch.BuildProfile(entry.Command)
		if shouldSuppressSmartHistoryCommand(queryProfile, entry.Command, profile) {
			continue
		}

		suggestion, ok := suggestionMap[entry.Command]
		if !ok {
			contextMatch := 0.0
			if entry.SourceOS == currentOS || entry.Shell == currentShell {
				contextMatch = 0.35
			}

			suggestion = Suggestion{
				Command:      entry.Command,
				Description:  historyLogDescription(1, entry.Timestamp, profile),
				Score:        historyLogBaseScore(match.Score, idx),
				Source:       "🌌 Smart History",
				Icon:         "🕘",
				UsageCount:   1,
				LastUsed:     entry.Timestamp,
				ContextMatch: contextMatch,
			}
		} else {
			suggestion.UsageCount++
			if entry.Timestamp.After(suggestion.LastUsed) {
				suggestion.LastUsed = entry.Timestamp
			}
			suggestion.Score += historyLogRepeatBoost(match.Score, idx)
		}

		suggestion.Score += historyEntrySourceBoost(entry, currentOS, currentShell)
		suggestion.Description = historyLogDescription(suggestion.UsageCount, suggestion.LastUsed, profile)
		suggestionMap[entry.Command] = suggestion
	}

	results := make([]Suggestion, 0, len(suggestionMap))
	for _, suggestion := range suggestionMap {
		results = append(results, suggestion)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			if results[i].UsageCount == results[j].UsageCount {
				return results[i].LastUsed.After(results[j].LastUsed)
			}
			return results[i].UsageCount > results[j].UsageCount
		}
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit*3 {
		results = results[:limit*3]
	}

	return results
}

// (Legacy method removed, handled via unified scoring above)

// getContextSuggestions gets context-specific suggestions
func (e *Engine) getContextSuggestions(ctx *appctx.Context, query string) []Suggestion {
	var suggestions []Suggestion

	// Define project-type specific commands
	projectCommands := map[string][]Suggestion{
		"go": {
			{Command: "go mod tidy", Description: "Clean up module dependencies", Source: "🎯 Context", Icon: "📦"},
			{Command: "go test ./...", Description: "Run all tests", Source: "🎯 Context", Icon: "🧪"},
			{Command: "go build ./...", Description: "Build all packages", Source: "🎯 Context", Icon: "🔨"},
			{Command: "go run .", Description: "Run current package", Source: "🎯 Context", Icon: "▶️"},
			{Command: "go fmt ./...", Description: "Format all Go files", Source: "🎯 Context", Icon: "✨"},
			{Command: "go vet ./...", Description: "Run static analysis", Source: "🎯 Context", Icon: "🔍"},
			{Command: "go mod download", Description: "Download dependencies", Source: "🎯 Context", Icon: "⬇️"},
			{Command: "go generate ./...", Description: "Run code generation", Source: "🎯 Context", Icon: "⚙️"},
		},
		"nodejs": {
			{Command: "npm install", Description: "Install dependencies", Source: "🎯 Context", Icon: "📦"},
			{Command: "npm run dev", Description: "Start dev server", Source: "🎯 Context", Icon: "🚀"},
			{Command: "npm run build", Description: "Build for production", Source: "🎯 Context", Icon: "🔨"},
			{Command: "npm test", Description: "Run test suite", Source: "🎯 Context", Icon: "🧪"},
			{Command: "npm run lint", Description: "Run linter", Source: "🎯 Context", Icon: "✨"},
			{Command: "npm run start", Description: "Start production server", Source: "🎯 Context", Icon: "▶️"},
			{Command: "npm outdated", Description: "Check outdated packages", Source: "🎯 Context", Icon: "📋"},
			{Command: "npm audit fix", Description: "Fix security issues", Source: "🎯 Context", Icon: "🔒"},
		},
		"python": {
			{Command: "pip install -r requirements.txt", Description: "Install dependencies", Source: "🎯 Context", Icon: "📦"},
			{Command: "python -m pytest", Description: "Run tests", Source: "🎯 Context", Icon: "🧪"},
			{Command: "python -m venv venv", Description: "Create virtual environment", Source: "🎯 Context", Icon: "🐍"},
			{Command: "source venv/bin/activate", Description: "Activate virtual environment", Source: "🎯 Context", Icon: "⚡"},
			{Command: "pip freeze > requirements.txt", Description: "Save dependencies", Source: "🎯 Context", Icon: "💾"},
			{Command: "black .", Description: "Format Python code", Source: "🎯 Context", Icon: "✨"},
			{Command: "flake8 .", Description: "Lint Python code", Source: "🎯 Context", Icon: "🔍"},
		},
		"docker": {
			{Command: "docker-compose up -d", Description: "Start services", Source: "🎯 Context", Icon: "🐳"},
			{Command: "docker-compose down", Description: "Stop services", Source: "🎯 Context", Icon: "🛑"},
			{Command: "docker-compose logs -f", Description: "Follow logs", Source: "🎯 Context", Icon: "📋"},
			{Command: "docker build -t myapp .", Description: "Build image", Source: "🎯 Context", Icon: "🔨"},
			{Command: "docker ps", Description: "List running containers", Source: "🎯 Context", Icon: "📊"},
			{Command: "docker images", Description: "List images", Source: "🎯 Context", Icon: "🖼️"},
			{Command: "docker system prune", Description: "Clean up resources", Source: "🎯 Context", Icon: "🧹"},
		},
		"git": {
			{Command: "git status", Description: "Check repository status", Source: "🎯 Context", Icon: "📊"},
			{Command: "git add .", Description: "Stage all changes", Source: "🎯 Context", Icon: "➕"},
			{Command: "git commit -m \"message\"", Description: "Commit changes", Source: "🎯 Context", Icon: "💾"},
			{Command: "git push", Description: "Push to remote", Source: "🎯 Context", Icon: "🚀"},
			{Command: "git pull", Description: "Pull from remote", Source: "🎯 Context", Icon: "⬇️"},
			{Command: "git log --oneline -10", Description: "View recent commits", Source: "🎯 Context", Icon: "📜"},
			{Command: "git branch", Description: "List branches", Source: "🎯 Context", Icon: "🌿"},
			{Command: "git diff", Description: "Show changes", Source: "🎯 Context", Icon: "📝"},
		},
		"rust": {
			{Command: "cargo build", Description: "Build project", Source: "🎯 Context", Icon: "🔨"},
			{Command: "cargo test", Description: "Run tests", Source: "🎯 Context", Icon: "🧪"},
			{Command: "cargo run", Description: "Run project", Source: "🎯 Context", Icon: "▶️"},
			{Command: "cargo check", Description: "Check code", Source: "🎯 Context", Icon: "✅"},
			{Command: "cargo clippy", Description: "Run linter", Source: "🎯 Context", Icon: "🔍"},
			{Command: "cargo fmt", Description: "Format code", Source: "🎯 Context", Icon: "✨"},
			{Command: "cargo update", Description: "Update dependencies", Source: "🎯 Context", Icon: "🔄"},
		},
	}

	// Get commands for current project type
	if cmds, ok := projectCommands[ctx.ProjectType]; ok {
		for _, cmd := range cmds {
			cmd.ContextMatch = 1.0
			suggestions = append(suggestions, cmd)
		}
	}

	// Git commands for git repos
	if ctx.IsGitRepo {
		if cmds, ok := projectCommands["git"]; ok {
			for _, cmd := range cmds {
				cmd.ContextMatch = maxFloat64(cmd.ContextMatch, 0.9)
				suggestions = append(suggestions, cmd)
			}
		}
	}

	// Filter by query
	if query == "" {
		return suggestions
	}

	return e.filterSuggestions(suggestions, query)
}

// getWorkflowSuggestions gets common workflow suggestions
func (e *Engine) getWorkflowSuggestions(ctx *appctx.Context, query string) []Suggestion {
	var suggestions []Suggestion

	// Quick actions based on context
	if ctx.IsGitRepo {
		if len(ctx.GitStatus.ModifiedFiles) > 0 || len(ctx.GitStatus.StagedFiles) > 0 {
			suggestions = append(suggestions, Suggestion{
				Command:      "git add . && git commit -m \"update\"",
				Description:  "Quick commit all changes",
				Source:       "⚡ Quick",
				Icon:         "⚡",
				ContextMatch: 0.8,
			})
		}
		if ctx.GitStatus.Ahead > 0 {
			suggestions = append(suggestions, Suggestion{
				Command:      "git push",
				Description:  "Push commits to remote",
				Source:       "⚡ Quick",
				Icon:         "🚀",
				ContextMatch: 0.9,
			})
		}
	}

	// Filter by query
	if query == "" {
		return suggestions
	}

	return e.filterSuggestions(suggestions, query)
}

// getCatalogSuggestions broadens discovery using the local command catalog and
// TLDR database so smart search can surface commands the user has not used yet.
func (e *Engine) getCatalogSuggestions(ctx context.Context, query string, limit int) []Suggestion {
	if e.storage == nil || strings.TrimSpace(query) == "" {
		return nil
	}

	suggestionMap := make(map[string]Suggestion)
	addSuggestion := func(s Suggestion) {
		if existing, ok := suggestionMap[s.Command]; ok {
			suggestionMap[s.Command] = mergeSuggestion(existing, s)
			return
		}
		suggestionMap[s.Command] = s
	}

	commands, err := e.storage.ListCommands(0)
	if err == nil {
		for _, match := range e.matcher.MatchMultiple(query, commands) {
			addSuggestion(Suggestion{
				Command:      match.Target,
				Description:  "Available in local command reference",
				Score:        0.8 + match.Score,
				Source:       "📚 Command DB",
				Icon:         "📚",
				ContextMatch: 0.15,
			})
			if limit > 0 && len(suggestionMap) >= limit*4 {
				break
			}
		}
	}

	searchPageLimit := 0
	if limit > 0 {
		searchPageLimit = limit * 6
	}
	pages, err := e.storage.SearchLocalLimited(query, searchPageLimit)
	if err == nil {
		for _, page := range pages {
			match := e.matcher.Match(strings.ToLower(query), strings.ToLower(page.Name+" "+page.Description))
			score := 0.6
			if match.Matched {
				score += match.Score
			}
			addSuggestion(Suggestion{
				Command:      page.Name,
				Description:  page.Description,
				Score:        score,
				Source:       "📚 Command DB",
				Icon:         "📚",
				ContextMatch: 0.25,
			})
		}
	}

	results := make([]Suggestion, 0, len(suggestionMap))
	for _, suggestion := range suggestionMap {
		results = append(results, suggestion)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit*3 {
		results = results[:limit*3]
	}
	return results
}

// getFuzzySuggestions gets fuzzy-matched suggestions from common commands
func (e *Engine) getFuzzySuggestions(query string, limit int) []Suggestion {
	if query == "" {
		return nil
	}

	commonCommands := []string{
		"git", "docker", "kubectl", "npm", "yarn", "cargo", "go",
		"ls", "cd", "pwd", "cat", "grep", "find", "awk", "sed",
		"ssh", "scp", "curl", "wget", "ping", "netstat",
		"tar", "zip", "unzip", "gzip",
		"chmod", "chown", "mkdir", "rm", "cp", "mv",
		"ps", "top", "htop", "kill",
		"vim", "nvim", "code", "nano",
		"make", "cmake", "gcc", "g++",
	}

	results := e.matcher.MatchMultiple(query, commonCommands)

	suggestions := make([]Suggestion, 0, len(results))
	for _, r := range results {
		suggestions = append(suggestions, Suggestion{
			Command:      r.Target,
			Score:        r.Score * e.weights.FuzzyMatch,
			Source:       "🔍 Fuzzy",
			Icon:         "🔍",
			ContextMatch: 0.1,
		})
	}

	return suggestions
}

// filterSuggestions filters suggestions by query
func (e *Engine) filterSuggestions(suggestions []Suggestion, query string) []Suggestion {
	if query == "" {
		return suggestions
	}

	queryLower := strings.ToLower(query)
	var filtered []Suggestion

	for _, s := range suggestions {
		cmdLower := strings.ToLower(s.Command)
		descLower := strings.ToLower(s.Description)
		cmdMatch := e.matcher.Match(queryLower, cmdLower)
		descMatch := e.matcher.Match(queryLower, descLower)

		if cmdMatch.Matched || descMatch.Matched || strings.Contains(cmdLower, queryLower) || strings.Contains(descLower, queryLower) {
			if strings.HasPrefix(cmdLower, queryLower) {
				s.Score += e.weights.PrefixMatch
			} else if strings.Contains(cmdLower, queryLower) {
				s.Score += e.weights.ContainsMatch
			}
			s.Score += maxFloat64(cmdMatch.Score, descMatch.Score*0.6) * e.weights.FuzzyMatch
			filtered = append(filtered, s)
		}
	}

	return filtered
}

// scoreAndSort scores and sorts suggestions
func (e *Engine) scoreAndSort(suggestions []Suggestion, query string, ctx *appctx.Context) []Suggestion {
	// Score each suggestion
	for i := range suggestions {
		suggestions[i] = e.calculateFinalScore(suggestions[i], query, ctx)
	}

	// Sort by score (descending)
	sort.Slice(suggestions, func(i, j int) bool {
		if suggestions[i].Score == suggestions[j].Score {
			if suggestions[i].UsageCount == suggestions[j].UsageCount {
				return suggestions[i].LastUsed.After(suggestions[j].LastUsed)
			}
			return suggestions[i].UsageCount > suggestions[j].UsageCount
		}
		return suggestions[i].Score > suggestions[j].Score
	})

	return suggestions
}

// calculateFinalScore calculates the final score for a suggestion
func (e *Engine) calculateFinalScore(s Suggestion, query string, ctx *appctx.Context) Suggestion {
	score := s.Score

	// Boost perfect matches
	if query != "" && strings.EqualFold(s.Command, query) {
		score += e.weights.ExactMatch
		s.IsPerfectMatch = true
	} else if query != "" {
		match := e.matcher.Match(query, s.Command)
		if match.Matched {
			score += match.Score * e.weights.FuzzyMatch
			if match.MatchStart == 0 {
				score += e.weights.PrefixMatch * 0.5
			}
		}
	}

	// Context relevance boost
	score += s.ContextMatch * e.weights.ContextRelevance

	if s.UsageCount > 0 {
		score += math.Min(1.0, math.Log1p(float64(s.UsageCount))/3.0) * e.weights.HistoryFreq
	}

	if !s.LastUsed.IsZero() {
		hoursSince := time.Since(s.LastUsed).Hours()
		switch {
		case hoursSince < 24:
			score += e.weights.Recency
		case hoursSince < 24*7:
			score += e.weights.Recency * 0.6
		case hoursSince < 24*30:
			score += e.weights.Recency * 0.3
		}
	}

	s.Score = score
	return s
}

// limitSuggestions limits the number of suggestions
func (e *Engine) limitSuggestions(suggestions []Suggestion, limit int) []Suggestion {
	if limit <= 0 {
		return suggestions
	}
	if len(suggestions) <= limit {
		return suggestions
	}
	return suggestions[:limit]
}

// formatCount formats a count for display
func formatCount(n int) string {
	if n == 1 {
		return "1 time"
	}
	return fmt.Sprintf("%d times", n)
}

func mergeSuggestion(existing, incoming Suggestion) Suggestion {
	existing.Score += incoming.Score
	existing.UsageCount = maxInt(existing.UsageCount, incoming.UsageCount)
	if incoming.LastUsed.After(existing.LastUsed) {
		existing.LastUsed = incoming.LastUsed
	}
	existing.ContextMatch = maxFloat64(existing.ContextMatch, incoming.ContextMatch)
	existing.IsPerfectMatch = existing.IsPerfectMatch || incoming.IsPerfectMatch

	if existing.Description == "" || (incoming.Description != "" && len(incoming.Description) < len(existing.Description)) {
		existing.Description = incoming.Description
	}
	if existing.Icon == "" && incoming.Icon != "" {
		existing.Icon = incoming.Icon
	}
	existing.Source = mergeSourceLabels(existing.Source, incoming.Source)
	return existing
}

func mergeSourceLabels(existing, incoming string) string {
	if existing == "" {
		return incoming
	}
	if incoming == "" || existing == incoming {
		return existing
	}

	parts := strings.Split(existing, " + ")
	for _, part := range parts {
		if part == incoming {
			return existing
		}
	}
	return existing + " + " + incoming
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func historySummariesToSamples(summaries []db.HistoryCommandSummary) []historyml.CommandSample {
	samples := make([]historyml.CommandSample, 0, len(summaries))
	for _, summary := range summaries {
		samples = append(samples, historyml.CommandSample{
			Command:     summary.Command,
			UsageCount:  summary.UsageCount,
			LastUsed:    summary.LastUsed,
			SourceOS:    summary.SourceOS,
			SourceShell: summary.SourceShell,
		})
	}
	return samples
}

func historySummaryBoost(summary db.HistoryCommandSummary, ranker *historyml.Ranker) float64 {
	score := math.Log1p(float64(summary.UsageCount)) * 0.85
	if !summary.LastUsed.IsZero() {
		hoursSince := time.Since(summary.LastUsed).Hours()
		switch {
		case hoursSince < 24:
			score += 0.9
		case hoursSince < 24*7:
			score += 0.55
		case hoursSince < 24*30:
			score += 0.2
		}
	}
	if ranker != nil {
		score += ranker.Score(historyml.CommandSample{
			Command:     summary.Command,
			UsageCount:  summary.UsageCount,
			LastUsed:    summary.LastUsed,
			SourceOS:    summary.SourceOS,
			SourceShell: summary.SourceShell,
		}) * 1.4
	}
	return score
}

func historySummarySourceBoost(summary db.HistoryCommandSummary, currentOS, currentShell string) float64 {
	boost := 0.0
	if summary.SourceOS == currentOS && currentOS != "" {
		boost += 0.25
	}
	if summary.SourceShell == currentShell && currentShell != "" {
		boost += 0.2
	}
	return boost
}

func historySummaryDescription(summary db.HistoryCommandSummary, profile commandsearch.Profile) string {
	parts := []string{fmt.Sprintf("Used %s", formatCount(summary.UsageCount))}
	if age := formatRelativeAge(summary.LastUsed); age != "" {
		parts = append(parts, age)
	}
	if profile.Intent != "" && profile.Intent != profile.Executable && profile.Intent != profile.Normalized {
		parts = append(parts, "intent: "+profile.Intent)
	}
	return strings.Join(parts, " · ")
}

func formatRelativeAge(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}

	hoursSince := time.Since(ts).Hours()
	switch {
	case hoursSince < 1:
		return "seen recently"
	case hoursSince < 24:
		return fmt.Sprintf("last seen %.0fh ago", math.Ceil(hoursSince))
	case hoursSince < 24*7:
		return fmt.Sprintf("last seen %.0fd ago", math.Ceil(hoursSince/24))
	default:
		return "used before"
	}
}

func historyLogBaseScore(matchScore float64, rank int) float64 {
	score := matchScore / 260.0
	switch {
	case rank < 3:
		score += 0.6
	case rank < 10:
		score += 0.3
	case rank < 25:
		score += 0.15
	}
	return score
}

func historyLogRepeatBoost(matchScore float64, rank int) float64 {
	boost := matchScore / 1200.0
	if rank < 10 {
		boost += 0.08
	}
	return boost
}

func historyEntrySourceBoost(entry db.CommandExecution, currentOS, currentShell string) float64 {
	boost := 0.0
	if entry.SourceOS == currentOS && currentOS != "" {
		boost += 0.06
	}
	if entry.Shell == currentShell && currentShell != "" {
		boost += 0.05
	}
	return boost
}

func historyLogDescription(matches int, lastUsed time.Time, profile commandsearch.Profile) string {
	parts := []string{fmt.Sprintf("Matched %s in history", formatCount(matches))}
	if age := formatRelativeAge(lastUsed); age != "" {
		parts = append(parts, age)
	}
	if profile.Intent != "" && profile.Intent != profile.Executable && profile.Intent != profile.Normalized {
		parts = append(parts, "intent: "+profile.Intent)
	}
	return strings.Join(parts, " · ")
}

func shouldSuppressSmartHistoryCommand(query commandsearch.Query, raw string, profile commandsearch.Profile) bool {
	if query.Normalized == "" || profile.Executable == "" {
		return false
	}

	if query.Executable != "" && query.Executable != profile.Executable {
		if !intentMentionsQueryExecutable(query, profile) {
			return true
		}
	}

	if query.Subcommand != "" {
		if profile.Subcommand != "" && query.Subcommand != profile.Subcommand {
			return true
		}
		if profile.Subcommand == "" && !rootCommandMentionsSubcommand(raw, query.Executable, query.Subcommand) {
			return true
		}
	}

	if isCompoundHistoryCommand(raw) {
		if query.Subcommand != "" && profile.Subcommand == "" && !strings.HasPrefix(profile.Normalized, query.Normalized) {
			return true
		}
	}

	switch profile.Executable {
	case "wut", "cd", "pushd", "popd", "pwd", "ls", "dir", "clear", "cls":
		return query.Executable != profile.Executable
	default:
		return false
	}
}

func intentMentionsQueryExecutable(query commandsearch.Query, profile commandsearch.Profile) bool {
	if query.Executable == "" {
		return false
	}
	if profile.Intent == query.Executable || strings.HasSuffix(profile.Intent, " "+query.Executable) {
		return true
	}
	return profile.Subcommand == query.Executable
}

func isCompoundHistoryCommand(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	return strings.Contains(raw, "&&") ||
		strings.Contains(raw, "||") ||
		strings.Contains(raw, " | ") ||
		strings.Contains(raw, " ; ") ||
		strings.Contains(raw, ";") ||
		strings.Contains(raw, "\n")
}

func rootCommandMentionsSubcommand(raw, executable, subcommand string) bool {
	if executable == "" || subcommand == "" {
		return false
	}

	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return false
	}
	if normalizeSmartToken(fields[0]) != executable {
		return false
	}

	for i := 1; i < len(fields) && i <= 6; i++ {
		token := strings.TrimSpace(fields[i])
		switch token {
		case "&&", "||", "|", ";":
			return false
		}
		if normalizeSmartToken(token) == subcommand {
			return true
		}
	}

	return false
}

func normalizeSmartToken(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "\"'`"))
	if value == "" {
		return ""
	}
	value = filepath.Base(value)
	value = strings.TrimSuffix(value, ".exe")
	value = strings.TrimSuffix(value, ".cmd")
	value = strings.TrimSuffix(value, ".bat")
	return strings.ToLower(value)
}

// GetFallbackSuggestions returns fallback suggestions when normal flow fails
func (e *Engine) GetFallbackSuggestions(ctx *appctx.Context, limit int) []Suggestion {
	if limit <= 0 {
		limit = 10
	}

	// Always provide context-based suggestions as fallback
	suggestions := e.getContextSuggestions(ctx, "")

	// If still empty, provide generic suggestions
	if len(suggestions) == 0 {
		suggestions = []Suggestion{
			{Command: "ls", Description: "List directory contents", Source: "📌 Common", Icon: "📄", Score: 1.0},
			{Command: "pwd", Description: "Print working directory", Source: "📌 Common", Icon: "📁", Score: 1.0},
			{Command: "cd ..", Description: "Go to parent directory", Source: "📌 Common", Icon: "🔙", Score: 1.0},
			{Command: "clear", Description: "Clear the screen", Source: "📌 Common", Icon: "🧹", Score: 0.9},
		}
	}

	// Add git commands if in git repo
	if ctx.IsGitRepo {
		suggestions = append([]Suggestion{
			{Command: "git status", Description: "Check repository status", Source: "🎯 Context", Icon: "📊", Score: 1.5},
			{Command: "git add .", Description: "Stage all changes", Source: "🎯 Context", Icon: "➕", Score: 1.4},
			{Command: "git commit -m \"message\"", Description: "Commit changes", Source: "🎯 Context", Icon: "💾", Score: 1.3},
		}, suggestions...)
	}

	return e.limitSuggestions(suggestions, limit)
}

// Preload preloads suggestions into cache
func (e *Engine) Preload(ctx context.Context, ctxData *appctx.Context) {
	// Preload empty query suggestions
	go func() {
		_, _ = e.Suggest(ctx, "", ctxData, 20)
	}()
}

// ClearCache clears the suggestion cache
func (e *Engine) ClearCache() {
	e.cache.Clear()
	e.ctxCache.Clear()
}

// GetAutocomplete returns autocomplete suggestions
func (e *Engine) GetAutocomplete(prefix string) []string {
	return e.autocomplete.Suggest(prefix)
}

// AddToAutocomplete adds a command to autocomplete
func (e *Engine) AddToAutocomplete(command string) {
	e.autocomplete.Add(command)
}
