package chat

import (
	"testing"
	"time"
)

func TestInsertAndBackspaceTrackCursor(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("hello")
	if b.text() != "hello" || b.cursor != 5 {
		t.Fatalf("got %q cursor=%d", b.text(), b.cursor)
	}
	b.Backspace()
	if b.text() != "hell" || b.cursor != 4 {
		t.Fatalf("got %q cursor=%d", b.text(), b.cursor)
	}
	b.MoveLeft(false, 1000)
	b.Delete()
	if b.text() != "hel" || b.cursor != 3 {
		t.Fatalf("got %q cursor=%d", b.text(), b.cursor)
	}
}

// Done criterion: "select-all-then-type replacing the selection."
func TestSelectAllThenTypeReplacesSelection(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("old text")
	b.SelectAll()
	if !b.hasSelection() {
		t.Fatal("select-all did not select")
	}
	b.InsertText("new")
	if b.text() != "new" {
		t.Fatalf("got %q, want %q", b.text(), "new")
	}
	if b.hasSelection() {
		t.Fatal("selection should be gone after typing over it")
	}
	if b.cursor != len([]rune("new")) {
		t.Fatalf("cursor = %d, want %d", b.cursor, len([]rune("new")))
	}
}

func TestShiftMovementSelectsThenBackspaceDeletesSelection(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("hello world")
	b.MoveHome(false, 1000)
	for range "hello" {
		b.MoveRight(true, 1000) // shift+right, five times
	}
	if !b.hasSelection() {
		t.Fatal("shift+right did not start a selection")
	}
	lo, hi := b.selectionRange()
	if lo != 0 || hi != 5 {
		t.Fatalf("selection = [%d,%d), want [0,5)", lo, hi)
	}
	b.Backspace()
	if b.text() != " world" {
		t.Fatalf("got %q, want %q", b.text(), " world")
	}
}

func TestCtrlA(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("abc")
	b.MoveLeft(false, 1000)
	b.SelectAll()
	lo, hi := b.selectionRange()
	if lo != 0 || hi != 3 {
		t.Fatalf("ctrl+a selection = [%d,%d), want [0,3)", lo, hi)
	}
}

func TestWordWiseMovement(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("foo.bar  baz")
	b.cursor = len(b.runes) // end
	b.anchor = -1

	// end -> "baz" start
	b.MoveWordLeft(false)
	if b.cursor != 9 {
		t.Fatalf("cursor = %d, want 9 (start of baz)", b.cursor)
	}
	// "baz" start -> skip the double space -> "bar" start
	b.MoveWordLeft(false)
	if b.cursor != 4 {
		t.Fatalf("cursor = %d, want 4 (start of bar)", b.cursor)
	}
	// "bar" start -> the dot is its own punctuation run, stop there
	b.MoveWordLeft(false)
	if b.cursor != 3 {
		t.Fatalf("cursor = %d, want 3 (the dot)", b.cursor)
	}
	// the dot -> "foo" start
	b.MoveWordLeft(false)
	if b.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", b.cursor)
	}

	b.MoveWordRight(false)
	if b.cursor != 3 {
		t.Fatalf("cursor = %d, want 3 (end of foo)", b.cursor)
	}
}

func TestDeleteWordLeft(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("explain the runtime")
	b.DeleteWordLeft()
	if b.text() != "explain the " {
		t.Fatalf("got %q", b.text())
	}
	b.DeleteWordLeft()
	if b.text() != "explain " {
		t.Fatalf("got %q", b.text())
	}
}

func TestDeleteWordLeftDeletesSelectionInstead(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("abc def")
	b.MoveHome(false, 1000)
	b.MoveRight(true, 1000)
	b.MoveRight(true, 1000) // select "ab"
	b.DeleteWordLeft()
	if b.text() != "c def" {
		t.Fatalf("got %q, want %q", b.text(), "c def")
	}
}

// Undo/redo: consecutive plain typing coalesces into one step; a paste (or
// any editOther edit) never coalesces with anything.
func TestUndoCoalescesTypingButNotPaste(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("h")
	b.InsertText("e")
	b.InsertText("l")
	b.InsertText("l")
	b.InsertText("o")
	if b.text() != "hello" {
		t.Fatalf("got %q", b.text())
	}
	if got := len(b.undo); got != 1 {
		t.Fatalf("undo depth = %d, want 1 (five fast keystrokes coalesce)", got)
	}

	b.InsertText(" world") // multi-rune: a paste-shaped insert, its own step
	if got := len(b.undo); got != 2 {
		t.Fatalf("undo depth = %d, want 2 after a paste-shaped insert", got)
	}

	b.Undo()
	if b.text() != "hello" {
		t.Fatalf("after one undo: got %q, want %q", b.text(), "hello")
	}
	b.Undo()
	if b.text() != "" {
		t.Fatalf("after two undos: got %q, want empty", b.text())
	}
	b.Redo()
	b.Redo()
	if b.text() != "hello world" {
		t.Fatalf("after redoing both: got %q", b.text())
	}
}

func TestUndoDoesNotCoalesceAcrossAGap(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("a")
	b.InsertText("b")
	// Simulate real time passing between keystrokes — beginEdit compares
	// against wall-clock time, so this is the only way to test the gap
	// without an actual sleep in the test.
	b.lastEditAt = time.Now().Add(-2 * undoCoalesceGap)
	b.InsertText("c")
	if got := len(b.undo); got != 2 {
		t.Fatalf("undo depth = %d, want 2 (a gap starts a new group)", got)
	}
}

func TestUndoRestoresCursorPosition(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("hello")
	b.lastEditAt = time.Now().Add(-2 * undoCoalesceGap)
	b.MoveLeft(false, 1000)
	b.MoveLeft(false, 1000)
	b.InsertText("XX")
	if b.text() != "helXXlo" {
		t.Fatalf("got %q", b.text())
	}
	b.Undo()
	if b.text() != "hello" || b.cursor != 3 {
		t.Fatalf("got %q cursor=%d, want %q cursor=3", b.text(), b.cursor, "hello")
	}
}

// A CJK/emoji rune is one rune, occupying two display cells — cursor
// arithmetic here is entirely rune-index based, so this is really testing
// that runes (not bytes) are what everything above counts in.
func TestCJKAndEmojiCursorMovement(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("日本語🎉")
	if got := len(b.runes); got != 4 {
		t.Fatalf("rune count = %d, want 4", got)
	}
	b.MoveLeft(false, 1000)
	b.MoveLeft(false, 1000)
	if b.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", b.cursor)
	}
	b.Backspace()
	if b.text() != "日語🎉" {
		t.Fatalf("got %q", b.text())
	}
}
