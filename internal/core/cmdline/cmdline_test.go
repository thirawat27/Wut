package cmdline

import (
	"reflect"
	"testing"
)

func TestParseBasics(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		program  string
		sub      []string
		flags    []string
		operands []string
		trailing string
	}{
		{
			name:    "bare program",
			in:      "ls",
			program: "ls",
		},
		{
			name:     "git subcommand with flag and quoted operand",
			in:       `git commit -m "fix the thing"`,
			program:  "git",
			sub:      []string{"commit"},
			flags:    []string{"-m"},
			operands: []string{"fix the thing"},
		},
		{
			name:     "two level subcommand",
			in:       "git remote add origin https://example.com/r.git",
			program:  "git",
			sub:      []string{"remote", "add"},
			operands: []string{"origin", "https://example.com/r.git"},
		},
		{
			name:    "long flag with value",
			in:      "git push --set-upstream=origin",
			program: "git",
			sub:     []string{"push"},
			flags:   []string{"--set-upstream"},
		},
		{
			name:     "pipeline is preserved verbatim and never parsed",
			in:       "cat file.txt | grep -i needle",
			program:  "cat",
			operands: []string{"file.txt"},
			trailing: "| grep -i needle",
		},
		{
			name:     "redirect ends interpretation",
			in:       "echo hi > out.txt",
			program:  "echo",
			operands: []string{"hi"},
			trailing: "> out.txt",
		},
		{
			name:     "pipe inside quotes is not a control operator",
			in:       `grep "a|b" file`,
			program:  "grep",
			operands: []string{"a|b", "file"},
		},
		{
			name:     "negative number is an operand not a flag",
			in:       "tail -n -5 file",
			program:  "tail",
			flags:    []string{"-n"},
			operands: []string{"-5", "file"},
		},
		{
			name:     "relative path is an operand not a flag",
			in:       "cp -r ./src ../dst",
			program:  "cp",
			flags:    []string{"-r"},
			operands: []string{"./src", "../dst"},
		},
		{
			name:     "double dash is a separator, not a flag",
			in:       "rm -- -weirdfile",
			program:  "rm",
			operands: []string{"-weirdfile"},
		},
		{
			name:    "empty input",
			in:      "",
			program: "",
		},
		{
			name:     "operand before subcommand depth is consumed as verb",
			in:       "npm run build",
			program:  "npm",
			sub:      []string{"run"},
			operands: []string{"build"},
		},
		{
			name:     "unknown program takes no subcommands",
			in:       "myprog foo bar",
			program:  "myprog",
			operands: []string{"foo", "bar"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.in)
			if got.Program != tc.program {
				t.Errorf("program = %q, want %q", got.Program, tc.program)
			}
			if !eqStrings(got.Subcommand, tc.sub) {
				t.Errorf("subcommand = %v, want %v", got.Subcommand, tc.sub)
			}
			var flagNames []string
			for _, f := range got.Flags {
				flagNames = append(flagNames, f.Name)
			}
			if !eqStrings(flagNames, tc.flags) {
				t.Errorf("flags = %v, want %v", flagNames, tc.flags)
			}
			if !eqStrings(got.Operands, tc.operands) {
				t.Errorf("operands = %v, want %v", got.Operands, tc.operands)
			}
			if got.Trailing != tc.trailing {
				t.Errorf("trailing = %q, want %q", got.Trailing, tc.trailing)
			}
		})
	}
}

// A backslash before an ordinary character is a Windows path separator far
// more often than it is a shell escape, so WUT keeps it. Before a character
// that genuinely needs escaping, it still behaves like a shell.
func TestBackslashHandling(t *testing.T) {
	tests := []struct {
		in   string
		want []string // expected operand texts
	}{
		{`cd C:\src\repo`, []string{`C:\src\repo`}},
		{`rm C:\tools\rm.exe`, []string{`C:\tools\rm.exe`}},
		{`echo a\ b`, []string{"a b"}},
		{`echo \$HOME`, []string{"$HOME"}},
		{`echo a\\b`, []string{`a\b`}},
	}
	for _, tc := range tests {
		got := Parse(tc.in)
		if !eqStrings(got.Operands, tc.want) {
			t.Errorf("Parse(%q).Operands = %q, want %q", tc.in, got.Operands, tc.want)
		}
	}
}

func TestHasFlagShortCluster(t *testing.T) {
	c := Parse("rm -rf build")
	for _, want := range []string{"-r", "-f", "-rf"} {
		if !c.HasFlag(want) {
			t.Errorf("HasFlag(%q) = false, want true", want)
		}
	}
	if c.HasFlag("-z") {
		t.Error("HasFlag(-z) = true, want false")
	}
	if c.HasFlag("--force") {
		t.Error("HasFlag(--force) = true, want false: a short cluster is not a long flag")
	}
}

func TestReplacePreservesEverythingElse(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		role  Role
		token string
		with  string
		want  string
	}{
		{
			name:  "program typo keeps flags and operands",
			in:    `gti commit -m "msg"`,
			role:  RoleProgram,
			token: "gti",
			with:  "git",
			want:  `git commit -m "msg"`,
		},
		{
			name:  "subcommand typo keeps quoting style",
			in:    `git psuh -u origin main`,
			role:  RoleSubcommand,
			token: "psuh",
			with:  "push",
			want:  `git push -u origin main`,
		},
		{
			name:  "replacement keeps the trailing pipeline",
			in:    "cta file.txt | grep x",
			role:  RoleProgram,
			token: "cta",
			with:  "cat",
			want:  "cat file.txt | grep x",
		},
		{
			name:  "value needing quotes gets quoted",
			in:    "cd folder",
			role:  RoleOperand,
			token: "folder",
			with:  "my folder",
			want:  "cd 'my folder'",
		},
		{
			name:  "single-quoted token stays single-quoted",
			in:    "cd 'old dir'",
			role:  RoleOperand,
			token: "old dir",
			with:  "new dir",
			want:  "cd 'new dir'",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Parse(tc.in)
			idx := c.TokenIndexOf(tc.role, tc.token)
			if idx < 0 {
				t.Fatalf("token %q with role %v not found in %q", tc.token, tc.role, tc.in)
			}
			if got := c.Replace(idx, tc.with); got != tc.want {
				t.Errorf("Replace = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReplaceOutOfRangeIsIdentity(t *testing.T) {
	c := Parse("ls -la")
	if got := c.Replace(99, "x"); got != "ls -la" {
		t.Errorf("Replace(99) = %q, want the original line", got)
	}
}

// Parse must never panic, whatever it is handed. Unbalanced quotes and stray
// operators are normal input: they are exactly what a failed command looks
// like.
func TestParseNeverPanics(t *testing.T) {
	inputs := []string{
		"", " ", "'", `"`, `git commit -m "unterminated`,
		"|", "&&", ">", "-", "--", `\`, `echo \`,
		"a'b\"c", "   \t\n  ", "git 'a b", `rm -rf "$(pwd)"`,
	}
	inputs = append(inputs, `C:\Program Files\app.exe`, `cd C:\src\repo`)
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Parse(%q) panicked: %v", in, r)
				}
			}()
			_ = Parse(in)
		}()
	}
}

func eqStrings(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
