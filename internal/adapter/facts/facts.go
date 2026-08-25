// Package facts is the only place outside the daemon's model supervisor where
// this program is allowed to start a process.
//
// Two properties make that safe, and both are enforced here rather than
// documented elsewhere:
//
//  1. Every probe is compared against a compile-time allowlist **argv for
//     argv**, not by prefix. A longer argv cannot smuggle arguments in behind
//     an allowed prefix, so nothing derived from the user's command can ever
//     reach a process.
//  2. Every probe is read-only. None of them changes a byte on disk.
//
// Everything is lazy and memoised: a rule that never asks about git costs
// nothing in a directory that has no git.
package facts

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	corefacts "github.com/thirawat27/wut/internal/core/facts"
)

// probeTimeout bounds any single probe. A wedged git in a network-mounted
// repository must not hang the prompt.
const probeTimeout = 800 * time.Millisecond

// allowedProbes is the complete set of programs and argument vectors WUT may
// run. Comparison is exact and element-wise.
//
// Read this list as the answer to "what could WUT possibly do to my machine".
// Nothing here writes, deletes, connects, or authenticates.
var allowedProbes = map[string][][]string{
	"git": {
		{"rev-parse", "--is-inside-work-tree"},
		{"rev-parse", "--abbrev-ref", "HEAD"},
		{"rev-parse", "--abbrev-ref", "@{u}"},
		{"remote"},
		{"branch", "--format=%(refname:short)"},
	},
}

// probeAllowed reports whether this exact invocation is permitted.
func probeAllowed(name string, args []string) bool {
	for _, allowed := range allowedProbes[name] {
		if len(allowed) != len(args) {
			continue
		}
		same := true
		for i := range allowed {
			if allowed[i] != args[i] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

// Provider builds Facts for a directory.
type Provider struct {
	// Enabled turns probing off entirely. With it false the provider still
	// answers from the filesystem but never starts a process.
	Enabled bool
}

// NewProvider returns a provider with probing enabled.
func NewProvider() *Provider { return &Provider{Enabled: true} }

// For returns the facts for a directory. The result memoises every answer, so
// it should be reused for the whole of one invocation and then discarded.
func (p *Provider) For(dir string) corefacts.Facts {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	return &live{dir: dir, probes: p.Enabled}
}

// live is the real Facts. Every field behind a sync.Once is computed at most
// once and only if something asks.
type live struct {
	dir    string
	probes bool

	entriesOnce sync.Once
	dirNames    []string
	fileNames   []string
	execNames   []string

	gitOnce sync.Once
	git     corefacts.Git

	npmOnce sync.Once
	npm     []string

	makeOnce sync.Once
	makeT    []string

	projOnce sync.Once
	proj     corefacts.ProjectKind

	pathOnce sync.Once
	pathCmds []string
}

var _ corefacts.Facts = (*live)(nil)

func (l *live) Dir() string { return l.dir }

func (l *live) loadEntries() {
	l.entriesOnce.Do(func() {
		entries, err := os.ReadDir(l.dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				l.dirNames = append(l.dirNames, name)
				continue
			}
			l.fileNames = append(l.fileNames, name)
			if isExecutableFile(filepath.Join(l.dir, name), e) {
				l.execNames = append(l.execNames, name)
			}
		}
	})
}

func (l *live) Entries() []string {
	l.loadEntries()
	out := make([]string, 0, len(l.dirNames)+len(l.fileNames))
	out = append(out, l.dirNames...)
	return append(out, l.fileNames...)
}

func (l *live) Dirs() []string  { l.loadEntries(); return l.dirNames }
func (l *live) Files() []string { l.loadEntries(); return l.fileNames }

func (l *live) Exists(name string) bool {
	if name == "" {
		return false
	}
	_, err := os.Lstat(filepath.Join(l.dir, name))
	return err == nil
}

func (l *live) IsDir(name string) bool {
	if name == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(l.dir, name))
	return err == nil && info.IsDir()
}

func (l *live) Executable(name string) bool {
	l.loadEntries()
	for _, e := range l.execNames {
		if e == name {
			return true
		}
	}
	return false
}

// isExecutableFile answers differently per platform on purpose: the Unix
// permission bit does not exist on Windows, where the extension is the only
// signal available.
func isExecutableFile(path string, entry os.DirEntry) bool {
	if runtime.GOOS == "windows" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".exe", ".bat", ".cmd", ".ps1", ".com", ".sh":
			return true
		}
		return false
	}
	info, err := entry.Info()
	if err != nil {
		return false
	}
	return info.Mode()&0o111 != 0
}

func (l *live) Git() corefacts.Git {
	l.gitOnce.Do(func() {
		if !l.probes {
			return
		}
		if out, ok := l.probe("git", "rev-parse", "--is-inside-work-tree"); !ok || strings.TrimSpace(out) != "true" {
			return
		}
		l.git.InRepo = true
		if out, ok := l.probe("git", "rev-parse", "--abbrev-ref", "HEAD"); ok {
			l.git.Branch = strings.TrimSpace(out)
		}
		if _, ok := l.probe("git", "rev-parse", "--abbrev-ref", "@{u}"); ok {
			l.git.HasUpstream = true
		}
		if out, ok := l.probe("git", "remote"); ok {
			l.git.Remotes = nonEmptyLines(out)
		}
		if out, ok := l.probe("git", "branch", "--format=%(refname:short)"); ok {
			l.git.Branches = nonEmptyLines(out)
		}
	})
	return l.git
}

func (l *live) NpmScripts() []string {
	l.npmOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(l.dir, "package.json"))
		if err != nil {
			return
		}
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) != nil {
			return
		}
		for name := range pkg.Scripts {
			l.npm = append(l.npm, name)
		}
		sort.Strings(l.npm)
	})
	return l.npm
}

func (l *live) MakeTargets() []string {
	l.makeOnce.Do(func() {
		for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
			f, err := os.Open(filepath.Join(l.dir, name))
			if err != nil {
				continue
			}
			l.makeT = parseMakeTargets(f)
			_ = f.Close()
			return
		}
	})
	return l.makeT
}

// parseMakeTargets reads target names without evaluating anything. A Makefile
// is a program; WUT only ever reads it as text.
func parseMakeTargets(r interface{ Read([]byte) (int, error) }) []string {
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '\t' || line[0] == '#' || line[0] == '.' {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		// An assignment is not a target: FOO := bar, FOO ?= bar.
		if colon+1 < len(line) && (line[colon+1] == '=') {
			continue
		}
		for _, name := range strings.Fields(line[:colon]) {
			if strings.ContainsAny(name, "$%(){}") || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// projectMarkers is ordered: the first match wins, so a Go service with a
// Dockerfile classifies as Go rather than as Docker.
var projectMarkers = []struct {
	file string
	kind corefacts.ProjectKind
}{
	{"go.mod", corefacts.ProjectGo},
	{"Cargo.toml", corefacts.ProjectRust},
	{"package.json", corefacts.ProjectNode},
	{"pyproject.toml", corefacts.ProjectPython},
	{"requirements.txt", corefacts.ProjectPython},
	{"setup.py", corefacts.ProjectPython},
	{"Gemfile", corefacts.ProjectRuby},
	{"pom.xml", corefacts.ProjectJava},
	{"build.gradle", corefacts.ProjectJava},
	{"build.gradle.kts", corefacts.ProjectJava},
	{"composer.json", corefacts.ProjectPHP},
	{"Dockerfile", corefacts.ProjectDocker},
	{"docker-compose.yml", corefacts.ProjectDocker},
	{"Makefile", corefacts.ProjectMake},
}

func (l *live) Project() corefacts.ProjectKind {
	l.projOnce.Do(func() {
		for _, m := range projectMarkers {
			if l.Exists(m.file) {
				l.proj = m.kind
				return
			}
		}
		// .NET has no fixed filename, only an extension.
		for _, f := range l.Files() {
			switch strings.ToLower(filepath.Ext(f)) {
			case ".csproj", ".fsproj", ".sln":
				l.proj = corefacts.ProjectDotNet
				return
			}
		}
	})
	return l.proj
}

// KnownCommands lists every executable name on PATH.
//
// This is the most expensive fact here — it walks every PATH directory — so it
// stays behind a sync.Once and is only reached when a program name failed to
// resolve, which is rare.
func (l *live) KnownCommands() []string {
	l.pathOnce.Do(func() {
		seen := map[string]bool{}
		exts := windowsExecExtensions()
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			if dir == "" {
				continue
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				if runtime.GOOS == "windows" {
					ext := strings.ToLower(filepath.Ext(name))
					if !exts[ext] {
						continue
					}
					name = strings.TrimSuffix(name, filepath.Ext(name))
					name = strings.ToLower(name)
				} else if !isExecutableFile(filepath.Join(dir, name), e) {
					continue
				}
				if !seen[name] {
					seen[name] = true
					l.pathCmds = append(l.pathCmds, name)
				}
			}
		}
		sort.Strings(l.pathCmds)
	})
	return l.pathCmds
}

func windowsExecExtensions() map[string]bool {
	out := map[string]bool{}
	pathext := os.Getenv("PATHEXT")
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD;.PS1"
	}
	for _, e := range strings.Split(pathext, ";") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out[e] = true
		}
	}
	return out
}

// probe runs one allowlisted, read-only command in the working directory.
//
// The allowlist check is not a formality: it is the mechanism that makes
// "WUT never runs your command" true rather than merely intended. A caller
// that gets this wrong gets nothing back, not an unexpected process.
func (l *live) probe(name string, args ...string) (string, bool) {
	if !l.probes || !probeAllowed(name, args) {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = l.dir
	cmd.Stdin = nil
	cmd.Stderr = nil
	// A probe must not inherit anything that makes git interactive or slow.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GCM_INTERACTIVE=never",
	)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
