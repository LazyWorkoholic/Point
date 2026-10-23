package editor

import (
	"strconv"
	"strings"
)

// Ghost text: the fill reply growing at the caret, display-only.
//
// # How ghost text is represented in the buffer model -- it is NOT
//
// This is the question F15's Record step asks, and the answer is the whole
// design: ghost text is **not in the buffer model at all**. It is a
// `[]string` on the workspace plus an anchor line, consumed by `renderCode`
// as extra ROWS it emits after the anchor. `Buffer` never sees it, `Splice`
// is never called, `Version()` never moves, no `editRecord` is written, the
// document is not marked dirty, and the undo history does not know it
// happened.
//
// That is what makes F15's guarantee structural rather than something a
// cancel path has to undo: "nothing on disk · nothing in the real buffer ·
// not undoable yet". `esc` mid-stream drops a slice of strings. There is
// nothing to roll back, and Done-when 3c ("the buffer is untouched and not
// marked dirty") cannot fail by omission -- it would take a new call to
// Splice to break it.
//
// The alternative -- splicing the tokens in as they arrive and splicing them
// back out on cancel -- was not built, and would have been wrong in a way
// this codebase has already paid for twice: every arriving token would be an
// `editRecord`, so `ctrl+z` would walk the stream backwards one token at a
// time (F13's "one undo operation" requirement, inverted), autosave would
// arm on a buffer the user never typed in (autosave rule 4 arms from
// `Version()`, which would move), and a crash mid-stream would leave model
// output in a file nobody accepted.
//
// **This is the pattern to reuse** for anything transient the editor needs
// to show later (F17's preview, a inline annotation): state beside the
// buffer, rows injected at render time, keyed to a line number the renderer
// already has.
//
// # Why the rows push real lines down instead of overlaying them
//
// A proposed insertion that covers the code it sits next to hides exactly
// the context needed to judge it. So the ghost rows are inserted into the
// rendered output and the real lines below shift down for the frame --
// visually identical to how the text would read if applied, which is the
// "watch it write" feeling F15 asks for, with none of the commitment.
//
// # There is no scroll offset here
//
// This type had a `top` field for a while: the ghost's own scroll position,
// because the renderer only emits its rows when it draws the anchor line
// and the tail of a long region was otherwise unreachable. It made the
// region a nested sub-viewport -- the insertion line pinned on screen with
// text scrolling inside it, and the code after the insertion point behind a
// wall. It is gone. The rows are entries in the document's ONE row
// sequence (viewrows.go), scrolled by the one scroll position, exactly as
// they will be once the edit is applied and they are real lines.
type ghostText struct {
	// anchorLine is the 0-based buffer line the rows are drawn AFTER.
	// Frozen when the invocation starts: the caret may move while the
	// stream runs, and the ghost belongs where the question was asked.
	anchorLine int

	// lines is what is drawn, already split. Rebuilt wholesale from the
	// accumulated reply on every token -- see setFromReply.
	lines []string

	// literal records that the reply did not look like a fill proposal, so
	// `lines` is the reply's own text rather than a proposed region. The
	// renderer labels the two differently, because a refusal drawn as a
	// proposed insertion would read as code the agent wants to add.
	literal bool
}

// rows is how many display rows this ghost occupies.
func (g *ghostText) rows() int {
	if g == nil {
		return 0
	}
	return len(g.lines)
}

// setFromReply rebuilds the ghost from everything that has arrived.
//
// Whole-rebuild rather than append, for Done-when 3b: the ghost is a pure
// function of `invocation.text`, so there is no incremental state that could
// be out of step with the accumulated reply after a workspace switch.
func (g *ghostText) setFromReply(reply string) {
	if g == nil {
		return
	}
	region, ok := partialReplacement(reply)
	if !ok {
		// Not a proposal (yet, or at all). Shown as the agent's own words
		// -- which is also what makes a plain-text refusal visible while
		// it streams, rather than a dead caret for the length of a turn.
		g.literal = true
		g.lines = splitGhostLines(strings.TrimSpace(reply))
		return
	}
	g.literal = false
	g.lines = splitGhostLines(region)
}

func splitGhostLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// partialReplacement pulls the `replacement` field out of a fill reply that
// may still be arriving, mid-string.
//
// # This is a PREVIEW, and it is not what gets applied
//
// Stated as plainly as possible, because the two look alike and only one of
// them is trustworthy. F15's paste block says: "Do not parse the model's
// JSON here". The applied region comes from `EvFillProposal` -- parsed,
// shape-checked and validated in the backend against the region the model
// was actually shown (B24), with the range checks this function does not
// and must not attempt. Nothing in `onFillProposal` reads anything this
// function returned, and nothing this function returns ever reaches a
// buffer.
//
// What it is for is the one thing B24's event cannot do: a proposal arrives
// once, complete, AFTER the terminal event. There is no partial proposal on
// the wire, so "the region streams back as ghost text at the cursor,
// growing as tokens arrive" (F15's Build, D85) has no other source. The
// choice is a best-effort preview of the text the user is about to be shown
// a diff of, or no ghost at all.
//
// So it is deliberately minimal and deliberately failure-tolerant: it finds
// the key, unescapes what is there, and reports ok=false for anything it
// does not understand rather than guessing. A false negative shows the raw
// reply dim, which is a legible outcome; there is no outcome in which a
// wrong answer here changes what is applied.
func partialReplacement(reply string) (string, bool) {
	i := strings.Index(reply, `"replacement"`)
	if i < 0 {
		return "", false
	}
	rest := reply[i+len(`"replacement"`):]

	// The colon, then the opening quote. Whitespace between them is legal
	// JSON and the demo fixture happens not to emit any; both are handled
	// rather than one assumed.
	rest = strings.TrimLeft(rest, " \t\r\n")
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	rest = strings.TrimLeft(rest[1:], " \t\r\n")
	if !strings.HasPrefix(rest, `"`) {
		return "", false
	}
	rest = rest[1:]

	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch c {
		case '"':
			// The string ended. Everything after it is the rest of the
			// object (`note`, the closing brace), which the ghost has no
			// interest in.
			return b.String(), true
		case '\\':
			if i+1 >= len(rest) {
				// A truncated escape at the very end of what has arrived:
				// one token short. Returning what is decoded so far is
				// right -- the next token completes it and this runs again
				// from the start.
				return b.String(), true
			}
			i++
			switch rest[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				// Dropped rather than written: the buffer model is
				// LF-only (B32 normalises on the wire, doc.go carries the
				// real ending as metadata), so a CR drawn into a row
				// would be a control character in the middle of a line.
			case '"', '\\', '/':
				b.WriteByte(rest[i])
			case 'b', 'f':
				// Legal JSON, meaningless in a code region. Dropped for
				// the same reason as CR.
			case 'u':
				if i+4 < len(rest) {
					if n, err := strconv.ParseUint(rest[i+1:i+5], 16, 32); err == nil {
						b.WriteRune(rune(n))
						i += 4
						continue
					}
				}
				return b.String(), true
			default:
				// Not an escape this understands. Stop here rather than
				// inventing a character -- the complete reply is about to
				// be validated by the backend anyway.
				return b.String(), true
			}
		default:
			b.WriteByte(c)
		}
	}
	// Ran out of input mid-string, which is the normal case while tokens
	// are still arriving.
	return b.String(), true
}
