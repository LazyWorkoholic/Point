//go:build !windows

package main

// sanitizeConsoleInput is a no-op away from Windows: the mode combination
// it repairs is a Win32 console concept, and termios has no equivalent
// failure. Present so main.go needs no build tag of its own.
func sanitizeConsoleInput() (string, error) { return "", nil }
