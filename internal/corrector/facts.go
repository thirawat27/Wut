package corrector

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Facts are read-only observations about the working directory that correction
// rules use instead of the failed command's output.
//
// This is the alternative to re-running the user's command. Tools in this space
// commonly execute the failed command again to read its stderr, which repeats
// whatever the command did — a push, a delete, a deploy. WUT never does that.
// It answers the same questions by reading files it chooses and by running a
// short, fixed list of read-only probes it chooses. The user's command is never
// among them.
//
// Facts are gathered lazily and memoised: a rule that never asks about git pays
// nothing for git.
type Facts struct {
	dir string

	once struct {
		entries     sync.Once
		npmScripts  sync.Once
		makeTgts    sync.Once
		gitBranch   sync.Once
		gitRemote   sync.Once
		gitUpstrm   sync.Once
		gitBranches sync.Once
	}

	dirNames  []string
	fileNames []string

	npmScripts  []string
	makeTargets []string

	gitBranch    string
	gitRemote    string
	gitHasUpstrm bool
	gitBranches  []string
}

// NewFacts returns a fact source rooted at the process working directory.
func NewFacts() *Facts {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	return &Facts{dir: dir}
}

// NewFactsIn returns a fact source rooted at dir. Used by tests.
func NewFactsIn(dir string) *Facts {
	return &Facts{dir: dir}
}

// ─── Probe execution ─────────────────────────────────────────────────────────

// probeTimeout bounds a single probe. Probes are read-only and local, so they
// should return in milliseconds; the timeout only guards a wedged binary.
const probeTimeout = 1500 * time.Millisecond

// allowedProbes is the complete set of commands WUT may run to gather facts.
//
// Every entry is read-only and takes no user-supplied arguments. A rule cannot
// add to this list at runtime, and nothing derived from the user's command is
// ever executed.
var allowedProbes = map[string][][]string{
	"git": {
		{"rev-parse", "--abbrev-ref", "HEAD"},
		{"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"},
		{"remote"},
		{"branch", "--format=%(refname:short)"},
	},
}

// probeAllowed reports whether argv is in the allowlist, compared element by
// element. Prefix matching would let a longer argv smuggle in extra arguments.
func probeAllowed(name string, args []string) bool {
	for _, allowed := range allowedProbes[name] {
		if len(allowed) != len(args) {
			continue
		}
		match := true
		for i := range allowed {
			if allowed[i] != args[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// probe runs an allowlisted read-only command in the fact directory and returns
// its trimmed stdout. A command that is not allowlisted never runs.
func (f *Facts) probe(name string, args ...string) (string, bool) {
	if !probeAllowed(name, args) {
		return "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = f.dir
	// Never hand a probe the user's stdin, and discard stderr: probes report
	// through their exit status and stdout only.
	cmd.Stdin = nil
	cmd.Stderr = nil

	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// ─── Directory facts (pure file reads) ───────────────────────────────────────

func (f *Facts) loadEntries() {
	f.once.entries.Do(func() {
		entries, err := os.ReadDir(f.dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() {
				f.dirNames = append(f.dirNames, entry.Name())
			} else {
				f.fileNames = append(f.fileNames, entry.Name())
			}
		}
	})
}

// Directories lists subdirectory names in the working directory.
func (f *Facts) Directories() []string {
	f.loadEntries()
	return f.dirNames
}

// Files lists file names in the working directory.
func (f *Facts) Files() []string {
	f.loadEntries()
	return f.fileNames
}

// IsDir reports whether name is a directory relative to the fact directory.
func (f *Facts) IsDir(name string) bool {
	info, err := os.Stat(f.resolve(name))
	return err == nil && info.IsDir()
}

// Exists reports whether name exists relative to the fact directory.
func (f *Facts) Exists(name string) bool {
	_, err := os.Stat(f.resolve(name))
	return err == nil
}

func (f *Facts) resolve(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(f.dir, name)
}

// ─── Project facts (pure file reads) ─────────────────────────────────────────

// NpmScripts returns the script names declared in package.json.
//
// Reading the manifest answers "did you mean this script?" without running the
// package manager, which would trigger whatever the script does.
func (f *Facts) NpmScripts() []string {
	f.once.npmScripts.Do(func() {
		data, err := os.ReadFile(f.resolve("package.json"))
		if err != nil {
			return
		}

		var manifest struct {
			Scripts map[string]json.RawMessage `json:"scripts"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return
		}

		for name := range manifest.Scripts {
			f.npmScripts = append(f.npmScripts, name)
		}
	})
	return f.npmScripts
}

// makeTargetRe matches a Makefile target definition at the start of a line,
// skipping pattern rules and special targets.
var makeTargetRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)\s*:(?:[^=]|$)`)

// MakeTargets returns the target names declared in a Makefile.
func (f *Facts) MakeTargets() []string {
	f.once.makeTgts.Do(func() {
		var file *os.File
		for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
			opened, err := os.Open(f.resolve(name))
			if err == nil {
				file = opened
				break
			}
		}
		if file == nil {
			return
		}
		defer file.Close()

		seen := make(map[string]struct{})
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			match := makeTargetRe.FindStringSubmatch(scanner.Text())
			if match == nil {
				continue
			}
			target := match[1]
			if _, ok := seen[target]; ok {
				continue
			}
			seen[target] = struct{}{}
			f.makeTargets = append(f.makeTargets, target)
		}
	})
	return f.makeTargets
}

// ─── Git facts (allowlisted read-only probes) ────────────────────────────────

// InGitRepo reports whether the working directory is inside a git repository,
// by looking for the directory rather than by running git.
func (f *Facts) InGitRepo() bool {
	dir := f.dir
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// GitBranch returns the current branch name, or "" when it cannot be resolved.
func (f *Facts) GitBranch() string {
	f.once.gitBranch.Do(func() {
		if !f.InGitRepo() {
			return
		}
		if out, ok := f.probe("git", "rev-parse", "--abbrev-ref", "HEAD"); ok && out != "HEAD" {
			f.gitBranch = out
		}
	})
	return f.gitBranch
}

// GitRemote returns the remote to push to, preferring "origin".
func (f *Facts) GitRemote() string {
	f.once.gitRemote.Do(func() {
		if !f.InGitRepo() {
			return
		}
		out, ok := f.probe("git", "remote")
		if !ok || out == "" {
			return
		}
		remotes := strings.Fields(out)
		for _, remote := range remotes {
			if remote == "origin" {
				f.gitRemote = remote
				return
			}
		}
		f.gitRemote = remotes[0]
	})
	return f.gitRemote
}

// GitHasUpstream reports whether the current branch tracks a remote branch.
//
// This is the fact behind "git push -> git push --set-upstream". Establishing it
// with rev-parse costs nothing and changes nothing; establishing it by re-running
// `git push` would push.
func (f *Facts) GitHasUpstream() bool {
	f.once.gitUpstrm.Do(func() {
		if !f.InGitRepo() {
			return
		}
		_, ok := f.probe("git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
		f.gitHasUpstrm = ok
	})
	return f.gitHasUpstrm
}

// GitBranches lists local branch names.
func (f *Facts) GitBranches() []string {
	f.once.gitBranches.Do(func() {
		if !f.InGitRepo() {
			return
		}
		out, ok := f.probe("git", "branch", "--format=%(refname:short)")
		if !ok {
			return
		}
		for _, line := range strings.Split(out, "\n") {
			if branch := strings.TrimSpace(line); branch != "" {
				f.gitBranches = append(f.gitBranches, branch)
			}
		}
	})
	return f.gitBranches
}
