package corrector

import (
	"fmt"
	"regexp"
	"strings"
)

// Rule turns a failed command into one or more corrected commands.
//
// SAFETY: a rule never executes the command it is correcting. It decides from
// three sources, in order of preference:
//
//  1. the command string alone;
//  2. Facts — files WUT chooses to read and a fixed allowlist of read-only
//     probes WUT chooses to run (see facts.go);
//  3. the failed command's output, when the caller captured it in the shell.
//
// Source 2 is what lets WUT answer questions like "does this branch have an
// upstream?" without repeating the command that failed.
type Rule struct {
	Name string
	// NeedsOutput marks rules that cannot decide without the failed command's
	// stdout/stderr. They stay dormant when no output is available.
	NeedsOutput bool
	// Match decides whether the rule applies. facts is never nil.
	Match func(command, output string, facts *Facts) bool
	// Suggest returns candidate corrections, best first.
	Suggest     func(command, output string, facts *Facts) []string
	Explanation string
}

// coreRules is ordered: the first rule that produces a candidate wins, so more
// specific rules come before general ones.
var coreRules = []Rule{
	// ── Fact-driven rules: no output needed, nothing re-executed ────────────
	{
		Name: "git_push_no_upstream",
		Match: func(command, output string, facts *Facts) bool {
			if !isGitPush(command) {
				return false
			}
			// The command already names a remote/branch or sets upstream.
			if strings.Contains(command, "--set-upstream") || strings.Contains(command, "-u ") {
				return false
			}
			if len(strings.Fields(command)) > 2 {
				return false
			}
			return facts.InGitRepo() && facts.GitBranch() != "" && !facts.GitHasUpstream()
		},
		Suggest: func(command, output string, facts *Facts) []string {
			remote := facts.GitRemote()
			if remote == "" {
				remote = "origin"
			}
			return []string{fmt.Sprintf("git push --set-upstream %s %s", remote, facts.GitBranch())}
		},
		Explanation: "This branch has no upstream yet — push and set one",
	},
	{
		Name: "git_checkout_unknown_branch",
		Match: func(command, output string, facts *Facts) bool {
			target, ok := gitCheckoutTarget(command)
			if !ok || !facts.InGitRepo() {
				return false
			}
			branches := facts.GitBranches()
			if len(branches) == 0 {
				return false
			}
			return !containsString(branches, target) && nearestName(target, branches) != ""
		},
		Suggest: func(command, output string, facts *Facts) []string {
			target, _ := gitCheckoutTarget(command)
			best := nearestName(target, facts.GitBranches())
			if best == "" {
				return nil
			}
			return []string{replaceLastToken(command, target, best)}
		},
		Explanation: "No such branch — did you mean this one?",
	},
	{
		Name: "npm_unknown_script",
		Match: func(command, output string, facts *Facts) bool {
			script, ok := runScriptTarget(command)
			if !ok {
				return false
			}
			scripts := facts.NpmScripts()
			if len(scripts) == 0 {
				return false
			}
			return !containsString(scripts, script) && nearestName(script, scripts) != ""
		},
		Suggest: func(command, output string, facts *Facts) []string {
			script, _ := runScriptTarget(command)
			best := nearestName(script, facts.NpmScripts())
			if best == "" {
				return nil
			}
			return []string{replaceLastToken(command, script, best)}
		},
		Explanation: "That script is not in package.json — did you mean this one?",
	},
	{
		Name: "make_unknown_target",
		Match: func(command, output string, facts *Facts) bool {
			target, ok := makeTarget(command)
			if !ok {
				return false
			}
			targets := facts.MakeTargets()
			if len(targets) == 0 {
				return false
			}
			return !containsString(targets, target) && nearestName(target, targets) != ""
		},
		Suggest: func(command, output string, facts *Facts) []string {
			target, _ := makeTarget(command)
			best := nearestName(target, facts.MakeTargets())
			if best == "" {
				return nil
			}
			return []string{replaceLastToken(command, target, best)}
		},
		Explanation: "No such Makefile target — did you mean this one?",
	},
	{
		Name: "cd_missing_space",
		Match: func(command, output string, facts *Facts) bool {
			return strings.HasPrefix(command, "cd..")
		},
		Suggest: func(command, output string, facts *Facts) []string {
			return []string{strings.Replace(command, "cd..", "cd ..", 1)}
		},
		Explanation: "Missing space in cd command",
	},
	{
		Name: "cd_unknown_directory",
		Match: func(command, output string, facts *Facts) bool {
			target, ok := singleArgument(command, "cd")
			if !ok || target == ".." || target == "-" || target == "~" {
				return false
			}
			if facts.Exists(target) {
				return false
			}
			return nearestName(target, facts.Directories()) != ""
		},
		Suggest: func(command, output string, facts *Facts) []string {
			target, _ := singleArgument(command, "cd")
			best := nearestName(target, facts.Directories())
			if best == "" {
				return nil
			}
			return []string{"cd " + quoteIfNeeded(best)}
		},
		Explanation: "No such directory here — did you mean this one?",
	},
	{
		Name: "missing_recursive_flag",
		Match: func(command, output string, facts *Facts) bool {
			target, ok := recursiveCandidate(command)
			return ok && facts.IsDir(target)
		},
		Suggest: func(command, output string, facts *Facts) []string {
			fields := strings.Fields(command)
			flag := "-r"
			if fields[0] == "rm" {
				flag = "-r"
			}
			return []string{fields[0] + " " + flag + " " + strings.Join(fields[1:], " ")}
		},
		Explanation: "That target is a directory — the recursive flag is required",
	},
	{
		Name: "local_script_needs_prefix",
		Match: func(command, output string, facts *Facts) bool {
			fields := strings.Fields(command)
			if len(fields) == 0 {
				return false
			}
			name := fields[0]
			// Already a path — ./x, ../x, /x, dir/x — needs no prefix. A bare
			// name that merely has an extension (deploy.sh) is exactly the case
			// worth catching, so this tests for separators, not for dots.
			if strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
				return false
			}
			return facts.Exists(name) && !facts.IsDir(name)
		},
		Suggest: func(command, output string, facts *Facts) []string {
			return []string{"./" + command}
		},
		Explanation: "A file with that name is here — run it with ./",
	},
	{
		Name: "apt_search",
		Match: func(command, output string, facts *Facts) bool {
			return strings.HasPrefix(command, "apt-get search") || strings.HasPrefix(command, "apt search")
		},
		Suggest: func(command, output string, facts *Facts) []string {
			return []string{
				strings.Replace(strings.Replace(command, "apt-get search", "apt-cache search", 1),
					"apt search", "apt-cache search", 1),
			}
		},
		Explanation: "Use apt-cache to search for packages instead",
	},
	{
		Name: "go_run_directory",
		Match: func(command, output string, facts *Facts) bool {
			return strings.TrimSpace(command) == "go run" ||
				(strings.HasPrefix(command, "go run") && strings.Contains(output, "go run: no go files listed"))
		},
		Suggest: func(command, output string, facts *Facts) []string {
			return []string{"go run ."}
		},
		Explanation: "Run all go files in the current directory",
	},

	// ── Output-driven rules: dormant until the shell supplies the output ────
	{
		Name:        "git_push_set_upstream",
		NeedsOutput: true,
		Match: func(command, output string, facts *Facts) bool {
			return isGitPush(command) && strings.Contains(output, "git push --set-upstream")
		},
		Suggest: func(command, output string, facts *Facts) []string {
			re := regexp.MustCompile(`git push --set-upstream [^\n]*`)
			if match := re.FindString(output); match != "" {
				return []string{strings.TrimSpace(match)}
			}
			return nil
		},
		Explanation: "Set upstream branch for git push",
	},
	{
		Name:        "sudo_permission_denied",
		NeedsOutput: true,
		Match: func(command, output string, facts *Facts) bool {
			if strings.HasPrefix(command, "sudo ") {
				return false
			}
			lower := strings.ToLower(output)
			return strings.Contains(lower, "permission denied") ||
				strings.Contains(lower, "operation not permitted") ||
				strings.Contains(lower, "are you root")
		},
		Suggest: func(command, output string, facts *Facts) []string {
			return []string{"sudo " + command}
		},
		Explanation: "Command requires elevated privileges (sudo)",
	},
	{
		Name:        "git_did_you_mean",
		NeedsOutput: true,
		Match: func(command, output string, facts *Facts) bool {
			if !strings.HasPrefix(command, "git ") {
				return false
			}
			// Git words this differently across versions and depending on
			// help.autocorrect, so match every phrasing that precedes a list.
			return strings.Contains(output, "Did you mean") ||
				strings.Contains(output, "The most similar command")
		},
		Suggest: func(command, output string, facts *Facts) []string {
			re := regexp.MustCompile(`(?m)^[ \t]+([a-z][a-z0-9-]*)[ \t]*$`)
			matches := re.FindAllStringSubmatch(output, -1)
			fields := strings.Fields(command)
			if len(fields) < 2 {
				return nil
			}

			var candidates []string
			for _, match := range matches {
				replaced := append([]string(nil), fields...)
				replaced[1] = match[1]
				candidates = append(candidates, strings.Join(replaced, " "))
			}
			return candidates
		},
		Explanation: "Git suggested a correct subcommand",
	},
	{
		Name:        "npm_missing_script",
		NeedsOutput: true,
		Match: func(command, output string, facts *Facts) bool {
			_, ok := runScriptTarget(command)
			return ok && strings.Contains(output, "Missing script:")
		},
		Suggest: func(command, output string, facts *Facts) []string {
			re := regexp.MustCompile(`(?m)^\s+([A-Za-z0-9_:-]+)\s*$`)
			matches := re.FindAllStringSubmatch(output, -1)
			script, _ := runScriptTarget(command)

			var candidates []string
			for _, match := range matches {
				candidates = append(candidates, replaceLastToken(command, script, match[1]))
			}
			return candidates
		},
		Explanation: "Likely a typo in the npm script name",
	},
	{
		Name:        "brew_install_update",
		NeedsOutput: true,
		Match: func(command, output string, facts *Facts) bool {
			return strings.HasPrefix(command, "brew install") && strings.Contains(output, "No available formula")
		},
		Suggest: func(command, output string, facts *Facts) []string {
			return []string{"brew update && " + command}
		},
		Explanation: "Formula not found, updating brew might help",
	},
	{
		Name:        "docker_not_running",
		NeedsOutput: true,
		Match: func(command, output string, facts *Facts) bool {
			return strings.HasPrefix(command, "docker ") && strings.Contains(output, "Cannot connect to the Docker daemon")
		},
		Suggest: func(command, output string, facts *Facts) []string {
			return []string{
				"sudo systemctl start docker && " + command,
				"sudo service docker start && " + command,
			}
		},
		Explanation: "Docker daemon is not running, starting it first",
	},
	{
		Name:        "port_in_use",
		NeedsOutput: true,
		Match: func(command, output string, facts *Facts) bool {
			return strings.Contains(output, "address already in use") ||
				strings.Contains(output, "port is already allocated")
		},
		Suggest: func(command, output string, facts *Facts) []string {
			re := regexp.MustCompile(`(?i):(\d{2,5})`)
			match := re.FindStringSubmatch(output)
			if len(match) < 2 {
				return nil
			}
			return []string{fmt.Sprintf("kill -9 $(lsof -t -i:%s) && %s", match[1], command)}
		},
		Explanation: "Port is in use, attempt to kill the blocking process",
	},
}

// evaluateRules matches the command — plus the facts WUT gathered itself, plus
// any output the caller captured — against the known error patterns.
//
// It never runs the command being corrected. When output is empty, only rules
// that can decide without it are considered.
func (c *Corrector) evaluateRules(command, output string, facts *Facts) *Correction {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	if facts == nil {
		facts = NewFacts()
	}

	hasOutput := strings.TrimSpace(output) != ""

	for _, rule := range coreRules {
		if rule.NeedsOutput && !hasOutput {
			continue
		}
		if !rule.Match(command, output, facts) {
			continue
		}

		candidates := dedupeCandidates(rule.Suggest(command, output, facts), command)
		if len(candidates) == 0 {
			continue
		}

		// Output-confirmed diagnoses are certain. A fact-driven diagnosis is
		// strong but inferred, so it is scored just below.
		confidence := 0.95
		explanation := "💡 " + rule.Explanation
		if rule.NeedsOutput {
			confidence = 1.0
			explanation = "💡 Output Context: " + rule.Explanation
		}

		return &Correction{
			Original:     command,
			Corrected:    candidates[0],
			Alternatives: candidates,
			Confidence:   confidence,
			Explanation:  explanation,
			Source:       rule.Name,
		}
	}

	return nil
}

// ─── Command shape helpers ───────────────────────────────────────────────────

func isGitPush(command string) bool {
	fields := strings.Fields(command)
	return len(fields) >= 2 && fields[0] == "git" && fields[1] == "push"
}

// gitCheckoutTarget returns the branch name from `git checkout <name>` or
// `git switch <name>`, ignoring forms that create or modify branches.
func gitCheckoutTarget(command string) (string, bool) {
	fields := strings.Fields(command)
	if len(fields) != 3 || fields[0] != "git" {
		return "", false
	}
	if fields[1] != "checkout" && fields[1] != "switch" {
		return "", false
	}
	if strings.HasPrefix(fields[2], "-") {
		return "", false
	}
	return fields[2], true
}

// runScriptTarget returns the script name from `npm|yarn|pnpm run <script>`.
func runScriptTarget(command string) (string, bool) {
	fields := strings.Fields(command)
	if len(fields) != 3 || fields[1] != "run" {
		return "", false
	}
	switch fields[0] {
	case "npm", "yarn", "pnpm", "bun":
		return fields[2], true
	}
	return "", false
}

// makeTarget returns the target name from `make <target>`.
func makeTarget(command string) (string, bool) {
	fields := strings.Fields(command)
	if len(fields) != 2 || fields[0] != "make" || strings.HasPrefix(fields[1], "-") {
		return "", false
	}
	return fields[1], true
}

// singleArgument returns the sole argument of `<name> <arg>`.
func singleArgument(command, name string) (string, bool) {
	fields := strings.Fields(command)
	if len(fields) != 2 || fields[0] != name || strings.HasPrefix(fields[1], "-") {
		return "", false
	}
	return strings.Trim(fields[1], `"'`), true
}

// recursiveCandidate returns the target of a copy/remove that is missing its
// recursive flag.
func recursiveCandidate(command string) (string, bool) {
	fields := strings.Fields(command)
	if len(fields) != 2 {
		return "", false
	}
	switch fields[0] {
	case "rm", "cp":
	default:
		return "", false
	}
	if strings.HasPrefix(fields[1], "-") {
		return "", false
	}
	return strings.Trim(fields[1], `"'`), true
}

// replaceLastToken swaps the final occurrence of old for replacement.
func replaceLastToken(command, old, replacement string) string {
	index := strings.LastIndex(command, old)
	if index < 0 {
		return command
	}
	return command[:index] + replacement + command[index+len(old):]
}

func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

// nearestName returns the closest name in corpus, using the same
// length-scaled edit-distance budget as token correction. It returns "" when
// nothing is close enough, so a rule can decline instead of guessing wildly.
func nearestName(name string, corpus []string) string {
	match, _ := bestMatch(name, corpus, maxDistForLen(name))
	return match
}

func containsString(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}

// dedupeCandidates drops blanks, duplicates, and any candidate identical to the
// original command — suggesting the command the user just ran is noise.
func dedupeCandidates(candidates []string, original string) []string {
	seen := make(map[string]struct{}, len(candidates))
	result := make([]string, 0, len(candidates))

	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || candidate == strings.TrimSpace(original) {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		result = append(result, candidate)
	}
	return result
}
