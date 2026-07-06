package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveWUTSection(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		removed bool
	}{
		{
			name:    "removes single block",
			input:   "foo\n# WUT Shell Integration\nbind stuff\n# End WUT Integration\nbar\n",
			want:    "foo\nbar\n",
			removed: true,
		},
		{
			name:    "removes legacy end marker",
			input:   "foo\n# WUT Shell Integration\nold\n# End WUT Shell Integration\nbar\n",
			want:    "foo\nbar\n",
			removed: true,
		},
		{
			name:    "no block returns unchanged",
			input:   "foo\nbar\n",
			want:    "foo\nbar\n",
			removed: false,
		},
		{
			name:    "collapses trailing blank lines",
			input:   "foo\n\n# WUT Shell Integration\n# End WUT Integration\n\n\n",
			want:    "foo\n",
			removed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, removed := removeWUTSection(tt.input)
			if removed != tt.removed {
				t.Fatalf("removed = %v, want %v", removed, tt.removed)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBackupAndRestore(t *testing.T) {
	tmp := t.TempDir()
	original := filepath.Join(tmp, "config")
	content := []byte("original shell config\n")
	if err := os.WriteFile(original, content, 0644); err != nil {
		t.Fatal(err)
	}

	// Override data directory for the test.
	oldFn := configDataDir
	configDataDir = func() string { return filepath.Join(tmp, "wut-data") }
	defer func() { configDataDir = oldFn }()

	backupPath, err := backupConfigFile(original)
	if err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	if backupPath == "" {
		t.Fatal("expected backup path")
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file not created: %v", err)
	}

	// Mutate original.
	if err := os.WriteFile(original, []byte("mutated\n"), 0644); err != nil {
		t.Fatal(err)
	}

	restored, err := restoreLatestBackup(original)
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if restored == "" {
		t.Fatal("expected restored backup path")
	}

	got, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("restored content = %q, want %q", got, content)
	}
}

func TestGenerateShellCodeDoesNotContainInvasiveHooks(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish", "powershell", "pwsh"} {
		code := GenerateShellCode(sh)
		if strings.Contains(code, "command_not_found") {
			t.Errorf("%s integration contains command_not_found hook", sh)
		}
	}
}
