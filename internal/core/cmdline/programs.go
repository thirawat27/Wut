package cmdline

// subcommandDepth records how many leading words a program treats as verbs
// rather than operands. This is structural knowledge about parsing, not a
// correction rule, so it lives in Go rather than in the rule data.
//
// A program that is absent has depth 0: everything after it is an operand.
// Getting this wrong is cheap in one direction and expensive in the other —
// a missing entry means a verb is read as an operand and a rule simply does
// not fire, whereas an over-long depth swallows real operands. Prefer the
// shallow answer when unsure.
var subcommandDepth = map[string]int{
	"git":           2,
	"docker":        2,
	"podman":        2,
	"kubectl":       2,
	"gh":            2,
	"aws":           2,
	"gcloud":        2,
	"az":            2,
	"systemctl":     1,
	"npm":           1,
	"pnpm":          1,
	"yarn":          1,
	"bun":           1,
	"deno":          1,
	"go":            1,
	"cargo":         1,
	"rustup":        2,
	"pip":           1,
	"pip3":          1,
	"poetry":        1,
	"uv":            1,
	"conda":         1,
	"apt":           1,
	"apt-get":       1,
	"dnf":           1,
	"yum":           1,
	"pacman":        0,
	"brew":          1,
	"choco":         1,
	"winget":        1,
	"scoop":         1,
	"terraform":     1,
	"helm":          1,
	"dotnet":        1,
	"composer":      1,
	"gem":           1,
	"bundle":        1,
	"mvn":           1,
	"gradle":        1,
	"flutter":       1,
	"adb":           1,
	"tmux":          1,
	"twine":         1,
	"svn":           1,
	"hg":            1,
	"jj":            1,
	"nix":           2,
	"make":          0,
	"goreleaser":    1,
	"golangci-lint": 1,
}

// secondLevelVerbs names the first-level verbs that genuinely take a second
// verb after them.
//
// Without this, a flat depth of 2 makes `git checkout develp` parse "develp"
// as a subcommand rather than a branch name, and every producer that works on
// operands goes silent. Depth alone is not enough: `git remote add` needs two
// words and `git checkout main` needs one, in the same program.
var secondLevelVerbs = map[string]map[string]bool{
	"git": {
		"remote": true, "stash": true, "submodule": true, "worktree": true,
		"sparse-checkout": true, "bisect": true, "notes": true,
		"maintenance": true, "config": true, "reflog": true, "lfs": true,
	},
	"docker": {
		"compose": true, "network": true, "volume": true, "system": true,
		"image": true, "container": true, "buildx": true, "context": true,
		"builder": true, "plugin": true, "secret": true, "stack": true,
		"swarm": true, "trust": true, "manifest": true, "config": true,
	},
	"podman": {
		"compose": true, "network": true, "volume": true, "system": true,
		"image": true, "container": true, "pod": true, "machine": true,
	},
	"gh": {
		"repo": true, "pr": true, "issue": true, "release": true,
		"workflow": true, "run": true, "auth": true, "gist": true,
		"secret": true, "codespace": true, "extension": true, "label": true,
		"org": true, "project": true, "ruleset": true, "variable": true,
		"ssh-key": true, "cache": true, "config": true,
	},
	"rustup": {"toolchain": true, "target": true, "component": true, "override": true, "self": true},
	"nix":    {"flake": true, "profile": true, "store": true, "registry": true, "shell": true},
}

// SubcommandDepth reports the maximum number of leading words after the
// program that may be subcommands.
func SubcommandDepth(program string) int {
	return subcommandDepth[program]
}

// TakesSecondVerb reports whether first is one of the verbs that is followed
// by another verb rather than by operands.
//
// A program with no entry keeps its declared depth, which is right for tools
// like kubectl where the second word is always a resource kind.
func TakesSecondVerb(program, first string) bool {
	verbs, ok := secondLevelVerbs[program]
	if !ok {
		return true
	}
	return verbs[first]
}

// IsMultiVerb reports whether the program takes subcommands at all.
func IsMultiVerb(program string) bool {
	return subcommandDepth[program] > 0
}
