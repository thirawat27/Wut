package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"wut/internal/config"
)

const (
	integrationStartMarker = "# WUT Shell Integration"
	integrationEndMarker   = "# End WUT Integration"
	legacyIntegrationEnd   = "# End WUT Shell Integration"
	cmdAutoRunKey          = `HKCU\Software\Microsoft\Command Processor`
	cmdAutoRunValue        = "AutoRun"
)

// Installer manages shell integration in a safe, reversible way.
type Installer struct {
	shells []string
	// DryRun, when true, reports what would change without touching files or the registry.
	DryRun bool
	// Backup, when true, creates a timestamped backup before modifying a shell config file.
	Backup bool
}

func NewInstaller() *Installer {
	return &Installer{
		shells: DetectInstallableShells(),
		Backup: true,
	}
}

// Install adds WUT integration for the named shell. It backs up the existing
// config first and refuses to install twice into the same file.
func (i *Installer) Install(shellName string) (*InstallPlan, error) {
	shellName = CanonicalName(shellName)
	if shellName == "" {
		return nil, fmt.Errorf("unsupported shell")
	}
	if !SupportsInstall(shellName) {
		return nil, fmt.Errorf("unsupported shell for installation: %s", shellName)
	}

	plan := &InstallPlan{Shell: shellName}

	if shellName == "cmd" {
		if i.DryRun {
			plan.Actions = append(plan.Actions, "write cmd init script and update HKCU\\Software\\Microsoft\\Command Processor\\AutoRun")
			return plan, nil
		}
		return plan, installCmdIntegration()
	}

	configFile, err := GetConfigFile(shellName)
	if err != nil {
		return nil, err
	}

	if i.DryRun {
		plan.ConfigFile = configFile
		plan.Actions = append(plan.Actions, fmt.Sprintf("append WUT block to %s", configFile))
		if i.Backup {
			plan.Actions = append(plan.Actions, fmt.Sprintf("create timestamped backup of %s", configFile))
		}
		return plan, nil
	}

	if err := os.MkdirAll(filepath.Dir(configFile), 0755); err != nil {
		return nil, fmt.Errorf("failed to create shell config directory: %w", err)
	}

	if IsInstalled(configFile) {
		return nil, fmt.Errorf("already installed")
	}

	if i.Backup {
		if backupPath, err := backupConfigFile(configFile); err != nil {
			return nil, fmt.Errorf("failed to back up shell config: %w", err)
		} else {
			plan.BackupFile = backupPath
		}
	}

	shellCode := strings.TrimSpace(GenerateShellCode(shellName))
	if shellCode == "" {
		return nil, fmt.Errorf("unsupported shell for installation: %s", shellName)
	}

	f, err := os.OpenFile(configFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open shell config: %w", err)
	}
	defer f.Close()

	marker := fmt.Sprintf("\n%s\n%s\n%s\n", integrationStartMarker, shellCode, integrationEndMarker)
	if _, err := f.WriteString(marker); err != nil {
		return nil, fmt.Errorf("failed to write shell config: %w", err)
	}

	plan.ConfigFile = configFile
	return plan, nil
}

// Uninstall removes WUT integration for the named shell and restores the
// most recent backup if one exists.
func (i *Installer) Uninstall(shellName string) (*InstallPlan, error) {
	shellName = CanonicalName(shellName)
	if shellName == "" {
		return nil, fmt.Errorf("unsupported shell")
	}

	plan := &InstallPlan{Shell: shellName}

	if shellName == "cmd" {
		if i.DryRun {
			plan.Actions = append(plan.Actions, "remove cmd init script and clean HKCU\\Software\\Microsoft\\Command Processor\\AutoRun")
			return plan, nil
		}
		return plan, uninstallCmdIntegration()
	}

	configFile, err := GetConfigFile(shellName)
	if err != nil {
		return nil, err
	}

	if i.DryRun {
		plan.ConfigFile = configFile
		plan.Actions = append(plan.Actions, fmt.Sprintf("remove WUT block from %s", configFile))
		if i.Backup {
			plan.Actions = append(plan.Actions, fmt.Sprintf("restore latest backup of %s if present", configFile))
		}
		return plan, nil
	}

	content, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read shell config: %w", err)
	}

	newContent, removed := removeWUTSection(string(content))
	if !removed {
		return nil, fmt.Errorf("WUT integration not found in %s", configFile)
	}

	if err := os.WriteFile(configFile, []byte(newContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write shell config: %w", err)
	}

	plan.ConfigFile = configFile

	if i.Backup {
		if backupPath, err := restoreLatestBackup(configFile); err == nil && backupPath != "" {
			plan.BackupFile = backupPath
		}
	}

	return plan, nil
}

// InstallPlan describes the changes an install/uninstall operation performed
// or would perform.
type InstallPlan struct {
	Shell      string
	ConfigFile string
	BackupFile string
	Actions    []string
}

func (i *Installer) GetDetectedShells() []string {
	return i.shells
}

func GetDetectedShells() []string {
	return DetectInstallableShells()
}

func GetConfigFile(shellName string) (string, error) {
	shellName = CanonicalName(shellName)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	_, xdgConfigHome := xdgDirs(home)
	appData := strings.TrimSpace(os.Getenv("APPDATA"))

	switch shellName {
	case "bash":
		defaultPath := filepath.Join(home, ".bashrc")
		if runtime.GOOS == "darwin" {
			defaultPath = filepath.Join(home, ".bash_profile")
		}
		return pickConfigPath(defaultPath,
			filepath.Join(home, ".bashrc"),
			filepath.Join(home, ".bash_profile"),
			filepath.Join(home, ".profile"),
		), nil
	case "zsh":
		return pickConfigPath(filepath.Join(home, ".zshrc"),
			filepath.Join(home, ".zshrc"),
			filepath.Join(home, ".zprofile"),
		), nil
	case "fish":
		return filepath.Join(xdgConfigHome, "fish", "config.fish"), nil
	case "powershell", "pwsh":
		if profile, err := queryPowerShellProfile(shellName); err == nil && profile != "" {
			return profile, nil
		}

		if runtime.GOOS == "windows" {
			if shellName == "powershell" {
				return filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"), nil
			}
			return filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"), nil
		}
		return filepath.Join(xdgConfigHome, "powershell", "Microsoft.PowerShell_profile.ps1"), nil
	case "nushell":
		if configPath, err := queryNuConfigPath(); err == nil && configPath != "" {
			return configPath, nil
		}
		if runtime.GOOS == "windows" && appData != "" {
			return filepath.Join(appData, "nushell", "config.nu"), nil
		}
		return filepath.Join(xdgConfigHome, "nushell", "config.nu"), nil
	case "xonsh":
		defaultPath := filepath.Join(home, ".xonshrc")
		if runtime.GOOS == "windows" && appData != "" {
			return pickConfigPath(defaultPath,
				filepath.Join(home, ".xonshrc"),
				filepath.Join(appData, "xonsh", "rc.xsh"),
				filepath.Join(home, ".config", "xonsh", "rc.xsh"),
			), nil
		}
		return pickConfigPath(defaultPath,
			filepath.Join(home, ".xonshrc"),
			filepath.Join(xdgConfigHome, "xonsh", "rc.xsh"),
		), nil
	case "elvish":
		legacyPath := filepath.Join(home, ".elvish", "rc.elv")
		if _, err := os.Stat(legacyPath); err == nil {
			return legacyPath, nil
		}
		if runtime.GOOS == "windows" && appData != "" {
			return filepath.Join(appData, "elvish", "rc.elv"), nil
		}
		return filepath.Join(xdgConfigHome, "elvish", "rc.elv"), nil
	case "tcsh":
		return pickConfigPath(filepath.Join(home, ".tcshrc"),
			filepath.Join(home, ".tcshrc"),
			filepath.Join(home, ".cshrc"),
		), nil
	case "csh":
		return filepath.Join(home, ".cshrc"), nil
	case "ksh":
		return filepath.Join(home, ".kshrc"), nil
	case "mksh":
		return filepath.Join(home, ".mkshrc"), nil
	case "yash":
		return filepath.Join(home, ".yashrc"), nil
	case "dash", "ash", "sh":
		return filepath.Join(home, ".profile"), nil
	case "cmd":
		return cmdInitScriptPath(), nil
	default:
		return "", fmt.Errorf("unsupported shell: %s", shellName)
	}
}

func IsInstalled(configFile string) bool {
	content, err := os.ReadFile(configFile)
	if err != nil {
		return false
	}
	return strings.Contains(string(content), integrationStartMarker)
}

func GenerateShellCode(shellName string) string {
	shellName = CanonicalName(shellName)
	switch shellName {
	case "bash", "zsh":
		return generateBashZshCode()
	case "fish":
		return generateFishCode()
	case "powershell", "pwsh":
		return generatePowerShellCode(shellName)
	case "nushell":
		return generateNushellCode()
	case "xonsh":
		return generateXonshCode()
	case "elvish":
		return generateElvishCode()
	case "cmd":
		return generateCmdCode()
	default:
		return ""
	}
}

func GetReloadCommand(shellName, configFile string) string {
	shellName = CanonicalName(shellName)
	switch shellName {
	case "bash", "zsh", "fish":
		return "source " + configFile
	case "powershell", "pwsh":
		return ". " + configFile
	default:
		return ""
	}
}

// generateBashZshCode creates a minimal, non-invasive integration.
// It provides keybindings and helper commands only. It does NOT replace
// command_not_found handlers or hook into the prompt, so it cannot break
// other shell extensions.
func generateBashZshCode() string {
	return `# WUT Key Bindings - Quick Access
# This block is managed by WUT. Remove it with: wut install --uninstall

# Only define helpers if wut is available.
if command -v wut >/dev/null 2>&1; then
    __wut_tui() {
        wut suggest
    }

    __wut_with_current() {
        local cmd="${READLINE_LINE}"
        READLINE_LINE=""
        READLINE_POINT=0
        wut suggest "$cmd"
    }

    oops() {
        local cmd=""
        if [[ $# -gt 0 ]]; then
            cmd="$*"
        else
            cmd="$(fc -ln -1 2>/dev/null | tail -n 1)"
            case "$cmd" in
                oops*|again*|wut\ *)
                    cmd="$(fc -ln -2 2>/dev/null | head -n 1)"
                    ;;
            esac
        fi
        cmd="$(printf '%s' "$cmd" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//')"
        if [[ -z "$cmd" || "$cmd" == wut\ * || "$cmd" == oops* || "$cmd" == again* ]]; then
            return 1
        fi
        wut fix "$cmd"
    }

    again() {
        oops "$@"
    }

    if [[ -n "$BASH_VERSION" ]]; then
        bind '"\C-@":"\C-uwut suggest\C-m"' 2>/dev/null || true
        bind '"\C-g":"\C-awut suggest \"\C-e\"\C-m"' 2>/dev/null || true
    elif [[ -n "$ZSH_VERSION" ]]; then
        autoload -Uz add-zsh-hook 2>/dev/null || true
        __wut_zle_tui() {
            BUFFER='wut suggest'
            zle accept-line
        }
        __wut_zle_current() {
            local cmd="$BUFFER"
            BUFFER="wut suggest ${(q)cmd}"
            zle accept-line
        }
        zle -N __wut_zle_tui 2>/dev/null || true
        zle -N __wut_zle_current 2>/dev/null || true
        bindkey '^@' __wut_zle_tui 2>/dev/null || true
        bindkey '^G' __wut_zle_current 2>/dev/null || true
    fi
fi
`
}

func generateFishCode() string {
	return `# WUT Key Bindings - Quick Access
# This block is managed by WUT. Remove it with: wut install --uninstall

if command -q wut
    function __wut_tui
        wut suggest
        commandline -f repaint
    end

    function __wut_with_current
        set -l cmd (commandline)
        wut suggest $cmd
        commandline -f repaint
    end

    function oops
        set -l cmd (string join ' ' $argv)
        if test -z "$cmd"
            set cmd $history[1]
            if string match -qr '^(oops|again|wut)\b' -- $cmd
                set cmd $history[2]
            end
        end

        set cmd (string trim -- $cmd)
        if test -z "$cmd"
            return 1
        end
        if string match -qr '^(oops|again|wut)\b' -- $cmd
            return 1
        end

        wut fix "$cmd"
    end

    function again
        oops $argv
    end

    bind \c@ __wut_tui 2>/dev/null; or true
    bind \cg __wut_with_current 2>/dev/null; or true
end
`
}

func generatePowerShellCode(sourceShell string) string {
	return fmt.Sprintf(`# WUT Key Bindings - Quick Access
# This block is managed by WUT. Remove it with: wut install --uninstall

if (-not (Get-Command wut -ErrorAction SilentlyContinue)) {
    return
}

function Invoke-WUT-TUI {
    wut suggest
}

function Invoke-WUT-WithCurrent {
    $line = $null
    $cursor = $null
    [Microsoft.PowerShell.PSConsoleReadLine]::GetBufferState([ref]$line, [ref]$cursor)
    [Microsoft.PowerShell.PSConsoleReadLine]::RevertLine()
    $cmdLine = 'wut suggest "' + $line + '"'
    [Microsoft.PowerShell.PSConsoleReadLine]::Insert($cmdLine)
    [Microsoft.PowerShell.PSConsoleReadLine]::AcceptLine()
}

function Invoke-WUTOops {
    param(
        [Parameter(ValueFromRemainingArguments = $true)]
        [string[]]$CommandLine
    )

    $target = ($CommandLine -join ' ').Trim()
    if (-not $target) {
        $history = @(Get-History -Count 2 -ErrorAction SilentlyContinue)
        if ($history.Count -gt 0) {
            $target = $history[0].CommandLine
            if (($target -like 'oops*' -or $target -like 'again*' -or $target -like 'wut *') -and $history.Count -gt 1) {
                $target = $history[1].CommandLine
            }
        }
    }

    if (-not $target -or $target -like 'oops*' -or $target -like 'again*' -or $target -like 'wut *') {
        return
    }

    $env:WUT_SOURCE_SHELL = '%s'
    wut fix "$target"
    Remove-Item Env:\WUT_SOURCE_SHELL -ErrorAction SilentlyContinue
}

Set-Alias oops Invoke-WUTOops -ErrorAction SilentlyContinue
Set-Alias again Invoke-WUTOops -ErrorAction SilentlyContinue

Set-PSReadLineKeyHandler -Chord 'Ctrl+Space' -ScriptBlock { Invoke-WUT-TUI } -ErrorAction SilentlyContinue
Set-PSReadLineKeyHandler -Chord 'Ctrl+g' -ScriptBlock { Invoke-WUT-WithCurrent } -ErrorAction SilentlyContinue
`, sourceShell)
}

func generateNushellCode() string {
	return `# WUT integration for Nushell
# This block is managed by WUT. Remove it with: wut install --uninstall

if (which wut | is-empty) {
    return
}

def --env wut-current-line [] {
    ^wut suggest (commandline)
}

def --env oops [...args] {
    if (($args | length) == 0) {
        ^wut fix --exec
    } else {
        ^wut fix --exec ...$args
    }
}

def --env again [...args] {
    oops ...$args
}
`
}

func generateXonshCode() string {
	return `# WUT integration for Xonsh
# This block is managed by WUT. Remove it with: wut install --uninstall

import shutil

if shutil.which("wut") is None:
    return

aliases["wut-tui"] = ["wut", "suggest"]
aliases["oops"] = lambda args: subprocess.run(["wut", "fix", "--exec", *args], check=False)
aliases["again"] = aliases["oops"]

@events.on_ptk_create
def _wut_keybindings(bindings, **kwargs):
    try:
        from prompt_toolkit.keys import Keys
    except Exception:
        return

    @bindings.add(Keys.ControlG)
    def _wut_with_current(event):
        line = event.current_buffer.text
        subprocess.run(["wut", "suggest", line], check=False)
        event.app.renderer.erase()
`
}

func generateElvishCode() string {
	return `# WUT integration for Elvish
# This block is managed by WUT. Remove it with: wut install --uninstall

if (not (has-external wut)) {
    return
}

set edit:insert:binding[Ctrl-G] = {
    wut suggest $edit:current-command
}

set edit:insert:binding[Ctrl-@] = {
    wut suggest
}

fn oops {|@args|
    wut fix --exec $@args
}

fn again {|@args|
    oops $@args
}
`
}

func generateCmdCode() string {
	return `@echo off
REM WUT Cmd Integration - managed by WUT. Remove with: wut install --uninstall
doskey wut-tui=wut suggest
doskey wut-current=wut suggest $*
doskey oops=wut fix --exec $*
doskey again=wut fix --exec $*
`
}

func queryPowerShellProfile(shellName string) (string, error) {
	out, err := exec.Command(shellName, "-NoProfile", "-Command", "Write-Output $PROFILE").Output()
	if err != nil {
		return "", err
	}

	profile := strings.TrimSpace(string(out))
	if profile == "" {
		return "", fmt.Errorf("empty profile path")
	}

	if err := os.MkdirAll(filepath.Dir(profile), 0755); err != nil {
		return "", fmt.Errorf("failed to create profile directory: %w", err)
	}
	return profile, nil
}

func queryNuConfigPath() (string, error) {
	out, err := exec.Command("nu", "-c", "$nu.config-path").Output()
	if err != nil {
		return "", err
	}

	configPath := strings.TrimSpace(string(out))
	if configPath == "" {
		return "", fmt.Errorf("empty config path")
	}
	return configPath, nil
}

func pickConfigPath(defaultPath string, candidates ...string) string {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return defaultPath
}

func installCmdIntegration() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("cmd integration is only supported on Windows")
	}

	scriptPath := cmdInitScriptPath()
	if isCmdInstalled(scriptPath) {
		return fmt.Errorf("already installed")
	}
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		return fmt.Errorf("failed to create cmd integration directory: %w", err)
	}

	// Back up existing registry value before changing it.
	if _, err := backupRegistryAutoRun(); err != nil {
		return fmt.Errorf("failed to back up cmd autorun: %w", err)
	}

	if err := os.WriteFile(scriptPath, []byte(generateCmdCode()), 0644); err != nil {
		return fmt.Errorf("failed to write cmd integration script: %w", err)
	}

	snippet := cmdAutoRunSnippet(scriptPath)
	currentValue, err := readRegistryString(cmdAutoRunKey, cmdAutoRunValue)
	if err != nil {
		return fmt.Errorf("failed to read cmd autorun: %w", err)
	}
	updatedValue := strings.TrimSpace(currentValue)
	if !strings.Contains(updatedValue, snippet) {
		if updatedValue == "" {
			updatedValue = snippet
		} else {
			updatedValue += " & " + snippet
		}
	}
	if err := writeRegistryString(cmdAutoRunKey, cmdAutoRunValue, updatedValue); err != nil {
		return fmt.Errorf("failed to configure cmd autorun: %w", err)
	}

	return nil
}

func uninstallCmdIntegration() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("cmd integration is only supported on Windows")
	}

	scriptPath := cmdInitScriptPath()
	snippet := cmdAutoRunSnippet(scriptPath)

	currentValue, err := readRegistryString(cmdAutoRunKey, cmdAutoRunValue)
	if err != nil {
		return fmt.Errorf("failed to read cmd autorun: %w", err)
	}

	updatedValue := strings.TrimSpace(currentValue)
	updatedValue = strings.Replace(updatedValue, " & "+snippet, "", 1)
	updatedValue = strings.Replace(updatedValue, snippet+" & ", "", 1)
	updatedValue = strings.Replace(updatedValue, snippet, "", 1)
	updatedValue = strings.Trim(strings.TrimSpace(updatedValue), "&")
	updatedValue = strings.TrimSpace(updatedValue)

	if updatedValue == "" {
		if err := deleteRegistryValue(cmdAutoRunKey, cmdAutoRunValue); err != nil {
			return fmt.Errorf("failed to remove cmd autorun: %w", err)
		}
	} else if updatedValue != currentValue {
		if err := writeRegistryString(cmdAutoRunKey, cmdAutoRunValue, updatedValue); err != nil {
			return fmt.Errorf("failed to update cmd autorun: %w", err)
		}
	}

	_ = os.Remove(scriptPath)
	return nil
}

func isCmdInstalled(scriptPath string) bool {
	currentValue, err := readRegistryString(cmdAutoRunKey, cmdAutoRunValue)
	if err != nil {
		return false
	}
	return strings.Contains(currentValue, cmdAutoRunSnippet(scriptPath))
}

func cmdInitScriptPath() string {
	return filepath.Join(config.GetDataDir(), "shell", "wut-cmd-init.cmd")
}

func cmdAutoRunSnippet(scriptPath string) string {
	scriptPath = strings.ReplaceAll(scriptPath, `"`, `\"`)
	return fmt.Sprintf(`if exist "%s" call "%s"`, scriptPath, scriptPath)
}

func readRegistryString(key, valueName string) (string, error) {
	cmd := exec.Command("reg", "query", key, "/v", valueName)
	output, err := cmd.Output()
	if err != nil {
		return "", nil
	}

	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 {
			continue
		}
		if !strings.EqualFold(fields[0], valueName) {
			continue
		}
		return strings.Join(fields[2:], " "), nil
	}

	return "", nil
}

func writeRegistryString(key, valueName, value string) error {
	cmd := exec.Command("reg", "add", key, "/v", valueName, "/t", "REG_EXPAND_SZ", "/d", value, "/f")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func deleteRegistryValue(key, valueName string) error {
	cmd := exec.Command("reg", "delete", key, "/v", valueName, "/f")
	if output, err := cmd.CombinedOutput(); err != nil {
		lower := strings.ToLower(string(output))
		if strings.Contains(lower, "unable to find") || strings.Contains(lower, "cannot find") {
			return nil
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// backupConfigFile writes a timestamped copy of path into WUT's backup
// directory. It returns the path to the backup file.
func backupConfigFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty config path")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", nil
	}

	backupDir := shellBackupDir()
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", err
	}

	base := filepath.Base(path)
	timestamp := time.Now().Format("20060102-150405")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("%s-%s.wut-backup", base, timestamp))

	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(backupPath, content, 0600); err != nil {
		return "", err
	}
	return backupPath, nil
}

// restoreLatestBackup restores the most recent backup for path if one exists.
func restoreLatestBackup(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty config path")
	}

	backupDir := shellBackupDir()
	base := filepath.Base(path)
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return "", err
	}

	prefix := base + "-"
	var latest os.DirEntry
	var latestTime time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".wut-backup") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if latest == nil || info.ModTime().After(latestTime) {
			latest = entry
			latestTime = info.ModTime()
		}
	}

	if latest == nil {
		return "", nil
	}

	backupPath := filepath.Join(backupDir, latest.Name())
	content, err := os.ReadFile(backupPath)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		return "", err
	}
	return backupPath, nil
}

// configDataDir returns the base directory for WUT data. It is a variable so
// tests can swap it for a temporary directory.
var configDataDir = config.GetDataDir

func shellBackupDir() string {
	return filepath.Join(configDataDir(), "backups", "shell")
}

// backupRegistryAutoRun saves the current cmd AutoRun value to a file so it can
// be inspected by the user even though registry restoration is handled by the
// uninstall logic.
func backupRegistryAutoRun() (string, error) {
	value, err := readRegistryString(cmdAutoRunKey, cmdAutoRunValue)
	if err != nil {
		return "", err
	}

	backupDir := shellBackupDir()
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", err
	}

	timestamp := time.Now().Format("20060102-150405")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("cmd-autorun-%s.wut-backup", timestamp))
	if err := os.WriteFile(backupPath, []byte(value), 0600); err != nil {
		return "", err
	}
	return backupPath, nil
}

// removeWUTSection strips the WUT integration block from shell config content.
// It also removes the legacy end marker for backwards compatibility.
func removeWUTSection(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	newLines := make([]string, 0, len(lines))
	inWUTSection := false
	removed := false

	for _, line := range lines {
		if strings.Contains(line, integrationStartMarker) {
			inWUTSection = true
			removed = true
			continue
		}
		if strings.Contains(line, integrationEndMarker) || strings.Contains(line, legacyIntegrationEnd) {
			inWUTSection = false
			continue
		}
		if !inWUTSection {
			newLines = append(newLines, line)
		}
	}

	// Collapse multiple trailing blank lines to a single newline.
	result := strings.Join(newLines, "\n")
	result = strings.TrimRight(result, "\n")
	if result != "" {
		result += "\n"
	}
	return result, removed
}
