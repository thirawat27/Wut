//go:build !windows

package shell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thirawat27/wut/internal/port"
)

// A startup file belongs to the user. Installing a hook into it is not licence
// to change who can read it.
func TestInstallKeepsTheStartupFilesOwnPermissions(t *testing.T) {
	m, home := newTestManager(t)
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte("export A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(rc, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(port.InstallRequest{Shells: []string{"bash"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(rc)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("install changed the mode to %04o, want 0644", got)
	}
}
