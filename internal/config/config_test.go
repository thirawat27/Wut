package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFileAtomicReplacesExistingFile covers the normal path: the new
// content lands and no temp file is left behind.
func TestWriteFileAtomicReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("old: true\n"), 0600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := writeFileAtomic(path, []byte("new: true\n"), 0600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "new: true\n" {
		t.Fatalf("content = %q, want %q", got, "new: true\n")
	}

	assertNoTempLeftovers(t, dir, "config.yaml")
}

// TestWriteFileAtomicCreatesMissingFile covers a first save, where there is no
// existing file to replace.
func TestWriteFileAtomicCreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := writeFileAtomic(path, []byte("fresh: true\n"), 0600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "fresh: true\n" {
		t.Fatalf("content = %q", got)
	}

	assertNoTempLeftovers(t, dir, "config.yaml")
}

// TestWriteFileAtomicLeavesOriginalOnFailure is the point of the atomic write:
// a failed save must not destroy the config that was already there. An
// unwritable destination directory stands in for an interrupted write.
func TestWriteFileAtomicLeavesOriginalOnFailure(t *testing.T) {
	dir := t.TempDir()
	// A path whose parent directory does not exist fails at temp-file creation,
	// before anything touches the destination.
	path := filepath.Join(dir, "missing-dir", "config.yaml")

	if err := writeFileAtomic(path, []byte("never: written\n"), 0600); err == nil {
		t.Fatal("expected an error when the destination directory does not exist")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("destination should not exist, stat error = %v", err)
	}
}

func assertNoTempLeftovers(t *testing.T, dir, base string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), base+".tmp-") {
			t.Fatalf("temp file left behind: %s", entry.Name())
		}
	}
}
