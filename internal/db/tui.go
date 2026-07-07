// Package db provides TLDR Pages TUI for WUT
package db

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// Styles for the TUI
var (
	// Colors
	primaryColor   = lipgloss.Color("#7C3AED") // Purple
	secondaryColor = lipgloss.Color("#10B981") // Emerald
	accentColor    = lipgloss.Color("#F59E0B") // Amber
	dangerColor    = lipgloss.Color("#EF4444") // Red
	infoColor      = lipgloss.Color("#3B82F6") // Blue
	mutedColor     = lipgloss.Color("#6B7280") // Gray
	textColor      = lipgloss.Color("#F3F4F6") // Light gray
	bgColor        = lipgloss.Color("#1F2937") // Dark gray

	// Title styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			Background(bgColor).
			Padding(0, 1).
			MarginBottom(1)

	// Command name style
	commandStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(secondaryColor).
			Background(lipgloss.Color("#064E3B")).
			Padding(0, 1)

	// Description style
	descriptionStyle = lipgloss.NewStyle().
				Foreground(textColor).
				Italic(true)

	// Example description style
	exampleDescStyle = lipgloss.NewStyle().
				Foreground(accentColor)

	// Command example style
	exampleCmdStyle = lipgloss.NewStyle().
			Foreground(textColor).
			Background(lipgloss.Color("#374151")).
			Padding(0, 1).
			MarginLeft(2)

	// Selected example style
	selectedExampleStyle = lipgloss.NewStyle().
				Foreground(textColor).
				Background(lipgloss.Color("#4B5563")).
				Padding(0, 1).
				MarginLeft(2).
				Bold(true)

	// Platform badge style
	platformStyle = lipgloss.NewStyle().
			Foreground(bgColor).
			Background(infoColor).
			Padding(0, 1).
			Bold(true)

	// Search input style
	inputStyle = lipgloss.NewStyle().
			Foreground(textColor).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(primaryColor).
			Padding(0, 1)

	// Help style
	helpStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			MarginTop(1)

	// Border styles
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(primaryColor).
			Padding(0, 1)

	// Notification style
	notificationStyle = lipgloss.NewStyle().
				Foreground(bgColor).
				Background(secondaryColor).
				Padding(0, 1).
				Bold(true)
)

// DBItem represents an item in the list
type DBItem struct {
	Page      *Page
	ItemTitle string
	ItemDesc  string
}

// FilterValue implements list.Item interface
func (i DBItem) FilterValue() string {
	return i.ItemTitle
}

// Title returns the item title for the list
func (i DBItem) Title() string {
	return i.ItemTitle
}

// Description returns the item description for the list
func (i DBItem) Description() string {
	return i.ItemDesc
}

// Model represents the DB TUI model
type Model struct {
	client           *Client
	storage          *Storage
	input            textinput.Model
	list             list.Model
	viewport         viewport.Model
	currentPage      *Page
	pages            []Page
	width            int
	height           int
	loading          bool
	err              error
	selected         string
	mode             string // "search", "detail"
	selectedExample  int    // Index of selected example in detail mode
	totalExamples    int
	notification     string
	notificationTime int
	executedCmd      string // Store command to execute after TUI closes
	searchToken      int
	lastSearchQuery  string
}

// NewModel creates a new DB TUI model
func NewModel() *Model {
	// Setup input
	input := textinput.New()
	input.Placeholder = "Search command (e.g., git, docker, npm)..."
	input.Focus()
	input.CharLimit = 120
	input.Width = 50

	// Setup list
	items := []list.Item{}
	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Command Reference"
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	// Setup viewport
	vp := viewport.New(0, 0)

	return &Model{
		client:          NewClient(),
		input:           input,
		list:            l,
		viewport:        vp,
		pages:           []Page{},
		mode:            "search",
		selectedExample: 0,
	}
}

// SetStorage sets the local storage for offline support
func (m *Model) SetStorage(storage *Storage) {
	m.storage = storage
	// Update client with storage
	m.client.SetStorage(storage)
}

// SetInitialPage opens the TUI directly in detail mode for a preloaded page.
func (m *Model) SetInitialPage(page *Page) {
	if page == nil {
		return
	}

	m.currentPage = page
	m.mode = "detail"
	m.selectedExample = 0
	m.totalExamples = len(page.Examples)
	m.refreshDetailViewport()
}

// GetExecutedCommand returns the command that should be executed
func (m *Model) GetExecutedCommand() string {
	return m.executedCmd
}

// Init initializes the model
func (m *Model) Init() tea.Cmd {
	if m.currentPage != nil {
		return textinput.Blink
	}
	return tea.Batch(
		textinput.Blink,
		m.loadSuggestions(""),
	)
}

// Update handles messages
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		w := msg.Width
		h := msg.Height

		inputW := w - 4
		if inputW > 120 {
			inputW = 120
		}
		if inputW < 10 {
			inputW = 10
		}
		m.input.Width = inputW
		m.input.CharLimit = inputW

		listH := h - 8
		if h < 15 {
			listH = h - 5
		}
		if listH < 4 {
			listH = 4
		}
		m.list.SetSize(w, listH)

		vpW := w - 4
		if vpW < 10 {
			vpW = 10
		}

		vpH := h - 10
		if h < 15 {
			vpH = h - 6
		}
		if vpH < 4 {
			vpH = 4
		}
		m.viewport.Width = vpW
		m.viewport.Height = vpH
		if m.currentPage != nil {
			m.refreshDetailViewport()
		}

	case tea.KeyMsg:
		// Global keys
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}

		// Mode-specific keys
		if m.mode == "search" {
			switch msg.String() {
			case "esc":
				return m, tea.Quit

			case "enter":
				query := strings.TrimSpace(m.input.Value())
				if query != "" {
					// Search for the command
					ctx := context.Background()
					page, err := m.client.GetPageAnyPlatform(ctx, query)
					if err == nil {
						m.currentPage = page
						m.mode = "detail"
						m.selectedExample = 0
						m.totalExamples = len(page.Examples)
						m.refreshDetailViewport()
					} else {
						// Select from list
						if item, ok := m.list.SelectedItem().(DBItem); ok {
							return m, m.showPage(item.Page.Name)
						}
					}
				} else {
					// Select from list
					if item, ok := m.list.SelectedItem().(DBItem); ok {
						return m, m.showPage(item.Page.Name)
					}
				}

			case "/":
				m.input.Focus()
			}
		} else { // detail mode
			switch msg.String() {
			case "esc", "backspace", "q":
				m.mode = "search"
				m.currentPage = nil
				m.selectedExample = 0
				return m, nil

			case "j", "down":
				if m.selectedExample < m.totalExamples-1 {
					m.selectedExample++
					m.refreshDetailViewport()
				}

			case "k", "up":
				if m.selectedExample > 0 {
					m.selectedExample--
					m.refreshDetailViewport()
				}

			case "c", "y":
				// Copy current example to clipboard
				if m.currentPage != nil && m.selectedExample < len(m.currentPage.Examples) {
					cmd := cleanCommand(m.currentPage.Examples[m.selectedExample].Command)
					if err := clipboard.WriteAll(cmd); err == nil {
						return m, m.showNotification("Copied to clipboard")
					} else {
						return m, m.showNotification("Copy failed: " + err.Error())
					}
				}

			case "e", "enter":
				// Copy current example to clipboard then exit
				if m.currentPage != nil && m.selectedExample < len(m.currentPage.Examples) {
					cmd := cleanCommand(m.currentPage.Examples[m.selectedExample].Command)
					if err := clipboard.WriteAll(cmd); err == nil {
						m.selected = cmd
					}
					return m, tea.Quit
				}

			case "1", "2", "3", "4", "5", "6", "7", "8", "9":
				// Jump to example number
				num := int(msg.Runes[0] - '1')
				if num < m.totalExamples {
					m.selectedExample = num
					m.refreshDetailViewport()
				}
			case "pgdown", "ctrl+f":
				m.viewport.PageDown()
			case "pgup", "ctrl+b":
				m.viewport.PageUp()
			}
		}

	case pageLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.currentPage = msg.page
			m.mode = "detail"
			m.selectedExample = 0
			m.totalExamples = len(msg.page.Examples)
			m.refreshDetailViewport()
		}
		return m, nil

	case searchResultsMsg:
		if msg.token != m.searchToken || msg.query != strings.TrimSpace(m.input.Value()) {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.pages = msg.pages
			items := make([]list.Item, len(msg.pages))
			suggestions := make([]string, 0, len(msg.pages))
			for i, page := range msg.pages {
				items[i] = DBItem{
					Page:      &page,
					ItemTitle: page.Name,
					ItemDesc:  page.Description,
				}
				suggestions = append(suggestions, page.Name)
			}
			m.list.SetItems(items)
			m.input.SetSuggestions(suggestions)
		}
		return m, nil

	case tickMsg:
		if m.notificationTime > 0 {
			m.notificationTime--
			if m.notificationTime == 0 {
				m.notification = ""
			}
			return m, m.tick()
		}
	}

	// Update components based on mode
	if m.mode == "search" {
		// Update input
		newInput, inputCmd := m.input.Update(msg)
		m.input = newInput
		cmds = append(cmds, inputCmd)

		// Update list
		newList, listCmd := m.list.Update(msg)
		m.list = newList
		cmds = append(cmds, listCmd)

		// Real-time search on input change
		if _, ok := msg.(tea.KeyMsg); ok {
			query := strings.TrimSpace(m.input.Value())
			if query != m.lastSearchQuery {
				cmds = append(cmds, m.loadSuggestions(query))
			}
		}
	} else {
		// Update viewport in detail mode
		newViewport, vpCmd := m.viewport.Update(msg)
		m.viewport = newViewport
		cmds = append(cmds, vpCmd)
	}

	return m, tea.Batch(cmds...)
}

// View renders the UI
func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}

	if m.mode == "search" {
		return m.searchView()
	}
	return m.detailView()
}

// searchView renders the search mode
func (m *Model) searchView() string {
	var b strings.Builder

	// Title
	title := titleStyle.Render("🔍 Command Reference")
	b.WriteString(title)
	b.WriteString("\n")

	// Search input
	inputBox := inputStyle.Render(m.input.View())
	b.WriteString(inputBox)
	b.WriteString("\n")

	// Loading indicator
	if m.loading {
		b.WriteString("⏳ Searching...")
		b.WriteString("\n")
	}

	// Error message
	if m.err != nil {
		errMsg := lipgloss.NewStyle().
			Foreground(dangerColor).
			Render(fmt.Sprintf("❌ Error: %v", m.err))
		b.WriteString(errMsg)
		b.WriteString("\n")
	}

	// List
	b.WriteString(m.list.View())

	// Help
	helpText := "enter: view • /: search • ↓↑: navigate • esc/q: quit"
	if m.width < 80 {
		helpText = "enter/open • /search • ↓↑ • esc/q"
	}
	if m.width < 55 {
		helpText = "enter • / • ↓↑ • esc"
	}
	if m.width < 35 {
		helpText = "/ • esc"
	}
	help := helpStyle.Render(helpText)
	b.WriteString("\n")
	b.WriteString(help)

	// Container box for search view to keep it clean and bounded
	boxW := m.width - 2
	if boxW < 20 {
		boxW = 20
	}

	// Create a wrapper with padding to prevent overflow
	wrapper := lipgloss.NewStyle().Width(boxW).Render(b.String())
	return wrapper
}

// detailView renders the detail mode
func (m *Model) detailView() string {
	if m.currentPage == nil {
		return "Loading..."
	}

	var b strings.Builder

	// Header with back button and command name
	header := lipgloss.JoinHorizontal(
		lipgloss.Left,
		lipgloss.NewStyle().Foreground(mutedColor).Render("← esc "),
		commandStyle.Render(m.currentPage.Name),
		" ",
		platformStyle.Render(m.currentPage.Platform),
	)
	b.WriteString(header)
	b.WriteString("\n")

	b.WriteString("\n\n")
	b.WriteString(m.viewport.View())

	// Notification
	if m.notification != "" {
		b.WriteString("\n")
		b.WriteString(notificationStyle.Render(m.notification))
	}

	// Footer
	footerText := "↑/↓: sel • pgup/pgdn: scroll • 1-9: jump • c: copy • e: copy+exit • esc: back"
	if m.width < 100 {
		footerText = "↑/↓: sel • pgup/pgdn • 1-9: jump • c: copy • e: copy+exit • esc: back"
	}
	if m.width < 75 {
		footerText = "↑/↓: sel • pgup/pgdn • 1-9 • c: copy • e: copy+exit • esc: back"
	}
	if m.width < 55 {
		footerText = "↑/↓ • pgup/pgdn • 1-9 • c • e • esc"
	}
	if m.width < 35 {
		footerText = "↑/↓ • c • e • esc"
	}

	footer := helpStyle.Render(footerText)
	b.WriteString("\n")
	b.WriteString(footer)

	boxW := m.width - 2
	if boxW < 20 {
		boxW = 20
	}

	activeBoxStyle := boxStyle.Width(boxW)
	return activeBoxStyle.Render(b.String())
}

// renderPage renders a page for viewport
func (m *Model) renderPage(page *Page) string {
	if page == nil {
		return ""
	}

	var b strings.Builder

	contentWidth := m.viewport.Width - 2
	if contentWidth < 10 {
		contentWidth = 10
	}

	wrapCmd := func(cmd string) string {
		return lipgloss.NewStyle().Width(contentWidth - 4).Render(cmd)
	}

	// Description
	if page.Description != "" {
		b.WriteString(descriptionStyle.Render(page.Description))
		b.WriteString("\n")
	}

	// Examples
	if len(page.Examples) > 0 {
		b.WriteString(lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			Render("Examples:"))
		b.WriteString("\n")

		for i, ex := range page.Examples {
			numStyle := lipgloss.NewStyle().Foreground(mutedColor)
			if i == m.selectedExample {
				numStyle = numStyle.Bold(true).Foreground(accentColor)
			}
			b.WriteString(numStyle.Render(fmt.Sprintf("%d.", i+1)))
			b.WriteString(" ")
			b.WriteString(exampleDescStyle.Render(ex.Description))
			b.WriteString("\n")

			cmdStyle := exampleCmdStyle
			if i == m.selectedExample {
				cmdStyle = selectedExampleStyle
			}
			b.WriteString(cmdStyle.Render(wrapCmd(ex.Command)))
			b.WriteString("\n")
		}
	}

	wrapper := lipgloss.NewStyle().Width(contentWidth)
	return wrapper.Render(b.String())
}

// Selected returns the selected command
func (m *Model) Selected() string {
	return m.selected
}

// Messages for async operations
type pageLoadedMsg struct {
	page *Page
	err  error
}
type searchResultsMsg struct {
	pages []Page
	err   error
	query string
	token int
}
type tickMsg struct{}

// showNotification shows a notification for a few seconds
func (m *Model) showNotification(msg string) tea.Cmd {
	m.notification = msg
	m.notificationTime = 3
	return m.tick()
}

// tick creates a tick command
func (m *Model) tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

// loadSuggestions refreshes search results for the current query.
func (m *Model) loadSuggestions(query string) tea.Cmd {
	query = strings.TrimSpace(query)
	m.loading = true
	m.err = nil
	m.lastSearchQuery = query
	m.searchToken++
	token := m.searchToken

	return func() tea.Msg {
		matchQuery := query
		if len(matchQuery) < 2 {
			matchQuery = ""
		}

		commands, err := m.client.FindCommandMatches(context.Background(), matchQuery, 50)
		if err != nil {
			return searchResultsMsg{err: err, query: query, token: token}
		}

		var pages []Page
		for _, cmd := range commands {
			pages = append(pages, Page{
				Name:        cmd,
				Description: fmt.Sprintf("Open examples for '%s'", cmd),
				Platform:    "common",
			})
		}

		if len(query) >= 2 && m.storage != nil {
			storedPages, err := m.storage.SearchLocalLimited(query, 50)
			if err == nil && len(storedPages) > 0 {
				pages = make([]Page, len(storedPages))
				for i, sp := range storedPages {
					pages[i] = Page{
						Name:        sp.Name,
						Platform:    sp.Platform,
						Description: sp.Description,
					}
				}
			}
		}

		if len(pages) == 0 && query != "" {
			return searchResultsMsg{err: fmt.Errorf("command not found: %s", query), query: query, token: token}
		}

		return searchResultsMsg{pages: pages, query: query, token: token}
	}
}

// showPage loads and shows a specific page
func (m *Model) showPage(command string) tea.Cmd {
	m.loading = true
	m.err = nil

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		page, err := m.client.GetPageAnyPlatform(ctx, command)
		return pageLoadedMsg{page: page, err: err}
	}
}

func (m *Model) refreshDetailViewport() {
	if m.currentPage == nil {
		return
	}
	m.viewport.SetContent(m.renderPage(m.currentPage))
	m.ensureSelectedExampleVisible()
}

func (m *Model) ensureSelectedExampleVisible() {
	if m.currentPage == nil || m.viewport.Height <= 0 {
		return
	}

	top := m.selectedExampleLine()
	bottom := top + 1

	switch {
	case m.viewport.YOffset > top:
		m.viewport.SetYOffset(top)
	case m.viewport.YOffset+m.viewport.Height-1 < bottom:
		m.viewport.SetYOffset(max(0, bottom-m.viewport.Height+1))
	}
}

func (m *Model) selectedExampleLine() int {
	if m.currentPage == nil || m.selectedExample < 0 {
		return 0
	}

	line := 0
	if m.currentPage.Description != "" {
		line++
	}
	if len(m.currentPage.Examples) > 0 {
		line++
	}
	line += m.selectedExample * 2
	return line
}

// cleanCommand removes placeholder syntax for execution
func cleanCommand(cmd string) string {
	// Remove <placeholder> syntax
	result := cmd
	for {
		start := strings.Index(result, "<")
		if start == -1 {
			break
		}
		end := strings.Index(result[start:], ">")
		if end == -1 {
			break
		}
		end += start

		// Extract the placeholder content
		placeholder := result[start+1 : end]

		// If it has | (choices), use the first one
		if before, _, ok := strings.Cut(placeholder, "|"); ok {
			choice := strings.TrimSpace(before)
			// Remove [ and ] if present
			choice = strings.Trim(choice, "[]")
			result = result[:start] + choice + result[end+1:]
		} else {
			// Just a simple placeholder, replace with empty
			result = result[:start] + result[end+1:]
		}
	}

	return strings.TrimSpace(result)
}

// ExecuteCommand executes a command in the shell
func ExecuteCommand(cmd string) error {
	cleanCmd := cleanCommand(cmd)

	var shell string
	var args []string

	switch runtime.GOOS {
	case "windows":
		// Try PowerShell first, then CMD
		if _, err := exec.LookPath("powershell"); err == nil {
			shell = "powershell"
			args = []string{"-Command", cleanCmd}
		} else {
			shell = "cmd"
			args = []string{"/C", cleanCmd}
		}
	default:
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		args = []string{"-c", cleanCmd}
	}

	command := exec.Command(shell, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin

	return command.Run()
}

// CreateTable creates a table for displaying multiple pages
func CreateTable(pages []Page) string {
	if len(pages) == 0 {
		return "No results found"
	}

	rows := [][]string{}
	for _, page := range pages {
		platform := platformStyle.Render(page.Platform)
		rows = append(rows, []string{
			commandStyle.Render(page.Name),
			page.Description,
			platform,
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(primaryColor)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				return lipgloss.NewStyle().
					Bold(true).
					Foreground(primaryColor).
					Background(bgColor).
					Padding(0, 1)
			}
			return lipgloss.NewStyle().Padding(0, 1)
		}).
		Headers("Command", "Description", "Platform").
		Rows(rows...)

	return t.String()
}

// FormatPage formats a single page for terminal output
func FormatPage(page *Page) string {
	if page == nil {
		return ""
	}

	var b strings.Builder

	// Title with platform
	title := lipgloss.JoinHorizontal(
		lipgloss.Left,
		titleStyle.Render(fmt.Sprintf("📖 %s", page.Name)),
		" ",
		platformStyle.Render(page.Platform),
	)
	b.WriteString(title)
	b.WriteString("\n")

	// Description
	if page.Description != "" {
		b.WriteString(descriptionStyle.Render(page.Description))
		b.WriteString("\n")
	}

	// Examples
	if len(page.Examples) > 0 {
		b.WriteString(lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			Render("Examples:"))
		b.WriteString("\n")

		for i, ex := range page.Examples {
			// Example number and description
			b.WriteString(lipgloss.NewStyle().
				Foreground(accentColor).
				Bold(true).
				Render(fmt.Sprintf("%d.", i+1)))
			b.WriteString(" ")
			b.WriteString(exampleDescStyle.Render(ex.Description))
			b.WriteString("\n")

			// Command
			b.WriteString(exampleCmdStyle.Render(ex.Command))
			b.WriteString("\n")
		}
	}

	return boxStyle.Render(b.String())
}
