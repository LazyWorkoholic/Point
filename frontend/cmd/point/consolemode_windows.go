//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// sanitizeConsoleInput repairs the console input mode if it is in a state
// Windows cannot restore -- see consolemode.go for the whole reason.
//
// # Everything here is VERIFIED by re-reading, never by trusting a return
//
// SetConsoleMode can fail and change the mode ANYWAY. Measured, trying to
// clear LINE_INPUT while ECHO stayed set:
//
//	poisoning console: 0x01F7 -> 0x01F5 (err=The parameter is incorrect.)
//
// the call reported ERROR_INVALID_PARAMETER and the console was 0x01F5
// afterwards regardless -- a partially applied write. That is how a
// console ends up in a mode nothing can restore in the first place, so a
// repair that believed its own return value would be built on the same
// mistake. Each step below reads the mode back.
//
// Returns a one-line note when something was changed (for the log and the
// diagnostics screen), empty when there was nothing to do, and an error
// only when the console could be read but not brought into a restorable
// state. A handle that is not a console at all (stdin redirected from a
// file or a pipe, which is how the tests and CI run) is not an error:
// there is no mode to repair.
func sanitizeConsoleInput() (string, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleMode := kernel32.NewProc("GetConsoleMode")
	setConsoleMode := kernel32.NewProc("SetConsoleMode")
	handle := uintptr(syscall.Handle(os.Stdin.Fd()))

	read := func() (uint32, bool) {
		var mode uint32
		r, _, _ := getConsoleMode.Call(handle, uintptr(unsafe.Pointer(&mode)))
		return mode, r != 0
	}
	write := func(mode uint32) error {
		r, _, err := setConsoleMode.Call(handle, uintptr(mode))
		if r == 0 {
			return err
		}
		return nil
	}

	mode, ok := read()
	if !ok {
		// Not a console. Nothing to repair and nothing wrong.
		return "", nil
	}

	repaired, changed := repairConsoleInputMode(mode)
	if !changed {
		return "", nil
	}

	writeErr := write(repaired)
	// Re-read rather than trusting writeErr, in either direction: the
	// write can fail and still have applied, and (in principle) succeed
	// without landing the value asked for.
	final, ok := read()
	if !ok {
		return "", fmt.Errorf("the console input mode could not be read back after repair (set 0x%04X: %v)", repaired, writeErr)
	}
	if _, stillBroken := repairConsoleInputMode(final); stillBroken {
		return "", fmt.Errorf(
			"the console input mode is 0x%04X (ECHO without LINE_INPUT) and could not be repaired to 0x%04X: %v",
			final, repaired, writeErr)
	}
	return fmt.Sprintf(
		"repaired the console input mode (0x%04X -> 0x%04X): it had ECHO without LINE_INPUT, which "+
			"Windows refuses to restore -- left alone, the shell hatch could not open and the "+
			"terminal would have been left unreadable",
		mode, final), nil
}
