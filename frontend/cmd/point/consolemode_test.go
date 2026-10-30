package main

import "testing"

// The rule that decides whether a console mode can be restored at all.
// Pure, so it runs without a console on any OS -- which matters, because
// the bug it encodes was only ever visible with a real console attached.
func TestRepairConsoleInputMode(t *testing.T) {
	cases := []struct {
		name        string
		in          uint32
		wantOut     uint32
		wantChanged bool
	}{
		{
			// The measured poisoned console: 0x01E4 is ECHO + INSERT +
			// QUICK_EDIT + EXTENDED_FLAGS + AUTO_POSITION, with no
			// LINE_INPUT. SetConsoleMode refuses it, which is what makes
			// tea.ExecProcess fail before it starts the shell.
			name:        "the poisoned console from the field",
			in:          0x01E4,
			wantOut:     0x01E4 | enableLineInput,
			wantChanged: true,
		},
		{
			name:        "echo with line input is already restorable",
			in:          enableProcessedInput | enableLineInput | enableEchoInput,
			wantOut:     enableProcessedInput | enableLineInput | enableEchoInput,
			wantChanged: false,
		},
		{
			// Raw mode: no ECHO, so no invalid combination. Bubble Tea
			// sets exactly this itself and it must be left alone.
			name:        "raw mode is left untouched",
			in:          enableVirtualTerminalInput,
			wantOut:     enableVirtualTerminalInput,
			wantChanged: false,
		},
		{
			name:        "zero is left untouched",
			in:          0,
			wantOut:     0,
			wantChanged: false,
		},
	}
	for _, c := range cases {
		out, changed := repairConsoleInputMode(c.in)
		if out != c.wantOut || changed != c.wantChanged {
			t.Errorf("%s: repair(0x%04X) = 0x%04X,%v; want 0x%04X,%v",
				c.name, c.in, out, changed, c.wantOut, c.wantChanged)
		}
		// Whatever comes out must itself be restorable, or the repair
		// has only moved the problem.
		if out&enableEchoInput != 0 && out&enableLineInput == 0 {
			t.Errorf("%s: repair produced 0x%04X, still ECHO without LINE_INPUT", c.name, out)
		}
	}
}

// Repairing must be idempotent: a second launch in a console this already
// fixed has nothing to do.
func TestRepairIsIdempotent(t *testing.T) {
	once, changed := repairConsoleInputMode(0x01E4)
	if !changed {
		t.Fatal("the first repair changed nothing")
	}
	twice, changedAgain := repairConsoleInputMode(once)
	if changedAgain || twice != once {
		t.Errorf("second repair changed 0x%04X -> 0x%04X (changed=%v)", once, twice, changedAgain)
	}
}

// sanitizeConsoleInput must not fail when stdin is not a console, which is
// exactly how it runs under `go test` and in CI.
func TestSanitizeWithoutAConsoleIsNotAnError(t *testing.T) {
	note, err := sanitizeConsoleInput()
	if err != nil {
		t.Errorf("sanitizeConsoleInput() errored with no console attached: %v", err)
	}
	_ = note
}
