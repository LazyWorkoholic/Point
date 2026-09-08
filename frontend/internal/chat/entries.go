package chat

// The transcript is ONE ordered sequence, assembled from three sources.
//
//	persisted messages (w.all, DB order, loaded once at session open)
//	live entries       (w.live, creation order — every send this session)
//	the pending bubble (w.queued, at most one, always last)
//
// The ordering rule is the whole point of this file: **display position is
// creation order, never role and never which list a thing lives in.**
//
// F5's first cut broke that. It put the user's message into w.all and only
// the reply into w.live, so the render was "all of w.all, then all of
// w.live" — and the second send in any session came out Q1 · Q2 · A1 · A2,
// every question clustered above every answer. The queue and retry paths
// made it obvious, but a plain second send did it too. Two append-only
// lists cannot express one interleaved order. A send now appends BOTH its
// entries to w.live, adjacent (stream.go's startSend), so creation order is
// display order and there is nothing left to interleave.
//
// w.all stays first because it is history: everything in it happened before
// this session's first send, by construction.
//
// S11b did NOT add a fourth source, on purpose. A reloaded slot or footer
// is a DECORATION on a row of w.all (w.hist, parallel and same-length), not
// a list of its own — so there is still one ordering rule, in one place,
// and the pairing that puts an answer under its own question is a reorder
// of w.all itself rather than a second sequence to interleave.

// entryKind says which source an absolute index falls in.
type entryKind int

const (
	entryMessage entryKind = iota
	entryLive
	entryPending
)

// histAt is the live-shaped decoration for persisted row i, or nil when
// that row renders through render.go's plain path.
//
// Bounds-safe on purpose: w.hist is only as long as the last setSession
// made it, and every test that assigns w.all directly leaves it nil. A
// short slice therefore means "no decorations", never a panic.
func (w *Workspace) histAt(i int) *liveEntry {
	if i < 0 || i >= len(w.hist) {
		return nil
	}
	return w.hist[i]
}

func (w *Workspace) pendingCount() int {
	if w.queued == nil {
		return 0
	}
	return 1
}

func (w *Workspace) entryCount() int {
	return len(w.all) + len(w.live) + w.pendingCount()
}

// entryAt resolves an absolute index. The second return is an index into
// whichever list the first names, and is meaningless for entryPending.
func (w *Workspace) entryAt(i int) (entryKind, int) {
	if i < len(w.all) {
		return entryMessage, i
	}
	if i-len(w.all) < len(w.live) {
		return entryLive, i - len(w.all)
	}
	return entryPending, 0
}

// entryRole is what gapLines (scroll.go) branches on — the exchange spacing
// rule, unchanged from F3.
func (w *Workspace) entryRole(i int) string {
	kind, idx := w.entryAt(i)
	switch kind {
	case entryMessage:
		if e := w.histAt(idx); e != nil && e.lane == laneTask {
			// gapLines spaces a task slot differently from a reply, and a
			// reloaded slot has to sit in the transcript the way the live
			// one it replaces did. Same rule, same answer.
			return roleTool
		}
		return w.all[idx].Role
	case entryLive:
		e := w.live[idx]
		if e.kind == liveUserMsg {
			return roleUser
		}
		if e.lane == laneTask {
			return roleTool
		}
		return roleAssistant
	default:
		return roleUser
	}
}

// entryLines renders one entry. Persisted messages come from F3's cache;
// live ones carry their own memo (renderLive), because their text changes
// on every token and cache.go's only invalidation is a width change.
func (w *Workspace) entryLines(i, width int, focused bool) []string {
	kind, idx := w.entryAt(i)
	switch kind {
	case entryMessage:
		// A decorated row goes through F5's own renderers — the collapsed
		// slot, the footer — because "identical to live" is a property to
		// get by construction, not one to re-implement and then test for.
		// It carries its own memo (renderLive), so it never touches the
		// shared cache, which keys by message id and has no invalidation
		// for a slot the user just opened.
		if e := w.histAt(idx); e != nil {
			return w.renderLive(e, width, focused)
		}
		if focused {
			return renderMessage(w.theme, w.all[idx], width, true)
		}
		return w.renderCached(w.all[idx], width)
	case entryLive:
		return w.renderLive(w.live[idx], width, focused)
	default:
		return w.renderPending(width, focused)
	}
}

// entryText is the raw text `y` copies — the source, never a rendered line,
// the same rule copy.go's doc comment states for a persisted message.
func (w *Workspace) entryText(i int) (string, bool) {
	if i < 0 || i >= w.entryCount() {
		return "", false
	}
	kind, idx := w.entryAt(i)
	switch kind {
	case entryMessage:
		// Still the persisted row's own text, decorated or not: `y` copies
		// the source. The one row where the two differ is the placeholder
		// applyTaskRows inserts for a task that produced nothing, and both
		// sides of it are empty anyway.
		return w.all[idx].Content.Text, true
	case entryLive:
		e := w.live[idx]
		if e.kind == liveUserMsg {
			return e.userText, true
		}
		return e.text, true
	default:
		return w.queued.text, true
	}
}

// liveAt returns the live entry at an absolute index, if that is what it is.
// The browse-mode actions (o, t, y, retry) act on the focused entry, and
// only a REPLY has any of those to act on — a user message has no thinking
// block, no slot and nothing to retry.
//
// S11b: a decorated persisted row answers here too, and that single line is
// what makes `o` expand a reloaded @research slot, `n`/`p` focus it, and
// `^R` retry a reloaded failure. All three were already written against
// this function; none of them needed a reload-aware copy.
func (w *Workspace) liveAt(i int) *liveEntry {
	if i < 0 || i >= w.entryCount() {
		return nil
	}
	switch kind, idx := w.entryAt(i); kind {
	case entryMessage:
		return w.histAt(idx)
	case entryLive:
		if w.live[idx].kind == liveReply {
			return w.live[idx]
		}
	}
	return nil
}

// focusedIndex is the index the transcript actions act on. No explicit focus
// means the newest entry — the same convention F3's copyFocused used, so `y`
// works without pressing a focus key first.
func (w *Workspace) focusedIndex() int {
	if w.focus >= 0 && w.focus < w.entryCount() {
		return w.focus
	}
	return w.entryCount() - 1
}
