package cmd

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// errPickerAborted reports that the user dismissed the picker without choosing.
var errPickerAborted = errors.New("selection aborted")

// pickCorrection shows the candidate corrections and returns the one the user
// accepted.
//
// The picker draws on the controlling terminal, never on stdout, because stdout
// carries the chosen command back to the shell function that will run it. That
// separation is what lets `oops` execute the result in the user's own shell —
// so `cd`, exported variables, and shell functions behave as if the user had
// typed the command themselves.
func pickCorrection(original string, candidates []string) (string, error) {
	if len(candidates) == 0 {
		return "", errPickerAborted
	}

	tty, closeTTY, err := openTTY()
	if err != nil {
		// No controlling terminal (a pipe, a CI job, an editor's task runner).
		// Choosing silently would run a command the user never saw, so decline.
		return "", errPickerAborted
	}
	defer closeTTY()

	model := pickerModel{
		original:   original,
		candidates: candidates,
	}

	program := tea.NewProgram(
		model,
		tea.WithInput(tty),
		tea.WithOutput(tty),
	)

	result, err := program.Run()
	if err != nil {
		return "", err
	}

	final, ok := result.(pickerModel)
	if !ok || !final.accepted {
		return "", errPickerAborted
	}
	return final.candidates[final.cursor], nil
}

// openTTY returns a handle on the controlling terminal.
func openTTY() (*os.File, func(), error) {
	name := "/dev/tty"
	if runtime.GOOS == "windows" {
		name = "CONIN$"
	}

	tty, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	return tty, func() { _ = tty.Close() }, nil
}

// ─── Model ───────────────────────────────────────────────────────────────────

type pickerModel struct {
	original   string
	candidates []string
	cursor     int
	accepted   bool
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "enter":
		m.accepted = true
		return m, tea.Quit
	case "ctrl+c", "esc", "q":
		return m, tea.Quit
	case "up", "k", "shift+tab":
		if m.cursor > 0 {
			m.cursor--
		} else {
			m.cursor = len(m.candidates) - 1
		}
	case "down", "j", "tab":
		m.cursor = (m.cursor + 1) % len(m.candidates)
	}
	return m, nil
}

var (
	pickerOriginalStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Strikethrough(true)
	pickerChosenStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981")).Bold(true)
	pickerOtherStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))
	pickerHintStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	pickerArrowStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8B5CF6")).Bold(true)
)

func (m pickerModel) View() string {
	var b strings.Builder

	b.WriteString("\n  " + pickerOriginalStyle.Render(m.original) + "\n\n")

	for i, candidate := range m.candidates {
		if i == m.cursor {
			b.WriteString(fmt.Sprintf("  %s %s\n", pickerArrowStyle.Render("❯"), pickerChosenStyle.Render(candidate)))
			continue
		}
		b.WriteString(fmt.Sprintf("    %s\n", pickerOtherStyle.Render(candidate)))
	}

	hint := "enter run · esc cancel"
	if len(m.candidates) > 1 {
		hint = "↑↓ choose · enter run · esc cancel"
	}
	b.WriteString("\n  " + pickerHintStyle.Render(hint) + "\n")

	return b.String()
}
