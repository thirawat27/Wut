// Package risk classifies a command against a declarative policy.
//
// the prototype scattered danger detection across two files: seven literal
// prefixes and two regexes in the corrector, plus a separate
// interactive-binary list in the evaluator. Neither was bound to a test. Here
// there is one policy, every rule carries a stable id that reaches the user,
// and the whole set is table-driven.
//
// This package is pure. It decides what a command *is*; whether to refuse it
// is the caller's decision.
package risk

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"

	"github.com/thirawat27/wut/internal/core/cmdline"
	"gopkg.in/yaml.v3"
)

// Level orders how much damage a command can do. Higher is worse.
type Level int

const (
	None Level = iota
	Caution
	Destructive
	Irreversible
)

func (l Level) String() string {
	switch l {
	case Caution:
		return "caution"
	case Destructive:
		return "destructive"
	case Irreversible:
		return "irreversible"
	default:
		return "none"
	}
}

// ParseLevel is the inverse of String. Unknown text is None.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "caution":
		return Caution
	case "destructive":
		return Destructive
	case "irreversible":
		return Irreversible
	default:
		return None
	}
}

// Class describes the kind of harm, independent of severity.
type Class string

const (
	ClassDataLoss        Class = "data-loss"
	ClassPrivilege       Class = "privilege"
	ClassNetworkMutating Class = "network-mutating"
	ClassRemoteState     Class = "remote-state"
	ClassHistoryRewrite  Class = "history-rewrite"
)

// Assessment is the verdict on one command.
type Assessment struct {
	Level  Level   `json:"level"`
	Class  []Class `json:"class,omitempty"`
	Reason string  `json:"reason,omitempty"`
	Rule   string  `json:"rule,omitempty"`
}

// Safe reports a command with nothing to warn about.
func (a Assessment) Safe() bool { return a.Level == None }

// Blocking reports a command that must never be emitted in shell mode, where
// acceptance runs it immediately.
func (a Assessment) Blocking() bool { return a.Level >= Destructive }

// Match is the condition half of a rule. Every field is optional; a rule
// matches when every field it does set is satisfied.
type Match struct {
	Program        string   `yaml:"program"`
	ProgramAnyOf   []string `yaml:"program_any_of"`
	Subcommand     []string `yaml:"subcommand"`
	FlagsAll       []string `yaml:"flags_all"`
	FlagsAny       []string `yaml:"flags_any"`
	OperandMatches string   `yaml:"operand_matches"`
	RawMatches     string   `yaml:"raw_matches"`
	// RawNotMatches exists because RE2 has no lookahead, and several real
	// policies are "X without Y" — an UPDATE with no WHERE, most of all.
	RawNotMatches string `yaml:"raw_not_matches"`

	operandRe *regexp.Regexp
	rawRe     *regexp.Regexp
	rawNotRe  *regexp.Regexp
}

// Rule is one policy entry.
type Rule struct {
	ID     string  `yaml:"id"`
	Level  string  `yaml:"level"`
	Class  []Class `yaml:"class"`
	Reason string  `yaml:"reason"`
	Match  Match   `yaml:"match"`
	AnyOf  []Match `yaml:"any_of"`

	level Level
}

type policyFile struct {
	Version int    `yaml:"version"`
	Rules   []Rule `yaml:"rules"`
}

//go:embed policy.yaml
var builtinPolicy []byte

// Policy is a compiled, ordered rule set.
type Policy struct {
	rules []Rule
}

// Builtin returns the policy compiled into the binary. It panics on a
// malformed embedded file, which can only happen at build time.
func Builtin() *Policy {
	p, err := Compile(builtinPolicy)
	if err != nil {
		panic("risk: embedded policy is invalid: " + err.Error())
	}
	return p
}

// Compile parses and validates a policy document.
func Compile(data []byte) (*Policy, error) {
	var pf policyFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	seen := make(map[string]bool, len(pf.Rules))
	out := make([]Rule, 0, len(pf.Rules))
	for i := range pf.Rules {
		r := pf.Rules[i]
		if r.ID == "" {
			return nil, fmt.Errorf("policy rule %d has no id", i)
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("duplicate policy rule id %q", r.ID)
		}
		seen[r.ID] = true
		r.level = ParseLevel(r.Level)
		if r.level == None {
			return nil, fmt.Errorf("rule %q has level %q, which classifies nothing", r.ID, r.Level)
		}
		if err := compileMatch(&r.Match, r.ID); err != nil {
			return nil, err
		}
		for j := range r.AnyOf {
			if err := compileMatch(&r.AnyOf[j], r.ID); err != nil {
				return nil, err
			}
		}
		out = append(out, r)
	}
	return &Policy{rules: out}, nil
}

func compileMatch(m *Match, id string) error {
	var err error
	if m.OperandMatches != "" {
		if m.operandRe, err = regexp.Compile(m.OperandMatches); err != nil {
			return fmt.Errorf("rule %q operand_matches: %w", id, err)
		}
	}
	if m.RawMatches != "" {
		if m.rawRe, err = regexp.Compile(m.RawMatches); err != nil {
			return fmt.Errorf("rule %q raw_matches: %w", id, err)
		}
	}
	if m.RawNotMatches != "" {
		if m.rawNotRe, err = regexp.Compile(m.RawNotMatches); err != nil {
			return fmt.Errorf("rule %q raw_not_matches: %w", id, err)
		}
	}
	return nil
}

// Merge returns a policy with extra rules appended. A user-supplied rule may
// only raise the level of a command, never lower one, so merging can add rules
// but a later rule never suppresses an earlier verdict — Assess takes the
// highest match, not the last.
func (p *Policy) Merge(extra *Policy) *Policy {
	if extra == nil {
		return p
	}
	merged := make([]Rule, 0, len(p.rules)+len(extra.rules))
	merged = append(merged, p.rules...)
	merged = append(merged, extra.rules...)
	return &Policy{rules: merged}
}

// Rules exposes the compiled set, for `wut risk list` and for tests that
// assert every rule is covered.
func (p *Policy) Rules() []Rule { return p.rules }

// Assess returns the highest-severity match. Ties keep the first rule, so
// ordering within a level is stable and the built-in policy wins over a user
// rule of equal severity.
func (p *Policy) Assess(c cmdline.CommandLine) Assessment {
	best := Assessment{}
	for _, r := range p.rules {
		if !ruleMatches(r, c) {
			continue
		}
		if r.level > best.Level {
			best = Assessment{Level: r.level, Class: r.Class, Reason: r.Reason, Rule: r.ID}
		}
	}
	return best
}

// AssessString is the convenience form for callers holding raw text.
func (p *Policy) AssessString(raw string) Assessment {
	return p.Assess(cmdline.Parse(raw))
}

func ruleMatches(r Rule, c cmdline.CommandLine) bool {
	if len(r.AnyOf) > 0 {
		for _, m := range r.AnyOf {
			if matches(m, c) {
				return true
			}
		}
		return false
	}
	return matches(r.Match, c)
}

func matches(m Match, c cmdline.CommandLine) bool {
	if m.Program != "" && !sameProgram(c.Program, m.Program) {
		return false
	}
	if len(m.ProgramAnyOf) > 0 {
		found := false
		for _, p := range m.ProgramAnyOf {
			if sameProgram(c.Program, p) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for i, want := range m.Subcommand {
		if c.Sub(i) != want {
			return false
		}
	}
	for _, f := range m.FlagsAll {
		if !c.HasFlag(f) {
			return false
		}
	}
	if len(m.FlagsAny) > 0 && !c.HasFlag(m.FlagsAny...) {
		return false
	}
	if m.operandRe != nil {
		found := false
		for _, op := range c.Operands {
			if m.operandRe.MatchString(op) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if m.rawRe != nil && !m.rawRe.MatchString(c.Raw) {
		return false
	}
	if m.rawNotRe != nil && m.rawNotRe.MatchString(c.Raw) {
		return false
	}
	// A match with no conditions at all would flag every command. Treat it as
	// a policy authoring error rather than a catch-all.
	return !emptyMatch(m)
}

func emptyMatch(m Match) bool {
	return m.Program == "" && len(m.ProgramAnyOf) == 0 && len(m.Subcommand) == 0 &&
		len(m.FlagsAll) == 0 && len(m.FlagsAny) == 0 && m.operandRe == nil && m.rawRe == nil
}

// HighestOf returns the worst assessment in a set, for callers holding several
// candidates.
func HighestOf(list ...Assessment) Assessment {
	best := Assessment{}
	for _, a := range list {
		if a.Level > best.Level {
			best = a
		}
	}
	return best
}

// sameProgram compares ignoring a directory prefix and a Windows extension, so
// a policy written for "rm" still matches "/bin/rm" and one for "npm" matches
// "npm.cmd".
func sameProgram(got, want string) bool {
	g := strings.ToLower(got)
	if i := strings.LastIndexAny(g, "/\\"); i >= 0 {
		g = g[i+1:]
	}
	for _, ext := range []string{".exe", ".cmd", ".bat", ".ps1"} {
		g = strings.TrimSuffix(g, ext)
	}
	return g == strings.ToLower(want)
}
