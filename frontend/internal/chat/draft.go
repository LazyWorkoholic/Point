package chat

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/config"
)

// draftDebounce matches F04-query-input.md exactly: "debounced ~500ms after
// typing stops."
const draftDebounce = 500 * time.Millisecond

type draftSaveMsg struct{ gen int }

// scheduleDraftSave marks the draft dirty and schedules a save
// draftDebounce from now. It follows app\paste.go's pasteFlushMsg pattern
// exactly: a generation counter that a later keystroke bumps, so an
// interrupted burst of typing produces exactly one write on the wire — er,
// on disk — not one per keystroke, and a stale tick that fires after being
// superseded is a no-op rather than an overwrite with old text.
func (w *Workspace) scheduleDraftSave() tea.Cmd {
	w.input.draftGen++
	gen := w.input.draftGen
	return tea.Tick(draftDebounce, func(time.Time) tea.Msg {
		return draftSaveMsg{gen: gen}
	})
}

// saveDraftIfDue is draftSaveMsg's handler, reached through Update.
func (w *Workspace) saveDraftIfDue(gen int) {
	if gen != w.input.draftGen {
		return // superseded by a later keystroke
	}
	w.persistDraft()
}

// persistDraft writes the current buffer into state.json, keyed by session
// ID — F04-query-input.md: "Scope: per session." An empty session ID (no
// backend yet, or bootstrap has not resolved one) has nowhere to key a
// draft under, so saving is skipped rather than writing under a "" key a
// real session could later collide with.
//
// saveStateFn defaults to config.SaveState (set in New) so a test can
// substitute a recorder — exactly sendFn's reason in protocol.go — which
// matters here specifically because the real function writes
// %LOCALAPPDATA%\Point\state.json, and CLAUDE.md is explicit that nothing
// automated may touch that file outside a scratch profile.
func (w *Workspace) persistDraft() {
	if w.sessionID == "" || w.saveStateFn == nil {
		return
	}
	text := w.input.buf.text()
	if text == "" {
		if w.state.Drafts != nil {
			delete(w.state.Drafts, w.sessionID)
		}
	} else {
		if w.state.Drafts == nil {
			w.state.Drafts = make(map[string]config.Draft)
		}
		w.state.Drafts[w.sessionID] = config.Draft{Text: text, Cursor: w.input.buf.cursor}
	}
	if err := w.saveStateFn(w.state); err != nil {
		w.log.Warn("saving draft", "err", err)
	}
}

// clearDraft is onSend's last step: drop this session's draft immediately
// instead of waiting out the debounce, so a crash between "sent" and the
// next tick cannot resurrect text the user already cleared.
func (w *Workspace) clearDraft() tea.Cmd {
	w.input.draftGen++ // supersede any pending save for the text just cleared
	w.persistDraft()
	return nil
}

// FlushDraft persists the current draft immediately, bypassing the debounce
// entirely — app.quit's escape hatch from it (app\draft.go's DraftFlusher).
//
// Bug 17: a verification round reported the persisted draft's cursor (and
// sometimes its text) landing stale after typing several characters right
// at a wrap-gap boundary and then quitting, "off by roughly the number of
// characters just typed" — with an intervening arrow key reliably making it
// correct again. Traced before touching anything: the in-memory
// b.cursor/b.runes were NEVER wrong at any point (TestTypingASequenceFromA
// GapAdjacentCursorMatchesManualRecount in input_wrap_test.go proves this —
// splice's lo+len(repl) is exact, every single insert, whether or not the
// result lands inside a gap). The bug was never in splice.
//
// It was in app.quit: scheduleDraftSave only ever returns a tea.Tick 500ms
// out (draftDebounce), and app.quit's Run returned tea.Quit with no
// synchronous flush of its own. Once bubbletea has already been handed
// tea.Quit, a still-sleeping Tick's eventual draftSaveMsg is never
// delivered — Update simply never runs again — so whatever the LAST
// successful debounced write happened to hold is what stays on disk,
// stale by exactly however much was typed after it. This reproduced with
// no dependency on gaps at all; a verification round's own report of it
// tracking "at/near a wrap gap boundary" specifically, and an arrow key
// reliably fixing it, both read as the natural result of how long a human
// pauses before quitting in each case, not a second, gap-specific defect
// (a gap-adjacent burst of same-key spaces types faster than reading and
// repositioning at a normal line, and pressing one more key before
// quitting is itself extra time for the 500ms window to close).
//
// FlushDraft is that synchronous flush: bump draftGen (so a Tick already
// in flight becomes a harmless, superseded no-op if it ever does arrive)
// and persist the live buffer right now, whatever it holds.
func (w *Workspace) FlushDraft() {
	w.input.draftGen++
	w.persistDraft()
}

// restoreDraft loads this session's draft. Called from setSession
// (protocol.go) — the same point F3 already resets focus/scroll/cache on a
// session change. It ALWAYS calls setText, even when there is no saved
// draft: a session with nothing on file must start with an empty input,
// not whatever text a previously open session left in the buffer.
func (w *Workspace) restoreDraft() {
	d := w.state.Drafts[w.sessionID] // zero value (empty, cursor 0) if absent

	// Filter on LOAD as well as on insert. A draft written before
	// filterInsertable existed can hold control characters that are
	// invisible on screen but shift every rune index after them -- which is
	// how a verification round saw the @-chip stop responding and STAY
	// broken across a full restart: the corruption was in state.json, and
	// faithfully restoring it restored the bug with it. Repairing on load
	// makes an already-corrupted draft self-healing instead of permanent.
	text := filterInsertable(d.Text)
	cursor := d.Cursor
	if text != d.Text {
		// Dropped runes make the saved cursor an index into text that no
		// longer exists; clamping is all that can be honestly recovered.
		w.log.Warn("repaired a draft containing control characters",
			"session", w.sessionID, "dropped", len([]rune(d.Text))-len([]rune(text)))
		if cursor > len([]rune(text)) {
			cursor = len([]rune(text))
		}
	}
	w.input.buf.setText(text, cursor)

	// A cursor from a shorter/older draft, or a hand-edited state.json, must
	// never sit past the end -- setText clamps, and this keeps the two in
	// step if that ever changes.
	if w.input.buf.cursor > len(w.input.buf.runes) {
		w.input.buf.cursor = len(w.input.buf.runes)
	}
}
