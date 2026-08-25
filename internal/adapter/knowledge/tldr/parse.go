package tldr

import (
	"strings"

	"github.com/thirawat27/wut/internal/core/knowledge"
)

// ParsePage reads one tldr markdown page.
//
// The format is small and stable:
//
//	# name
//
//	> description line
//	> more description
//	> More information: <url>.
//
//	- what this example does:
//
//	`the command {{with placeholders}}`
//
// Placeholders are kept as written. Rewriting {{file.txt}} into something
// concrete would be WUT inventing an argument, and the whole design says the
// command text comes from the source rather than from us.
func ParsePage(name string, platform knowledge.Platform, body string) knowledge.Page {
	page := knowledge.Page{Name: name, Platform: platform}

	var (
		descriptions []string
		pendingDesc  string
		titleSeen    bool
	)

	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue

		case strings.HasPrefix(trimmed, "# "):
			// The *first* heading only. Some pages carry further "# " headings
			// in their body — a "# Locations" section, for instance — and
			// taking the last one renamed the page after a subsection, which
			// then surfaced in search results as a command that does not exist.
			if titleSeen {
				continue
			}
			if title := strings.TrimSpace(trimmed[2:]); title != "" {
				page.Name = title
				titleSeen = true
			}

		case strings.HasPrefix(trimmed, ">"):
			text := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			if text == "" {
				continue
			}
			if url, ok := moreInfo(text); ok {
				page.MoreInfo = url
				continue
			}
			descriptions = append(descriptions, text)

		case strings.HasPrefix(trimmed, "- "):
			// The description of the example that follows.
			pendingDesc = strings.TrimSuffix(strings.TrimSpace(trimmed[2:]), ":")

		case strings.HasPrefix(trimmed, "`") && strings.HasSuffix(trimmed, "`") && len(trimmed) > 2:
			cmd := strings.TrimSpace(strings.Trim(trimmed, "`"))
			if cmd == "" {
				continue
			}
			page.Examples = append(page.Examples, knowledge.Example{
				Description: pendingDesc,
				Command:     cmd,
			})
			pendingDesc = ""
		}
	}

	page.Description = strings.Join(descriptions, " ")
	return page
}

// moreInfo recognises the trailing reference line, which is a URL rather than
// a description and should not be indexed as one.
func moreInfo(text string) (string, bool) {
	lower := strings.ToLower(text)
	for _, prefix := range []string{"more information:", "see also:", "more info:"} {
		if strings.HasPrefix(lower, prefix) {
			rest := strings.TrimSpace(text[len(prefix):])
			rest = strings.Trim(rest, "<>.")
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}
