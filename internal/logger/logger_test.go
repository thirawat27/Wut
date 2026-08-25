package logger

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestRotatingWriterConcurrentWrites is the regression guard for the data race
// in log rotation: Write and rotate both mutated file/size with no
// synchronisation, while the sync worker pool, the signal handler, and the
// suggestion engine all logged at the same time. Run with -race.
func TestRotatingWriterConcurrentWrites(t *testing.T) {
	dir := t.TempDir()

	rw, err := newRotatingWriter(Config{
		File:       filepath.Join(dir, "wut.log"),
		MaxSize:    1, // 1 MiB, small enough that rotation triggers
		MaxBackups: 3,
	})
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	defer rw.Close()

	line := []byte(strings.Repeat("x", 1024) + "\n")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if _, err := rw.Write(line); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestRotatingWriterResetsSizeAfterRotate covers the second defect in the same
// code: open() only refreshes size when the file already exists, and after a
// rotation it does not. The counter stayed at its pre-rotation value, so every
// following write rotated again and shifted the backups away.
func TestRotatingWriterResetsSizeAfterRotate(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "wut.log")

	rw, err := newRotatingWriter(Config{
		File:       logPath,
		MaxSize:    1,
		MaxBackups: 3,
	})
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	defer rw.Close()

	// Fill past 1 MiB so exactly one rotation happens.
	chunk := []byte(strings.Repeat("y", 64*1024))
	for i := 0; i < 17; i++ {
		if _, err := rw.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("expected a rotated backup at %s.1: %v", logPath, err)
	}

	// A third backup means the writer kept rotating on every write.
	if _, err := os.Stat(logPath + ".3"); err == nil {
		t.Fatal("writer rotated repeatedly: size was not reset after rotation")
	}

	rw.mu.Lock()
	size := rw.size
	rw.mu.Unlock()

	if size > int64(rw.maxSize)*1024*1024 {
		t.Fatalf("size counter (%d) exceeds the rotation threshold; it was not reset", size)
	}
}
