package db

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-json"
	"go.etcd.io/bbolt"

	"wut/internal/commandsearch"
	"wut/internal/historyml"
	"wut/internal/performance"
	shellmeta "wut/internal/shell"
)

const (
	historyBucketName           = "command_execution_log"
	historyImportStateBucket    = "history_import_state"
	historyImportTailWindowSize = 16
)

// CommandExecution represents a single execution of a command
type CommandExecution struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	Timestamp time.Time `json:"timestamp"`
	Dir       string    `json:"dir"`
	SessionID string    `json:"session_id"`
	SourceOS  string    `json:"source_os,omitempty"`
	Shell     string    `json:"source_shell,omitempty"`
}

// HistoryCommandSummary represents aggregated history for a single command.
type HistoryCommandSummary struct {
	Command     string
	UsageCount  int
	LastUsed    time.Time
	SourceOS    string
	SourceShell string
}

// HistoryStats represents statistics computed from the raw execution log
type HistoryStats struct {
	TotalExecutions   int
	UniqueCommands    int
	MostUsedCommand   string
	MostUsedCount     int
	TopCommands       []CommandStat
	TimeDistribution  map[string]int
	OSDistribution    map[string]int
	ShellDistribution map[string]int
}

// HistoryImportState tracks incremental shell-history import progress.
type HistoryImportState struct {
	ImportedCount int       `json:"imported_count"`
	TailCommands  []string  `json:"tail_commands,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// HistorySearchMatch represents one ranked raw execution-log match.
type HistorySearchMatch struct {
	Entry CommandExecution
	Score float64
}

// CommandStat represents a command and its occurrence count
type CommandStat struct {
	Command string
	Count   int
}

// AddHistory adds a strictly logged command execution to the DB
func (s *Storage) AddHistory(ctx context.Context, command string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("storage not initialized")
	}

	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}

	now := time.Now()
	dir, _ := os.Getwd()
	sessionID := os.Getenv("WUT_SESSION_ID") // optional grouping

	exec := CommandExecution{
		Command:   command,
		Timestamp: now,
		Dir:       dir,
		SessionID: sessionID,
		SourceOS:  currentSourceOS(),
		Shell:     currentSourceShell(),
	}

	_, err := s.AddHistoryBatch(ctx, []CommandExecution{exec})
	return err
}

// GetHistory retrieves command execution logs, newest first
func (s *Storage) GetHistory(ctx context.Context, limit int) ([]CommandExecution, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	var entries []CommandExecution

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket, err := requireBucket(tx, historyBucketName)
		if err != nil {
			return err
		}
		if bucket == nil {
			return nil
		}

		// Cursor to iterate over keys, since ID is padded timestamp we can iterate in reverse
		c := bucket.Cursor()
		count := 0
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var entry CommandExecution
			if err := json.Unmarshal(v, &entry); err == nil {
				ensureHistoryMetadata(&entry)
				entries = append(entries, entry)
				count++
				if limit > 0 && count >= limit {
					break
				}
			}
		}
		return nil
	})

	return entries, err
}

// GetAllHistory retrieves all command executions
func (s *Storage) GetAllHistory(ctx context.Context) ([]CommandExecution, error) {
	return s.GetHistory(ctx, 0)
}

// SearchHistory searches history logs by command text
func (s *Storage) SearchHistory(ctx context.Context, query string, limit int) ([]CommandExecution, error) {
	matches, err := s.SearchHistoryMatches(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	entries := make([]CommandExecution, len(matches))
	for i, match := range matches {
		entries[i] = match.Entry
	}

	return entries, nil
}

// SearchHistoryMatches searches the raw execution log and returns ranked raw
// matches so callers can reuse the same retrieval path as `wut history`.
func (s *Storage) SearchHistoryMatches(ctx context.Context, query string, limit int) ([]HistorySearchMatch, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	query = strings.TrimSpace(query)
	if query == "" {
		entries, err := s.GetRecentUniqueHistory(ctx, limit, 0)
		if err != nil {
			return nil, err
		}
		matches := make([]HistorySearchMatch, len(entries))
		for i, entry := range entries {
			matches[i] = HistorySearchMatch{
				Entry: entry,
				Score: recencyBonus(entry.Timestamp),
			}
		}
		return matches, nil
	}
	if limit <= 0 {
		limit = 20
	}

	matcher := performance.NewFastMatcher(false, 0.25, 3)
	queryProfile := commandsearch.ParseQuery(query)
	results := make([]scoredHistoryEntry, 0, limit)
	commandStats := make(map[string]*HistoryCommandSummary)
	scanRank := 0

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket, err := requireBucket(tx, historyBucketName)
		if err != nil {
			return err
		}
		if bucket == nil {
			return nil
		}

		c := bucket.Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			if err := ctx.Err(); err != nil {
				return err
			}

			var entry CommandExecution
			if err := json.Unmarshal(v, &entry); err != nil {
				continue
			}
			ensureHistoryMetadata(&entry)

			updateHistorySummary(commandStats, entry)

			score, matched := scoreHistoryEntry(queryProfile, entry.Command, matcher)
			if !matched {
				scanRank++
				continue
			}

			results = append(results, scoredHistoryEntry{
				entry: entry,
				score: score + recencyBonus(entry.Timestamp),
				rank:  scanRank,
			})
			scanRank++
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	modelSamples := make([]historyml.CommandSample, 0, len(commandStats))
	for _, summary := range commandStats {
		modelSamples = append(modelSamples, historyml.CommandSample{
			Command:     summary.Command,
			UsageCount:  summary.UsageCount,
			LastUsed:    summary.LastUsed,
			SourceOS:    summary.SourceOS,
			SourceShell: summary.SourceShell,
		})
	}
	ranker := historyml.Train(modelSamples, time.Now())

	for i := range results {
		summary := commandStats[results[i].entry.Command]
		if summary == nil {
			continue
		}
		results[i].score += historyRankBoost(results[i].entry, summary, ranker)
	}

	sort.SliceStable(results, func(i, j int) bool {
		return historyResultLess(results[i], results[j])
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	matches := make([]HistorySearchMatch, len(results))
	for i, result := range results {
		matches[i] = HistorySearchMatch{
			Entry: result.entry,
			Score: result.score,
		}
	}

	return matches, nil
}

// AddHistoryBatch adds multiple history entries in a single transaction while
// preserving their relative order.
func (s *Storage) AddHistoryBatch(ctx context.Context, entries []CommandExecution) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("storage not initialized")
	}

	prepared := make([]CommandExecution, 0, len(entries))
	now := time.Now()
	dir, _ := os.Getwd()
	sessionID := os.Getenv("WUT_SESSION_ID")

	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		entry.Command = strings.TrimSpace(entry.Command)
		if entry.Command == "" {
			continue
		}
		if entry.Timestamp.IsZero() {
			entry.Timestamp = now.Add(time.Duration(i) * time.Nanosecond)
		} else {
			entry.Timestamp = entry.Timestamp.Add(time.Duration(i) * time.Nanosecond)
		}
		if entry.Dir == "" {
			entry.Dir = dir
		}
		if entry.SessionID == "" {
			entry.SessionID = sessionID
		}
		if entry.SourceOS == "" {
			entry.SourceOS = currentSourceOS()
		}
		if entry.Shell == "" {
			entry.Shell = currentSourceShell()
		}
		entry.ID = historyID(entry.Timestamp)
		prepared = append(prepared, entry)
	}

	if len(prepared) == 0 {
		return 0, nil
	}

	err := s.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(historyBucketName))
		if err != nil {
			return err
		}

		for _, entry := range prepared {
			data, err := json.Marshal(entry)
			if err != nil {
				return fmt.Errorf("failed to marshal command execution: %w", err)
			}
			if err := bucket.Put([]byte(entry.ID), data); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return len(prepared), nil
}

// TrimHistory removes the oldest history entries so the bucket contains at
// most maxEntries items.
func (s *Storage) TrimHistory(ctx context.Context, maxEntries int) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("storage not initialized")
	}
	if maxEntries <= 0 {
		return nil
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := requireBucket(tx, historyBucketName)
		if err != nil {
			return err
		}
		if bucket == nil {
			return nil
		}

		removeCount := bucket.Stats().KeyN - maxEntries
		if removeCount <= 0 {
			return nil
		}

		c := bucket.Cursor()
		keys := make([][]byte, 0, removeCount)
		for k, _ := c.First(); k != nil && len(keys) < removeCount; k, _ = c.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			keys = append(keys, append([]byte(nil), k...))
		}

		for _, key := range keys {
			if err := bucket.Delete(key); err != nil {
				return err
			}
		}

		return nil
	})
}

// GetRecentUniqueHistory returns the newest distinct commands without loading a
// much larger slice just to deduplicate it afterwards.
func (s *Storage) GetRecentUniqueHistory(ctx context.Context, limit int, scanLimit int) ([]CommandExecution, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("storage not initialized")
	}
	if limit <= 0 {
		limit = 20
	}

	results := make([]CommandExecution, 0, limit)
	seen := make(map[string]struct{}, limit)
	scanned := 0

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket, err := requireBucket(tx, historyBucketName)
		if err != nil {
			return err
		}
		if bucket == nil {
			return nil
		}

		c := bucket.Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			if err := ctx.Err(); err != nil {
				return err
			}

			var entry CommandExecution
			if err := json.Unmarshal(v, &entry); err != nil {
				continue
			}
			ensureHistoryMetadata(&entry)

			scanned++
			trimmed := strings.TrimSpace(entry.Command)
			if trimmed == "" {
				if scanLimit > 0 && scanned >= scanLimit {
					break
				}
				continue
			}
			if _, ok := seen[trimmed]; ok {
				if scanLimit > 0 && scanned >= scanLimit {
					break
				}
				continue
			}

			seen[trimmed] = struct{}{}
			results = append(results, entry)
			if len(results) >= limit {
				break
			}
			if scanLimit > 0 && scanned >= scanLimit {
				break
			}
		}

		return nil
	})

	return results, err
}

// GetHistoryCommandSummaries aggregates usage counts and last-used timestamps
// per command without materializing the full execution log.
func (s *Storage) GetHistoryCommandSummaries(ctx context.Context, scanLimit int) ([]HistoryCommandSummary, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	summaries := make(map[string]*HistoryCommandSummary)
	scanned := 0

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket, err := requireBucket(tx, historyBucketName)
		if err != nil {
			return err
		}
		if bucket == nil {
			return nil
		}

		c := bucket.Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			if err := ctx.Err(); err != nil {
				return err
			}

			var entry CommandExecution
			if err := json.Unmarshal(v, &entry); err != nil {
				continue
			}
			ensureHistoryMetadata(&entry)

			scanned++
			if strings.TrimSpace(entry.Command) == "" {
				if scanLimit > 0 && scanned >= scanLimit {
					break
				}
				continue
			}
			updateHistorySummary(summaries, entry)

			if scanLimit > 0 && scanned >= scanLimit {
				break
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	results := make([]HistoryCommandSummary, 0, len(summaries))
	for _, summary := range summaries {
		results = append(results, *summary)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].UsageCount == results[j].UsageCount {
			return results[i].LastUsed.After(results[j].LastUsed)
		}
		return results[i].UsageCount > results[j].UsageCount
	})

	return results, nil
}

// GetCommandUsageCount counts how often an exact command appears in history.
// If stopAt is positive, the scan stops early once the count reaches that value.
func (s *Storage) GetCommandUsageCount(ctx context.Context, command string, stopAt int) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("storage not initialized")
	}

	command = strings.TrimSpace(command)
	if command == "" {
		return 0, nil
	}

	count := 0
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket, err := requireBucket(tx, historyBucketName)
		if err != nil {
			return err
		}
		if bucket == nil {
			return nil
		}

		c := bucket.Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			if err := ctx.Err(); err != nil {
				return err
			}

			var entry CommandExecution
			if err := json.Unmarshal(v, &entry); err != nil {
				continue
			}
			if entry.Command != command {
				continue
			}

			count++
			if stopAt > 0 && count >= stopAt {
				return errStopScan
			}
		}

		return nil
	})
	if errors.Is(err, errStopScan) {
		err = nil
	}

	return count, err
}

// GetHistoryImportState retrieves persisted incremental-import state for a
// shell history source.
func (s *Storage) GetHistoryImportState(ctx context.Context, sourceKey string) (*HistoryImportState, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	sourceKey = strings.TrimSpace(sourceKey)
	if sourceKey == "" {
		return nil, nil
	}

	var state HistoryImportState
	err := s.db.View(func(tx *bbolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		bucket, err := requireBucket(tx, historyImportStateBucket)
		if err != nil {
			return err
		}
		if bucket == nil {
			return nil
		}

		data := bucket.Get([]byte(sourceKey))
		if len(data) == 0 {
			return nil
		}

		return json.Unmarshal(data, &state)
	})
	if err != nil {
		return nil, err
	}
	if state.ImportedCount == 0 && len(state.TailCommands) == 0 && state.UpdatedAt.IsZero() {
		return nil, nil
	}

	return &state, nil
}

// SaveHistoryImportState stores incremental-import state for a shell history
// source so subsequent imports can avoid duplicating the same commands.
func (s *Storage) SaveHistoryImportState(ctx context.Context, sourceKey string, state *HistoryImportState) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("storage not initialized")
	}

	sourceKey = strings.TrimSpace(sourceKey)
	if sourceKey == "" {
		return nil
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		bucket, err := tx.CreateBucketIfNotExists([]byte(historyImportStateBucket))
		if err != nil {
			return err
		}
		if state == nil {
			return bucket.Delete([]byte(sourceKey))
		}

		payload, err := json.Marshal(state)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(sourceKey), payload)
	})
}

// ClearHistory clears all recorded command execution logs
func (s *Storage) ClearHistory(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("storage not initialized")
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		_ = tx.DeleteBucket([]byte(historyBucketName))
		// Support removing the legacy history bucket too
		_ = tx.DeleteBucket([]byte("command_history"))
		_, err := tx.CreateBucket([]byte(historyBucketName))
		return err
	})
}

// ExportHistory exports raw execution history to a JSON file
func (s *Storage) ExportHistory(ctx context.Context, filepath string) error {
	entries, err := s.GetAllHistory(ctx)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal history: %w", err)
	}

	return os.WriteFile(filepath, data, 0644)
}

// ImportHistory imports execution log history from a JSON file
func (s *Storage) ImportHistory(ctx context.Context, filepath string) error {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	var entries []CommandExecution
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("failed to parse history: %w", err)
	}

	_, err = s.AddHistoryBatch(ctx, entries)
	return err
}

// GetHistoryStats returns aggregated statistics about command history
func (s *Storage) GetHistoryStats(ctx context.Context) (*HistoryStats, error) {
	entries, err := s.GetAllHistory(ctx)
	if err != nil {
		return nil, err
	}

	stats := &HistoryStats{
		TotalExecutions:   len(entries),
		TopCommands:       []CommandStat{},
		TimeDistribution:  make(map[string]int),
		OSDistribution:    make(map[string]int),
		ShellDistribution: make(map[string]int),
	}

	if len(entries) == 0 {
		return stats, nil
	}

	counts := make(map[string]int)
	for _, entry := range entries {
		ensureHistoryMetadata(&entry)
		counts[entry.Command]++
		stats.OSDistribution[entry.SourceOS]++
		stats.ShellDistribution[entry.Shell]++

		hour := entry.Timestamp.Hour()
		if hour >= 6 && hour < 12 {
			stats.TimeDistribution["Morning (06:00-12:00)"]++
		} else if hour >= 12 && hour < 18 {
			stats.TimeDistribution["Afternoon (12:00-18:00)"]++
		} else if hour >= 18 && hour < 24 {
			stats.TimeDistribution["Evening (18:00-24:00)"]++
		} else {
			stats.TimeDistribution["Night (00:00-06:00)"]++
		}
	}

	stats.UniqueCommands = len(counts)

	var cmds []CommandStat
	for c, count := range counts {
		cmds = append(cmds, CommandStat{Command: c, Count: count})
	}
	sort.Slice(cmds, func(i, j int) bool {
		return cmds[i].Count > cmds[j].Count
	})

	if len(cmds) > 0 {
		stats.MostUsedCommand = cmds[0].Command
		stats.MostUsedCount = cmds[0].Count
	}

	limit := len(cmds)
	if limit > 10 {
		limit = 10
	}
	stats.TopCommands = cmds[:limit]

	return stats, nil
}

func currentSourceOS() string {
	if sourceOS := strings.TrimSpace(os.Getenv("WUT_SOURCE_OS")); sourceOS != "" {
		return strings.ToLower(sourceOS)
	}
	return runtime.GOOS
}

func currentSourceShell() string {
	return shellmeta.DetectCurrentShell()
}

func ensureHistoryMetadata(entry *CommandExecution) {
	if entry == nil {
		return
	}
	entry.SourceOS = strings.ToLower(strings.TrimSpace(entry.SourceOS))
	if entry.SourceOS == "" {
		entry.SourceOS = "unknown"
	}
	entry.Shell = normalizeShellName(entry.Shell)
	if entry.Shell == "" {
		entry.Shell = "unknown"
	}
}

func normalizeShellName(shell string) string {
	return shellmeta.CanonicalName(shell)
}

func updateHistorySummary(summaries map[string]*HistoryCommandSummary, entry CommandExecution) {
	command := strings.TrimSpace(entry.Command)
	if command == "" {
		return
	}

	summary, ok := summaries[command]
	if !ok {
		summary = &HistoryCommandSummary{
			Command:     command,
			LastUsed:    entry.Timestamp,
			SourceOS:    entry.SourceOS,
			SourceShell: entry.Shell,
		}
		summaries[command] = summary
	}

	summary.UsageCount++
	if entry.Timestamp.After(summary.LastUsed) {
		summary.LastUsed = entry.Timestamp
		summary.SourceOS = entry.SourceOS
		summary.SourceShell = entry.Shell
	}
}

func historyRankBoost(entry CommandExecution, summary *HistoryCommandSummary, ranker *historyml.Ranker) float64 {
	if summary == nil {
		return 0
	}

	usageBoost := math.Log1p(float64(summary.UsageCount)) * 18
	mlBoost := 0.0
	if ranker != nil {
		mlBoost = ranker.Score(historyml.CommandSample{
			Command:     summary.Command,
			UsageCount:  summary.UsageCount,
			LastUsed:    summary.LastUsed,
			SourceOS:    summary.SourceOS,
			SourceShell: summary.SourceShell,
		}) * 70
	}

	shellBoost := 0.0
	if entry.SourceOS == currentSourceOS() && entry.SourceOS != "" {
		shellBoost += 8
	}
	if entry.Shell == currentSourceShell() && entry.Shell != "" {
		shellBoost += 6
	}

	return usageBoost + mlBoost + shellBoost
}

type scoredHistoryEntry struct {
	entry CommandExecution
	score float64
	rank  int
}

func historyID(ts time.Time) string {
	return fmt.Sprintf("%020d", ts.UnixNano())
}

func scoreHistoryEntry(query commandsearch.Query, command string, matcher *performance.FastMatcher) (float64, bool) {
	if query.Normalized == "" || strings.TrimSpace(command) == "" {
		return 0, false
	}

	profile := commandsearch.BuildProfile(command)
	if !commandsearch.HasAnchor(query, profile, matcher) {
		return 0, false
	}
	return commandsearch.Score(query, profile, matcher)
}

func recencyBonus(ts time.Time) float64 {
	if ts.IsZero() {
		return 0
	}

	hours := time.Since(ts).Hours()
	switch {
	case hours < 24:
		return 40
	case hours < 24*7:
		return 18
	case hours < 24*30:
		return 6
	default:
		return 0
	}
}

func insertHistoryResult(results []scoredHistoryEntry, candidate scoredHistoryEntry, limit int) []scoredHistoryEntry {
	insertAt := sort.Search(len(results), func(i int) bool {
		return historyResultLess(candidate, results[i])
	})

	results = append(results, scoredHistoryEntry{})
	copy(results[insertAt+1:], results[insertAt:])
	results[insertAt] = candidate

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results
}

func historyResultLess(left, right scoredHistoryEntry) bool {
	if left.score == right.score {
		return left.rank < right.rank
	}
	return left.score > right.score
}
