package correct

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/thirawat27/wut/internal/core/candidate"
	"github.com/thirawat27/wut/internal/core/cmdline"
	"github.com/thirawat27/wut/internal/core/facts"
	"gopkg.in/yaml.v3"
)

//go:embed rules.yaml
var builtinRules []byte

// WhyTemplate is a reason with template holes, filled in when the rule fires.
type WhyTemplate struct {
	Code   string  `yaml:"code"`
	Text   string  `yaml:"text"`
	Ref    string  `yaml:"ref"`
	Weight float64 `yaml:"weight"`
}

// Rule is one fact-driven correction.
type Rule struct {
	ID           string            `yaml:"id"`
	Program      string            `yaml:"program"`
	ProgramAnyOf []string          `yaml:"program_any_of"`
	Subcommand   []string          `yaml:"subcommand"`
	NoneOfFlags  []string          `yaml:"none_of_flags"`
	AnyOfFlags   []string          `yaml:"any_of_flags"`
	Require      map[string]string `yaml:"require"`
	Rewrite      string            `yaml:"rewrite"`
	Title        string            `yaml:"title"`
	Why          []WhyTemplate     `yaml:"why"`
}

type ruleFile struct {
	Version int    `yaml:"version"`
	Rules   []Rule `yaml:"rules"`
}

// requireKeys is the closed set of condition keys. Closing it is the point:
// an unknown key is a typo in a rule, and a typo that silently never matches
// is the worst possible failure for a data-driven system.
var requireKeys = map[string]bool{
	"git.in_repo":       true,
	"git.has_upstream":  true,
	"git.single_remote": true,
	"project":           true,
	"operands.empty":    true,
	"operand0.exists":   true,
	"operand0.is_dir":   true,
	"operand0.is_file":  true,
}

// RuleSet is a validated collection of rules.
type RuleSet struct {
	rules []Rule
}

// LoadRules parses and validates a rule document.
func LoadRules(data []byte) (*RuleSet, error) {
	var rf ruleFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("parse rules: %w", err)
	}
	seen := make(map[string]bool, len(rf.Rules))
	for _, r := range rf.Rules {
		switch {
		case r.ID == "":
			return nil, fmt.Errorf("a rule has no id")
		case seen[r.ID]:
			return nil, fmt.Errorf("duplicate rule id %q", r.ID)
		case r.Rewrite == "":
			return nil, fmt.Errorf("rule %q has no rewrite", r.ID)
		case len(r.Why) == 0:
			return nil, fmt.Errorf("rule %q has no why: a correction with no reason cannot be shown", r.ID)
		case r.Program == "" && len(r.ProgramAnyOf) == 0:
			return nil, fmt.Errorf("rule %q names no program, so it would match everything", r.ID)
		}
		seen[r.ID] = true
		for key := range r.Require {
			if !requireKeys[key] {
				return nil, fmt.Errorf("rule %q requires unknown fact %q", r.ID, key)
			}
		}
	}
	return &RuleSet{rules: rf.Rules}, nil
}

// BuiltinRules returns the rules compiled into the binary.
func BuiltinRules() *RuleSet {
	rs, err := LoadRules(builtinRules)
	if err != nil {
		panic("correct: embedded rules are invalid: " + err.Error())
	}
	return rs
}

// Rules exposes the set, for `wut rules list` and for coverage tests.
func (rs *RuleSet) Rules() []Rule { return rs.rules }

// Apply returns a candidate for every rule that matches.
func (rs *RuleSet) Apply(c cmdline.CommandLine, f facts.Facts) []candidate.Candidate {
	var out []candidate.Candidate
	for _, r := range rs.rules {
		cand, ok := applyRule(r, c, f)
		if ok {
			out = append(out, cand)
		}
	}
	return out
}

func applyRule(r Rule, c cmdline.CommandLine, f facts.Facts) (candidate.Candidate, bool) {
	if !ruleMatchesShape(r, c) {
		return candidate.Candidate{}, false
	}
	for key, want := range r.Require {
		if !factHolds(key, want, c, f) {
			return candidate.Candidate{}, false
		}
	}
	vars := templateVars(c, f)
	rewritten := expand(r.Rewrite, vars)
	if rewritten == "" || normalizeSpace(rewritten) == normalizeSpace(c.Raw) {
		// A rewrite identical to the input is not a correction.
		return candidate.Candidate{}, false
	}
	why := make([]candidate.Why, 0, len(r.Why))
	for _, w := range r.Why {
		why = append(why, candidate.Why{
			Code:   w.Code,
			Text:   expand(w.Text, vars),
			Ref:    expand(w.Ref, vars),
			Weight: w.Weight,
		})
	}
	cand := candidate.New(
		candidate.KindCorrection,
		rewritten,
		candidate.Provenance{Producer: candidate.ProducerRules, Ref: r.ID},
		why...,
	)
	return cand.WithTitle(expand(r.Title, vars)), true
}

func ruleMatchesShape(r Rule, c cmdline.CommandLine) bool {
	if r.Program != "" && !sameProgram(c.Program, r.Program) {
		return false
	}
	if len(r.ProgramAnyOf) > 0 {
		found := false
		for _, p := range r.ProgramAnyOf {
			if sameProgram(c.Program, p) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for i, want := range r.Subcommand {
		if c.Sub(i) != want {
			return false
		}
	}
	for _, f := range r.NoneOfFlags {
		if c.HasFlag(f) {
			return false
		}
	}
	if len(r.AnyOfFlags) > 0 && !c.HasFlag(r.AnyOfFlags...) {
		return false
	}
	return true
}

// factHolds evaluates one condition. Unknown keys are rejected at load time,
// so anything reaching here is in the closed set.
func factHolds(key, want string, c cmdline.CommandLine, f facts.Facts) bool {
	operand0 := ""
	if len(c.Operands) > 0 {
		operand0 = c.Operands[0]
	}
	switch key {
	case "git.in_repo":
		return boolIs(f.Git().InRepo, want)
	case "git.has_upstream":
		return boolIs(f.Git().HasUpstream, want)
	case "git.single_remote":
		return boolIs(f.Git().Remote() != "", want)
	case "project":
		return string(f.Project()) == want
	case "operands.empty":
		return boolIs(len(c.Operands) == 0, want)
	case "operand0.exists":
		return operand0 != "" && boolIs(f.Exists(operand0), want)
	case "operand0.is_dir":
		return operand0 != "" && boolIs(f.IsDir(operand0), want)
	case "operand0.is_file":
		return operand0 != "" && boolIs(f.Exists(operand0) && !f.IsDir(operand0), want)
	}
	return false
}

func boolIs(actual bool, want string) bool {
	switch strings.ToLower(strings.TrimSpace(want)) {
	case "true":
		return actual
	case "false":
		return !actual
	}
	return false
}

func templateVars(c cmdline.CommandLine, f facts.Facts) map[string]string {
	g := f.Git()
	vars := map[string]string{
		"program":    c.Program,
		"sub0":       c.Sub(0),
		"sub1":       c.Sub(1),
		"operands":   strings.Join(quoteAll(c.Operands), " "),
		"raw":        c.Raw,
		"git.branch": g.Branch,
		"git.remote": g.Remote(),
	}
	if len(c.Operands) > 0 {
		vars["operand0"] = c.Operands[0]
	} else {
		vars["operand0"] = ""
	}
	// args is everything after the program, spliced from the original text so
	// flags, quoting, and ordering survive a rewrite untouched.
	if len(c.Tokens) > 1 {
		vars["args"] = strings.TrimSpace(c.Head[c.Tokens[1].Start:]) + c.Trailing
	}
	return vars
}

// quoteAll re-quotes operands that need it, so a rewrite built from a template
// survives words containing spaces.
func quoteAll(operands []string) []string {
	out := make([]string, len(operands))
	for i, op := range operands {
		if strings.ContainsAny(op, " \t'\"|&;<>()$`") {
			out[i] = "'" + strings.ReplaceAll(op, "'", `'\''`) + "'"
		} else {
			out[i] = op
		}
	}
	return out
}

// expand fills {{name}} holes. An unknown name expands to nothing, and the
// resulting collapse of whitespace is what keeps a half-filled template from
// producing a command with a hole in the middle of it.
func expand(tmpl string, vars map[string]string) string {
	if tmpl == "" || !strings.Contains(tmpl, "{{") {
		return tmpl
	}
	var b strings.Builder
	for {
		open := strings.Index(tmpl, "{{")
		if open < 0 {
			b.WriteString(tmpl)
			break
		}
		closeIdx := strings.Index(tmpl[open:], "}}")
		if closeIdx < 0 {
			b.WriteString(tmpl)
			break
		}
		closeIdx += open
		b.WriteString(tmpl[:open])
		name := strings.TrimSpace(tmpl[open+2 : closeIdx])
		b.WriteString(vars[name])
		tmpl = tmpl[closeIdx+2:]
	}
	return normalizeSpace(b.String())
}

func normalizeSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

func sameProgram(got, want string) bool {
	return normalizeProgram(got) == normalizeProgram(want)
}
