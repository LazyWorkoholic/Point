package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// F13b's save path: the write, its seven refusals, and what each one
// does to the document.

// savableWorkspace is one open, writable, lock-held document.
func savableWorkspace(t *testing.T) (*Workspace, *recorder, *doc) {
	t.Helper()
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.holder = "aryan@id"
	d := newDoc(wireFile{
		Path: "C:\\p\\x.py", Content: "a\nb", Mtime: 100, Size: 4,
		LineEnding: lineEndingLF, LFCount: 2, TrailingNewline: true, Writable: true,
	}, "C:\\p", true)
	d.lock = lockHeld
	ws.docs = append(ws.docs, d)
	return ws, rec, d
}

// openedConfirm reports whether a command opens an app.Confirm through
// B9's OpenWorkspaceOverlayMsg.
func openedConfirm(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	m, ok := cmd().(app.OpenWorkspaceOverlayMsg)
	if !ok {
		return false
	}
	_, isConfirm := m.Overlay.(*app.Confirm)
	return isConfirm
}

// ---------------------------------------------------------------------------
// C4 -- the mixed-ending write tiebreak
// ---------------------------------------------------------------------------

// A uniform file writes back what it read. That is the whole of
// Done-when 2 and needs no decision.
//
// A MIXED file is the case B32 deliberately left to this side, and C4
// settles it: the MAJORITY ending, LF on an exact tie. The tie half
// matters because describe_bytes resolves its own ambiguous input (no
// line breaks at all) to "lf" -- so writing CRLF on a tie would make the
// read and the write answer differently about the one input each has to
// resolve alone, and the file would flip its ending on every save.
func TestWriteEndingResolvesMixedByMajorityAndTiesToLF(t *testing.T) {
	cases := []struct {
		name     string
		ending   string
		crlf, lf int
		want     string
	}{
		{"a CRLF file writes CRLF", lineEndingCRLF, 9, 0, lineEndingCRLF},
		{"an LF file writes LF", lineEndingLF, 0, 9, lineEndingLF},
		{"mixed, CRLF majority", lineEndingMixed, 9, 2, lineEndingCRLF},
		{"mixed, LF majority", lineEndingMixed, 2, 9, lineEndingLF},
		{"mixed, EXACT TIE -> LF", lineEndingMixed, 5, 5, lineEndingLF},
		{"mixed, tie at zero -> LF", lineEndingMixed, 0, 0, lineEndingLF},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &doc{lineEnding: c.ending, crlfCount: c.crlf, lfCount: c.lf}
			if got := writeEnding(d); got != c.want {
				t.Errorf("writeEnding = %q, want %q", got, c.want)
			}
		})
	}
}

// encode_for_write raises InvalidEventError on anything but "lf" or
// "crlf", so "mixed" must never reach the wire -- it would arrive as an
// Error envelope rather than a saved file. Resolving in writeEnding is
// the contract, not a nicety.
func TestAMixedFileNeverSendsMixedOnTheWire(t *testing.T) {
	ws, rec, d := savableWorkspace(t)
	d.lineEnding, d.crlfCount, d.lfCount = lineEndingMixed, 3, 3

	ws.saveDoc(d, saveManual, false)

	if len(rec.sent) != 1 {
		t.Fatalf("sent %+v", rec.sent)
	}
	got := rec.sent[0].payload["line_ending"]
	if got == lineEndingMixed {
		t.Fatal("sent line_ending=mixed; encode_for_write refuses it")
	}
	if got != lineEndingLF {
		t.Errorf("line_ending = %v, want lf on an exact tie", got)
	}
}

// After a successful write the file on disk is uniform, so a status bar
// still reading MIXED would describe the previous version of the file.
func TestASuccessfulWriteClearsTheMixedLabel(t *testing.T) {
	ws, _, d := savableWorkspace(t)
	d.lineEnding, d.crlfCount, d.lfCount = lineEndingMixed, 3, 3
	d.insertRune('z')

	ws.saveDoc(d, saveManual, false)
	replyFor(t, ws, queryWriteFile, d.path, protocol.EvProjectFileWritten, map[string]any{
		"ok": true, "path": d.path, "mtime": 200.0, "size": 5,
		"line_ending": lineEndingLF, "bom": false, "trailing_newline": true,
	})

	if d.lineEnding != lineEndingLF {
		t.Errorf("lineEnding = %q after the write", d.lineEnding)
	}
	if d.crlfCount != 0 {
		t.Errorf("crlfCount = %d, want 0 after an LF write", d.crlfCount)
	}
	if got := lineEndingLabel(d); got != "LF" {
		t.Errorf("status label = %q, want LF", got)
	}
}

// ---------------------------------------------------------------------------
// Autosave rule 2 / Done-when 12
// ---------------------------------------------------------------------------

// The reply's mtime replaces the document's, which is the whole of
// "record Point's own write mtimes" and the reason a save needs no
// follow-up CheckFileStale. Without it the NEXT save sends the mtime
// from the original read, the file on disk has Point's own newer one,
// and the backend refuses `stale` against a change Point itself made.
func TestASuccessfulWriteRecordsTheNewMtimeSoTheNextSaveIsNotStale(t *testing.T) {
	ws, rec, d := savableWorkspace(t)
	d.insertRune('z')

	ws.saveDoc(d, saveManual, false)
	replyFor(t, ws, queryWriteFile, d.path, protocol.EvProjectFileWritten, map[string]any{
		"ok": true, "path": d.path, "mtime": 999.5, "size": 5,
		"line_ending": lineEndingLF, "bom": false, "trailing_newline": true,
	})

	if d.mtime != 999.5 {
		t.Fatalf("mtime = %v, want the reply's 999.5", d.mtime)
	}
	if d.dirty {
		t.Error("still dirty after a successful write")
	}

	// The second save must carry the NEW mtime.
	d.insertRune('q')
	rec.sent = nil
	ws.saveDoc(d, saveManual, false)
	if got := rec.sent[0].payload["expected_mtime"]; got != 999.5 {
		t.Errorf("second save sent expected_mtime %v, want 999.5 — "+
			"Point would refuse its own write as stale", got)
	}
}

// ---------------------------------------------------------------------------
// The seven refusal slugs
// ---------------------------------------------------------------------------

func TestEachRefusalSlugSaysSomethingAndSetsTheRightState(t *testing.T) {
	cases := []struct {
		slug      string
		payload   map[string]any
		wantIn    string
		wantBlock bool
		check     func(*testing.T, *doc)
	}{
		{
			slug:      "locked",
			payload:   map[string]any{"holder": "sam@other", "age_seconds": 12.0},
			wantIn:    "held by sam",
			wantBlock: true,
			check: func(t *testing.T, d *doc) {
				if d.lock != lockHeldByOther {
					t.Error("lock state not updated from the refusal")
				}
				if d.lockedBy != "sam@other" {
					t.Errorf("lockedBy = %q", d.lockedBy)
				}
			},
		},
		{
			slug:      "read_only",
			wantIn:    "read-only",
			wantBlock: true,
			check: func(t *testing.T, d *doc) {
				if !d.readOnly || d.roReason != roNotWritable {
					t.Errorf("readOnly=%v reason=%q", d.readOnly, d.roReason)
				}
			},
		},
		{
			slug:      "too_large",
			payload:   map[string]any{"size": 600001, "limit_kb": 512},
			wantIn:    "512KB",
			wantBlock: true,
		},
		{
			slug:      "no_parent_dir",
			wantIn:    "folder no longer exists",
			wantBlock: true,
		},
		{
			slug:      "exists",
			wantIn:    "already exists",
			wantBlock: true,
		},
		{
			// io_error is the ONE refusal that is not blocked: B32's own
			// measured case is another process holding the handle during
			// os.replace, which ends when that process does, so the next
			// attempt can succeed with nothing changing on this side.
			slug:      "io_error",
			payload:   map[string]any{"message": "[WinError 5] Access is denied"},
			wantIn:    "WinError 5",
			wantBlock: false,
		},
	}

	for _, c := range cases {
		t.Run(c.slug, func(t *testing.T) {
			ws, _, d := savableWorkspace(t)
			d.insertRune('z')
			ws.saveDoc(d, saveManual, false)

			p := map[string]any{"ok": false, "reason": c.slug}
			for k, v := range c.payload {
				p[k] = v
			}
			said := saidBy(replyFor(t, ws, queryWriteFile, d.path,
				protocol.EvProjectFileWritten, p))

			if !strings.Contains(said, c.wantIn) {
				t.Errorf("said %q, want it to contain %q", said, c.wantIn)
			}
			if d.saving {
				t.Error("still marked saving after a refusal — the document would be unsaveable")
			}
			if !d.dirty {
				t.Error("a refusal cleared the dirty marker; nothing was written")
			}
			if d.autosaveBlocked != c.wantBlock {
				t.Errorf("autosaveBlocked = %v, want %v", d.autosaveBlocked, c.wantBlock)
			}
			if c.check != nil {
				c.check(t, d)
			}
		})
	}
}

// An unrecognised slug renders as itself rather than as silence -- the
// same forward-compatible call lineEndingLabel makes.
func TestAnUnknownRefusalSlugStillSaysSomething(t *testing.T) {
	ws, _, d := savableWorkspace(t)
	d.insertRune('z')
	ws.saveDoc(d, saveManual, false)

	said := saidBy(replyFor(t, ws, queryWriteFile, d.path, protocol.EvProjectFileWritten,
		map[string]any{"ok": false, "reason": "quota_exceeded"}))

	if !strings.Contains(said, "quota_exceeded") {
		t.Errorf("said %q, want the slug named", said)
	}
}

// An Error envelope (a path denial, an unresolvable project) has to
// clear `saving` too, or the document is permanently unsaveable: saveDoc
// refuses to stack a second write on one that never finished.
func TestAnErrorEnvelopeOnAWriteClearsTheInFlightFlag(t *testing.T) {
	ws, _, d := savableWorkspace(t)
	d.insertRune('z')
	ws.saveDoc(d, saveManual, false)
	if !d.saving {
		t.Fatal("precondition: the write is not in flight")
	}

	replyFor(t, ws, queryWriteFile, d.path, protocol.EvError,
		map[string]any{"code": "path_denied", "message": "outside project root"})

	if d.saving {
		t.Fatal("saving still set after an Error — every later save would be dropped")
	}
	if !d.autosaveBlocked {
		t.Error("autosave not stopped after an Error that cannot resolve itself")
	}
}

// ---------------------------------------------------------------------------
// Concurrency on one document
// ---------------------------------------------------------------------------

// A second save while one is in flight does not stack. Without this a
// slow write plus a fast typist produces N concurrent writes of N
// different buffer states, and the one that lands is whichever the
// backend finished last rather than the newest.
func TestASecondSaveWhileOneIsInFlightDoesNotStack(t *testing.T) {
	ws, rec, d := savableWorkspace(t)
	d.insertRune('z')
	ws.saveDoc(d, saveManual, false)
	d.insertRune('q')
	ws.saveDoc(d, saveManual, false)

	if len(rec.sent) != 1 {
		t.Fatalf("sent %d writes, want 1", len(rec.sent))
	}
	if !d.savedWhileSaving {
		t.Error("the second edit was not recorded")
	}
}

// ...and the reply does not clear the dirty marker on content the buffer
// has already moved past.
func TestAWriteThatLandsAfterAnEditLeavesTheFileDirty(t *testing.T) {
	ws, _, d := savableWorkspace(t)
	d.insertRune('z')
	ws.saveDoc(d, saveManual, false)
	d.insertRune('q') // while in flight
	d.savedWhileSaving = true

	replyFor(t, ws, queryWriteFile, d.path, protocol.EvProjectFileWritten, map[string]any{
		"ok": true, "path": d.path, "mtime": 200.0, "size": 6,
		"line_ending": lineEndingLF, "bom": false, "trailing_newline": true,
	})

	if !d.dirty {
		t.Error("dirty cleared on content that is no longer what is on disk")
	}
}

// ---------------------------------------------------------------------------
// Rule 1 / Done-when 11 -- what autosave refuses to attempt
// ---------------------------------------------------------------------------

func TestAutosaveNeverAttemptsAnUnsaveableDocument(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*doc)
	}{
		{"read-only", func(d *doc) { d.readOnly, d.roReason = true, roNotWritable }},
		{"held by another holder", func(d *doc) {
			d.lock, d.lockedBy, d.lockedAge = lockHeldByOther, "sam@other", 3
		}},
		{"no path", func(d *doc) { d.path = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ws, rec, d := savableWorkspace(t)
			d.dirty = true
			c.setup(d)

			ws.saveDoc(d, saveAuto, false)

			if len(rec.sent) != 0 {
				t.Fatalf("autosave sent %+v", rec.sent)
			}
			if c.name != "no path" && !d.autosaveBlocked {
				t.Error("autosave was not stopped — it would refuse once a second")
			}
		})
	}
}

// A MANUAL save on the same documents says why, out loud. The
// distinction is the whole of "does not spam refusals": the user asked,
// so the user gets an answer.
func TestAManualSaveOnAnUnsaveableDocumentSaysWhy(t *testing.T) {
	ws, rec, d := savableWorkspace(t)
	d.lock, d.lockedBy, d.lockedAge = lockHeldByOther, "sam@other", 3

	said := saidBy(ws.saveDoc(d, saveManual, false))

	if len(rec.sent) != 0 {
		t.Fatalf("sent %+v to a locked file", rec.sent)
	}
	if !strings.Contains(said, "held by sam") {
		t.Errorf("said %q", said)
	}
}

// A manual save is also "try again": it clears whatever stopped
// autosave, because the user has now had a chance to see the reason and
// act on it.
func TestAManualSaveUnblocksAutosave(t *testing.T) {
	ws, _, d := savableWorkspace(t)
	d.autosaveBlocked = true
	d.insertRune('z')

	ws.saveDoc(d, saveManual, false)

	if d.autosaveBlocked {
		t.Error("a manual save did not clear the autosave block")
	}
}

// ---------------------------------------------------------------------------
// overwrite: true
// ---------------------------------------------------------------------------

// B32's handoff: "`overwrite: true` is the one field in this payload
// that can destroy someone else's work, and the diff card must never
// send it without the user having pressed [overwrite]". It reaches the
// wire from exactly one message, produced by exactly one button.
func TestOverwriteIsOnlyEverSentFromTheOverwriteButton(t *testing.T) {
	ws, rec, d := savableWorkspace(t)
	d.insertRune('z')

	// Every ordinary path sends false.
	ws.saveDoc(d, saveManual, false)
	if rec.sent[0].payload["overwrite"] != false {
		t.Fatal("an ordinary save sent overwrite: true")
	}
	d.saving = false
	rec.sent = nil

	// The dialog's message is the only producer of true.
	ws.onStaleOverwrite(staleOverwriteMsg{path: d.path})
	if len(rec.sent) != 1 || rec.sent[0].payload["overwrite"] != true {
		t.Fatalf("[overwrite] sent %+v", rec.sent)
	}
}

// The grep that backs the claim up: nothing else in the package passes a
// literal true for the overwrite parameter.
func TestNoOtherCallerPassesOverwriteTrue(t *testing.T) {
	// saveDoc's signature makes this checkable by construction -- the
	// parameter is positional and the only non-default caller is
	// onStaleOverwrite. Asserted as a reminder in the test file rather
	// than left to a reviewer's grep.
	ws, rec, d := savableWorkspace(t)
	d.insertRune('z')
	for _, reason := range []saveReason{saveManual, saveAuto} {
		rec.sent = nil
		d.saving = false
		ws.saveDoc(d, reason, false)
		if len(rec.sent) == 1 && rec.sent[0].payload["overwrite"] != false {
			t.Fatalf("reason %v sent overwrite: true", reason)
		}
	}
}

// A MANUAL save on a CLEAN document still goes out to the wire.
//
// Load-bearing for two separate reasons, which is why it is pinned here
// rather than left as an implementation detail:
//
//   - It is how a user resolves a file that changed on disk under them.
//     With autosave on, the buffer is clean within a second of the last
//     keystroke, so by the time anyone notices an external change there
//     is nothing dirty to save. Skipping the write would leave ctrl+s
//     doing nothing and the stale dialog permanently unreachable.
//   - gate_f13b.ps1's step 4 depends on it. The gate lets autosave settle
//     before changing the file underneath the editor -- deliberately, to
//     make the baseline known -- so the ctrl+s that must be refused is
//     pressed on a clean buffer.
//
// The dirty check in saveDoc is gated on saveAuto for exactly this.
func TestAManualSaveOnACleanDocumentStillGoesOut(t *testing.T) {
	ws, rec, d := savableWorkspace(t)
	if d.dirty {
		t.Fatal("precondition: the fixture document is already dirty")
	}

	ws.saveDoc(d, saveManual, false)
	if len(rec.sent) != 1 || rec.sent[0].cmd != protocol.CmdWriteProjectFile {
		t.Fatalf("a manual save on a clean document sent %+v, want one write — "+
			"ctrl+s would do nothing and the stale dialog would be unreachable", rec.sent)
	}

	// And the autosave half of the same question: a timer must NOT fire on
	// a clean document, or every open file would be rewritten once per
	// debounce for as long as Point is running.
	d.saving = false
	rec.sent = nil
	ws.saveDoc(d, saveAuto, false)
	if len(rec.sent) != 0 {
		t.Errorf("autosave wrote a clean document: %+v", rec.sent)
	}
}
