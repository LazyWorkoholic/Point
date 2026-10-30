package main

// Windows console input-mode repair, and the reason it has to exist.
//
// # The failure this prevents
//
// A console whose input mode has ENABLE_ECHO_INPUT set WITHOUT
// ENABLE_LINE_INPUT is a mode Windows will not accept back: SetConsoleMode
// refuses that combination with ERROR_INVALID_PARAMETER ("The parameter is
// incorrect"). Measured directly, in a console that had been left that way:
//
//	console input mode = 0x01E4
//	  set: ENABLE_ECHO_INPUT
//	  set: ENABLE_INSERT_MODE
//	  set: ENABLE_QUICK_EDIT_MODE
//	  set: ENABLE_EXTENDED_FLAGS
//	  set: ENABLE_AUTO_POSITION
//	!! ECHO_INPUT without LINE_INPUT -- SetConsoleMode refuses this combination
//	!! SetConsoleMode(0x01E4) FAILED: The parameter is incorrect.
//
// A console gets into that state when a program that put it in raw mode
// (raw clears PROCESSED|LINE|ECHO, which Point itself does through Bubble
// Tea) is killed before it can restore the mode, and something afterwards
// re-sets ECHO on its own. Point's own Tier 0 `ctrl+c` (taskkill /T /F on a
// console-owning child) and any hard kill of Point itself both produce it.
//
// # Why it is fatal rather than cosmetic
//
// Bubble Tea saves the console mode at startup and restores it inside
// ReleaseTerminal, which is the FIRST thing tea.ExecProcess does. When that
// restore fails, bubbletea's exec returns early -- before launching the
// child -- and it never calls RestoreTerminal, because that is only reached
// after a successful release. The program is left with its input reader
// cancelled, its renderer stopped and the alt screen already exited: alive,
// invisible, and unable to receive a single further keystroke, which is
// indistinguishable from having crashed. Reproduced in an isolated program
// using Point's own Bubble Tea options:
//
//	about to return tea.ExecProcess for: [powershell -NoLogo]
//	ExecProcess callback fired: err=error restoring console: The parameter is incorrect.
//
// 86ms, and no shell ever started. The same binary in a FRESH console
// launched the shell and came back normally, which is why this reproduced
// "every time" for one terminal tab and never in a new one -- the state is
// the console's, not the profile's, so a clean scratch profile cannot help.
//
// # What this does about it
//
// Repairs the mode before Bubble Tea ever reads it, so the value bubbletea
// saves is one Windows will accept back. That is the whole fix: it makes
// the hatch work in a poisoned console AND stops Point from being the thing
// that leaves the next program a poisoned console.

// Console input-mode flags (wincon.h). Declared here rather than taken from
// a dependency: x/sys/windows is not currently a direct dependency of this
// module and CLAUDE.md asks for a reason before adding one -- five
// constants and one DLL call did not justify it.
const (
	enableProcessedInput       = 0x0001
	enableLineInput            = 0x0002
	enableEchoInput            = 0x0004
	enableVirtualTerminalInput = 0x0200
)

// repairConsoleInputMode returns the mode to set, and whether it differs
// from what was passed in.
//
// ONE rule, and deliberately only one: ECHO without LINE_INPUT is the
// combination SetConsoleMode rejects, so LINE_INPUT is added back. Nothing
// else is normalised -- QuickEdit, insert mode, VT input and the rest are
// the user's console preferences, and a launcher that "tidied" them would
// be changing settings it was never asked to change. Bubble Tea puts the
// console into raw mode a moment later regardless; all this has to produce
// is a value that can be restored afterwards.
//
// Pure, and in a platform-independent file, so the rule itself is testable
// without a console on any OS.
func repairConsoleInputMode(mode uint32) (uint32, bool) {
	if mode&enableEchoInput != 0 && mode&enableLineInput == 0 {
		return mode | enableLineInput, true
	}
	return mode, false
}
