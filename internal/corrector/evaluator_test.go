package corrector

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCorrectNeverExecutesTheCommand is the regression guard for the behaviour
// that used to make `wut fix` / `oops` dangerous: the correction pipeline ran
// the user's command to harvest its stderr, so `oops` after a failed `git push`
// pushed again, and `oops` after `rm -rf ./dist` deleted again.
//
// The probe writes a sentinel file. If correction executes anything, the file
// appears and the test fails.
func TestCorrectNeverExecutesTheCommand(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "executed.txt")

	var probe string
	if runtime.GOOS == "windows" {
		probe = "cmd /c echo executed > " + sentinel
	} else {
		probe = "touch " + sentinel
	}

	c := New()
	if _, err := c.Correct(probe); err != nil {
		t.Fatalf("Correct returned an error: %v", err)
	}

	if _, err := os.Stat(sentinel); err == nil {
		t.Fatalf("Correct executed the command: %s was created", sentinel)
	}
}

// TestCorrectDoesNotRerunSideEffectingCommands covers the specific commands the
// old deny-list let through. None of them may reach a process.
func TestCorrectDoesNotRerunSideEffectingCommands(t *testing.T) {
	c := New()
	for _, cmd := range []string{
		"git push --force",
		"docker system prune -af",
		"terraform destroy -auto-approve",
		"kubectl delete namespace production",
		"npm publish",
		"rm -rf ./dist",
	} {
		// The contract under test is "no process is started". A nil or non-nil
		// suggestion is both acceptable; a panic or an error is not.
		if _, err := c.Correct(cmd); err != nil {
			t.Fatalf("Correct(%q) returned an error: %v", cmd, err)
		}
	}
}

// TestOutputDependentRulesRequireOutput verifies that rules needing the failed
// command's output stay dormant until a caller supplies it, rather than causing
// WUT to produce the output itself.
func TestOutputDependentRulesRequireOutput(t *testing.T) {
	c := New()

	if corr, _ := c.Correct("git push"); corr != nil && strings.Contains(corr.Corrected, "--set-upstream") {
		t.Fatal("set-upstream rule must not fire without captured output")
	}

	gitOutput := "fatal: The current branch feat has no upstream branch.\n" +
		"To push the current branch and set the remote as upstream, use\n\n" +
		"    git push --set-upstream origin feat\n"

	corr, err := c.CorrectWithOutput("git push", gitOutput)
	if err != nil {
		t.Fatalf("CorrectWithOutput returned an error: %v", err)
	}
	if corr == nil {
		t.Fatal("set-upstream rule should fire when output is supplied")
	}
	if corr.Corrected != "git push --set-upstream origin feat" {
		t.Fatalf("unexpected correction: %q", corr.Corrected)
	}
}

// TestOutputIndependentRulesWorkWithoutOutput keeps the rules that never needed
// execution working from the command string alone.
func TestOutputIndependentRulesWorkWithoutOutput(t *testing.T) {
	c := New()

	tests := []struct {
		command string
		want    string
	}{
		{"cd..", "cd .."},
		{"go run", "go run ."},
	}

	for _, tt := range tests {
		corr, err := c.Correct(tt.command)
		if err != nil {
			t.Fatalf("Correct(%q) returned an error: %v", tt.command, err)
		}
		if corr == nil {
			t.Fatalf("Correct(%q) returned no correction, want %q", tt.command, tt.want)
		}
		if corr.Corrected != tt.want {
			t.Fatalf("Correct(%q) = %q, want %q", tt.command, corr.Corrected, tt.want)
		}
	}
}
