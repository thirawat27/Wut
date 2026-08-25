package corrector

import (
	"os"
	"path/filepath"
	"testing"
)

// correctorIn returns a Corrector whose facts come from dir rather than the
// process working directory.
func correctorIn(dir string) *Corrector {
	c := New()
	c.SetFacts(NewFactsIn(dir))
	return c
}

func TestNpmUnknownScriptUsesPackageJSON(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"scripts":{"build":"tsc","test":"vitest","dev":"vite"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	corr, err := correctorIn(dir).Correct("npm run biuld")
	if err != nil {
		t.Fatalf("Correct: %v", err)
	}
	if corr == nil {
		t.Fatal("expected a correction for a misspelled script name")
	}
	if corr.Corrected != "npm run build" {
		t.Fatalf("Corrected = %q, want %q", corr.Corrected, "npm run build")
	}
	if corr.Source != "npm_unknown_script" {
		t.Fatalf("Source = %q, want the fact-driven rule", corr.Source)
	}
}

func TestNpmKnownScriptIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"scripts":{"build":"tsc"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	corr, err := correctorIn(dir).Correct("npm run build")
	if err != nil {
		t.Fatalf("Correct: %v", err)
	}
	if corr != nil && corr.Source == "npm_unknown_script" {
		t.Fatalf("a script that exists must not be corrected, got %q", corr.Corrected)
	}
}

func TestMakeUnknownTargetUsesMakefile(t *testing.T) {
	dir := t.TempDir()
	makefile := "build:\n\tgo build ./...\ninstall:\n\tgo install ./...\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0600); err != nil {
		t.Fatalf("write makefile: %v", err)
	}

	corr, err := correctorIn(dir).Correct("make instal")
	if err != nil {
		t.Fatalf("Correct: %v", err)
	}
	if corr == nil || corr.Corrected != "make install" {
		t.Fatalf("got %+v, want 'make install'", corr)
	}
}

func TestCdUnknownDirectorySuggestsNeighbour(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	corr, err := correctorIn(dir).Correct("cd intenral")
	if err != nil {
		t.Fatalf("Correct: %v", err)
	}
	if corr == nil || corr.Corrected != "cd internal" {
		t.Fatalf("got %+v, want 'cd internal'", corr)
	}
}

func TestCdExistingDirectoryIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	corr, err := correctorIn(dir).Correct("cd internal")
	if err != nil {
		t.Fatalf("Correct: %v", err)
	}
	if corr != nil && corr.Source == "cd_unknown_directory" {
		t.Fatalf("an existing directory must not be corrected, got %q", corr.Corrected)
	}
}

func TestMissingRecursiveFlagOnlyForDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "build"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	c := correctorIn(dir)

	corr, err := c.Correct("rm build")
	if err != nil {
		t.Fatalf("Correct: %v", err)
	}
	if corr == nil || corr.Corrected != "rm -r build" {
		t.Fatalf("got %+v, want 'rm -r build'", corr)
	}

	corr, err = c.Correct("rm notes.txt")
	if err != nil {
		t.Fatalf("Correct: %v", err)
	}
	if corr != nil && corr.Source == "missing_recursive_flag" {
		t.Fatalf("a plain file must not get -r, got %q", corr.Corrected)
	}
}

func TestLocalScriptNeedsPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deploy.sh"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	corr, err := correctorIn(dir).Correct("deploy.sh")
	if err != nil {
		t.Fatalf("Correct: %v", err)
	}
	if corr == nil || corr.Corrected != "./deploy.sh" {
		t.Fatalf("got %+v, want './deploy.sh'", corr)
	}
}

// TestAlternativesAreAlwaysPopulated keeps the picker's contract: every
// correction offers at least one candidate, and Corrected leads the list.
func TestAlternativesAreAlwaysPopulated(t *testing.T) {
	dir := t.TempDir()
	c := correctorIn(dir)

	for _, command := range []string{"gti status", "cd..", "go run", "doker ps"} {
		corr, err := c.Correct(command)
		if err != nil {
			t.Fatalf("Correct(%q): %v", command, err)
		}
		if corr == nil || corr.Corrected == "" {
			continue
		}
		if len(corr.Alternatives) == 0 {
			t.Fatalf("Correct(%q) produced no alternatives", command)
		}
		if corr.Alternatives[0] != corr.Corrected {
			t.Fatalf("Correct(%q): alternatives lead with %q, want %q",
				command, corr.Alternatives[0], corr.Corrected)
		}
	}
}

// TestGitDidYouMeanOffersEveryCandidate covers the multi-candidate path the
// picker exists for.
func TestGitDidYouMeanOffersEveryCandidate(t *testing.T) {
	output := "git: 'stat' is not a git command. See 'git --help'.\n\n" +
		"The most similar commands are\n\tstatus\n\tstash\n"

	corr, err := correctorIn(t.TempDir()).CorrectWithOutput("git stat", output)
	if err != nil {
		t.Fatalf("CorrectWithOutput: %v", err)
	}
	if corr == nil {
		t.Fatal("expected a correction from git's suggestion list")
	}
	if len(corr.Alternatives) < 2 {
		t.Fatalf("alternatives = %v, want both suggestions", corr.Alternatives)
	}
	if corr.Alternatives[0] != "git status" {
		t.Fatalf("first candidate = %q, want 'git status'", corr.Alternatives[0])
	}
}

// TestRulesNeverExecuteTheCommand re-asserts the core safety property against
// the new fact-driven rule set: a probe may run, the user's command may not.
func TestRulesNeverExecuteTheCommand(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "executed.txt")

	c := correctorIn(dir)
	for _, command := range []string{
		"cmd /c echo x > " + sentinel,
		"touch " + sentinel,
		"git push --force",
		"npm run build",
		"make install",
		"rm -rf ./dist",
	} {
		if _, err := c.Correct(command); err != nil {
			t.Fatalf("Correct(%q): %v", command, err)
		}
	}

	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("a rule executed the command under correction")
	}
}
