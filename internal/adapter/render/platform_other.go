//go:build !windows

package render

// isLegacyWindowsConsole is always false off Windows.
func isLegacyWindowsConsole() bool { return false }
