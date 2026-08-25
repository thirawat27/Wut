package cmd

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"
	"github.com/spf13/cobra"

	"wut/internal/config"
	"wut/internal/db"
	"wut/internal/logger"
	"wut/internal/shell"
)

// historyCmd represents the history command
var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "View command history log",
	Long:  `View, search, and analyze your complete sequential command execution log.`,
	Example: `  wut history
  wut history --limit 50
  wut history --search "docker"
  wut history --stats
  wut history --import-shell`,
	RunE: runHistory,
}

var (
	historyLimit       int
	historySearch      string
	historyStats       bool
	historyClear       bool
	historyExport      string
	historyImport      string
	historyImportShell bool
)

func init() {
	rootCmd.AddCommand(historyCmd)

	historyCmd.Flags().IntVarP(&historyLimit, "limit", "l", 20, "number of entries to show")
	historyCmd.Flags().StringVarP(&historySearch, "search", "s", "", "search term")
	historyCmd.Flags().BoolVar(&historyStats, "stats", false, "show statistics based on complete execution log")
	historyCmd.Flags().BoolVar(&historyClear, "clear", false, "clear complete history")
	historyCmd.Flags().StringVarP(&historyExport, "export", "e", "", "export history to JSON file")
	historyCmd.Flags().StringVarP(&historyImport, "import", "i", "", "import history from JSON file")
	historyCmd.Flags().BoolVar(&historyImportShell, "import-shell", false, "import from shell history files")
}

func runHistory(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	log := logger.With("history")

	storage, err := db.NewStorage(config.GetDatabasePath())
	if err != nil {
		log.Error("failed to initialize storage", "error", err)
		return fmt.Errorf("failed to initialize storage: %w", err)
	}
	defer storage.Close()

	if historyClear {
		if err := storage.ClearHistory(ctx); err != nil {
			log.Error("failed to clear history", "error", err)
			return fmt.Errorf("failed to clear history: %w", err)
		}
		fmt.Println("✅ Complete command sequence history cleared successfully")
		return nil
	}

	if historyExport != "" {
		if err := storage.ExportHistory(ctx, historyExport); err != nil {
			log.Error("failed to export history", "error", err, "file", historyExport)
			return fmt.Errorf("failed to export history: %w", err)
		}
		fmt.Printf("✅ Sequential history exported to %s\n", historyExport)
		return nil
	}

	if historyImport != "" {
		if err := storage.ImportHistory(ctx, historyImport); err != nil {
			log.Error("failed to import history", "error", err, "file", historyImport)
			return fmt.Errorf("failed to import history: %w", err)
		}
		fmt.Printf("✅ Sequential history imported from %s\n", historyImport)
		return nil
	}

	if historyImportShell {
		return importShellHistory(ctx, storage)
	}

	hydrateHistoryFromShell(ctx, storage)

	if historyStats {
		return showHistoryStats(ctx, storage)
	}

	return showHistory(ctx, storage)
}

// deduplicateHistory filters out duplicate commands from history entries, keeping the most recent.
func deduplicateHistory(entries []db.CommandExecution) []db.CommandExecution {
	seen := make(map[string]bool)
	var result []db.CommandExecution
	for _, e := range entries {
		cmdTrimmed := strings.TrimSpace(e.Command)
		if !seen[cmdTrimmed] && cmdTrimmed != "" {
			seen[cmdTrimmed] = true
			result = append(result, e)
		}
	}
	return result
}

type historyModel struct {
	entries  []db.CommandExecution
	cursor   int
	page     int
	pageSize int
	numPages int
	total    int
	msg      string
	width    int
	height   int
}

func newHistoryModel(entries []db.CommandExecution, total int) historyModel {
	msg := ""

	numPages := int(math.Ceil(float64(len(entries)) / 10.0))
	if numPages == 0 {
		numPages = 1
	}

	return historyModel{
		entries:  entries,
		pageSize: 10,
		numPages: numPages,
		total:    total,
		msg:      msg,
	}
}

func (m historyModel) Init() tea.Cmd {
	return nil
}

type clearMsg struct{}

func tickClearMsg() tea.Cmd {
	return tea.Tick(time.Second*2, func(_ time.Time) tea.Msg {
		return clearMsg{}
	})
}

func (m historyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case clearMsg:
		m.msg = ""
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				if m.cursor < m.page*m.pageSize {
					m.page--
				}
			}
		case "down", "j":
			if m.cursor < len(m.entries)-1 {
				m.cursor++
				if m.cursor >= (m.page+1)*m.pageSize {
					m.page++
				}
			}
		case "left", "h", "pgup":
			if m.page > 0 {
				m.page--
				m.cursor = m.page * m.pageSize
			}
		case "right", "l", "pgdown":
			if m.page < m.numPages-1 {
				m.page++
				m.cursor = m.page * m.pageSize
			}
		case "enter", "c", "y": // c for copy, y for yank, enter for copy
			if m.cursor >= 0 && m.cursor < len(m.entries) {
				targetCmd := m.entries[m.cursor].Command
				if err := clipboard.WriteAll(targetCmd); err == nil {
					m.msg = "📋 Copied to clipboard"
					return m, tickClearMsg()
				} else {
					m.msg = string("❌ Copy failed: " + err.Error())
					return m, tickClearMsg()
				}
			}
		}
	}
	return m, nil
}

func (m historyModel) View() string {
	if len(m.entries) == 0 {
		return "No execution logs found.\n"
	}

	start := m.page * m.pageSize
	end := start + m.pageSize
	if end > len(m.entries) {
		end = len(m.entries)
	}

	// ── Responsive widths ───────────────────────────────────────────────────
	w := m.width
	if w <= 0 {
		w = 80 // ค่าเริ่มต้นก่อนได้ WindowSizeMsg
	}

	// box padding ปรับตามความกว้างจอ
	boxPadX := 2
	if w < 60 {
		boxPadX = 1
	}

	// boxWidth = เต็มจอ ลบ 2 สำหรับขอบ border ทั้งสองข้าง
	boxWidth := w - 2
	if boxWidth < 30 {
		boxWidth = 30
	}

	// innerWidth = พื้นที่ใช้งานจริงภายในกล่อง
	innerWidth := boxWidth - 2 - (boxPadX * 2)
	if innerWidth < 20 {
		innerWidth = 20
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED"))
	titleStr := headerStyle.Render("📜 Execution Log (Newest First)")

	var sb strings.Builder
	if m.msg != "" {
		alertIcon := lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981")).Bold(true).Render("✔️  ")
		alertText := lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB")).Bold(true).Render(m.msg)

		alertStr := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#10B981")).
			Padding(0, 2).
			Render(alertIcon + alertText)

		titleWidth := lipgloss.Width(titleStr)
		alertWidth := lipgloss.Width(alertStr)

		padding := innerWidth - titleWidth - alertWidth
		if padding < 1 {
			padding = 1
		}

		titleBox := lipgloss.NewStyle().Height(lipgloss.Height(alertStr)).AlignVertical(lipgloss.Center).Render(titleStr)
		spaceBox := lipgloss.NewStyle().Width(padding).Render("")

		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Center, titleBox, spaceBox, alertStr) + "\n\n")
	} else {
		sb.WriteString(titleStr + "\n\n")
	}

	indexStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Width(4).Align(lipgloss.Right)
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))

	// ซ่อน timestamp บนจอแคบ (< 50 col)
	showTime := w >= 50
	showSource := w >= 78

	// availWidth: พื้นที่สำหรับ command text
	// index(4) + space(1) + time+brackets(13) + spaces(3) + cursor(2) = 23 เมื่อมี time
	// index(4) + space(1) + cursor(2) = 7 เมื่อไม่มี time
	var availWidth int
	if showTime {
		availWidth = innerWidth - 23
	} else {
		availWidth = innerWidth - 7
	}
	if showSource {
		availWidth -= 20
	}
	if availWidth < 10 {
		availWidth = 10
	}

	for i := start; i < end; i++ {
		entry := m.entries[i]
		cursor := "  "
		cmdStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#10B981"))

		if m.cursor == i {
			cursor = "👉"
			cmdStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#3B82F6")).Padding(0, 1)
		}

		dispCmd := entry.Command
		if lipgloss.Width(dispCmd) > availWidth {
			dispCmd = truncate.StringWithTail(dispCmd, uint(availWidth), "...")
		}

		if showTime {
			timeStr := entry.Timestamp.Local().Format("01-02 15:04")
			source := ""
			if showSource {
				if label := formatHistorySource(entry); label != "" {
					source = metaStyle.Render(label) + "  "
				}
			}
			sb.WriteString(fmt.Sprintf("%s %s %s   %s%s\n\n", cursor, indexStyle.Render(fmt.Sprintf("%d.", i+1)), metaStyle.Render("["+timeStr+"]"), source, cmdStyle.Render(dispCmd)))
		} else {
			sb.WriteString(fmt.Sprintf("%s %s %s\n\n", cursor, indexStyle.Render(fmt.Sprintf("%d.", i+1)), cmdStyle.Render(dispCmd)))
		}
	}

	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Render(
		fmt.Sprintf("Showing %d unique executions out of %d total recorded.", len(m.entries), m.total)))
	sb.WriteString("\n\n")

	// ── Footer text (responsive) ──────────────────────────────────────────────
	footerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#EAB308")).Bold(true)
	sb.WriteString(footerStyle.Render(fmt.Sprintf("Page %d/%d", m.page+1, m.numPages)))

	var footerNav string
	if w >= 90 {
		footerNav = " | [↑/↓] Navigate | [←/→] Prev/Next Page | [c/enter] Copy | [q] Quit"
	} else if w >= 60 {
		footerNav = " | ↑/↓ nav | ←/→ page | c copy | q quit"
	} else {
		footerNav = " | ↑/↓ | ←/→ | c | q"
	}
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF")).Render(footerNav + "\n"))

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7C3AED")).
		Padding(1, boxPadX).
		Width(boxWidth)

	return boxStyle.Render(strings.TrimRight(sb.String(), "\n"))
}

func showHistory(ctx context.Context, storage *db.Storage) error {
	var entries []db.CommandExecution
	var err error

	if historySearch != "" {
		entries, err = searchHistoryOptimized(ctx, storage, historySearch, historyLimit)
	} else {
		fetchLimit := historyLimit
		if fetchLimit <= 20 {
			fetchLimit = 200
		}
		scanLimit := fetchLimit * 25
		if scanLimit < 500 {
			scanLimit = 500
		}
		entries, err = storage.GetRecentUniqueHistory(ctx, fetchLimit, scanLimit)
	}

	if err != nil {
		return fmt.Errorf("failed to get history: %w", err)
	}

	entries = deduplicateHistory(entries)

	if len(entries) == 0 {
		fmt.Println("No execution logs found.")
		return nil
	}

	total := getTotalCount(ctx, storage)
	p := tea.NewProgram(newHistoryModel(entries, total))
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running history UI: %w", err)
	}

	return nil
}

func searchHistoryOptimized(ctx context.Context, storage *db.Storage, query string, limit int) ([]db.CommandExecution, error) {
	if limit <= 0 {
		limit = 50
	}
	return storage.SearchHistory(ctx, query, limit)
}

func getTotalCount(ctx context.Context, storage *db.Storage) int {
	stats, err := storage.GetHistoryStats(ctx)
	if err != nil {
		return 0
	}
	return stats.TotalExecutions
}

func showHistoryStats(ctx context.Context, storage *db.Storage) error {
	log := logger.With("history.stats")
	log.Debug("getting sequential history statistics")

	stats, err := storage.GetHistoryStats(ctx)
	if err != nil {
		return fmt.Errorf("failed to get history statistics: %w", err)
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED"))
	fmt.Printf("\n%s\n\n", headerStyle.Render("📊 Execution Log Insights"))

	statStyle := lipgloss.NewStyle().Bold(true)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))

	fmt.Printf("  %s %s\n", statStyle.Render("Total Executions :"), valueStyle.Render(fmt.Sprintf("%d", stats.TotalExecutions)))
	fmt.Printf("  %s %s\n", statStyle.Render("Unique Commands  :"), valueStyle.Render(fmt.Sprintf("%d", stats.UniqueCommands)))
	if stats.MostUsedCommand != "" {
		fmt.Printf("  %s %s\n", statStyle.Render("Favorite Command :"), valueStyle.Render(fmt.Sprintf("%s (%d times)", stats.MostUsedCommand, stats.MostUsedCount)))
	}
	fmt.Println()

	if len(stats.TimeDistribution) > 0 {
		catStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#3B82F6"))
		fmt.Printf("%s\n", catStyle.Render("🕒 Time Distribution:"))
		printSortedDistribution(stats.TimeDistribution)
		fmt.Println()
	}

	if len(stats.OSDistribution) > 0 {
		catStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#8B5CF6"))
		fmt.Printf("%s\n", catStyle.Render("🖥️ OS Distribution:"))
		printSortedDistribution(stats.OSDistribution)
		fmt.Println()
	}

	if len(stats.ShellDistribution) > 0 {
		catStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#06B6D4"))
		fmt.Printf("%s\n", catStyle.Render("🐚 Shell Distribution:"))
		printSortedDistribution(stats.ShellDistribution)
		fmt.Println()
	}

	if len(stats.TopCommands) > 0 {
		topStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F59E0B"))
		fmt.Printf("%s\n", topStyle.Render("🏆 Most Used Combinations/Commands:"))
		for i, cmd := range stats.TopCommands {
			fmt.Printf("  %d. %s (%d times)\n", i+1, cmd.Command, cmd.Count)
		}
		fmt.Println()
	}

	return nil
}

func formatHistorySource(entry db.CommandExecution) string {
	sourceOS := strings.TrimSpace(entry.SourceOS)
	shellName := strings.TrimSpace(entry.Shell)
	if sourceOS == "unknown" {
		sourceOS = ""
	}
	if shellName == "unknown" {
		shellName = ""
	}
	switch {
	case sourceOS != "" && shellName != "":
		return fmt.Sprintf("[%s/%s]", sourceOS, shellName)
	case sourceOS != "":
		return fmt.Sprintf("[%s]", sourceOS)
	case shellName != "":
		return fmt.Sprintf("[%s]", shellName)
	default:
		return ""
	}
}

func printSortedDistribution(values map[string]int) {
	keys := make([]string, 0, len(values))
	for key, count := range values {
		if strings.TrimSpace(key) == "" || count <= 0 {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Printf("  • %-20s: %d\n", key, values[key])
	}
}

func importShellHistory(ctx context.Context, storage *db.Storage) error {
	summary, err := importShellHistoryEntries(ctx, storage, 0)
	if err != nil {
		return err
	}

	fmt.Println("🔍 Detected shells:")
	for _, source := range summary.sources {
		fmt.Printf("  • %s: %s\n", source.Shell, source.DisplayPath())
	}
	fmt.Println()

	for _, line := range summary.perShell {
		fmt.Println(line)
	}

	if summary.imported == 0 {
		fmt.Println("\nNo history entries found in shell files")
		return nil
	}

	fmt.Printf("\n✅ Successfully imported %d execution steps in %v\n", summary.imported, summary.duration)
	return nil
}

func hydrateHistoryFromShell(ctx context.Context, storage *db.Storage) {
	stats, err := storage.GetHistoryStats(ctx)
	if err != nil || stats.TotalExecutions > 0 {
		return
	}

	hydrateCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, _ = importShellHistoryEntries(hydrateCtx, storage, 500)
}

func bootstrapShellHistoryImport(ctx context.Context) (*shellHistoryImportSummary, error) {
	if !config.Get().History.Enabled {
		return &shellHistoryImportSummary{}, nil
	}
	if err := config.EnsureDirs(); err != nil {
		return nil, err
	}

	sources := shell.DetectHistorySources()
	if len(sources) == 0 {
		return &shellHistoryImportSummary{}, nil
	}

	storage, err := db.NewStorage(config.GetDatabasePath())
	if err != nil {
		return nil, err
	}
	defer storage.Close()

	return importShellHistoryEntries(ctx, storage, 0)
}

type shellHistoryImportSummary struct {
	sources  []shell.HistorySource
	perShell []string
	imported int
	duration time.Duration
}

func importShellHistoryEntries(ctx context.Context, storage *db.Storage, limitPerShell int) (*shellHistoryImportSummary, error) {
	sources := shell.DetectHistorySources()
	if len(sources) == 0 {
		return nil, fmt.Errorf("no shell history files detected")
	}

	start := time.Now()
	allEntries := make([]db.CommandExecution, 0, 4096)
	perShell := make([]string, 0, len(sources))
	type importStateUpdate struct {
		key   string
		state *db.HistoryImportState
	}
	stateUpdates := make([]importStateUpdate, 0, len(sources))

	for _, source := range sources {
		commands, err := shell.ReadHistory(source)
		if err != nil {
			perShell = append(perShell, fmt.Sprintf("  ! %s (%s): failed to read history (%v)", source.Shell, source.DisplayPath(), err))
			continue
		}

		totalCommands := len(commands)
		newCommands := commands
		if limitPerShell > 0 && len(newCommands) > limitPerShell {
			newCommands = newCommands[len(newCommands)-limitPerShell:]
		}

		if limitPerShell == 0 {
			stateKey := shellHistorySourceKey(source)
			state, err := storage.GetHistoryImportState(ctx, stateKey)
			if err == nil {
				startAt := determineShellImportStart(commands, state)
				if startAt > len(commands) {
					startAt = len(commands)
				}
				newCommands = commands[startAt:]
			}
			stateUpdates = append(stateUpdates, importStateUpdate{
				key:   stateKey,
				state: buildHistoryImportState(commands),
			})
		}

		for _, command := range newCommands {
			allEntries = append(allEntries, db.CommandExecution{
				Command:  command,
				SourceOS: runtime.GOOS,
				Shell:    source.Shell,
			})
		}
		if limitPerShell == 0 {
			perShell = append(perShell, fmt.Sprintf("  ✓ %s: %d new / %d total (%s)", source.Shell, len(newCommands), totalCommands, source.DisplayPath()))
		} else {
			perShell = append(perShell, fmt.Sprintf("  ✓ %s: %d commands (%s)", source.Shell, len(newCommands), source.DisplayPath()))
		}
	}

	if len(allEntries) == 0 {
		if limitPerShell == 0 {
			for _, update := range stateUpdates {
				_ = storage.SaveHistoryImportState(ctx, update.key, update.state)
			}
		}
		return &shellHistoryImportSummary{
			sources:  sources,
			perShell: perShell,
			duration: time.Since(start),
		}, nil
	}

	imported, err := storage.AddHistoryBatch(ctx, allEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to import shell history: %w", err)
	}
	if maxEntries := config.Get().History.MaxEntries; maxEntries > 0 {
		if err := storage.TrimHistory(ctx, maxEntries); err != nil {
			return nil, fmt.Errorf("failed to trim history: %w", err)
		}
	}
	if limitPerShell == 0 {
		for _, update := range stateUpdates {
			if err := storage.SaveHistoryImportState(ctx, update.key, update.state); err != nil {
				return nil, fmt.Errorf("failed to save import state: %w", err)
			}
		}
	}

	return &shellHistoryImportSummary{
		sources:  sources,
		perShell: perShell,
		imported: imported,
		duration: time.Since(start),
	}, nil
}

func shellHistorySourceKey(source shell.HistorySource) string {
	return strings.Join([]string{
		source.Shell,
		string(source.Kind),
		strings.TrimSpace(source.Path),
		strings.TrimSpace(source.Command),
		strings.Join(source.Args, "\x00"),
	}, "\x1f")
}

func determineShellImportStart(commands []string, state *db.HistoryImportState) int {
	if len(commands) == 0 || state == nil {
		return 0
	}

	if state.ImportedCount > 0 && state.ImportedCount <= len(commands) && importStateTailMatches(commands, state.ImportedCount, state.TailCommands) {
		return state.ImportedCount
	}

	if overlap := findImportOverlap(commands, state.TailCommands); overlap > 0 {
		return overlap
	}

	if state.ImportedCount >= len(commands) && importStateTailMatches(commands, len(commands), state.TailCommands) {
		return len(commands)
	}

	return 0
}

func importStateTailMatches(commands []string, end int, tail []string) bool {
	if end <= 0 || len(commands) == 0 || len(tail) == 0 || end > len(commands) {
		return false
	}

	window := minInt(historyImportTailWindow, minInt(end, len(tail)))
	if window <= 0 {
		return false
	}

	left := commands[end-window : end]
	right := tail[len(tail)-window:]
	for i := 0; i < window; i++ {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func findImportOverlap(commands []string, tail []string) int {
	if len(commands) == 0 || len(tail) == 0 {
		return 0
	}

	maxWindow := minInt(historyImportTailWindow, minInt(len(commands), len(tail)))
	for window := maxWindow; window >= 3; window-- {
		needle := tail[len(tail)-window:]
		for end := len(commands); end >= window; end-- {
			match := true
			for i := 0; i < window; i++ {
				if commands[end-window+i] != needle[i] {
					match = false
					break
				}
			}
			if match {
				return end
			}
		}
	}

	return 0
}

func buildHistoryImportState(commands []string) *db.HistoryImportState {
	state := &db.HistoryImportState{
		ImportedCount: len(commands),
		UpdatedAt:     time.Now(),
	}
	if len(commands) == 0 {
		return state
	}

	window := minInt(historyImportTailWindow, len(commands))
	state.TailCommands = append([]string(nil), commands[len(commands)-window:]...)
	return state
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

const historyImportTailWindow = 16
