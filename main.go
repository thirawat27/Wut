// WUT - Command Helper
// Main entry point for the application
package main

import (
	"fmt"
	"os"
	"runtime"

	"wut/cmd"
)

var (
	// Version is set during build via ldflags
	Version = "1.0.1"
	// BuildTime is set during build via ldflags
	BuildTime = "unknown"
	// Commit is set during build via ldflags
	Commit = "unknown"
	// GoVersion is the Go version used to build
	GoVersion = runtime.Version()
)

func main() {
	// Set version info in cmd package
	cmd.Version = Version
	cmd.BuildTime = BuildTime
	cmd.Commit = Commit
	cmd.SetVersionInfo()

	// Run the application
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Execute root command
	cmd.Execute()
	return nil
}
