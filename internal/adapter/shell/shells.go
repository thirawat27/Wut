// Package shell generates and installs the managed block in a user's rc file.
//
// Two things make this harder than it looks, and both shape the design:
//
//  1. The hook must not spawn a process. Running `wut record` after every
//     command costs 5–15ms on Linux and considerably more on Windows, which
//     users notice as a slower prompt and uninstall over. So the hook writes a
//     record with shell builtins only, and WUT ingests it later.
//  2. The nine supported shells have wildly different hook surfaces. Rather
//     than pretend otherwise, each is declared in a support class and WUT says
//     which one it got.
package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Class is how much a shell can honestly deliver.
type Class string

const (
	// ClassFull captures automatically: T0 and T0.5 after install.
	ClassFull Class = "full"
	// ClassFullLater has the capability but needs coexistence work first.
	ClassFullLater Class = "full-later"
	// ClassManual has no usable hook surface. `wut fix "<cmd>"` and piping
	// still work exactly as well as anywhere else.
	ClassManual Class = "manual"
)

// Spec describes one shell.
type Spec struct {
	Name string
	// Class is what this shell can deliver. Declared, never inferred.
	Class Class
	// Tier is the best capture tier reachable here.
	Tier string
	// Comment is the line-comment marker used to delimit the managed block.
	Comment string
	// RCFiles are candidate startup files, best first.
	RCFiles []string
	// Notes explains any caveat, and is shown by `wut doctor`.
	Notes string
	// render builds the block body.
	render func(Params) string
}

// Params is what a hook needs to know about this installation.
type Params struct {
	// SessionsDir is where record files go.
	SessionsDir string
	// Alias is an optional extra trigger word.
	Alias string
	// Binary is the path to the wut executable, when it is not on PATH.
	Binary string
}

// Marker delimiters. Distinctive enough that a block can be found and replaced
// rather than duplicated, and readable enough that someone who did not install
// it can tell what it is.
const (
	blockBegin = "wut managed block >>>"
	blockEnd   = "wut managed block <<<"
	// legacyBegin is the marker the prototype wrote. WUT never edits one — per
	// the clean break there is no shim — but doctor detects it so the user is
	// told why their old `oops` stopped working.
	legacyBegin = "WUT Key Bindings"
)

// specs is the complete support matrix. Order is the order shells are offered.
var specs = []Spec{
	{
		Name: "zsh", Class: ClassFull, Tier: "T0.5", Comment: "#",
		RCFiles: []string{".zshrc"},
		Notes:   "appends to precmd_functions and preexec_functions; nothing is overwritten",
		render:  renderZsh,
	},
	{
		Name: "bash", Class: ClassFullLater, Tier: "T0.5", Comment: "#",
		RCFiles: []string{".bashrc", ".bash_profile", ".profile"},
		Notes:   "uses the DEBUG trap; if bash-preexec, oh-my-bash, or Starship also use it, capture may not fire",
		render:  renderBash,
	},
	{
		Name: "fish", Class: ClassFull, Tier: "T0.5", Comment: "#",
		RCFiles: []string{filepath.Join(".config", "fish", "config.fish")},
		Notes:   "native events; no globals are touched",
		render:  renderFish,
	},
	{
		Name: "pwsh", Class: ClassFull, Tier: "T1", Comment: "#",
		RCFiles: []string{},
		Notes:   "$Error[0] gives the last error with no redirection, so T1 is free here",
		render:  renderPowerShell,
	},
	{
		Name: "powershell", Class: ClassFull, Tier: "T1", Comment: "#",
		RCFiles: []string{},
		Notes:   "same as pwsh; Windows PowerShell 5.1 profile",
		render:  renderPowerShell,
	},
	{
		Name: "nu", Class: ClassFull, Tier: "T0.5", Comment: "#",
		RCFiles: []string{filepath.Join(".config", "nushell", "config.nu")},
		Notes:   "hooks are configuration values; WUT appends to the list",
		render:  renderNushell,
	},
	{
		Name: "xonsh", Class: ClassFull, Tier: "T1", Comment: "#",
		RCFiles: []string{".xonshrc"},
		Notes:   "on_postcommand receives the output, so T1 needs no redirection",
		render:  renderXonsh,
	},
	{
		Name: "elvish", Class: ClassFull, Tier: "T0", Comment: "#",
		RCFiles: []string{filepath.Join(".config", "elvish", "rc.elv")},
		Notes:   "after-command reports duration and error, but not the error text",
		render:  renderElvish,
	},
	{
		Name: "sh", Class: ClassManual, Tier: "none", Comment: "#",
		RCFiles: []string{".profile"},
		Notes:   "POSIX sh has no DEBUG trap; use wut fix \"<command>\" or pipe the error in",
		render:  renderManualPosix,
	},
	{
		Name: "dash", Class: ClassManual, Tier: "none", Comment: "#",
		RCFiles: []string{".profile"},
		Notes:   "same as sh",
		render:  renderManualPosix,
	},
	{
		Name: "ksh", Class: ClassManual, Tier: "none", Comment: "#",
		RCFiles: []string{".kshrc", ".profile"},
		Notes:   "same as sh",
		render:  renderManualPosix,
	},
	{
		Name: "cmd", Class: ClassManual, Tier: "none", Comment: "::",
		RCFiles: []string{},
		Notes:   "cmd.exe has no hook surface at all; only wut fix \"<command>\" works",
		render:  renderManualCmd,
	},
}

// Lookup returns the spec for a shell name.
func Lookup(name string) (Spec, bool) {
	n := normalize(name)
	for _, s := range specs {
		if s.Name == n {
			return s, true
		}
	}
	return Spec{}, false
}

// All returns every supported shell.
func All() []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	return out
}

// FullClass returns the shells that capture automatically, which is the set
// the container test matrix is obliged to cover.
func FullClass() []string {
	var out []string
	for _, s := range specs {
		if s.Class == ClassFull {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

// normalize maps the many names a shell answers to onto one.
func normalize(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndexAny(n, "/\\"); i >= 0 {
		n = n[i+1:]
	}
	n = strings.TrimSuffix(n, ".exe")
	switch {
	case n == "nushell":
		return "nu"
	case n == "pwsh", n == "pwsh-preview":
		return "pwsh"
	case strings.Contains(n, "windowspowershell"), n == "powershell", n == "powershell_ise":
		return "powershell"
	case strings.HasPrefix(n, "bash"):
		return "bash"
	case strings.HasPrefix(n, "zsh"):
		return "zsh"
	case n == "ash", n == "posh":
		return "sh"
	case n == "ksh93", n == "mksh":
		return "ksh"
	}
	return n
}

// Detect finds the shells installed on this machine.
//
// Detection reads the environment and the filesystem. It never runs a shell to
// ask what it is — starting a login shell to identify it can execute the
// user's entire startup file, which is both slow and a real surprise.
func Detect(home string) []Detected {
	active := activeShell()
	var out []Detected

	for _, spec := range specs {
		rc, ok := rcPathFor(spec, home)
		if !ok && spec.Name != "cmd" {
			continue
		}
		d := Detected{
			Spec:   spec,
			RCFile: rc,
			Active: spec.Name == active,
		}
		if rc != "" {
			if data, err := os.ReadFile(rc); err == nil {
				body := string(data)
				d.Installed = strings.Contains(body, blockBegin)
				d.Legacy = strings.Contains(body, legacyBegin)
			}
		}
		out = append(out, d)
	}
	return out
}

// Detected is a shell found on this machine.
type Detected struct {
	Spec      Spec
	RCFile    string
	Active    bool
	Installed bool
	Legacy    bool
}

// activeShell reads the current shell from the environment. Each variable is
// set by exactly one shell, so this is a fact rather than a guess.
func activeShell() string {
	switch {
	case os.Getenv("NU_VERSION") != "":
		return "nu"
	case os.Getenv("XONSH_VERSION") != "":
		return "xonsh"
	case os.Getenv("ELVISH_VERSION") != "":
		return "elvish"
	case os.Getenv("FISH_VERSION") != "":
		return "fish"
	case os.Getenv("ZSH_VERSION") != "":
		return "zsh"
	case os.Getenv("BASH_VERSION") != "":
		return "bash"
	case os.Getenv("PSModulePath") != "" && runtime.GOOS == "windows":
		return "pwsh"
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return normalize(sh)
	}
	return ""
}

// rcPathFor resolves the startup file to edit.
func rcPathFor(spec Spec, home string) (string, bool) {
	switch spec.Name {
	case "pwsh", "powershell":
		if p := powerShellProfile(spec.Name, home); p != "" {
			return p, true
		}
		return "", false
	case "cmd":
		return "", false
	}
	for _, name := range spec.RCFiles {
		p := filepath.Join(home, name)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	// Nothing exists yet. Offer the first candidate: installing into a shell
	// whose rc file has not been created is a normal first-run case.
	if len(spec.RCFiles) > 0 {
		return filepath.Join(home, spec.RCFiles[0]), false
	}
	return "", false
}

// powerShellProfile resolves the profile path per edition and platform.
func powerShellProfile(name, home string) string {
	if runtime.GOOS == "windows" {
		docs := filepath.Join(home, "Documents")
		if od := os.Getenv("OneDrive"); od != "" {
			if _, err := os.Stat(filepath.Join(od, "Documents")); err == nil {
				docs = filepath.Join(od, "Documents")
			}
		}
		if name == "pwsh" {
			return filepath.Join(docs, "PowerShell", "Microsoft.PowerShell_profile.ps1")
		}
		return filepath.Join(docs, "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1")
	}
	if name == "pwsh" {
		return filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1")
	}
	return ""
}

// Block wraps a body in the managed markers.
func Block(spec Spec, body string) string {
	c := spec.Comment
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", c, blockBegin)
	fmt.Fprintf(&b, "%s Managed by WUT. Remove it with: wut shell uninstall\n", c)
	fmt.Fprintf(&b, "%s Type `wut` after a command fails, or `wut <question>` any time.\n", c)
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteByte('\n')
	fmt.Fprintf(&b, "%s %s\n", c, blockEnd)
	return b.String()
}

// Render returns the complete managed block for a shell.
func Render(spec Spec, p Params) string {
	if spec.render == nil {
		return ""
	}
	return Block(spec, spec.render(p))
}
