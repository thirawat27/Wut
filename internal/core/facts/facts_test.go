package facts

import "testing"

// Static is the fake every use-case test runs against, so it has to behave
// exactly like the live one. A divergence here makes every test above it
// wrong in the same direction and none of them notice.

func TestStaticAnswersFromWhatItWasGiven(t *testing.T) {
	s := &Static{
		Directory:   "/src",
		FileNames:   []string{"go.mod", "Makefile"},
		DirNames:    []string{"internal", "cmd"},
		Executables: []string{"build.sh"},
		Scripts:     []string{"build", "test"},
		Targets:     []string{"all", "clean"},
		Kind:        ProjectGo,
		Commands:    []string{"make", "go"},
	}

	if s.Dir() != "/src" {
		t.Errorf("Dir = %q", s.Dir())
	}
	if len(s.Entries()) != 4 {
		t.Errorf("entries = %v, want files and directories together", s.Entries())
	}
	for _, name := range []string{"go.mod", "internal"} {
		if !s.Exists(name) {
			t.Errorf("Exists(%q) = false", name)
		}
	}
	if s.Exists("nothing") {
		t.Error("Exists reported a file that was never listed")
	}
	if !s.IsDir("internal") || s.IsDir("go.mod") {
		t.Error("IsDir does not distinguish files from directories")
	}
	if !s.Executable("build.sh") || s.Executable("go.mod") {
		t.Error("Executable does not distinguish executables")
	}
	if s.Project() != ProjectGo {
		t.Errorf("Project = %q", s.Project())
	}
	if len(s.NpmScripts()) != 2 || len(s.MakeTargets()) != 2 || len(s.KnownCommands()) != 2 {
		t.Error("a list handed in came back the wrong length")
	}
}

// Empty is what a use case gets when facts are unavailable — probing off, an
// unreadable directory. Every method must answer without a special case at the
// call site, because that is the whole reason it exists.
func TestEmptyAnswersEverything(t *testing.T) {
	var e Empty
	if e.Dir() != "" || e.Entries() != nil || e.Dirs() != nil || e.Files() != nil {
		t.Error("Empty returned something")
	}
	if e.Exists("x") || e.IsDir("x") || e.Executable("x") {
		t.Error("Empty claimed something exists")
	}
	if e.Project() != ProjectUnknown {
		t.Errorf("Empty project kind = %q", e.Project())
	}
	if e.NpmScripts() != nil || e.MakeTargets() != nil || e.KnownCommands() != nil {
		t.Error("Empty returned a list")
	}
	if g := e.Git(); g.InRepo || g.Branch != "" || g.Remote() != "" {
		t.Errorf("Empty reported a repository: %+v", g)
	}
}

// Both implementations satisfy the interface. This is a compile-time
// assertion, so it needs no test body — but it belongs next to the tests that
// depend on it rather than buried in the source file.
var (
	_ Facts = (*Static)(nil)
	_ Facts = Empty{}
)

// Guessing the remote is only safe when there is exactly one, or when one of
// them is called origin. Guessing wrong writes to the wrong place.
func TestRemoteOnlyGuessesWhenItIsSafe(t *testing.T) {
	cases := map[string]struct {
		remotes []string
		want    string
	}{
		"no remotes":              {nil, ""},
		"exactly one":             {[]string{"upstream"}, "upstream"},
		"two, one called origin":  {[]string{"upstream", "origin"}, "origin"},
		"two, neither is origin":  {[]string{"upstream", "fork"}, ""},
		"three including origin":  {[]string{"a", "origin", "b"}, "origin"},
		"one, and it is origin":   {[]string{"origin"}, "origin"},
		"several, none is origin": {[]string{"a", "b", "c"}, ""},
	}
	for name, tc := range cases {
		got := Git{Remotes: tc.remotes}.Remote()
		if got != tc.want {
			t.Errorf("%s: Remote() = %q, want %q", name, got, tc.want)
		}
	}
}

func TestStaticZeroValueIsUsable(t *testing.T) {
	var s Static
	if s.Exists("anything") || len(s.Entries()) != 0 {
		t.Error("the zero value claimed to know something")
	}
	if s.Project() != ProjectUnknown {
		t.Errorf("the zero value has project kind %q", s.Project())
	}
}
