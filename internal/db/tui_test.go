package db

import (
	"testing"
)

func TestCleanCommand(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "choice placeholder keeps first option",
			in:   `git add <[-A|--all]>`,
			want: `git add -A`,
		},
		{
			name: "simple placeholder removed",
			in:   `docker exec -it <container> <command>`,
			want: `docker exec -it`,
		},
		{
			name: "mixed placeholders are normalized",
			in:   `tar -czf <archive_name> <file_1> <file_2>`,
			want: `tar -czf`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanCommand(tt.in); got != tt.want {
				t.Fatalf("cleanCommand(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestModelIgnoresStaleSearchResults(t *testing.T) {
	model := NewModel()
	model.input.SetValue("git")
	model.searchToken = 2

	stale := searchResultsMsg{
		token: 1,
		query: "docker",
		pages: []Page{{Name: "docker"}},
	}

	updated, _ := model.Update(stale)
	got := updated.(*Model)
	if len(got.pages) != 0 {
		t.Fatalf("stale results should be ignored, got %+v", got.pages)
	}

	fresh := searchResultsMsg{
		token: 2,
		query: "git",
		pages: []Page{{Name: "git", Description: "Open examples for 'git'"}},
	}

	updated, _ = got.Update(fresh)
	got = updated.(*Model)
	if len(got.pages) != 1 || got.pages[0].Name != "git" {
		t.Fatalf("fresh results should be applied, got %+v", got.pages)
	}
}

func TestSelectedExampleLine(t *testing.T) {
	model := NewModel()
	model.currentPage = &Page{
		Description: "Common Git operations",
		Examples: []Example{
			{Description: "Status", Command: "git status"},
			{Description: "Add", Command: "git add ."},
			{Description: "Commit", Command: `git commit -m "msg"`},
		},
	}
	model.selectedExample = 2

	if got := model.selectedExampleLine(); got != 6 {
		t.Fatalf("selectedExampleLine() = %d, want 6", got)
	}
}

func TestParsePageHandlesLargeContent(t *testing.T) {
	c := NewClient()
	content := "> A command that lists directory contents.\n\n- List files\n`ls`\n- List all files\n`ls -a`\n\n"
	page := c.parsePage(content, "ls", "common", "en")
	if page.Name == "" {
		t.Fatal("page name should not be empty")
	}
	if len(page.Examples) != 2 {
		t.Fatalf("expected 2 examples, got %d", len(page.Examples))
	}
}

func TestFetchBodyLimit(t *testing.T) {
	c := NewClient()
	largeContent := make([]byte, maxBodySize+1024)
	for i := range largeContent {
		largeContent[i] = 'x'
	}
	page := c.parsePage(string(largeContent), "test", "common", "en")
	if page == nil {
		t.Fatal("parsePage should handle large content without panic")
	}
}
