<div align="center">

# ⚡ WUT (What ?)

### The Smart Command Line Assistant That Actually Understands You

*Stop memorizing commands. Start getting things done.*

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.26-blue)](https://golang.org)
[![Platforms](https://img.shields.io/badge/platforms-Windows%20%7C%20macOS%20%7C%20Linux-blue)]
[![Release](https://img.shields.io/github/v/release/thirawat27/wut)](https://github.com/thirawat27/wut/releases)

[Features](#key-features) • [Install](#installation) • [Quick Start](#getting-started) • [Commands](#command-reference) • [Docs](#configuration)

</div>

---

**WUT** is a standalone command-line assistant that runs as its own terminal application. Launch it with `wut terminal` (or `wut t`), search commands, fix typos, and execute—all without touching your shell config. Optional shell key bindings are available if you want them, but WUT works out of the box as a separate terminal tool.

## Table of Contents

- [Key Features](#key-features)
- [Installation](#installation)
- [Getting Started](#getting-started)
- [Command Reference](#command-reference)
  - [Terminal](#1-terminal-command)
  - [Suggest](#2-suggest-command)
- [Configuration](#configuration)
- [Advanced Usage](#advanced-usage)
- [Troubleshooting](#troubleshooting)

## Key Features

- **Standalone Terminal App**: Launch with `wut terminal` or `wut t`—no shell hooks required
- **Smart Command Suggestions**: Context-aware command recommendations based on your project type and history
- **Typo Correction**: Detect and fix typos across the **entire command sentence** (not just the first word)
- **Undo Assistant**: Instantly suggests how to revert your last command with `wut undo`
- **Command Explanations**: Get detailed breakdowns of what commands do and their potential risks
- **Command Database**: Quick access to practical command examples from the command database
- **History Tracking**: Learn from your command usage patterns
- **Optional Shell Integration**: Add key bindings later with `wut install` if you want them
- **Cross-Platform**: Works on Windows, macOS, Linux, and BSD systems (FreeBSD, OpenBSD, NetBSD)
- **Privacy-Focused**: All processing happens locally on your machine

## Installation

### Windows

#### Option 1: GUI Installer (Recommended for Beginners)

> Note: GUI installer requires building from source with Inno Setup.

1. Clone the repository and build the installer:
   ```powershell
   git clone https://github.com/thirawat27/wut.git
   cd wut
   # Build installer using scripts/wut-installer.iss with Inno Setup
   ```
2. Run the generated `wut-setup.exe` and follow the setup wizard
3. Open a new PowerShell or Command Prompt window
4. Verify installation:
   ```powershell
   wut --version
   ```

#### Option 2: PowerShell Script

Open PowerShell and run:

```powershell
irm https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.ps1 | iex
```

This will automatically download, install, and configure WUT for your system.

### macOS

#### Installation Script (Recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.sh | bash
```

### Linux

#### Installation Script

```bash
curl -fsSL https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.sh | bash
```

### BSD Systems (FreeBSD, OpenBSD, NetBSD)

#### Installation Script

```bash
curl -fsSL https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.sh | bash
```

The script will:
- Detect your system architecture
- Download the appropriate binary
- Install it to `/usr/local/bin` (or `~/.local/bin` for non-root users)
- Set up shell integration
- Initialize configuration

Supported platforms: Linux, macOS, FreeBSD, OpenBSD, NetBSD

### Installation Options

All installation scripts support these options:

| Option (Linux/macOS) | Option (Windows) | Description | Example |
|---------------------|------------------|-------------|---------|
| `--version` | `-Version` | Install specific version | `--version v0.3.0` |
| `--no-init` | `-NoInit` | Skip automatic initialization | `--no-init` |
| `--no-shell` | `-NoShell` | Skip shell integration | `--no-shell` |
| `--force` | `-Force` | Overwrite existing installation | `--force` |
| `--uninstall` | `-Uninstall` | Uninstall WUT | `--uninstall` |

Example with options:
```bash
# Linux/macOS/BSD
curl -fsSL https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.sh | bash -s -- --version v0.3.0 --no-init

# Windows
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.ps1))) -Version v0.3.0 -NoInit
```

### Docker

```bash
# Build the image
docker build -t wut:latest .

# Run WUT
docker run --rm -it wut:latest suggest

# With persistent configuration
docker run --rm -it \
  -v ~/.config/wut:/home/wut/.config/wut \
  wut:latest
```

### Build from Source

Requirements:
- Go 1.26 or higher
- Git
- Make (optional)

```bash
# Clone the repository
git clone https://github.com/thirawat27/wut.git
cd wut

# Build using Make
make build

# Or build directly with Go
go build -o wut .

# Install to system
sudo mv wut /usr/local/bin/
```

## Getting Started

### Initial Setup

After installation, run the initialization command once:

```bash
# Interactive setup (recommended for first-time users)
wut init

# Quick setup with defaults
wut init --quick
```

`wut init` only creates configuration directories and preferences. It does not
modify your shell config unless you explicitly choose to install shell
integration during setup.

The initialization process will:
1. Create configuration directories
2. Set up your preferred theme
3. Optionally set up shell integration (opt-in; no hooks are installed without your consent)
4. Optionally download a curated offline command database with `wut db sync`

### Shell Integration

Shell integration is **opt-in**. WUT will never modify your shell config
automatically during installation or initialization unless you explicitly ask
for it. The integration is intentionally minimal: it adds keyboard shortcuts
and helper commands, but does **not** replace your shell's command-not-found
handler or prompt function.

Before modifying anything, WUT creates a timestamped backup of your shell
config that can be restored with `wut install --uninstall`.

Enable keyboard shortcuts and helper commands:

```bash
# Install for the detected active shell (asks for confirmation)
wut install

# Install for a specific shell
wut install --shell bash
wut install --shell zsh
wut install --shell fish
wut install --shell powershell

# Preview changes without modifying anything
wut install --shell bash --dry-run

# Install for all detected shells
wut install --all --yes

# Skip confirmation prompts
wut install --yes
```

After installation, these keyboard shortcuts will be available:
- **Ctrl+Space**: Open WUT interactive mode
- **Ctrl+G**: Open WUT with the current command line pre-filled
- **oops** / **again**: Show WUT's correction for the last command

To remove shell integration and restore the latest backup:
```bash
wut install --uninstall --shell bash
wut install --uninstall --all
```

### First Commands

WUT works as a standalone terminal app. Try these:

```bash
# Open the WUT terminal (interactive TUI)
wut terminal
wut t                      # Shortcut

# Search for a specific command
wut suggest git

# Fix a typo
wut fix "gti status"

# Explain what a command does
wut explain "docker-compose up -d"

# Get smart suggestions based on your project
wut smart
```

## Command Reference

### Command Shortcuts

WUT provides convenient shortcuts for faster typing:

| Shortcut | Full Command | Description |
|----------|--------------|-------------|
| `wut t` | `wut terminal` | Open WUT as a standalone terminal |
| `wut s` | `wut suggest` | Get command suggestions |
| `wut h` | `wut history` | View command history |
| `wut x` | `wut explain` | Explain a command |
| `wut a` | `wut alias` | Manage aliases |
| `wut c` | `wut config` | Manage configuration |
| `wut d` | `wut db` | Database management |
| `wut f` | `wut fix` | Fix command typos |
| `wut ?` | `wut smart` | Smart suggestions |
| `wut b` | `wut bookmark` | Manage bookmarks |
| `wut undo` | `wut undo` | Revert your last command |

### 1. Terminal Command

Open WUT as a standalone terminal application. This is the recommended way to
use WUT because it does not require any shell integration.

```bash
# Open the interactive WUT terminal
wut terminal
wut t
```

The terminal launches a full-screen TUI where you can search commands, browse
examples, and execute commands directly.

### 2. Suggest Command

Get command suggestions and examples from the command database.

```bash
# Interactive mode with live search
wut suggest
wut s

# Get help for a specific command
wut suggest git
wut s docker

# Output in plain text format
wut suggest npm --raw

# Show only command examples (no descriptions)
wut suggest git --quiet

# Force offline mode (use local database only)
wut suggest docker --offline

# Limit number of examples shown
wut suggest git --limit 5

# Execute selected command after selection
wut suggest git --exec
```

**Interactive Mode Features:**
- Type to search through thousands of commands
- Arrow keys to navigate
- Enter to view detailed examples
- Esc to exit

### 3. Fix Command

Automatically detect and correct typos in commands. WUT analyzes the **entire command sentence**, not just the first word, finding and fixing all misspelled tokens in a single pass.

```bash
# Fix a typo in any part of the command
wut fix "gti comit -m 'update'"
# → git commit -m 'update'

wut fix "docker buld ."
# → docker build .

wut f "doker ps"
wut f "kubectl depoly -f app.yaml"

# Check for dangerous commands
wut fix "rm -rf /"

# List common typos that WUT can fix
wut fix --list
```

**How It Works:**
WUT tokenizes the full command and runs each token through:
1. Exact dictionary lookup (highest confidence)
2. Levenshtein distance ≤ 2 fuzzy matching across all tokens
3. History-based full-sentence comparison
4. Confusable pattern detection (missing `git` prefix, etc.)

**Common Typos Detected:**
- `gti comit` → `git commit` (multi-token fix)
- `docker buld` → `docker build`
- `kubectl depoly` → `kubectl deploy`
- `cd..` → `cd ..`
- `grpe` → `grep`
- `npn isntall` → `npm install`
- And many more across git, docker, kubectl, terraform...

### 4. Explain Command

Get detailed explanations of what commands do, including warnings for dangerous operations.

```bash
# Explain a command
wut explain "git rebase -i HEAD~3"
wut x "kubectl apply -f deployment.yaml"

# Get verbose explanation with more details
wut explain "docker build -t myapp ." --verbose

# Check specifically for dangerous commands
wut explain "rm -rf /" --dangerous
```

**Explanation Includes:**
- Command summary and description
- Argument and flag explanations
- Usage examples
- Safety warnings for dangerous operations
- Alternative commands
- Helpful tips

### 5. Smart Command

Get intelligent, context-aware suggestions based on your project type and command history.

```bash
# Get suggestions for current project
wut smart
wut ?

# Search with a query
wut ? "how to find large files"
wut ? "compress folder"

# Limit number of suggestions
wut smart --limit 5

# Execute selected command immediately
wut smart --exec

# Disable typo correction
wut smart --correct=false
```

**Context Detection:**
WUT automatically detects your project type and provides relevant suggestions:
- **Go projects**: `go mod tidy`, `go test ./...`, `go build`
- **Node.js projects**: `npm install`, `npm run dev`, `npm test`
- **Docker projects**: `docker-compose up`, `docker build`
- **Git repositories**: Branch info, commit status, push/pull suggestions

### 6. History Command

Track and analyze your command usage patterns.

```bash
# View recent commands
wut history
wut h

# Show usage statistics
wut h --stats

# Search command history
wut h --search "docker"
wut h --search "git commit"

# Import commands from shell history
wut h --import-shell

# Import history from file
wut h --import history.json

# Clear history
wut h --clear

# Export history to file
wut h --export history.json
```

### 7. Alias Command

Manage command aliases for frequently used commands.

```bash
# List all aliases
wut alias
wut a

# Add a new alias
wut a --add --name gs --command "git status"
wut a --add --name dc --command "docker-compose"

# Generate smart aliases based on your project
wut a --generate

# Apply aliases to shell config
wut a --apply
```

### 8. Config Command

Manage WUT configuration settings.

```bash
# Show all configuration
wut config
wut c

# Get a specific value
wut config --get ui.theme
wut c -g fuzzy.threshold

# Set a configuration value
wut config --set ui.theme --value dark
wut c -s history.enabled --value true

# Edit configuration file in default editor
wut config --edit

# Reset to default configuration
wut config --reset

# Export configuration
wut config --export backup.yaml

# Import configuration
wut config --import backup.yaml
```

### 9. Database Command

Manage the command database for offline use.

```bash
# Download a curated set of popular commands
wut db sync
wut d sync

# Download specific commands
wut db sync git docker npm kubectl

# Download the full TLDR catalog (may take a while)
wut db sync --all

# Force update existing pages
wut db sync --force
wut db sync git --force

# Import only from a local tldr-main checkout (no network)
wut db sync --offline git

# Check database status, stale pages, and DB size
wut db status

# Update pages older than the configured sync interval (7 days by default)
wut db update
wut db update --days 3
wut db update --offline

# Clear local database and reset sync metadata
wut db clear
```

### 10. Install Command

Manage shell integration safely.

```bash
# Install for the detected active shell (asks for confirmation)
wut install

# Install for a specific shell
wut install --shell bash
wut install --shell zsh
wut install --shell fish
wut install --shell powershell

# Preview changes without modifying anything
wut install --shell bash --dry-run

# Install for all detected shells
wut install --all --yes

# Uninstall shell integration and restore the latest backup
wut install --uninstall
wut install --uninstall --shell bash
wut install --uninstall --all
```

### 11. Bookmark Command

Save and organize your favorite commands with labels and notes.

```bash
# List all bookmarks
wut bookmark
wut b

# Add a new bookmark
wut bookmark add "docker ps" --label docker
wut b add "git status" -l git -n "Check git status"

# Remove a bookmark
wut bookmark remove 1
wut b rm docker

# Search through bookmarks
wut bookmark search docker
wut b search git
```

### 12. Stats Command

View WUT usage statistics and productivity metrics.

```bash
# View usage statistics
wut stats

# Shows:
# - Total command executions
# - Top commands leaderboard
# - Time-of-day usage heatmap
# - Productivity score
```

### 13. Undo Command

Accidentally ran a command? `wut undo` looks at your recent history (or an explicit command you provide) and tells you exactly how to revert it.

```bash
# Auto-detect last command and suggest how to undo it
wut undo

# Explicitly provide the command to undo
wut undo "git add ."
wut undo "git commit"
wut undo "tar -xf archive.tar"
wut undo "systemctl start nginx"
wut undo "mkdir my-folder"
```

**Supported Undo Patterns:**

| Command | Undo Suggestion |
|---------|----------------|
| `git add .` | `git restore --staged .` |
| `git commit` | `git reset --soft HEAD~1` |
| `git push` | `git revert HEAD` |
| `git merge` | `git merge --abort` |
| `git rebase` | `git rebase --abort` |
| `tar -xf file.tar` | `tar -tf file.tar \| xargs rm -rf` |
| `mkdir dir` | `rmdir dir` |
| `touch file` | `rm file` |
| `systemctl start svc` | `sudo systemctl stop svc` |
| `npm install pkg` | `npm uninstall pkg` |
| `docker run ...` | `docker stop && docker rm` |

## Configuration

### Configuration File Location

WUT stores its configuration in:
- **Linux/macOS**: `~/.config/wut/config.yaml`
- **Windows**: `%USERPROFILE%\.config\wut\config.yaml`
- **XDG**: `$XDG_CONFIG_HOME/wut/config.yaml`

The primary WUT database is `wut.db`. The TLDR cache lives next to it as `tldr.db`.

### Available Configuration Options

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `app.name` | string | `wut` | Application name |
| `app.debug` | bool | `false` | Enable debug mode |
| `ui.theme` | string | `auto` | Theme: `auto`, `dark`, `light` |
| `ui.show_confidence` | bool | `true` | Show confidence scores |
| `ui.show_explanations` | bool | `true` | Show detailed explanations |
| `ui.syntax_highlighting` | bool | `true` | Enable syntax highlighting |
| `ui.pagination` | int | `10` | Items per page |
| `fuzzy.enabled` | bool | `true` | Enable fuzzy matching |
| `fuzzy.case_sensitive` | bool | `false` | Case-sensitive matching |
| `fuzzy.max_distance` | int | `3` | Maximum edit distance |
| `fuzzy.threshold` | float | `0.6` | Fuzzy match threshold (0-1) |
| `history.enabled` | bool | `true` | Track command history |
| `history.max_entries` | int | `10000` | Maximum history entries |
| `history.track_frequency` | bool | `true` | Track command frequency |
| `history.track_context` | bool | `true` | Track command context |
| `history.track_timing` | bool | `true` | Track command timing |
| `database.type` | string | `bbolt` | Database type |
| `database.path` | string | `~/.config/wut/wut.db` | Primary WUT database file path |
| `database.max_size` | int | `100` | Max database size (MB) |
| `database.backup_enabled` | bool | `true` | Enable backups |
| `database.backup_interval` | int | `24` | Backup interval (hours) |
| `tldr.enabled` | bool | `true` | Enable TLDR pages |
| `tldr.auto_sync` | bool | `true` | Auto-sync TLDR pages |
| `tldr.auto_sync_interval` | int | `7` | Auto-sync interval (days) |
| `tldr.offline_mode` | bool | `false` | Force offline mode |
| `tldr.auto_detect_online` | bool | `true` | Auto-detect online status |
| `tldr.max_cache_age` | int | `30` | Max cache age (days) |
| `tldr.default_platform` | string | `common` | Default platform |
| `context.enabled` | bool | `true` | Enable context analysis |
| `context.git_integration` | bool | `true` | Enable Git integration |
| `context.project_detection` | bool | `true` | Auto-detect project types |
| `context.environment_vars` | bool | `true` | Track environment variables |
| `context.directory_analysis` | bool | `true` | Analyze directories |
| `logging.level` | string | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `logging.file` | string | `~/.config/wut/logs/wut.log` | Log file path |
| `logging.max_size` | int | `10` | Max log size (MB) |
| `logging.max_backups` | int | `5` | Max log backups |
| `logging.max_age` | int | `30` | Max log age (days) |
| `privacy.local_only` | bool | `true` | Keep data local |
| `privacy.encrypt_data` | bool | `true` | Encrypt sensitive data |
| `privacy.anonymize_commands` | bool | `false` | Anonymize commands |
| `privacy.share_analytics` | bool | `false` | Share analytics |

### Example Configuration

```yaml
app:
  debug: false

ui:
  theme: dark
  show_confidence: true
  show_explanations: true
  syntax_highlighting: true
  pagination: 10

fuzzy:
  enabled: true
  case_sensitive: false
  max_distance: 3
  threshold: 0.6

history:
  enabled: true
  max_entries: 10000
  track_frequency: true
  track_context: true
  track_timing: true

logging:
  level: info
  file: ~/.config/wut/logs/wut.log
  max_size: 10
  max_backups: 5
  max_age: 30

tldr:
  enabled: true
  auto_sync: true
  auto_sync_interval: 7
  offline_mode: false
  max_cache_age: 30
  default_platform: common

context:
  enabled: true
  git_integration: true
  project_detection: true
  environment_vars: true
  directory_analysis: true

database:
  type: bbolt
  path: ~/.config/wut/wut.db
  max_size: 100
  backup_enabled: true
  backup_interval: 24

privacy:
  local_only: true
  encrypt_data: true
  anonymize_commands: false
  share_analytics: false
```

### Environment Variables

Override configuration with environment variables using the `WUT_` prefix and uppercase key names with `_` as separator:

```bash
# Set theme
export WUT_UI_THEME=dark

# Enable debug mode
export WUT_APP_DEBUG=true

# Set log level
export WUT_LOGGING_LEVEL=debug

# Set fuzzy threshold
export WUT_FUZZY_THRESHOLD=0.8
```

Note: Environment variables use the `WUT_` prefix with uppercase key names. Nested keys use `_` as separator. For example, `ui.theme` becomes `WUT_UI_THEME`.

## Advanced Usage

### Piping and Scripting

WUT can be used in scripts and pipelines:

```bash
# Get command and pipe to execution
wut suggest git --quiet | head -1 | bash

# Fix typo and view result
wut fix "gti status"

# Export history for analysis
wut history --export history.json
cat history.json | jq '.[] | select(.usage_count > 10)'
```

### Custom Workflows

Create custom workflows by combining WUT commands:

```bash
# Create a helper script
#!/bin/bash
echo "Checking project context..."
wut smart

echo "Getting test suggestions..."
wut ? "run tests"

echo "Getting build suggestions..."
wut ? "build"
```

### Integration with Other Tools

WUT works well with other command-line tools:

```bash
# Use with fzf for enhanced search
wut history | fzf

# Combine with ripgrep
wut suggest | rg "docker"

# Use with watch for monitoring
watch -n 5 'wut smart --limit 3'
```

## Troubleshooting

### Common Issues

#### Command Not Found After Installation

**Windows:**
1. Close and reopen your terminal
2. Check if WUT is in PATH:
   ```powershell
   $env:PATH -split ';' | Select-String 'WUT'
   ```
3. If not found, add manually (use the path where WUT was installed):
   ```powershell
   # For non-admin installs (default):
   [Environment]::SetEnvironmentVariable("PATH", "$env:PATH;$env:LOCALAPPDATA\WUT", "User")
   # For admin installs:
   [Environment]::SetEnvironmentVariable("PATH", "$env:PATH;$env:ProgramFiles\WUT", "Machine")
   ```

**Linux/macOS:**
1. Check if binary exists:
   ```bash
   which wut
   ```
2. If not found, ensure `/usr/local/bin` is in PATH:
   ```bash
   echo $PATH
   export PATH="/usr/local/bin:$PATH"
   ```

#### Windows SmartScreen Warning

When running the installer or downloaded binary, Windows may show a protection warning:
1. Click "More info"
2. Click "Run anyway"

This is common with new executables downloaded from the internet. The software is safe to use.

#### Permission Denied (Linux/macOS)

```bash
# Make binary executable
chmod +x /usr/local/bin/wut

# Or install with sudo
sudo mv wut /usr/local/bin/
```

#### Shell Integration Not Working

```bash
# Check what would change before installing
wut install --shell bash --dry-run

# Remove integration and restore the latest backup
wut install --uninstall --shell bash

# Reinstall shell integration
wut install --shell bash

# Reload shell configuration
source ~/.bashrc  # Bash
source ~/.zshrc   # Zsh
```

#### Database Not Found

```bash
# Download popular offline pages
wut db sync

# Check status, stale pages, and file size
wut db status

# Force re-download the full catalog
wut db clear
wut db sync --all
```

For a network-free import, place a TLDR checkout in `./tldr-main` (or `./tldr-main/tldr-main`) and run:

```bash
wut db sync --offline
```

#### Configuration Reset

If WUT behaves unexpectedly, reset configuration:

```bash
# Reset to defaults
wut config --reset

# Or manually delete config
rm -rf ~/.config/wut
wut init --quick
```

### Debug Mode

Enable debug mode for detailed logging:

```bash
# Via flag
wut --debug suggest

# Via environment variable
export WUT_APP_DEBUG=true
wut suggest

# Via configuration
wut config --set logging.level --value debug
```

### Getting Help

- **Bug Reports**: [GitHub Issues](https://github.com/thirawat27/wut/issues)
- **Feature Requests**: [GitHub Issues](https://github.com/thirawat27/wut/issues)
- **Discussions**: [GitHub Discussions](https://github.com/thirawat27/wut/discussions)
- **Security Reports**: Follow [SECURITY.md](SECURITY.md) for private disclosure



## Security and Privacy

- All processing runs locally on your machine
- Command history and local databases stay on your machine
- TLDR sync/download features fetch public documentation from upstream sources when online
- Command history stored locally in BBolt database
- Optional encryption for sensitive data
- Open source - audit the code yourself
- Security issues should be reported privately first as described in [SECURITY.md](SECURITY.md)
- For non-security diagnostics, run `wut bug-report` and review the output before sharing it

## Contributing

Contributions are welcome! Please follow these steps:

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Make your changes
4. Run tests: `make test`
5. Format code: `make fmt`
6. Commit changes: `git commit -m 'Add amazing feature'`
7. Push to branch: `git push origin feature/amazing-feature`
8. Open a Pull Request

Please ensure:
- Code follows Go best practices
- All tests pass
- Code is properly formatted
- Documentation is updated

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

## Acknowledgments

WUT is built with these excellent open-source projects:

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - Terminal UI framework
- [Cobra](https://github.com/spf13/cobra) - CLI framework
- [Viper](https://github.com/spf13/viper) - Configuration management
- [Lipgloss](https://github.com/charmbracelet/lipgloss) - Style definitions for terminal
- [BBolt](https://github.com/etcd-io/bbolt) - Embedded key/value database
- [TLDR Pages](https://tldr.sh/) - Community-driven command examples

## Support the Project

If you find WUT useful, please consider:
- ⭐ Starring the repository
- 🐛 Reporting bugs
- 💡 Suggesting features
- 📖 Improving documentation
- 🔀 Contributing code

---

Made with ❤️ by [@thirawat27](https://github.com/thirawat27)
