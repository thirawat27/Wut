# Contributing to WUT

Thank you for your interest in contributing to WUT! This document provides guidelines and instructions for contributing.

## 🚀 Getting Started

### Prerequisites
- Go 1.26.0 or higher
- Git
- Make (optional, for using Makefile commands)

### Setup Development Environment

1. Fork and clone the repository:
```bash
git clone https://github.com/yourusername/wut.git
cd wut
```

2. Install dependencies:
```bash
go mod download
```

3. Run tests to verify setup:
```bash
go test ./...
```

4. Build the project:
```bash
go build -o wut .
```

## 📝 Development Workflow

### 1. Create a Branch
```bash
git checkout -b feature/your-feature-name
# or
git checkout -b fix/your-bug-fix
```

### 2. Make Changes
- Write clean, idiomatic Go code
- Follow the existing code style
- Add tests for new functionality
- Update documentation as needed

### 3. Run Tests
```bash
# Run all tests
go test ./...

# Run static checks
go vet ./...

# Run tests with coverage
go test -cover ./...

# Run tests with race detector
go test -race ./...

# Run specific package tests
go test ./internal/db/...
```

### 4. Run Linters
```bash
# Install golangci-lint if not already installed
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Run linter
golangci-lint run
```

### 5. Commit Changes
Follow conventional commit format:
```bash
git commit -m "feat: add new feature"
git commit -m "fix: resolve bug in parser"
git commit -m "docs: update README"
git commit -m "test: add tests for fuzzy matcher"
```

Commit types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `test`: Adding or updating tests
- `refactor`: Code refactoring
- `perf`: Performance improvements
- `chore`: Maintenance tasks

### 6. Push and Create Pull Request
```bash
git push origin feature/your-feature-name
```

Then create a Pull Request on GitHub.

## 🧪 Testing Guidelines

### Writing Tests
- Place test files next to the code they test (e.g., `parser.go` → `parser_test.go`)
- Use table-driven tests when appropriate
- Aim for >80% code coverage
- Include both positive and negative test cases

Example test structure:
```go
func TestFunctionName(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
        wantErr  bool
    }{
        {"valid input", "test", "result", false},
        {"invalid input", "", "", true},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := FunctionName(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
            }
            if result != tt.expected {
                t.Errorf("got %v, want %v", result, tt.expected)
            }
        })
    }
}
```

### Benchmarks
Add benchmarks for performance-critical code:
```go
func BenchmarkFunctionName(b *testing.B) {
    for i := 0; i < b.N; i++ {
        FunctionName("test")
    }
}
```

## 📚 Code Style Guidelines

### General Principles
- Follow [Effective Go](https://golang.org/doc/effective_go.html)
- Use `gofmt` to format code
- Keep functions small and focused
- Write self-documenting code with clear names
- Add comments for exported functions and complex logic

### Package Organization
```
wut/
├── cmd/           # CLI commands
├── internal/      # Private application code
│   ├── db/        # TLDR cache and command storage
│   ├── config/    # Configuration management
│   ├── performance/ # Matchers, caches, worker helpers
│   ├── smart/     # Smart suggestion engine
│   └── ...
└── main.go        # Application entry point
```

### Error Handling
```go
// Good: Wrap errors with context
if err != nil {
    return fmt.Errorf("failed to parse command: %w", err)
}

// Bad: Lose error context
if err != nil {
    return err
}
```

### Naming Conventions
- Use camelCase for variables and functions
- Use PascalCase for exported identifiers
- Use descriptive names (avoid single letters except in loops)
- Prefix interfaces with "I" only if necessary for clarity

## 🐛 Reporting Bugs

### Before Submitting
1. Check existing issues to avoid duplicates
2. Verify the bug exists in the latest version
3. Collect relevant information (OS, Go version, error messages)
4. For user-facing CLI issues, include `wut bug-report` output after reviewing it for secrets

Security vulnerabilities should not be filed as public issues. Follow [SECURITY.md](SECURITY.md) for private reporting.

### Bug Report Template
```markdown
**Describe the bug**
A clear description of what the bug is.

**To Reproduce**
Steps to reproduce the behavior:
1. Run command '...'
2. See error

**Expected behavior**
What you expected to happen.

**Environment:**
- OS: [e.g., Windows 11, Ubuntu 22.04]
- Go version: [e.g., 1.25.0]
- WUT version: [e.g., v1.0.1]

**Additional context**
Any other relevant information.
```

## 💡 Feature Requests

We welcome feature requests! Please:
1. Check if the feature already exists or is planned
2. Clearly describe the use case
3. Explain why it would be valuable
4. Consider implementation complexity

## 📖 Documentation

### Code Documentation
- Add godoc comments for all exported functions, types, and packages
- Include examples in documentation when helpful
- Keep comments up-to-date with code changes

Example:
```go
// ParseCommand parses a shell command string into structured components.
// It handles pipes, redirections, flags, and arguments.
//
// Example:
//   parsed, err := ParseCommand("git commit -m 'message'")
//   if err != nil {
//       log.Fatal(err)
//   }
//   fmt.Println(parsed.Command) // Output: git
func ParseCommand(input string) (*ParsedCommand, error) {
    // ...
}
```

### README and Guides
- Update README.md for user-facing changes
- Add examples for new features
- Keep installation instructions current
- Update SECURITY.md when disclosure/reporting guidance changes

## 🔍 Code Review Process

### What We Look For
- ✅ Code quality and style
- ✅ Test coverage
- ✅ Documentation
- ✅ Performance implications
- ✅ Security considerations
- ✅ Backward compatibility

### Review Timeline
- Initial review: Within 2-3 days
- Follow-up reviews: Within 1-2 days
- Merge: After approval from maintainers

## 🎯 Areas for Contribution

### Good First Issues
Look for issues labeled `good first issue` - these are great for newcomers!

### High Priority Areas
- Adding more unit tests
- Improving documentation
- Performance optimizations
- Cross-platform compatibility
- New command suggestions
- UI/UX improvements

### Advanced Contributions
- AI model improvements
- New search algorithms
- Shell integration enhancements
- Plugin system development

## 📞 Getting Help

- 💬 GitHub Discussions: Ask questions and share ideas
- 🐛 GitHub Issues: Report bugs and request features
- 📧 Email: [maintainer email]

## 📜 License

By contributing, you agree that your contributions will be licensed under the same license as the project (see LICENSE file).

## 🙏 Thank You!

Your contributions make WUT better for everyone. We appreciate your time and effort!

---

**Happy Coding! 🚀**
