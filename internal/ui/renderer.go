// Package ui provides UI components for WUT
package ui

import (
	"fmt"
	"os"
	"strings"

	"wut/internal/config"
	"golang.org/x/term"
)

// Capabilities represents terminal capabilities
type Capabilities struct {
	Supports256Colors bool
	SupportsTrueColor bool
	SupportsEmoji     bool
	SupportsNerdFonts bool
	Width             int
	Height            int
}

// Detect detects terminal capabilities
func detectCapabilities() *Capabilities {
	caps := &Capabilities{
		Supports256Colors: os.Getenv("TERM") != "dumb",
		SupportsTrueColor: os.Getenv("COLORTERM") == "truecolor",
		SupportsEmoji:     os.Getenv("LANG") != "C" && !strings.Contains(os.Getenv("TERM"), "linux"),
		SupportsNerdFonts: false,
		Width:             80,
		Height:            24,
	}

	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		if w > 0 {
			caps.Width = w
		}
		if h > 0 {
			caps.Height = h
		}
	}

	return caps
}

// ShouldUseASCII returns true if terminal doesn't support Unicode
func (c *Capabilities) ShouldUseASCII() bool {
	return !c.SupportsEmoji
}

// ShouldUseEmoji returns true if terminal supports emoji
func (c *Capabilities) ShouldUseEmoji() bool {
	return c.SupportsEmoji
}

// ShouldUseNerdFonts returns true if terminal supports nerd fonts
func (c *Capabilities) ShouldUseNerdFonts() bool {
	return c.SupportsNerdFonts
}

// Renderer provides UI rendering capabilities with terminal adaptation
type Renderer struct {
	config config.UIConfig
	caps   *Capabilities
}

// NewRenderer creates a new UI renderer
func NewRenderer(cfg config.UIConfig) *Renderer {
	return &Renderer{
		config: cfg,
		caps:   detectCapabilities(),
	}
}

// PrintHeader prints a header
func (r *Renderer) PrintHeader(title string) {
	if r.caps.ShouldUseASCII() {
		fmt.Println("=== " + title + " ===")
	} else {
		fmt.Println(StyleTitle.Render(title))
	}
}

// PrintBox prints a box around content
func (r *Renderer) PrintBox(content string) {
	if r.caps.ShouldUseASCII() {
		lines := strings.Split(content, "\n")
		maxLen := 0
		for _, line := range lines {
			if len(line) > maxLen {
				maxLen = len(line)
			}
		}

		fmt.Println("+" + strings.Repeat("-", maxLen+2) + "+")
		for _, line := range lines {
			fmt.Printf("| %s%s |\n", line, strings.Repeat(" ", maxLen-len(line)))
		}
		fmt.Println("+" + strings.Repeat("-", maxLen+2) + "+")
	} else {
		fmt.Println(content)
	}
}

// Icon returns an icon adapted to terminal capabilities
func (r *Renderer) Icon(name string) string {
	if r.caps == nil {
		r.caps = detectCapabilities()
	}

	icons := map[string]map[string]string{
		"check": {
			"emoji": "✓",
			"ascii": "[OK]",
			"nerd":  "\uf00c",
		},
		"cross": {
			"emoji": "✗",
			"ascii": "[X]",
			"nerd":  "\uf00d",
		},
		"info": {
			"emoji": "ℹ",
			"ascii": "[i]",
			"nerd":  "\uf129",
		},
		"warning": {
			"emoji": "⚠",
			"ascii": "[!]",
			"nerd":  "\uf071",
		},
		"rocket": {
			"emoji": "🚀",
			"ascii": "=>",
			"nerd":  "\uf135",
		},
		"star": {
			"emoji": "⭐",
			"ascii": "*",
			"nerd":  "\uf005",
		},
		"arrow": {
			"emoji": "→",
			"ascii": "->",
			"nerd":  "\uf061",
		},
		"bullet": {
			"emoji": "•",
			"ascii": "*",
			"nerd":  "\uf111",
		},
		"folder": {
			"emoji": "📁",
			"ascii": "[DIR]",
			"nerd":  "\uf07b",
		},
		"file": {
			"emoji": "📄",
			"ascii": "[FILE]",
			"nerd":  "\uf15b",
		},
	}

	iconSet, ok := icons[name]
	if !ok {
		return ""
	}

	if r.caps.ShouldUseNerdFonts() {
		return iconSet["nerd"]
	}
	if r.caps.ShouldUseEmoji() {
		return iconSet["emoji"]
	}
	return iconSet["ascii"]
}
