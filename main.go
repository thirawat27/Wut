// WUT - Command Helper
// Main entry point for the application
package main

import (
	"os"

	"wut/cmd"
)

var (
	// Version is set during build via ldflags
	Version = "1.0.1"
	// BuildTime is set during build via ldflags
	BuildTime = "unknown"
	// Commit is set during build via ldflags
	Commit = "unknown"
)

func main() {
	// Set version info in cmd package
	cmd.Version = Version
	cmd.BuildTime = BuildTime
	cmd.Commit = Commit
	cmd.SetVersionInfo()

	// cmd.Execute reports the exit code rather than terminating itself, so its
	// deferred cleanup runs before the process ends.
	os.Exit(cmd.Execute())
}
