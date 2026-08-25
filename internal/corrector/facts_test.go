package corrector

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProbeAllowlistRejectsAnythingElse is the guard on the one place WUT still
// runs a process. Only the fixed, read-only argv lists in allowedProbes may run;
// anything derived from a user's command must be refused.
func TestProbeAllowlistRejectsAnythingElse(t *testing.T) {
	facts := NewFactsIn(t.TempDir())

	refused := []struct {
		name string
		args []string
	}{
		{"git", []string{"push"}},
		{"git", []string{"reset", "--hard"}},
		{"git", []string{"rev-parse", "--abbrev-ref", "HEAD", "; rm -rf /"}},
		{"rm", []string{"-rf", "."}},
		{"sh", []string{"-c", "echo hi"}},
		{"docker", []string{"system", "prune", "-af"}},
		{"npm", []string{"publish"}},
	}

	for _, probe := range refused {
		if probeAllowed(probe.name, probe.args) {
			t.Fatalf("probe %q %v must not be allowed", probe.name, probe.args)
		}
		if out, ok := facts.probe(probe.name, probe.args...); ok {
			t.Fatalf("probe %q %v ran and returned %q", probe.name, probe.args, out)
		}
	}
}

// TestProbeAllowlistAcceptsExactMatches confirms the allowlist compares whole
// argv lists, so a longer argv cannot smuggle in extra arguments behind an
// allowed prefix.
func TestProbeAllowlistAcceptsExactMatches(t *testing.T) {
	if !probeAllowed("git", []string{"rev-parse", "--abbrev-ref", "HEAD"}) {
		t.Fatal("the branch probe should be allowed")
	}
	if probeAllowed("git", []string{"rev-parse", "--abbrev-ref", "HEAD", "extra"}) {
		t.Fatal("a longer argv must not match an allowed prefix")
	}
	if probeAllowed("git", []string{"rev-parse"}) {
		t.Fatal("a shorter argv must not match")
	}
}

func TestNpmScriptsReadsPackageJSON(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"name":"demo","scripts":{"build":"tsc","test":"vitest","dev":"vite"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	scripts := NewFactsIn(dir).NpmScripts()
	for _, want := range []string{"build", "test", "dev"} {
		if !containsString(scripts, want) {
			t.Fatalf("scripts %v missing %q", scripts, want)
		}
	}
}

func TestMakeTargetsReadsMakefile(t *testing.T) {
	dir := t.TempDir()
	makefile := "" +
		".PHONY: build test\n" +
		"CFLAGS := -O2\n" +
		"build:\n\tgo build ./...\n" +
		"test-all:\n\tgo test ./...\n" +
		"%.o: %.c\n\tcc -c $<\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0600); err != nil {
		t.Fatalf("write makefile: %v", err)
	}

	targets := NewFactsIn(dir).MakeTargets()
	for _, want := range []string{"build", "test-all"} {
		if !containsString(targets, want) {
			t.Fatalf("targets %v missing %q", targets, want)
		}
	}
	// CFLAGS := ... is a variable assignment, not a target.
	if containsString(targets, "CFLAGS") {
		t.Fatalf("targets %v should not include a variable assignment", targets)
	}
}

func TestDirectoryFacts(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	facts := NewFactsIn(dir)

	if !containsString(facts.Directories(), "internal") {
		t.Fatalf("directories %v missing 'internal'", facts.Directories())
	}
	if !containsString(facts.Files(), "main.go") {
		t.Fatalf("files %v missing 'main.go'", facts.Files())
	}
	if !facts.IsDir("internal") {
		t.Fatal("internal should be reported as a directory")
	}
	if facts.IsDir("main.go") {
		t.Fatal("main.go should not be reported as a directory")
	}
	if !facts.Exists("main.go") || facts.Exists("nope.go") {
		t.Fatal("Exists disagrees with the directory contents")
	}
}
