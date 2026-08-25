// Package facts defines what WUT is allowed to know about the working
// directory, and a static implementation for tests.
//
// Facts are *runtime context*, not a knowledge source. tldr answers "what does
// this command do"; facts answer "what is true in this directory right now",
// and no amount of syncing can answer the second. This is also what lets the
// correction engine work without ever running the user's command: the
// questions a rule needs answered are answered by reading, not by executing.
//
// Every implementation must be read-only, lazy, and memoised. A rule that
// never asks about git must cost nothing in a directory that has no git.
package facts

// ProjectKind is a coarse classification of the working directory.
type ProjectKind string

const (
	ProjectUnknown ProjectKind = ""
	ProjectGo      ProjectKind = "go"
	ProjectNode    ProjectKind = "node"
	ProjectRust    ProjectKind = "rust"
	ProjectPython  ProjectKind = "python"
	ProjectRuby    ProjectKind = "ruby"
	ProjectJava    ProjectKind = "java"
	ProjectDotNet  ProjectKind = "dotnet"
	ProjectPHP     ProjectKind = "php"
	ProjectDocker  ProjectKind = "docker"
	ProjectMake    ProjectKind = "make"
)

// Git is what WUT knows about the repository, if there is one.
type Git struct {
	InRepo      bool
	Branch      string
	HasUpstream bool
	Remotes     []string
	Branches    []string
}

// Remote returns the single remote when there is exactly one, which is the
// only case where WUT can safely guess which one you meant.
func (g Git) Remote() string {
	if len(g.Remotes) == 1 {
		return g.Remotes[0]
	}
	for _, r := range g.Remotes {
		if r == "origin" {
			return r
		}
	}
	return ""
}

// Facts is the read-only view of the working directory.
type Facts interface {
	// Dir is the directory these facts describe.
	Dir() string
	// Entries lists the names directly inside Dir.
	Entries() []string
	// Dirs lists only the directory names inside Dir.
	Dirs() []string
	// Files lists only the file names inside Dir.
	Files() []string
	// Exists reports whether a name exists inside Dir.
	Exists(name string) bool
	// IsDir reports whether a name exists and is a directory.
	IsDir(name string) bool
	// Executable reports whether a name exists and is runnable as a program.
	Executable(name string) bool
	// Git describes the repository, or the zero value outside one.
	Git() Git
	// NpmScripts lists the scripts declared in package.json.
	NpmScripts() []string
	// MakeTargets lists the targets declared in a Makefile.
	MakeTargets() []string
	// Project classifies the directory.
	Project() ProjectKind
	// KnownCommands lists the program names available on PATH. It is the
	// corpus for correcting a mistyped program, and the most expensive fact
	// here, so it must stay lazy.
	KnownCommands() []string
}

// Static is an in-memory Facts, used by tests and by any caller that wants to
// pin the world. Every field is optional.
type Static struct {
	Directory   string
	DirNames    []string
	FileNames   []string
	Executables []string
	GitInfo     Git
	Scripts     []string
	Targets     []string
	Kind        ProjectKind
	Commands    []string
}

var _ Facts = (*Static)(nil)

func (s *Static) Dir() string { return s.Directory }

func (s *Static) Entries() []string {
	out := make([]string, 0, len(s.DirNames)+len(s.FileNames))
	out = append(out, s.DirNames...)
	out = append(out, s.FileNames...)
	return out
}

func (s *Static) Dirs() []string  { return s.DirNames }
func (s *Static) Files() []string { return s.FileNames }

func (s *Static) Exists(name string) bool { return contains(s.Entries(), name) }
func (s *Static) IsDir(name string) bool  { return contains(s.DirNames, name) }

func (s *Static) Executable(name string) bool { return contains(s.Executables, name) }

func (s *Static) Git() Git                { return s.GitInfo }
func (s *Static) NpmScripts() []string    { return s.Scripts }
func (s *Static) MakeTargets() []string   { return s.Targets }
func (s *Static) Project() ProjectKind    { return s.Kind }
func (s *Static) KnownCommands() []string { return s.Commands }

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// Empty is a Facts that knows nothing. Rules that need facts stay dormant
// against it, which is exactly the behaviour wanted when facts are disabled or
// the directory is unreadable.
type Empty struct{}

var _ Facts = Empty{}

func (Empty) Dir() string             { return "" }
func (Empty) Entries() []string       { return nil }
func (Empty) Dirs() []string          { return nil }
func (Empty) Files() []string         { return nil }
func (Empty) Exists(string) bool      { return false }
func (Empty) IsDir(string) bool       { return false }
func (Empty) Executable(string) bool  { return false }
func (Empty) Git() Git                { return Git{} }
func (Empty) NpmScripts() []string    { return nil }
func (Empty) MakeTargets() []string   { return nil }
func (Empty) Project() ProjectKind    { return ProjectUnknown }
func (Empty) KnownCommands() []string { return nil }
