//go:build ignore

// Build script for WUT
// Usage: go run build.go [flags]
//
// Examples:
//   go run build.go                    # Build to build/windows/wut.exe
//   go run build.go -o custom.exe      # Build to custom path
//   go run build.go -v                 # Build with version info
//   go run build.go -run               # Build and run immediately
//   go run build.go -install           # Build and install to system

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	version   = "1.0.1"
	buildTime = time.Now().Format("2006-01-02_15:04:05")
	commit    = "unknown"
)

func main() {
	// Parse flags
	output := ""
	shouldRun := false
	shouldInstall := false
	verbose := false
	ldflags := ""

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]

		// Stop parsing flags after --
		if arg == "--" {
			break
		}

		switch {
		case arg == "-h" || arg == "--help":
			printHelp()
			return
		case arg == "-v" || arg == "--version":
			verbose = true
		case arg == "-r" || arg == "-run" || arg == "--run":
			shouldRun = true
		case arg == "-i" || arg == "-install" || arg == "--install":
			shouldInstall = true
		case strings.HasPrefix(arg, "-o="):
			output = strings.TrimPrefix(arg, "-o=")
		case arg == "-o":
			if i+1 < len(os.Args) {
				output = os.Args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "-ldflags="):
			ldflags = strings.TrimPrefix(arg, "-ldflags=")
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", arg)
				os.Exit(1)
			}
		}
	}

	// Get git info
	getGitInfo()

	// Determine output path
	if output == "" {
		output = getDefaultOutput()
	}

	// Ensure output directory exists
	outputDir := filepath.Dir(output)
	if outputDir != "" && outputDir != "." {
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory: %v\n", err)
			os.Exit(1)
		}
	}

	// Build ldflags
	if ldflags == "" {
		ldflags = fmt.Sprintf(`-s -w -X main.Version=%s -X main.BuildTime=%s -X main.Commit=%s`,
			version, buildTime, commit)
	}

	if verbose {
		fmt.Printf("Building WUT...\n")
		fmt.Printf("  Version:   %s\n", version)
		fmt.Printf("  BuildTime: %s\n", buildTime)
		fmt.Printf("  Commit:    %s\n", commit)
		fmt.Printf("  Output:    %s\n", output)
		fmt.Printf("  LDFLAGS:   %s\n", ldflags)
		fmt.Println()
	}

	// Build command
	args := []string{"build", "-ldflags", ldflags, "-o", output, "."}
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Build failed: %v\n", err)
		os.Exit(1)
	}

	if verbose {
		fmt.Printf("\n✓ Build complete: %s\n", output)
	}

	// Run if requested
	if shouldRun {
		if verbose {
			fmt.Printf("\nRunning: %s\n", output)
		}

		// Collect args after -- to pass to the binary
		var runArgs []string
		doubleDashFound := false
		for _, arg := range os.Args[1:] {
			if doubleDashFound {
				runArgs = append(runArgs, arg)
			} else if arg == "--" {
				doubleDashFound = true
			}
		}

		runCmd := exec.Command(output, runArgs...)
		runCmd.Stdout = os.Stdout
		runCmd.Stderr = os.Stderr
		runCmd.Stdin = os.Stdin
		if err := runCmd.Run(); err != nil {
			os.Exit(1)
		}
		return
	}

	// Install if requested
	if shouldInstall {
		installPath := getInstallPath()
		if verbose {
			fmt.Printf("\nInstalling to: %s\n", installPath)
		}

		// Copy file
		src, err := os.ReadFile(output)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading binary: %v\n", err)
			os.Exit(1)
		}

		if err := os.WriteFile(installPath, src, 0755); err != nil {
			// Try with sudo on Unix
			if runtime.GOOS != "windows" {
				cpCmd := exec.Command("sudo", "cp", output, installPath)
				cpCmd.Stdout = os.Stdout
				cpCmd.Stderr = os.Stderr
				if err := cpCmd.Run(); err != nil {
					fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
					os.Exit(1)
				}
			} else {
				fmt.Fprintf(os.Stderr, "Install failed (try running as admin): %v\n", err)
				os.Exit(1)
			}
		}

		if verbose {
			fmt.Printf("✓ Installed to: %s\n", installPath)
		}
	}
}

func getDefaultOutput() string {
	// Windows: build/windows/wut.exe
	// Unix: build/wut
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}

	subdir := ""
	if runtime.GOOS == "windows" {
		subdir = "windows"
	} else if runtime.GOOS == "darwin" {
		subdir = "darwin"
	} else if runtime.GOOS == "linux" {
		subdir = "linux"
	}

	if subdir != "" {
		return filepath.Join("build", subdir, "wut"+ext)
	}
	return filepath.Join("build", "wut"+ext)
}

func getInstallPath() string {
	if runtime.GOOS == "windows" {
		// Try Program Files first, fallback to LocalAppData
		programFiles := os.Getenv("ProgramFiles")
		if programFiles != "" {
			return filepath.Join(programFiles, "WUT", "wut.exe")
		}
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "WUT", "wut.exe")
	}

	// Unix: prefer /usr/local/bin, fallback to ~/.local/bin
	if _, err := os.Stat("/usr/local/bin"); err == nil {
		return "/usr/local/bin/wut"
	}
	return filepath.Join(os.Getenv("HOME"), ".local", "bin", "wut")
}

func getGitInfo() {
	// Version is hardcoded - edit this when releasing new version
	version = "v1.0.1"

	// Try to get commit hash
	if out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); err == nil {
		commit = strings.TrimSpace(string(out))
	}
}

func printHelp() {
	fmt.Println(`Build script for WUT

Usage: go run build.go [flags]

Flags:
  -o PATH         Output path (default: auto-detect based on OS)
  -v, --version   Show version info during build
  -r, -run        Build and run immediately
  -i, -install    Build and install to system
  -h, --help      Show this help

Examples:
  go run build.go                    # Build to build/windows/wut.exe
  go run build.go -v                 # Build with verbose output
  go run build.go -o myapp.exe       # Build to custom path
  go run build.go -run               # Build and run
  go run build.go -install           # Build and install

Auto-detected output paths:
  Windows:  build/windows/wut.exe
  macOS:    build/darwin/wut
  Linux:    build/linux/wut
  Others:   build/wut`)
}
