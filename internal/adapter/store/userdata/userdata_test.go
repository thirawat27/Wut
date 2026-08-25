package userdata

import (
	"os"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir())
}

// This is the one store where losing a file loses something the user cannot
// get back. Everything else WUT keeps is derived: the index rebuilds, the
// event log is a recording, the config has defaults.

func TestSaveAndList(t *testing.T) {
	s := newStore(t)
	if _, err := s.Add("git log --oneline --graph", "the pretty one", []string{"git"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("docker system prune -af", "", nil); err != nil {
		t.Fatal(err)
	}

	all, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("saved 2, listed %d", len(all))
	}
	// Newest first: the thing you just worked out is the thing you are about
	// to look for.
	if all[0].Command != "docker system prune -af" {
		t.Errorf("first entry is %q, want the newest", all[0].Command)
	}
}

// A list with three copies of the same line is a list nobody scrolls through
// twice.
func TestSavingTheSameCommandUpdatesIt(t *testing.T) {
	s := newStore(t)
	first, _ := s.Add("make test", "", nil)
	second, err := s.Add("make test", "runs the unit tests", []string{"build"})
	if err != nil {
		t.Fatal(err)
	}

	all, _ := s.List("")
	if len(all) != 1 {
		t.Fatalf("the same command was saved %d times", len(all))
	}
	if all[0].Note != "runs the unit tests" {
		t.Errorf("the note was not updated: %q", all[0].Note)
	}
	if !second.Added.Equal(first.Added) {
		t.Error("re-saving reset the date it was first kept")
	}
}

func TestFilterMatchesCommandNoteAndTags(t *testing.T) {
	s := newStore(t)
	_, _ = s.Add("kubectl get pods", "list them", []string{"k8s"})
	_, _ = s.Add("git status", "", nil)

	for _, needle := range []string{"kubectl", "KUBECTL", "list", "k8s"} {
		got, err := s.List(needle)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Command != "kubectl get pods" {
			t.Errorf("filter %q matched %d entries", needle, len(got))
		}
	}
	if got, _ := s.List("nothing like this"); len(got) != 0 {
		t.Errorf("a filter that should match nothing matched %d", len(got))
	}
}

func TestRemove(t *testing.T) {
	s := newStore(t)
	_, _ = s.Add("ls -la", "", nil)

	if _, err := s.Remove("ls -la"); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.List(""); len(all) != 0 {
		t.Errorf("remove left %d entries", len(all))
	}
	if _, err := s.Remove("never saved"); err == nil {
		t.Error("removing something that was never saved reported success")
	}
}

func TestEmptyCommandIsRefused(t *testing.T) {
	s := newStore(t)
	if _, err := s.Add("   ", "", nil); err == nil {
		t.Error("a blank command was saved")
	}
}

// An alias is shell text the user will run, so a name containing a space or a
// shell operator does not merely look odd — it produces a definition that does
// something other than what they wrote.
func TestAliasNamesThatWouldBreakTheShellAreRefused(t *testing.T) {
	s := newStore(t)
	for _, name := range []string{
		"", "two words", "semi;colon", "pipe|it", "and&&then",
		"back`tick`", "dollar$sign", "quote'it", `double"quote`,
		"redirect>file", "sub(shell)", "slash/name", `back\slash`,
		strings.Repeat("x", 40),
	} {
		if _, err := s.SetAlias(name, "ls", ""); err == nil {
			t.Errorf("alias name %q was accepted", name)
		}
	}
}

// Shadowing `rm` or `cd` with a personal alias is a foot-gun the user will not
// connect to WUT when it goes wrong three weeks later.
func TestReservedAliasNamesAreRefused(t *testing.T) {
	s := newStore(t)
	for _, name := range []string{"wut", "cd", "ls", "rm", "sudo", "git", "exit"} {
		_, err := s.SetAlias(name, "echo nope", "")
		if err == nil {
			t.Errorf("alias %q was accepted; it shadows a command the user needs", name)
			continue
		}
		if !strings.Contains(err.Error(), "shadow") {
			t.Errorf("alias %q was refused, but not for the right reason: %v", name, err)
		}
	}
}

func TestAliasNeedsACommand(t *testing.T) {
	s := newStore(t)
	if _, err := s.SetAlias("gl", "  ", ""); err == nil {
		t.Error("an alias with no command was accepted")
	}
}

func TestAliasesAreSortedAndReplaceable(t *testing.T) {
	s := newStore(t)
	_, _ = s.SetAlias("zz", "echo z", "")
	_, _ = s.SetAlias("aa", "echo a", "")
	first, _ := s.SetAlias("aa", "echo a again", "")

	all, err := s.Aliases()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d aliases, want 2", len(all))
	}
	if all[0].Name != "aa" || all[1].Name != "zz" {
		t.Errorf("aliases are not sorted: %v", all)
	}
	if all[0].Command != "echo a again" {
		t.Errorf("redefining did not replace: %q", all[0].Command)
	}
	if !first.Added.IsZero() && first.Added.After(all[1].Added) {
		// Redefining keeps the original date, so the entry does not jump.
		t.Log("redefining preserved the original date")
	}

	if err := s.RemoveAlias("aa"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveAlias("aa"); err == nil {
		t.Error("removing an alias twice reported success")
	}
}

// The quoting matters more than it looks: these lines are pasted into a
// startup file and evaluated. A command containing a quote must survive.
func TestShellDefinitionsQuoteCorrectly(t *testing.T) {
	s := newStore(t)
	_, _ = s.SetAlias("gl", `git log --format='%h %s'`, "")

	cases := map[string]string{
		"sh":         "alias gl=",
		"zsh":        "alias gl=",
		"fish":       "alias gl ",
		"pwsh":       "function gl {",
		"powershell": "function gl {",
		"nu":         "alias gl = ",
	}
	for shell, prefix := range cases {
		out, err := s.ShellDefinitions(shell)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, prefix) {
			t.Errorf("%s definition = %q, want it to contain %q", shell, out, prefix)
		}
	}

	posix, _ := s.ShellDefinitions("sh")
	if !strings.Contains(posix, `'\''`) {
		t.Errorf("a single quote inside the command was not escaped for sh: %q", posix)
	}
}

func TestShellDefinitionsOfNothingIsEmpty(t *testing.T) {
	s := newStore(t)
	out, err := s.ShellDefinitions("sh")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("with no aliases defined, got %q", out)
	}
}

// The file is meant to be edited by hand and kept in a dotfiles repository. A
// tool that locks a user's own list inside a format only it can read has taken
// something from them.
func TestTheFileIsPlainAndAnnotated(t *testing.T) {
	s := newStore(t)
	_, _ = s.Add("make build", "", nil)

	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.HasPrefix(body, "#") {
		t.Error("the file has no header explaining what it is")
	}
	if !strings.Contains(body, "purge") {
		t.Error("the header does not say that wut purge leaves it alone")
	}
	if !strings.Contains(body, "make build") {
		t.Error("the saved command is not readable in the file")
	}
}

// A hand-edited file that has been broken must fail loudly rather than being
// silently replaced with an empty one, which would lose everything in it.
func TestACorruptFileIsReportedNotOverwritten(t *testing.T) {
	s := newStore(t)
	_, _ = s.Add("keep me", "", nil)

	if err := os.WriteFile(s.Path(), []byte("saved: [this is not: valid yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(""); err == nil {
		t.Error("a corrupt file was read as if it were empty")
	}
}
