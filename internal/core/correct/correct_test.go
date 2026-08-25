package correct

import (
	"strings"
	"testing"

	"github.com/thirawat27/wut/internal/core/facts"
)

// goRepo is a Go module with a git remote and no upstream for the branch —
// the shape the flagship correction is aimed at.
func goRepo() *facts.Static {
	return &facts.Static{
		Directory: "/proj",
		DirNames:  []string{"internal", "cmd", "docs"},
		FileNames: []string{"go.mod", "main.go", "Makefile", "deploy.sh"},
		Kind:      facts.ProjectGo,
		Targets:   []string{"build", "test", "install", "lint"},
		GitInfo: facts.Git{
			InRepo:      true,
			Branch:      "feature/login",
			HasUpstream: false,
			Remotes:     []string{"origin"},
			Branches:    []string{"main", "develop", "feature/login"},
		},
		Executables: []string{"deploy.sh"},
		Commands:    []string{"git", "go", "make", "ls", "cd", "docker", "npm", "cat", "rm", "cp"},
	}
}

func nodeRepo() *facts.Static {
	return &facts.Static{
		Directory: "/app",
		DirNames:  []string{"src", "test", "node_modules"},
		FileNames: []string{"package.json"},
		Kind:      facts.ProjectNode,
		Scripts:   []string{"build", "test", "start", "lint", "dev"},
		Commands:  []string{"npm", "node", "git", "ls", "cd"},
	}
}

func TestCorrections(t *testing.T) {
	e := New()
	tests := []struct {
		name  string
		in    string
		facts facts.Facts
		want  string
	}{
		{"program transposition", "gti status", goRepo(), "git status"},
		{"subcommand transposition", "git psuh", goRepo(), "git push"},
		{"subcommand typo keeps the rest of the line", "git comit -m 'x'", goRepo(), "git commit -m 'x'"},
		{"long flag typo", "git push --set-upsteam origin main", goRepo(), "git push --set-upstream origin main"},
		{"cd with no space", "cd..", goRepo(), "cd .."},
		{"cd two levels", "cd...", goRepo(), "cd ../.."},
		{"unknown directory", "cd intenral", goRepo(), "cd internal"},
		{"local script needs a prefix", "deploy.sh", goRepo(), "./deploy.sh"},
		{"make target", "make instal", goRepo(), "make install"},
		{"npm script", "npm run biuld", nodeRepo(), "npm run build"},
		{"legacy compose", "docker-compose up -d", goRepo(), "docker compose up -d"},
		{"go run with no target", "go run", goRepo(), "go run ."},
		{"git branch that does not exist", "git checkout develp", goRepo(), "git checkout develop"},
		{"copy a directory without -r", "cp internal backup", goRepo(), "cp -r internal backup"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := e.Correct(tc.in, tc.facts)
			if len(got) == 0 {
				t.Fatalf("Correct(%q) produced nothing, want %q", tc.in, tc.want)
			}
			if got[0].Command != tc.want {
				var all []string
				for _, c := range got {
					all = append(all, c.Command)
				}
				t.Errorf("Correct(%q) top = %q, want %q (all: %v)", tc.in, got[0].Command, tc.want, all)
			}
			if len(got[0].Why) == 0 {
				t.Error("top candidate has no Why: it must never be presentable")
			}
			for _, w := range got[0].Why {
				if strings.TrimSpace(w.Text) == "" {
					t.Errorf("Why %q has empty text", w.Code)
				}
				if strings.Contains(w.Text, "{{") {
					t.Errorf("Why %q has an unexpanded template: %q", w.Code, w.Text)
				}
			}
		})
	}
}

// The flagship correction, checked in full rather than by its command string
// alone: the reasons a user sees are part of the contract.
func TestGitPushNoUpstream(t *testing.T) {
	got := New().Correct("git push", goRepo())
	if len(got) == 0 {
		t.Fatal("no candidate")
	}
	top := got[0]
	if want := "git push --set-upstream origin feature/login"; top.Command != want {
		t.Fatalf("command = %q, want %q", top.Command, want)
	}
	if top.Confidence != "high" {
		t.Errorf("confidence = %q, want high", top.Confidence)
	}
	if top.Source.Ref != "git/push-no-upstream" {
		t.Errorf("provenance ref = %q, want the rule id", top.Source.Ref)
	}
	if top.Source.Generated {
		t.Error("a rule-derived candidate must not be marked as generated")
	}
	codes := map[string]bool{}
	for _, w := range top.Why {
		codes[w.Code] = true
	}
	for _, want := range []string{"git.no_upstream", "git.single_remote"} {
		if !codes[want] {
			t.Errorf("missing Why %q; got %v", want, codes)
		}
	}
	if !strings.Contains(top.Why[0].Text, "feature/login") {
		t.Errorf("Why text did not name the branch: %q", top.Why[0].Text)
	}
}

// The negative set is the one that decides whether anyone keeps the tool
// installed. A correction offered on a command that was already right is worse
// than no correction at all, so this must stay at exactly zero.
func TestNoFalseCorrections(t *testing.T) {
	e := New()
	repo, node := goRepo(), nodeRepo()

	cases := []struct {
		cmd string
		f   facts.Facts
	}{
		{"git status", repo},
		{"git commit -m 'a message'", repo},
		{"git checkout main", repo},
		{"git checkout -b brand-new-branch", repo},
		{"git switch develop", repo},
		{"git push --set-upstream origin feature/login", repo},
		{"git log --oneline --graph", repo},
		{"go build ./...", repo},
		{"go test -race ./...", repo},
		{"go run .", repo},
		{"make build", repo},
		{"make test", repo},
		{"cd internal", repo},
		{"cd ..", repo},
		{"cd", repo},
		{"ls -la", repo},
		{"cat main.go", repo},
		{"cp main.go backup.go", repo},
		{"rm main.go", repo},
		{"./deploy.sh", repo},
		{"docker compose up -d", repo},
		{"npm run build", node},
		{"npm run test", node},
		{"npm install", node},
		{"npm run dev -- --port 3000", node},
		{"", repo},
		{"   ", repo},
	}

	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			if got := e.Correct(tc.cmd, tc.f); len(got) != 0 {
				t.Errorf("Correct(%q) offered %q, want nothing", tc.cmd, got[0].Command)
			}
		})
	}
}

// With no facts at all — the Manual-class shell, or a directory WUT cannot
// read — corrections that need facts must stay silent rather than guess.
func TestFactlessStaysQuiet(t *testing.T) {
	e := New()
	for _, cmd := range []string{"git push", "npm run biuld", "make instal", "cd intenral"} {
		if got := e.Correct(cmd, facts.Empty{}); len(got) != 0 {
			t.Errorf("Correct(%q) with no facts offered %q, want nothing", cmd, got[0].Command)
		}
	}
	// A pure text correction still works without facts, because it needs none.
	if got := e.Correct("cd..", facts.Empty{}); len(got) == 0 || got[0].Command != "cd .." {
		t.Errorf("cd.. should still correct with no facts, got %v", got)
	}
}

// Every rule in the data file must have a case above that proves it fires.
func TestEveryRuleHasACase(t *testing.T) {
	e := New()
	fired := map[string]bool{}
	probes := []struct {
		cmd string
		f   facts.Facts
	}{
		{"git push", goRepo()},
		{"cd..", goRepo()},
		{"cd...", goRepo()},
		{"cp internal backup", goRepo()},
		{"rm internal", goRepo()},
		{"docker-compose up", goRepo()},
		{"go run", goRepo()},
	}
	for _, p := range probes {
		for _, c := range e.Correct(p.cmd, p.f) {
			fired[c.Source.Ref] = true
		}
	}
	for _, r := range e.Rules().Rules() {
		if !fired[r.ID] {
			t.Errorf("rule %q never fired in any probe: it has no proof it works", r.ID)
		}
	}
}

func TestDangerousCorrectionsCarryTheirRisk(t *testing.T) {
	got := New().Correct("rm internal", goRepo())
	if len(got) == 0 {
		t.Fatal("no candidate")
	}
	if got[0].Command != "rm -r internal" {
		t.Fatalf("command = %q", got[0].Command)
	}
	if got[0].Risk.Safe() {
		t.Error("a recursive remove came back with no risk assessment")
	}
}

func TestDamerauCountsTranspositionAsOne(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"psuh", "push", 1},
		{"gti", "git", 1},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"comit", "commit", 1},
		{"instal", "install", 1},
		{"kitten", "sitting", 3},
	}
	for _, tc := range tests {
		if got := Damerau(tc.a, tc.b); got != tc.want {
			t.Errorf("Damerau(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestBestMatchesSkipsExactHits(t *testing.T) {
	if got := BestMatches("push", []string{"push", "pull"}, 3); got != nil {
		t.Errorf("BestMatches on an exact hit returned %v, want nil", got)
	}
}

func TestLoadRulesRejectsBadData(t *testing.T) {
	bad := map[string]string{
		"no id":         "version: 1\nrules:\n  - {program: git, rewrite: x, why: [{code: a, text: b, weight: 1}]}\n",
		"no rewrite":    "version: 1\nrules:\n  - {id: a, program: git, why: [{code: a, text: b, weight: 1}]}\n",
		"no why":        "version: 1\nrules:\n  - {id: a, program: git, rewrite: x}\n",
		"no program":    "version: 1\nrules:\n  - {id: a, rewrite: x, why: [{code: a, text: b, weight: 1}]}\n",
		"unknown fact":  "version: 1\nrules:\n  - {id: a, program: git, rewrite: x, why: [{code: a, text: b, weight: 1}], require: {git.mood: happy}}\n",
		"duplicate ids": "version: 1\nrules:\n  - {id: a, program: git, rewrite: x, why: [{code: a, text: b, weight: 1}]}\n  - {id: a, program: go, rewrite: y, why: [{code: a, text: b, weight: 1}]}\n",
	}
	for name, doc := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadRules([]byte(doc)); err == nil {
				t.Error("LoadRules succeeded, want an error")
			}
		})
	}
}

func TestCorporaNormalizeProgramName(t *testing.T) {
	co := BuiltinCorpora()
	for _, name := range []string{"npm", "npm.cmd", "/usr/local/bin/npm", `C:\node\npm.exe`} {
		if !co.Known(name) {
			t.Errorf("Known(%q) = false, want true", name)
		}
	}
}
