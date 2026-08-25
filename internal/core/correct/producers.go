package correct

import (
	"strings"

	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/cmdline"
	"github.com/thirawat27/wut/internal/core/facts"
)

// The producers in this file are the corrections that need a fuzzy search
// rather than a condition, which is why they are Go and not rule data. Each
// one is independent: it looks at the parsed line plus facts and returns zero
// or more candidates, and never sees what another producer found.

type producer func(c cmdline.CommandLine, f facts.Facts, co *Corpora) []candidate.Candidate

var producers = []producer{
	localScriptPrefix,
	programTypo,
	subcommandTypo,
	longFlagTypo,
	npmScriptTypo,
	makeTargetTypo,
	cdUnknownDirectory,
	gitBranchTypo,
}

// programTypo corrects a mistyped program name.
//
// It only fires when the program is genuinely not runnable. Correcting a name
// that resolves to something real is how a tool ends up "fixing" a private
// script into a lookalike from PATH.
func programTypo(c cmdline.CommandLine, f facts.Facts, _ *Corpora) []candidate.Candidate {
	if c.Program == "" || LooksLikePathOrURL(c.Program) || programResolves(c.Program, f) {
		return nil
	}
	corpus := f.KnownCommands()
	if len(corpus) == 0 {
		corpus = FallbackCommands()
	}
	var out []candidate.Candidate
	for _, m := range BestMatches(c.Program, corpus, 3) {
		fixed := c.Replace(0, m.Value)
		out = append(out, candidate.New(
			candidate.KindCorrection, fixed,
			candidate.Provenance{Producer: candidate.ProducerTypo, Ref: "typo/program"},
			candidate.Why{
				Code:   "typo.program",
				Text:   quote(c.Program) + " is " + editWord(m.Distance) + " from " + quote(m.Value),
				Ref:    "typo/program",
				Weight: m.Confidence,
			},
			candidate.Why{
				Code:   "path.not_found",
				Text:   quote(c.Program) + " is not an executable on PATH or in this directory",
				Weight: 0.05,
			},
		).WithTitle("Run "+quote(m.Value)+" instead"))
	}
	return out
}

// localScriptPrefix suggests ./name for a file that exists here and is
// runnable but is not on PATH — the "command not found" people hit most.
func localScriptPrefix(c cmdline.CommandLine, f facts.Facts, _ *Corpora) []candidate.Candidate {
	name := c.Program
	if name == "" || strings.ContainsAny(name, "/\\") || onPath(name, f) {
		return nil
	}
	if !f.Executable(name) {
		return nil
	}
	return []candidate.Candidate{candidate.New(
		candidate.KindCorrection, c.Replace(0, "./"+name),
		candidate.Provenance{Producer: candidate.ProducerRules, Ref: "path/local-script"},
		candidate.Why{
			Code:   "path.local_not_in_path",
			Text:   quote(name) + " is a runnable file here, but this directory is not on PATH",
			Ref:    "stat ./" + name,
			Weight: 0.8,
		},
	).WithTitle("Run the script in this directory")}
}

// subcommandTypo corrects a mistyped verb for a program with a known corpus.
func subcommandTypo(c cmdline.CommandLine, _ facts.Facts, co *Corpora) []candidate.Candidate {
	sub := c.Sub(0)
	if sub == "" || !co.Known(c.Program) || co.HasSubcommand(c.Program, sub) {
		return nil
	}
	idx := c.TokenIndexOf(cmdline.RoleSubcommand, sub)
	if idx < 0 {
		return nil
	}
	var out []candidate.Candidate
	for _, m := range BestMatches(sub, co.Subcommands(c.Program), 3) {
		out = append(out, candidate.New(
			candidate.KindCorrection, c.Replace(idx, m.Value),
			candidate.Provenance{Producer: candidate.ProducerTypo, Ref: "typo/subcommand"},
			candidate.Why{
				Code:   "typo.subcommand",
				Text:   quote(sub) + " is " + editWord(m.Distance) + " from " + quote(m.Value),
				Ref:    "typo/subcommand",
				Weight: m.Confidence,
			},
			candidate.Why{
				Code:   "corpus.unknown_subcommand",
				Text:   c.Program + " has no " + quote(sub) + " subcommand",
				Weight: 0.05,
			},
		).WithTitle(c.Program+" "+m.Value))
	}
	return out
}

// longFlagTypo corrects a mistyped long option.
//
// Short options are deliberately left alone: at two characters almost every
// short flag is one edit from another one, so the suggestions would be noise
// and occasionally dangerous — -f and -r are one edit apart and mean very
// different things.
func longFlagTypo(c cmdline.CommandLine, _ facts.Facts, co *Corpora) []candidate.Candidate {
	if !co.Known(c.Program) {
		return nil
	}
	corpus := co.Flags(c.Program, c.Sub(0))
	if len(corpus) == 0 {
		return nil
	}
	var out []candidate.Candidate
	for _, fl := range c.Flags {
		if !fl.Long || containsString(corpus, fl.Name) {
			continue
		}
		m, ok := BestMatch(fl.Name, corpus)
		if !ok || !strings.HasPrefix(m.Value, "--") {
			continue
		}
		replacement := m.Value
		if fl.HasValue {
			replacement += "=" + fl.Value
		}
		out = append(out, candidate.New(
			candidate.KindCorrection, c.Replace(fl.Index, replacement),
			candidate.Provenance{Producer: candidate.ProducerTypo, Ref: "typo/flag"},
			candidate.Why{
				Code:   "typo.flag",
				Text:   quote(fl.Name) + " is " + editWord(m.Distance) + " from " + quote(m.Value),
				Ref:    "typo/flag",
				Weight: m.Confidence,
			},
		).WithTitle("Use "+m.Value))
	}
	return out
}

// npmScriptTypo corrects a script name against what package.json actually
// declares. This is the case tldr can never help with, because the answer is
// specific to this repository.
func npmScriptTypo(c cmdline.CommandLine, f facts.Facts, _ *Corpora) []candidate.Candidate {
	prog := normalizeProgram(c.Program)
	if prog != "npm" && prog != "pnpm" && prog != "yarn" && prog != "bun" {
		return nil
	}
	if c.Sub(0) != "run" || len(c.Operands) == 0 {
		return nil
	}
	script := c.Operands[0]
	scripts := f.NpmScripts()
	if len(scripts) == 0 || containsString(scripts, script) {
		return nil
	}
	idx := c.TokenIndexOf(cmdline.RoleOperand, script)
	if idx < 0 {
		return nil
	}
	var out []candidate.Candidate
	for _, m := range BestMatches(script, scripts, 3) {
		out = append(out, candidate.New(
			candidate.KindCorrection, c.Replace(idx, m.Value),
			candidate.Provenance{Producer: candidate.ProducerRules, Ref: "npm/unknown-script"},
			candidate.Why{
				Code:   "npm.unknown_script",
				Text:   "package.json declares no " + quote(script) + " script",
				Ref:    "package.json",
				Weight: 0.35,
			},
			candidate.Why{
				Code:   "npm.closest_script",
				Text:   quote(m.Value) + " is " + editWord(m.Distance) + " away and is declared",
				Ref:    "package.json",
				Weight: m.Confidence * 0.6,
			},
		).WithTitle("Run the "+m.Value+" script"))
	}
	return out
}

// makeTargetTypo corrects a target against the Makefile in this directory.
func makeTargetTypo(c cmdline.CommandLine, f facts.Facts, _ *Corpora) []candidate.Candidate {
	if normalizeProgram(c.Program) != "make" || len(c.Operands) == 0 {
		return nil
	}
	target := c.Operands[0]
	targets := f.MakeTargets()
	if len(targets) == 0 || containsString(targets, target) {
		return nil
	}
	idx := c.TokenIndexOf(cmdline.RoleOperand, target)
	if idx < 0 {
		return nil
	}
	var out []candidate.Candidate
	for _, m := range BestMatches(target, targets, 3) {
		out = append(out, candidate.New(
			candidate.KindCorrection, c.Replace(idx, m.Value),
			candidate.Provenance{Producer: candidate.ProducerRules, Ref: "make/unknown-target"},
			candidate.Why{
				Code:   "make.unknown_target",
				Text:   "the Makefile has no " + quote(target) + " target",
				Ref:    "Makefile",
				Weight: 0.35,
			},
			candidate.Why{
				Code:   "make.closest_target",
				Text:   quote(m.Value) + " is " + editWord(m.Distance) + " away and is declared",
				Ref:    "Makefile",
				Weight: m.Confidence * 0.6,
			},
		).WithTitle("make "+m.Value))
	}
	return out
}

// cdUnknownDirectory corrects a directory name against what is actually here.
func cdUnknownDirectory(c cmdline.CommandLine, f facts.Facts, _ *Corpora) []candidate.Candidate {
	if normalizeProgram(c.Program) != "cd" || len(c.Operands) != 1 {
		return nil
	}
	target := c.Operands[0]
	if target == "" || strings.ContainsAny(target, "/\\") || strings.HasPrefix(target, "..") ||
		strings.HasPrefix(target, "~") || strings.HasPrefix(target, "$") || f.IsDir(target) {
		return nil
	}
	dirs := f.Dirs()
	if len(dirs) == 0 {
		return nil
	}
	idx := c.TokenIndexOf(cmdline.RoleOperand, target)
	if idx < 0 {
		return nil
	}
	var out []candidate.Candidate
	for _, m := range BestMatches(target, dirs, 3) {
		out = append(out, candidate.New(
			candidate.KindCorrection, c.Replace(idx, m.Value),
			candidate.Provenance{Producer: candidate.ProducerRules, Ref: "cd/unknown-directory"},
			candidate.Why{
				Code:   "fs.no_such_directory",
				Text:   "there is no " + quote(target) + " directory here",
				Ref:    f.Dir(),
				Weight: 0.35,
			},
			candidate.Why{
				Code:   "fs.closest_directory",
				Text:   quote(m.Value) + " is " + editWord(m.Distance) + " away and does exist",
				Ref:    f.Dir(),
				Weight: m.Confidence * 0.6,
			},
		).WithTitle("cd "+m.Value))
	}
	return out
}

// gitBranchTypo corrects a branch name against the branches this repository
// actually has.
func gitBranchTypo(c cmdline.CommandLine, f facts.Facts, _ *Corpora) []candidate.Candidate {
	if normalizeProgram(c.Program) != "git" {
		return nil
	}
	sub := c.Sub(0)
	if sub != "checkout" && sub != "switch" {
		return nil
	}
	// -b creates a branch, so an unknown name is the whole point.
	if c.HasFlag("-b", "-B", "--orphan") || len(c.Operands) != 1 {
		return nil
	}
	target := c.Operands[0]
	branches := f.Git().Branches
	if target == "" || target == "-" || target == "." || len(branches) == 0 ||
		containsString(branches, target) {
		return nil
	}
	idx := c.TokenIndexOf(cmdline.RoleOperand, target)
	if idx < 0 {
		return nil
	}
	var out []candidate.Candidate
	for _, m := range BestMatches(target, branches, 3) {
		out = append(out, candidate.New(
			candidate.KindCorrection, c.Replace(idx, m.Value),
			candidate.Provenance{Producer: candidate.ProducerRules, Ref: "git/unknown-branch"},
			candidate.Why{
				Code:   "git.no_such_branch",
				Text:   "this repository has no branch called " + quote(target),
				Ref:    "git branch --format %(refname:short)",
				Weight: 0.35,
			},
			candidate.Why{
				Code:   "git.closest_branch",
				Text:   quote(m.Value) + " is " + editWord(m.Distance) + " away and does exist",
				Ref:    "git branch",
				Weight: m.Confidence * 0.6,
			},
		).WithTitle("git "+sub+" "+m.Value))
	}
	return out
}

// ── helpers ────────────────────────────────────────────────────────────────

// programResolves reports whether the program can actually be run: on PATH, a
// runnable file here, or a shell builtin.
func programResolves(name string, f facts.Facts) bool {
	return onPath(name, f) || f.Executable(name) || shellBuiltins[normalizeProgram(name)]
}

func onPath(name string, f facts.Facts) bool {
	known := f.KnownCommands()
	if len(known) == 0 {
		// With no PATH listing available, fall back to the built-in corpus.
		// This is why the fallback list exists: without it every command would
		// look unknown and every line would produce a correction.
		return containsString(FallbackCommands(), normalizeProgram(name))
	}
	return containsString(known, normalizeProgram(name)) || containsString(known, name)
}

// shellBuiltins never appear on PATH but are perfectly valid commands.
var shellBuiltins = map[string]bool{
	"cd": true, "echo": true, "export": true, "alias": true, "unalias": true,
	"source": true, "history": true, "pwd": true, "exit": true, "set": true,
	"unset": true, "eval": true, "exec": true, "jobs": true, "fg": true,
	"bg": true, "kill": true, "type": true, "test": true, "read": true,
	"trap": true, "wait": true, "umask": true, "ulimit": true, "shift": true,
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func quote(s string) string { return "'" + s + "'" }

func editWord(d int) string {
	if d == 1 {
		return "1 edit"
	}
	return itoa(d) + " edits"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
