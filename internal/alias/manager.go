// Package alias provides intelligent alias management
package alias

import (
	"bufio"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"wut/internal/context"
)

var aliasPattern = regexp.MustCompile(`^alias\s+([^=]+)=['"]?(.+?)['"]?\s*(?:#.*)?$`)

// Alias represents a shell alias
type Alias struct {
	Name        string
	Command     string
	Description string
	Category    string
	AutoGen     bool // Whether this was auto-generated
	UsageCount  int
}

// Manager manages shell aliases
type Manager struct {
	aliases   map[string]*Alias
	shell     string
	configDir string
}

// NewManager creates a new alias manager
func NewManager(shell string) *Manager {
	home, _ := os.UserHomeDir()
	return &Manager{
		aliases:   make(map[string]*Alias),
		shell:     shell,
		configDir: filepath.Join(home, ".config", "wut"),
	}
}

// Load loads existing aliases from shell config
func (m *Manager) Load() error {
	// Load from shell config
	shellAliases, err := m.loadFromShell()
	if err == nil {
		for name, cmd := range shellAliases {
			m.aliases[name] = &Alias{
				Name:     name,
				Command:  cmd,
				Category: "shell",
			}
		}
	}

	// Load from wut aliases
	wutAliases, _ := m.loadFromWut()
	maps.Copy(m.aliases, wutAliases)

	return nil
}

// Get returns an alias by name
func (m *Manager) Get(name string) (*Alias, bool) {
	alias, ok := m.aliases[name]
	return alias, ok
}

// GetAll returns all aliases
func (m *Manager) GetAll() map[string]*Alias {
	return m.aliases
}

// GetByCategory returns aliases by category
func (m *Manager) GetByCategory(category string) []*Alias {
	var result []*Alias
	for _, alias := range m.aliases {
		if alias.Category == category {
			result = append(result, alias)
		}
	}
	return result
}

// Add adds a new alias
func (m *Manager) Add(name, command, description, category string) error {
	// Validate alias name
	if !m.isValidName(name) {
		return fmt.Errorf("invalid alias name: %s", name)
	}

	alias := &Alias{
		Name:        name,
		Command:     command,
		Description: description,
		Category:    category,
		AutoGen:     false,
	}

	m.aliases[name] = alias
	return m.save()
}

// Remove removes an alias
func (m *Manager) Remove(name string) error {
	delete(m.aliases, name)
	return m.save()
}

// GenerateSmartAliases generates aliases based on project context
func (m *Manager) GenerateSmartAliases(ctx *context.Context) []Alias {
	var suggestions []Alias

	switch ctx.ProjectType {
	case "go":
		suggestions = append(suggestions, m.generateGoAliases()...)
	case "nodejs":
		suggestions = append(suggestions, m.generateNodeAliases()...)
	case "docker":
		suggestions = append(suggestions, m.generateDockerAliases()...)
	case "git":
		suggestions = append(suggestions, m.generateGitAliases()...)
	}

	// Filter out existing aliases
	var newAliases []Alias
	for _, alias := range suggestions {
		if _, exists := m.aliases[alias.Name]; !exists {
			newAliases = append(newAliases, alias)
		}
	}

	return newAliases
}

// generateGoAliases generates Go-specific aliases
func (m *Manager) generateGoAliases() []Alias {
	return []Alias{
		{Name: "gb", Command: "go build", Description: "Build Go project", Category: "go", AutoGen: true},
		{Name: "gt", Command: "go test ./...", Description: "Run all tests", Category: "go", AutoGen: true},
		{Name: "gtv", Command: "go test -v ./...", Description: "Run tests verbose", Category: "go", AutoGen: true},
		{Name: "gr", Command: "go run .", Description: "Run current package", Category: "go", AutoGen: true},
		{Name: "gmod", Command: "go mod tidy", Description: "Tidy modules", Category: "go", AutoGen: true},
		{Name: "gf", Command: "go fmt ./...", Description: "Format all files", Category: "go", AutoGen: true},
		{Name: "gget", Command: "go get -u ./...", Description: "Update dependencies", Category: "go", AutoGen: true},
	}
}

// generateNodeAliases generates Node.js-specific aliases
func (m *Manager) generateNodeAliases() []Alias {
	aliases := []Alias{
		{Name: "ni", Command: "npm install", Description: "Install dependencies", Category: "node", AutoGen: true},
		{Name: "nid", Command: "npm install -D", Description: "Install dev dependency", Category: "node", AutoGen: true},
		{Name: "nr", Command: "npm run", Description: "Run npm script", Category: "node", AutoGen: true},
		{Name: "ns", Command: "npm start", Description: "Start application", Category: "node", AutoGen: true},
		{Name: "nt", Command: "npm test", Description: "Run tests", Category: "node", AutoGen: true},
		{Name: "nb", Command: "npm run build", Description: "Build project", Category: "node", AutoGen: true},
		{Name: "nup", Command: "npm update", Description: "Update packages", Category: "node", AutoGen: true},
		{Name: "nout", Command: "npm outdated", Description: "Check outdated packages", Category: "node", AutoGen: true},
	}

	return aliases
}

// generateDockerAliases generates Docker-specific aliases
func (m *Manager) generateDockerAliases() []Alias {
	return []Alias{
		{Name: "d", Command: "docker", Description: "Docker command", Category: "docker", AutoGen: true},
		{Name: "dc", Command: "docker-compose", Description: "Docker Compose", Category: "docker", AutoGen: true},
		{Name: "dcu", Command: "docker-compose up -d", Description: "Start services", Category: "docker", AutoGen: true},
		{Name: "dcd", Command: "docker-compose down", Description: "Stop services", Category: "docker", AutoGen: true},
		{Name: "dcl", Command: "docker-compose logs -f", Description: "Follow logs", Category: "docker", AutoGen: true},
		{Name: "dps", Command: "docker ps", Description: "List containers", Category: "docker", AutoGen: true},
		{Name: "dimg", Command: "docker images", Description: "List images", Category: "docker", AutoGen: true},
		{Name: "dr", Command: "docker run --rm -it", Description: "Run interactive container", Category: "docker", AutoGen: true},
	}
}

// generateGitAliases generates Git-specific aliases
func (m *Manager) generateGitAliases() []Alias {
	return []Alias{
		{Name: "g", Command: "git", Description: "Git command", Category: "git", AutoGen: true},
		{Name: "gst", Command: "git status", Description: "Git status", Category: "git", AutoGen: true},
		{Name: "ga", Command: "git add", Description: "Git add", Category: "git", AutoGen: true},
		{Name: "gaa", Command: "git add --all", Description: "Git add all", Category: "git", AutoGen: true},
		{Name: "gc", Command: "git commit -m", Description: "Git commit", Category: "git", AutoGen: true},
		{Name: "gcam", Command: "git commit -am", Description: "Git commit all with message", Category: "git", AutoGen: true},
		{Name: "gp", Command: "git push", Description: "Git push", Category: "git", AutoGen: true},
		{Name: "gpl", Command: "git pull", Description: "Git pull", Category: "git", AutoGen: true},
		{Name: "gl", Command: "git log --oneline", Description: "Git log oneline", Category: "git", AutoGen: true},
		{Name: "gco", Command: "git checkout", Description: "Git checkout", Category: "git", AutoGen: true},
		{Name: "gcb", Command: "git checkout -b", Description: "Git checkout new branch", Category: "git", AutoGen: true},
		{Name: "gb", Command: "git branch", Description: "Git branch", Category: "git", AutoGen: true},
		{Name: "gd", Command: "git diff", Description: "Git diff", Category: "git", AutoGen: true},
		{Name: "gds", Command: "git diff --staged", Description: "Git diff staged", Category: "git", AutoGen: true},
		{Name: "grb", Command: "git rebase", Description: "Git rebase", Category: "git", AutoGen: true},
		{Name: "grbi", Command: "git rebase -i", Description: "Git interactive rebase", Category: "git", AutoGen: true},
		{Name: "gstp", Command: "git stash push", Description: "Git stash", Category: "git", AutoGen: true},
		{Name: "gstl", Command: "git stash pop", Description: "Git stash pop", Category: "git", AutoGen: true},
	}
}

// SuggestAlias suggests an alias for a frequently used command
func (m *Manager) SuggestAlias(command string, frequency int) *Alias {
	// Only suggest for commands used frequently
	if frequency < 5 {
		return nil
	}

	// Parse command
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil
	}

	// Generate alias name from command
	name := m.generateAliasName(parts)
	if name == "" {
		return nil
	}

	// Check if alias already exists
	if _, exists := m.aliases[name]; exists {
		return nil
	}

	return &Alias{
		Name:        name,
		Command:     command,
		Description: fmt.Sprintf("Auto-generated for: %s", command),
		Category:    "autogen",
		AutoGen:     true,
		UsageCount:  frequency,
	}
}

// generateAliasName generates a short alias name from command parts
func (m *Manager) generateAliasName(parts []string) string {
	if len(parts) == 0 {
		return ""
	}

	// Simple heuristic: first letter of each word
	var name strings.Builder
	for i, part := range parts {
		if i >= 3 {
			break // Max 3 characters
		}
		if len(part) > 0 {
			name.WriteByte(part[0])
		}
	}

	return name.String()
}

// ApplyToShell applies aliases to shell configuration. It creates a timestamped
// backup of the shell config before modifying it.
func (m *Manager) ApplyToShell() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	var configFile string
	switch m.shell {
	case "bash":
		configFile = filepath.Join(homeDir, ".bashrc")
	case "zsh":
		configFile = filepath.Join(homeDir, ".zshrc")
	case "fish":
		configFile = filepath.Join(homeDir, ".config", "fish", "config.fish")
	default:
		return "", fmt.Errorf("unsupported shell: %s", m.shell)
	}

	// Read existing config
	content, err := os.ReadFile(configFile)
	if err != nil {
		return "", err
	}

	backupPath, err := backupShellConfig(configFile, content)
	if err != nil {
		return "", fmt.Errorf("failed to back up shell config: %w", err)
	}

	// Check if wut aliases section exists
	marker := "# WUT Aliases - Auto-generated\n"
	markerEnd := "# End WUT Aliases\n"

	strContent := string(content)
	startIdx := strings.Index(strContent, marker)
	endIdx := strings.Index(strContent, markerEnd)

	// Build aliases section
	var aliasesSection strings.Builder
	aliasesSection.WriteString(marker)

	// Get sorted list of alias names
	var names []string
	for name := range m.aliases {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		alias := m.aliases[name]
		if alias.AutoGen || alias.Category == "shell" {
			continue // Skip auto-generated for now
		}
		switch m.shell {
		case "bash", "zsh":
			aliasesSection.WriteString(fmt.Sprintf("alias %s='%s' # %s\n", alias.Name, alias.Command, alias.Description))
		case "fish":
			aliasesSection.WriteString(fmt.Sprintf("alias %s '%s' # %s\n", alias.Name, alias.Command, alias.Description))
		}
	}
	aliasesSection.WriteString(markerEnd)

	// Replace or append
	var newContent string
	if startIdx >= 0 && endIdx > startIdx {
		// Replace existing section
		newContent = strContent[:startIdx] + aliasesSection.String() + strContent[endIdx+len(markerEnd):]
	} else {
		// Append to end
		newContent = strContent + "\n" + aliasesSection.String()
	}

	if err := os.WriteFile(configFile, []byte(newContent), 0644); err != nil {
		return "", err
	}
	return backupPath, nil
}

// backupShellConfig writes a timestamped copy of path next to it.
func backupShellConfig(path string, content []byte) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty config path")
	}
	if len(content) == 0 {
		return "", nil
	}

	base := filepath.Base(path)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	timestamp := time.Now().Format("20060102-150405")
	backupPath := filepath.Join(filepath.Dir(path), fmt.Sprintf("%s-%s-backup%s", name, timestamp, ext))

	if err := os.WriteFile(backupPath, content, 0600); err != nil {
		return "", err
	}
	return backupPath, nil
}

// isValidName checks if an alias name is valid
func (m *Manager) isValidName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// loadFromShell loads aliases from shell configuration
func (m *Manager) loadFromShell() (map[string]string, error) {
	aliases := make(map[string]string)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return aliases, err
	}

	var configFile string
	switch m.shell {
	case "bash":
		configFile = filepath.Join(homeDir, ".bashrc")
	case "zsh":
		configFile = filepath.Join(homeDir, ".zshrc")
	case "fish":
		configFile = filepath.Join(homeDir, ".config", "fish", "config.fish")
	default:
		return aliases, nil
	}

	file, err := os.Open(configFile)
	if err != nil {
		return aliases, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		if matches := aliasPattern.FindStringSubmatch(line); matches != nil {
			name := strings.TrimSpace(matches[1])
			command := strings.TrimSpace(matches[2])
			aliases[name] = command
		}
	}

	return aliases, scanner.Err()
}

// loadFromWut loads wut-specific aliases
func (m *Manager) loadFromWut() (map[string]*Alias, error) {
	aliases := make(map[string]*Alias)

	aliasFile := filepath.Join(m.configDir, "aliases.json")
	data, err := os.ReadFile(aliasFile)
	if err != nil {
		return aliases, err
	}

	// Simple parsing (in real implementation, use JSON)
	_ = data
	return aliases, nil
}

// save saves aliases to disk
func (m *Manager) save() error {
	if err := os.MkdirAll(m.configDir, 0755); err != nil {
		return err
	}

	// Save to wut aliases file
	aliasFile := filepath.Join(m.configDir, "aliases.json")

	// Build JSON manually for now
	var sb strings.Builder
	sb.WriteString("{\n")

	// Get sorted list of names
	var names []string
	for name := range m.aliases {
		names = append(names, name)
	}
	sort.Strings(names)

	first := true
	for _, name := range names {
		alias := m.aliases[name]
		if !first {
			sb.WriteString(",\n")
		}
		first = false
		sb.WriteString(fmt.Sprintf(`  "%s": {
    "command": "%s",
    "description": "%s",
    "category": "%s",
    "auto_gen": %t
  }`, alias.Name, alias.Command, alias.Description, alias.Category, alias.AutoGen))
	}
	sb.WriteString("\n}\n")

	return os.WriteFile(aliasFile, []byte(sb.String()), 0644)
}

// GetPopularAliases returns commonly useful aliases
func GetPopularAliases() []Alias {
	return []Alias{
		{Name: "..", Command: "cd ..", Description: "Go up one directory", Category: "nav"},
		{Name: "...", Command: "cd ../..", Description: "Go up two directories", Category: "nav"},
		{Name: "~", Command: "cd ~", Description: "Go home", Category: "nav"},
		{Name: "l", Command: "ls -CF", Description: "List files", Category: "nav"},
		{Name: "la", Command: "ls -A", Description: "List all files", Category: "nav"},
		{Name: "ll", Command: "ls -alF", Description: "List files in long format", Category: "nav"},
		{Name: "mkdirp", Command: "mkdir -p", Description: "Create directory recursively", Category: "nav"},
		{Name: "c", Command: "clear", Description: "Clear screen", Category: "util"},
		{Name: "h", Command: "history", Description: "Show history", Category: "util"},
		{Name: "s", Command: "sudo", Description: "Sudo command", Category: "util"},
		{Name: "grep", Command: "grep --color=auto", Description: "Grep with color", Category: "util"},
	}
}
