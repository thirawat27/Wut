package correct

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed corpora.yaml
var builtinCorpora []byte

// Program is the corpus for one program.
type Program struct {
	Subcommands []string            `yaml:"subcommands"`
	Flags       []string            `yaml:"flags"`
	SubFlags    map[string][]string `yaml:"sub_flags"`
}

// Corpora is the set of known verbs and options, keyed by program name.
type Corpora struct {
	programs map[string]Program
}

// LoadCorpora parses a corpus document.
func LoadCorpora(data []byte) (*Corpora, error) {
	var m map[string]Program
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse corpora: %w", err)
	}
	return &Corpora{programs: m}, nil
}

// BuiltinCorpora returns the corpora compiled into the binary.
func BuiltinCorpora() *Corpora {
	c, err := LoadCorpora(builtinCorpora)
	if err != nil {
		panic("correct: embedded corpora are invalid: " + err.Error())
	}
	return c
}

// Known reports whether the program has a corpus at all. A program with no
// corpus gets no subcommand or flag corrections, which is the safe default:
// silence beats a confident guess drawn from nothing.
func (c *Corpora) Known(program string) bool {
	_, ok := c.programs[normalizeProgram(program)]
	return ok
}

// Subcommands returns the known verbs for a program.
func (c *Corpora) Subcommands(program string) []string {
	return c.programs[normalizeProgram(program)].Subcommands
}

// HasSubcommand reports whether the verb is already correct.
func (c *Corpora) HasSubcommand(program, sub string) bool {
	for _, s := range c.Subcommands(program) {
		if s == sub {
			return true
		}
	}
	return false
}

// Flags returns the options valid for a program, including the ones specific
// to the given subcommand when one is supplied.
func (c *Corpora) Flags(program, sub string) []string {
	p := c.programs[normalizeProgram(program)]
	out := make([]string, 0, len(p.Flags)+8)
	out = append(out, p.Flags...)
	if sub != "" {
		out = append(out, p.SubFlags[sub]...)
	}
	return out
}

// Programs lists every program with a corpus, sorted, for `wut doctor` and for
// tests that assert the data is well formed.
func (c *Corpora) Programs() []string {
	out := make([]string, 0, len(c.programs))
	for name := range c.programs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// normalizeProgram strips a directory prefix and a Windows extension so a
// corpus written for "npm" still matches "npm.cmd" and "/usr/bin/npm".
func normalizeProgram(p string) string {
	s := strings.ToLower(p)
	if i := strings.LastIndexAny(s, "/\\"); i >= 0 {
		s = s[i+1:]
	}
	for _, ext := range []string{".exe", ".cmd", ".bat", ".ps1"} {
		s = strings.TrimSuffix(s, ext)
	}
	return s
}

// commonCommands is the fallback corpus for correcting a mistyped program name
// when PATH cannot be read — in a restricted environment, or when the caller
// supplied Empty facts.
//
// Ordered roughly by how often people type them, because a distance tie keeps
// corpus order.
var commonCommands = []string{
	"ls", "cd", "cat", "grep", "find", "git", "cp", "mv", "rm", "mkdir",
	"echo", "touch", "chmod", "chown", "ps", "kill", "top", "df", "du",
	"tar", "zip", "unzip", "curl", "wget", "ssh", "scp", "rsync", "ping",
	"docker", "kubectl", "npm", "npx", "pnpm", "yarn", "node", "deno", "bun",
	"go", "cargo", "rustc", "python", "python3", "pip", "pip3", "ruby", "gem",
	"java", "javac", "mvn", "gradle", "dotnet", "php", "composer",
	"make", "cmake", "gcc", "g++", "clang", "sed", "awk", "sort", "uniq",
	"head", "tail", "less", "more", "wc", "diff", "which", "whereis", "man",
	"sudo", "su", "apt", "apt-get", "dnf", "yum", "pacman", "brew", "winget",
	"systemctl", "service", "journalctl", "crontab", "env", "export",
	"terraform", "ansible", "helm", "gh", "aws", "gcloud", "az",
	"vim", "nvim", "nano", "code", "tmux", "screen", "htop", "jq", "rg", "fd",
}

// FallbackCommands returns the built-in program corpus.
func FallbackCommands() []string { return commonCommands }
