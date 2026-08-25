package shell

import (
	"strings"
	"testing"
)

// TestGeneratedHooksNeverReexecuteTheFailedCommand guards the property that the
// Go side enforces and the shell side must not undo: the `oops` helper asks WUT
// what the command should have been, it never runs the original to find out.
//
// A hook that piped the failed command back into a shell would reintroduce the
// exact hazard the correction pipeline was rewritten to remove.
func TestGeneratedHooksNeverReexecuteTheFailedCommand(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		code := GenerateShellCode(shell)
		if code == "" {
			t.Fatalf("%s: no integration code generated", shell)
		}

		// The corrected command is what may run, and only through the variable
		// the fix was written into.
		for _, forbidden := range []string{
			`eval "$cmd"`,
			`eval $cmd`,
			`Invoke-Expression $target`,
			`Invoke-Expression "$target"`,
		} {
			if strings.Contains(code, forbidden) {
				t.Fatalf("%s: hook executes the original command via %q", shell, forbidden)
			}
		}
	}
}

// TestGeneratedHooksRequestOnlyTheAcceptedCommand checks the contract between
// the hook and `wut fix --shell`: stdout carries the accepted command, so the
// hook must read it from there rather than parse WUT's human-facing output.
func TestGeneratedHooksRequestOnlyTheAcceptedCommand(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		code := GenerateShellCode(shell)
		if !strings.Contains(code, "wut fix --shell") {
			t.Fatalf("%s: hook should call 'wut fix --shell'", shell)
		}
	}
}

// TestGeneratedHooksDefineTheHelpers keeps the documented entry points present
// in every shell WUT claims to support.
func TestGeneratedHooksDefineTheHelpers(t *testing.T) {
	tests := []struct {
		shell string
		want  []string
	}{
		{"bash", []string{"oops()", "again()", "__wut_last_command"}},
		{"zsh", []string{"oops()", "again()", "__wut_last_command"}},
		{"fish", []string{"function oops", "function again"}},
		{"powershell", []string{"Invoke-WUTOops", "Set-Alias oops", "Set-Alias again"}},
	}

	for _, tt := range tests {
		code := GenerateShellCode(tt.shell)
		for _, want := range tt.want {
			if !strings.Contains(code, want) {
				t.Fatalf("%s: hook is missing %q", tt.shell, want)
			}
		}
	}
}

// TestPowerShellHookReadsTheMostRecentHistoryEntry pins the off-by-one that made
// `oops` target the command before the one that failed: Get-History returns
// entries oldest-first, so the newest is at index -1, not 0.
func TestPowerShellHookReadsTheMostRecentHistoryEntry(t *testing.T) {
	code := GenerateShellCode("powershell")

	if strings.Contains(code, "$history[0].CommandLine") {
		t.Fatal("hook reads the oldest history entry; Get-History is oldest-first")
	}
	if !strings.Contains(code, "$history[-1].CommandLine") {
		t.Fatal("hook should read the most recent history entry at index -1")
	}
}
