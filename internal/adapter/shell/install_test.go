package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thirawat27/wut/internal/port"
)

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	home := t.TempDir()
	return New(home, Params{SessionsDir: filepath.Join(home, "state", "sessions")}), home
}

// The property that matters most: uninstall must leave the file exactly as it
// was. A tool that edits a user's rc file and cannot undo itself cleanly is
// one nobody should install.
func TestInstallThenUninstallIsByteIdentical(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			m, home := newTestManager(t)
			spec, _ := Lookup(shell)
			rc := filepath.Join(home, spec.RCFiles[0])
			if err := os.MkdirAll(filepath.Dir(rc), 0o700); err != nil {
				t.Fatal(err)
			}

			original := "# my shell config\nexport EDITOR=vim\nalias ll='ls -la'\n"
			if err := os.WriteFile(rc, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := m.Install(port.InstallRequest{Shells: []string{shell}}); err != nil {
				t.Fatalf("install: %v", err)
			}
			after, err := os.ReadFile(rc)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(after), blockBegin) {
				t.Fatal("managed block was not written")
			}
			if !strings.Contains(string(after), original) {
				t.Fatal("install did not preserve the user's own content")
			}

			if _, err := m.Uninstall(port.InstallRequest{Shells: []string{shell}}); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			restored, err := os.ReadFile(rc)
			if err != nil {
				t.Fatal(err)
			}
			if string(restored) != original {
				t.Errorf("uninstall did not restore the file\n got: %q\nwant: %q", restored, original)
			}
		})
	}
}

// Re-running install must replace the block, never stack a second one.
func TestInstallIsIdempotent(t *testing.T) {
	m, home := newTestManager(t)
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte("export A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, err := m.Install(port.InstallRequest{Shells: []string{"bash"}}); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), blockBegin); n != 1 {
		t.Errorf("found %d managed blocks after three installs, want 1", n)
	}
	if n := strings.Count(string(data), blockEnd); n != 1 {
		t.Errorf("found %d end markers, want 1", n)
	}
}

func TestSecondInstallReportsUnchanged(t *testing.T) {
	m, home := newTestManager(t)
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("export A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, _ := m.Install(port.InstallRequest{Shells: []string{"bash"}})
	if first.Changes[0].Action != "installed" {
		t.Fatalf("first action = %q, want installed", first.Changes[0].Action)
	}
	second, _ := m.Install(port.InstallRequest{Shells: []string{"bash"}})
	if second.Changes[0].Action != "unchanged" {
		t.Errorf("second action = %q, want unchanged", second.Changes[0].Action)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	m, home := newTestManager(t)
	rc := filepath.Join(home, ".bashrc")
	original := "export A=1\n"
	if err := os.WriteFile(rc, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := m.Install(port.InstallRequest{Shells: []string{"bash"}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rep.Changes[0].Action, "would-") {
		t.Errorf("action = %q, want a would- prefix", rep.Changes[0].Action)
	}
	if rep.Changes[0].Diff == "" {
		t.Error("dry run reported no diff summary")
	}
	data, _ := os.ReadFile(rc)
	if string(data) != original {
		t.Error("dry run modified the file")
	}
}

func TestInstallBacksUpFirst(t *testing.T) {
	m, home := newTestManager(t)
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte("export A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, _ := m.Install(port.InstallRequest{Shells: []string{"bash"}})
	backup := rep.Changes[0].Backup
	if backup == "" {
		t.Fatal("no backup was taken")
	}
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("backup unreadable: %v", err)
	}
	if string(data) != "export A=1\n" {
		t.Errorf("backup content = %q", data)
	}
}

// A block whose end marker was deleted by hand must be left alone. Guessing
// where it ends risks deleting the rest of the user's file.
func TestHandEditedBlockIsNotTouched(t *testing.T) {
	m, home := newTestManager(t)
	rc := filepath.Join(home, ".bashrc")
	broken := "# " + blockBegin + "\nsomething\n# the end marker is gone\nexport A=1\n"
	if err := os.WriteFile(rc, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Uninstall(port.InstallRequest{Shells: []string{"bash"}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(rc)
	if string(data) != broken {
		t.Errorf("a block with no end marker was modified:\n%q", data)
	}
}

// Every Full-class shell must produce a block that carries the record writer
// and defines wut as a function. Without both, capture silently does nothing.
func TestFullClassShellsRenderAWorkingBlock(t *testing.T) {
	p := Params{SessionsDir: "/tmp/wut/sessions"}
	for _, name := range FullClass() {
		t.Run(name, func(t *testing.T) {
			spec, _ := Lookup(name)
			block := Render(spec, p)
			if block == "" {
				t.Fatal("empty block")
			}
			for _, want := range []string{blockBegin, blockEnd, "wut"} {
				if !strings.Contains(block, want) {
					t.Errorf("block does not mention %q", want)
				}
			}
			// The record separators are the wire format. If they are missing,
			// the hook is writing something WUT cannot read.
			if !strings.Contains(block, `\037`) && !strings.Contains(block, "0x1F") &&
				!strings.Contains(block, `\x1f`) && !strings.Contains(block, `\u001f`) &&
				!strings.Contains(block, "char --integer 31") {
				t.Error("block never writes a unit separator, so it cannot be producing records")
			}
			if !strings.Contains(block, p.SessionsDir) {
				t.Error("block does not reference the sessions directory")
			}
		})
	}
}

// These fragments guard failures found only after generated blocks were
// sourced by their real interpreters. Keeping the checks beside Render makes
// the generator fail fast before the slower live-shell matrix runs.
func TestGeneratedHooksCarryTheirRuntimeRequirements(t *testing.T) {
	p := Params{SessionsDir: "/tmp/wut/sessions"}
	tests := map[string][]string{
		"bash":   {"__wut_tier='T0'"},
		"zsh":    {"zmodload zsh/mathfunc", `__wut_nf="$__wut_dir/$WUT_SESSION.nf"`},
		"nu":     {"$env.config? | default {}", "commandline", "WUT_NF"},
		"xonsh":  {"@events.on_command_not_found", `"notfound": ""`},
		"elvish": {`\u001f`, `\u001e`},
	}
	for name, fragments := range tests {
		t.Run(name, func(t *testing.T) {
			spec, _ := Lookup(name)
			block := Render(spec, p)
			for _, fragment := range fragments {
				if !strings.Contains(block, fragment) {
					t.Errorf("generated block does not contain %q", fragment)
				}
			}
			if strings.Contains(block, `\u{`) {
				t.Error("generated block contains an unsupported braced Unicode escape")
			}
		})
	}
}

// The hook must not fork. A subshell in the prompt path is the one mistake
// that makes users uninstall, and it is invisible until someone measures it.
func TestPosixHooksDoNotForkInThePromptPath(t *testing.T) {
	p := Params{SessionsDir: "/tmp/wut/sessions"}
	for _, name := range []string{"bash", "zsh"} {
		t.Run(name, func(t *testing.T) {
			spec, _ := Lookup(name)
			block := Render(spec, p)
			// Only inspect the recording path, not the wut() function, which
			// runs when the user asks rather than on every prompt.
			recording := block
			if i := strings.Index(block, "  wut() {"); i > 0 {
				recording = block[:i]
			}
			recording = stripShellComments(recording)
			// $(( )) is arithmetic expansion and does not fork, so it is
			// removed before looking for real command substitution.
			recording = strings.ReplaceAll(recording, "$((", "")
			for _, forbidden := range []string{"$(", "`", "date ", "awk ", "sed ", "cut ", "expr "} {
				if strings.Contains(recording, forbidden) {
					t.Errorf("recording path contains %q, which forks a process on every prompt", forbidden)
				}
			}
		})
	}
}

// stripShellComments removes whole-line comments so prose in the header does
// not get inspected as code.
func stripShellComments(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func TestAliasIsOptedInOnly(t *testing.T) {
	spec, _ := Lookup("bash")
	without := Render(spec, Params{SessionsDir: "/s"})
	if strings.Contains(without, "uh()") {
		t.Error("an alias appeared without being asked for")
	}
	with := Render(spec, Params{SessionsDir: "/s", Alias: "uh"})
	if !strings.Contains(with, "uh()") {
		t.Error("the requested alias was not defined")
	}
}

func TestManualClassInstallsNothing(t *testing.T) {
	m, _ := newTestManager(t)
	rep, err := m.Install(port.InstallRequest{Shells: []string{"cmd", "dash"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Changes {
		if c.Action != "skipped" {
			t.Errorf("%s: action = %q, want skipped", c.Shell, c.Action)
		}
		if c.Message == "" {
			t.Errorf("%s: skipped with no explanation", c.Shell)
		}
	}
}

func TestNormalizeShellNames(t *testing.T) {
	tests := map[string]string{
		"/bin/bash": "bash", "bash": "bash", "-bash": "-bash",
		"/usr/local/bin/zsh": "zsh", "nushell": "nu", "nu": "nu",
		"pwsh.exe": "pwsh", "powershell.exe": "powershell",
		"ash": "sh", "mksh": "ksh", "fish": "fish",
	}
	for in, want := range tests {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
