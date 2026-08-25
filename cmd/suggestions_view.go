package cmd

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"

	appctx "wut/internal/context"
	"wut/internal/smart"
)

type smartListModel struct {
	query       string
	context     *appctx.Context
	suggestions []smart.Suggestion
	cursor      int
	page        int
	pageSize    int
	numPages    int
	msg         string
	width       int
	height      int
}

func showSmartSuggestions(query string, ctx *appctx.Context, suggestions []smart.Suggestion) error {
	if len(suggestions) == 0 {
		fmt.Println("No smart suggestions found.")
		return nil
	}

	model := newSmartListModel(query, ctx, suggestions)
	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running smart UI: %w", err)
	}

	return nil
}

func newSmartListModel(query string, ctx *appctx.Context, suggestions []smart.Suggestion) smartListModel {
	pageSize := 12
	if len(suggestions) > 0 {
		pageSize = max(5, min(20, len(suggestions)/3))
	}
	numPages := int(math.Ceil(float64(len(suggestions)) / float64(pageSize)))
	if numPages == 0 {
		numPages = 1
	}

	return smartListModel{
		query:       query,
		context:     ctx,
		suggestions: suggestions,
		pageSize:    pageSize,
		numPages:    numPages,
	}
}

func (m smartListModel) Init() tea.Cmd {
	return nil
}

func (m smartListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Adapt page size to terminal height
		if msg.Height > 0 {
			m.pageSize = max(5, min(30, (msg.Height-10)/2))
			m.numPages = int(math.Ceil(float64(len(m.suggestions)) / float64(m.pageSize)))
			if m.numPages == 0 {
				m.numPages = 1
			}
			if m.page >= m.numPages {
				m.page = m.numPages - 1
			}
		}
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
			if m.cursor < len(m.suggestions)-1 {
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
		case "enter", "c", "y":
			if m.cursor >= 0 && m.cursor < len(m.suggestions) {
				targetCmd := m.suggestions[m.cursor].Command
				if err := clipboard.WriteAll(targetCmd); err == nil {
					m.msg = "📋 Copied to clipboard"
					return m, tickClearMsg()
				}
				m.msg = "❌ Copy failed"
				return m, tickClearMsg()
			}
		}
	}
	return m, nil
}

func (m smartListModel) View() string {
	if len(m.suggestions) == 0 {
		return "No smart suggestions found.\n"
	}

	start := m.page * m.pageSize
	end := start + m.pageSize
	if end > len(m.suggestions) {
		end = len(m.suggestions)
	}

	w := m.width
	if w <= 0 {
		w = 80
	}

	boxPadX := 2
	if w < 60 {
		boxPadX = 1
	}
	if w < 40 {
		boxPadX = 0
	}

	boxWidth := w - 2
	if boxWidth < 30 {
		boxWidth = 30
	}

	innerWidth := boxWidth - 2 - (boxPadX * 2)
	if innerWidth < 24 {
		innerWidth = 24
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED"))
	queryStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#3B82F6")).Bold(true)
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))
	sourceStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA"))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))

	title := "💡 Smart Suggestions"
	if strings.TrimSpace(m.query) != "" {
		title += "  " + queryStyle.Render(m.query)
	}

	var sb strings.Builder
	if m.msg != "" {
		alertText := lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB")).Bold(true).Render(m.msg)
		alertStr := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#10B981")).
			Padding(0, 1).
			Render(alertText)

		titleWidth := lipgloss.Width(title)
		alertWidth := lipgloss.Width(alertStr)
		padding := innerWidth - titleWidth - alertWidth
		if padding < 1 {
			padding = 1
		}

		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Center,
			headerStyle.Render(title),
			lipgloss.NewStyle().Width(padding).Render(""),
			alertStr,
		))
		sb.WriteString("\n\n")
	} else {
		sb.WriteString(headerStyle.Render(title))
		sb.WriteString("\n\n")
	}

	sb.WriteString(metaStyle.Render(smartContextSummary(m.context)))
	sb.WriteString("\n\n")
	if smartLine := smartDifferenceSummary(m.suggestions, innerWidth); smartLine != "" {
		sb.WriteString(metaStyle.Render(smartLine))
		sb.WriteString("\n\n")
	}

	indexStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Width(4).Align(lipgloss.Right)
	showDesc := w >= 70
	showSource := w >= 55

	availWidth := innerWidth - 7
	if showSource {
		availWidth -= 18
	}
	if availWidth < 12 {
		availWidth = 12
	}

	for i := start; i < end; i++ {
		suggestion := m.suggestions[i]
		cursor := "  "
		cmdStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#10B981"))
		if m.cursor == i {
			cursor = "👉"
			cmdStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.Color("#3B82F6")).
				Padding(0, 1)
		}

		command := suggestion.Command
		if lipgloss.Width(command) > availWidth {
			command = truncate.StringWithTail(command, uint(availWidth), "...")
		}

		sourceLabel := ""
		if showSource {
			sourceLabel = sourceStyle.Render("["+compactSuggestionSource(suggestion.Source)+"]") + "  "
		}

		sb.WriteString(fmt.Sprintf("%s %s %s%s\n", cursor, indexStyle.Render(fmt.Sprintf("%d.", i+1)), sourceLabel, cmdStyle.Render(command)))

		if showDesc {
			if extra := smartSuggestionMeta(suggestion, innerWidth-6); extra != "" {
				sb.WriteString("      " + descStyle.Render(extra) + "\n")
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString(metaStyle.Render(fmt.Sprintf("Showing %d suggestions total.", len(m.suggestions))))
	sb.WriteString("\n\n")

	footerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#EAB308")).Bold(true)
	sb.WriteString(footerStyle.Render(fmt.Sprintf("Page %d/%d", m.page+1, m.numPages)))

	var footerNav string
	if w >= 80 {
		footerNav = " | [↑/↓] Navigate | [←/→] Prev/Next Page | [c/enter] Copy | [q] Quit"
	} else if w >= 60 {
		footerNav = " | ↑/↓ nav | ←/→ page | c copy | q quit"
	} else if w >= 40 {
		footerNav = " | ↑/↓ | ←/→ | c | q"
	} else {
		footerNav = " | ↑↓ | ←→ | c | q"
	}
	sb.WriteString(metaStyle.Render(footerNav + "\n"))

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7C3AED")).
		Padding(1, boxPadX).
		Width(boxWidth)

	return boxStyle.Render(strings.TrimRight(sb.String(), "\n"))
}

func smartContextSummary(ctx *appctx.Context) string {
	if ctx == nil {
		return "No context available"
	}

	parts := []string{}
	workspace := filepath.Base(ctx.WorkingDir)
	if workspace == "." || workspace == "/" || workspace == "\\" || workspace == "" {
		workspace = ctx.WorkingDir
	}
	if workspace != "" {
		parts = append(parts, "Project: "+workspace)
	}
	if ctx.ProjectType != "" && ctx.ProjectType != "unknown" {
		parts = append(parts, "Type: "+ctx.ProjectType)
	}
	if ctx.Shell != "" {
		parts = append(parts, "Shell: "+strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(ctx.Shell, ".exe"), ".cmd"), ".bat"))
	}
	if ctx.OS != "" {
		parts = append(parts, "OS: "+ctx.OS)
	}
	if ctx.IsGitRepo {
		if ctx.GitBranch != "" {
			parts = append(parts, "Branch: "+ctx.GitBranch)
		}
		if ctx.GitStatus.IsClean {
			parts = append(parts, "Clean")
		} else {
			parts = append(parts, "Dirty")
		}
	}
	return strings.Join(parts, "  |  ")
}

func compactSuggestionSource(source string) string {
	source = strings.TrimSpace(source)
	switch {
	case strings.Contains(source, "Smart History"):
		return "history"
	case strings.Contains(source, "Context"):
		return "context"
	case strings.Contains(source, "Quick"):
		return "quick"
	case strings.Contains(source, "Command DB"):
		return "reference"
	case strings.Contains(source, "Fuzzy"):
		return "fuzzy"
	default:
		return strings.ToLower(source)
	}
}

func smartSuggestionMeta(suggestion smart.Suggestion, width int) string {
	parts := make([]string, 0, 4)
	if suggestion.Description != "" {
		parts = append(parts, suggestion.Description)
	}
	if hint := smartSuggestionHint(suggestion); hint != "" {
		parts = append(parts, hint)
	}
	if suggestion.IsPerfectMatch {
		parts = append(parts, "exact")
	}
	if suggestion.ContextMatch >= 0.3 {
		parts = append(parts, "local context")
	}
	if suggestion.UsageCount > 1 {
		parts = append(parts, fmt.Sprintf("used %d times", suggestion.UsageCount))
	}
	if meta := strings.Join(parts, "  ·  "); meta != "" {
		if width > 0 && lipgloss.Width(meta) > width {
			return truncate.StringWithTail(meta, uint(width), "...")
		}
		return meta
	}
	return ""
}

func smartDifferenceSummary(suggestions []smart.Suggestion, width int) string {
	if len(suggestions) == 0 {
		return ""
	}

	historyCount := 0
	contextCount := 0
	referenceCount := 0
	exploreCount := 0
	var bestNonHistory string

	for _, suggestion := range suggestions {
		switch compactSuggestionSource(suggestion.Source) {
		case "history":
			historyCount++
		case "context", "quick":
			contextCount++
			if bestNonHistory == "" {
				bestNonHistory = suggestion.Command
			}
		case "reference":
			referenceCount++
			if bestNonHistory == "" {
				bestNonHistory = suggestion.Command
			}
		case "fuzzy", "common":
			exploreCount++
			if bestNonHistory == "" {
				bestNonHistory = suggestion.Command
			}
		default:
			if bestNonHistory == "" && !strings.Contains(strings.ToLower(suggestion.Source), "history") {
				bestNonHistory = suggestion.Command
			}
		}
	}

	parts := []string{
		fmt.Sprintf("Smart layer: %d history", historyCount),
	}
	if contextCount > 0 {
		parts = append(parts, fmt.Sprintf("%d context", contextCount))
	}
	if referenceCount > 0 {
		parts = append(parts, fmt.Sprintf("%d reference", referenceCount))
	}
	if exploreCount > 0 {
		parts = append(parts, fmt.Sprintf("%d explore", exploreCount))
	}
	if bestNonHistory != "" {
		parts = append(parts, "best new idea: "+bestNonHistory)
	}

	summary := strings.Join(parts, "  |  ")
	if width > 0 && lipgloss.Width(summary) > width {
		return truncate.StringWithTail(summary, uint(width), "...")
	}
	return summary
}

func smartSuggestionHint(suggestion smart.Suggestion) string {
	switch compactSuggestionSource(suggestion.Source) {
	case "context":
		return "context pick"
	case "quick":
		return "workflow shortcut"
	case "reference":
		return "not required in your history"
	case "fuzzy":
		return "discovery match"
	default:
		return ""
	}
}
