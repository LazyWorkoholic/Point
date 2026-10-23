package chat

import (
	"strings"

	"github.com/lazyworkoholic/point/frontend/internal/app"
)

// PrefillDraft is app.ChatDraftTarget — F15's `send to chat`.
//
// The Editor's explain panel hands Root an exchange, Root switches here and
// calls this. What arrives is already quoted and labelled with the file it
// is about (editor\explainpanel.go's `exchange`); this function's whole job
// is to put it in the input without losing what was already there.
//
// # It APPENDS when the input is not empty, and that is the point
//
// `setText` clears the input's undo history (input.go), so a straight
// replacement would make whatever the user had half-typed unrecoverable by
// any key — and this is reached from a keypress in another workspace, which
// is exactly the kind of surprise that must not be able to destroy typing.
// So a non-empty input keeps its content and the exchange goes in below it,
// separated by a blank line. An empty one is simply filled.
//
// The caret lands at the END, which is where the user's next keystroke
// belongs: the exchange is context for a question they are about to write,
// not the question itself. Nothing is sent — F15's own wording is that the
// action copies the exchange, and Root says so in the status line.
func (w *Workspace) PrefillDraft(text string) {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}

	// Held as well as applied, because this is reached SYNCHRONOUSLY from
	// Root's switch to this workspace, and the session load that switch
	// starts is still in flight: setSession's restoreDraft would
	// otherwise setText straight over the top of it. Cleared below once
	// there is a session, and by applyPendingPrefill when the load lands.
	w.pendingPrefill = text

	existing := string(w.input.buf.runes)
	combined := text
	if strings.TrimSpace(existing) != "" {
		combined = strings.TrimRight(existing, "\n") + "\n\n" + text
	}

	w.input.buf.setText(combined, len([]rune(combined)))
	w.pinned = true

	// Persisted now rather than on the next keystroke's debounce: the
	// text came from somewhere else, so there may not BE a next keystroke
	// for a while, and a draft that exists on screen but not on disk is
	// the Bug 17 shape draft.go already records (FlushDraft exists for
	// exactly that asymmetry).
	if w.sessionID != "" {
		w.FlushDraft()
		// There is a session, so no load is coming to clobber it.
		w.pendingPrefill = ""
	}
}

// applyPendingPrefill re-applies a prefill that arrived before this
// workspace had a session, and is called from setSession immediately after
// restoreDraft.
//
// It goes through PrefillDraft again rather than assigning the buffer
// directly, so the combine-rather-than-replace rule above holds for this
// path too -- a restored draft and a prefilled exchange both survive, in
// that order.
func (w *Workspace) applyPendingPrefill() {
	text := w.pendingPrefill
	if text == "" {
		return
	}
	w.pendingPrefill = ""
	w.PrefillDraft(text)
}

// Compile-time proof that Root will find it. Same reason editor's own
// assertions exist: app.ChatDraftTarget is consulted through a type
// assertion that fails silently, so a renamed method would quietly turn
// `send to chat` into "could not open Chat" rather than breaking the build.
var _ app.ChatDraftTarget = (*Workspace)(nil)
