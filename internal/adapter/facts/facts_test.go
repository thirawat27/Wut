package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	corefacts "github.com/thirawat27/wut/internal/core/facts"
)

// This package is the only user-facing path from which WUT can start a
// process. The allowlist is what stands between "reads context about your
// project" and "runs whatever it likes", so it is tested first and hardest.

func TestOnlyExactInvocationsAreAllowed(t *testing.T) {
	allowed := [][]string{
		{"git", "rev-parse", "--is-inside-work-tree"},
		{"git", "rev-parse", "--abbrev-ref", "HEAD"},
		{"git", "rev-parse", "--abbrev-ref", "@{u}"},
		{"git", "remote"},
		{"git", "branch", "--format=%(refname:short)"},
	}
	for _, argv := range allowed {
		if !probeAllowed(argv[0], argv[1:]) {
			t.Errorf("%v is on the allowlist but was refused", argv)
		}
	}
}

// A prefix match would let a longer argv smuggle arguments in behind an
// allowed prefix. Each of these shares a prefix with something permitted and
// must still be refused.
func TestArgumentsCannotBeSmuggledIn(t *testing.T) {
	refused := [][]string{
		{"git", "rev-parse", "--is-inside-work-tree", "; rm -rf /"},
		{"git", "remote", "add", "evil", "https://evil.example/x"},
		{"git", "remote", "--exec=whatever"},
		{"git", "branch", "--format=%(refname:short)", "-D", "main"},
		{"git", "rev-parse"},
		{"git"},
		{"git", "push"},
		{"git", "config", "--global", "core.pager", "sh"},
		{"sh", "-c", "echo hi"},
		{"rm", "-rf", "/"},
		{"node", "-e", "process.exit(1)"},
		{"", ""},
	}
	for _, argv := range refused {
		name := argv[0]
		var args []string
		if len(argv) > 1 {
			args = argv[1:]
		}
		if probeAllowed(name, args) {
			t.Errorf("%v was allowed to run", argv)
		}
	}
}

// Order matters as much as content: the same words in a different order are a
// different command.
func TestArgumentOrderIsPartOfTheMatch(t *testing.T) {
	if probeAllowed("git", []string{"--abbrev-ref", "rev-parse", "HEAD"}) {
		t.Error("a reordered argv was allowed")
	}
}

// The allowlist must stay small and read-only. A new entry here is a new way
// for WUT to start a process, and that is a decision, not a detail.
func TestTheAllowlistStaysSmallAndReadOnly(t *testing.T) {
	if len(allowedProbes) != 1 {
		t.Errorf("the allowlist covers %d programs; it covered one by design", len(allowedProbes))
	}
	writing := []string{
		"add", "rm", "commit", "push", "pull", "fetch", "reset", "checkout",
		"clean", "config", "init", "clone", "merge", "rebase", "-c", "--exec",
	}
	for name, invocations := range allowedProbes {
		for _, argv := range invocations {
			for _, arg := range argv {
				for _, verb := range writing {
					if arg == verb {
						t.Errorf("%s %v can change state; probes must only read", name, argv)
					}
				}
			}
		}
	}
}

func TestProbingCanBeTurnedOffEntirely(t *testing.T) {
	p := &Provider{Enabled: false}
	f := p.For(t.TempDir())
	if git := f.Git(); git.InRepo {
		t.Error("probing is disabled but a git repository was reported")
	}
}

func TestFactsForAMissingDirectoryAreEmptyNotAnError(t *testing.T) {
	p := NewProvider()
	f := p.For(filepath.Join(t.TempDir(), "does-not-exist"))
	if len(f.Entries()) != 0 {
		t.Errorf("a missing directory listed %v", f.Entries())
	}
	if f.Exists("anything") {
		t.Error("a missing directory reported a file")
	}
	if f.Project() != corefacts.ProjectUnknown {
		t.Errorf("a missing directory has project kind %q", f.Project())
	}
}

func TestDirectoryListing(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "README.md"), "hello")
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	f := NewProvider().For(dir)
	if !f.Exists("README.md") || !f.Exists("src") {
		t.Errorf("entries = %v", f.Entries())
	}
	if !f.IsDir("src") || f.IsDir("README.md") {
		t.Error("directories and files were not told apart")
	}
	if len(f.Files()) != 1 || f.Files()[0] != "README.md" {
		t.Errorf("files = %v", f.Files())
	}
	if len(f.Dirs()) != 1 || f.Dirs()[0] != "src" {
		t.Errorf("dirs = %v", f.Dirs())
	}
}

func TestProjectKindFromWhatIsOnDisk(t *testing.T) {
	cases := map[string]struct {
		file string
		want corefacts.ProjectKind
	}{
		"node":   {"package.json", corefacts.ProjectNode},
		"go":     {"go.mod", corefacts.ProjectGo},
		"rust":   {"Cargo.toml", corefacts.ProjectRust},
		"python": {"pyproject.toml", corefacts.ProjectPython},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, tc.file), "{}")
			if got := NewProvider().For(dir).Project(); got != tc.want {
				t.Errorf("project kind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNpmScriptsAreReadFromPackageJSON(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "package.json"),
		`{"name":"x","scripts":{"build":"tsc","test":"vitest","dev":"vite"}}`)

	got := NewProvider().For(dir).NpmScripts()
	for _, want := range []string{"build", "test", "dev"} {
		if !contains(got, want) {
			t.Errorf("scripts = %v, want it to contain %q", got, want)
		}
	}
}

// A package.json that is not valid JSON is a normal state — someone is
// mid-edit. It must produce no scripts rather than an error that stops WUT
// from answering a question that never needed them.
func TestABrokenPackageJSONIsSilent(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "package.json"), "{not json")
	if got := NewProvider().For(dir).NpmScripts(); len(got) != 0 {
		t.Errorf("a broken package.json produced %v", got)
	}
}

func TestMakeTargets(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Makefile"), strings.Join([]string{
		"# a comment",
		".PHONY: build test",
		"",
		"build:",
		"\tgo build ./...",
		"",
		"test: build",
		"\tgo test ./...",
		"",
		"VAR := value",
		"$(GENERATED): dep",
		"\ttouch $@",
	}, "\n"))

	got := NewProvider().For(dir).MakeTargets()
	for _, want := range []string{"build", "test"} {
		if !contains(got, want) {
			t.Errorf("targets = %v, want it to contain %q", got, want)
		}
	}
	for _, unwanted := range []string{".PHONY", "VAR", "$(GENERATED)"} {
		if contains(got, unwanted) {
			t.Errorf("targets = %v, want it not to contain %q", got, unwanted)
		}
	}
}

func TestExecutableDetection(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "deploy.sh")
	mustWrite(t, script, "#!/bin/sh\necho hi\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Skip("cannot set the executable bit here")
	}
	mustWrite(t, filepath.Join(dir, "notes.txt"), "hello")

	f := NewProvider().For(dir)
	if f.Executable("notes.txt") {
		t.Error("a plain text file was reported as executable")
	}
}

// KnownCommands is what the corrector matches a mistyped program against, so
// it has to include what is actually in this directory.
func TestKnownCommandsIncludeLocalScripts(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "package.json"), `{"scripts":{"lint":"eslint ."}}`)

	got := NewProvider().For(dir).KnownCommands()
	if len(got) == 0 {
		t.Fatal("no known commands at all")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
