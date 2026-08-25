//go:build windows

package render

import "os"

// isLegacyWindowsConsole reports a console that cannot be trusted with
// box-drawing characters.
//
// Windows Terminal, VS Code, and any ConEmu-family host set an identifying
// variable and handle Unicode correctly. Bare conhost on a legacy code page
// does not, and renders "·" as mojibake, so ASCII is used there instead.
func isLegacyWindowsConsole() bool {
	for _, key := range []string{"WT_SESSION", "TERM_PROGRAM", "ConEmuANSI", "ANSICON"} {
		if os.Getenv(key) != "" {
			return false
		}
	}
	// A TERM value at all means an emulator that speaks ANSI (Git Bash, MSYS).
	return os.Getenv("TERM") == ""
}
