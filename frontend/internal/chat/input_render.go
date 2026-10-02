package chat

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// editorMode is the mode chip's state — F4.md's "▸ chat" / "▸ @research".
type editorMode int

const (
	modeChat editorMode = iota
	// modePending: the leading @token is still being typed and is a
	// viable PREFIX of a registered agent, but does not name one yet. A
	// verification round asked for this specifically: resolving strictly
	// against the registry (Bug 10) correctly stopped a bare "@" from
	// claiming research, but left "@" with no feedback at all that it
	// had registered. Pending is that feedback, and it is not a claim.
	modePending
	modeResearch
)

// knownAgents is a stopgap, not a real registry lookup: the exact three
// built-in agent ids backend\agents\builtins.py seeds
// (GENERAL_AGENT_ID/FILLER_EXPLAINER_AGENT_ID/RESEARCH_AGENT_ID), hardcoded
// here because there is no protocol command yet to fetch them
// (no ListAgents — only ListProviders/ListProviderModels exist).
// F8's @ picker is the episode expected to add one; its Handoff should note
// this map needs replacing with a live fetch, and that F8's list needs
// agents AND files together (an @token can name either).
//
// A verification round flagged this precisely: the previous rule ("@ at
// position 0" alone) would flip the chip to research for `@report.pdf` —
// no agent involved — same false positive backend\cli.py's own
// `_strip_agent_prefix` already avoids by requiring an EXACT registered
// agent match. matchedAgentID mirrors that rule as closely as a static map
// allows.
//
// S11b widened the VALUE from a bool to a lane, because reload has to decide
// the lane with no TaskStarted to reconcile against.
//
// THE LANE IS A HARDCODED BACKEND FACT, and a deliberate one — but the map
// that held it no longer lives here. `app.IsKnownAgent` /
// `app.IsBackgroundAgent` (app\tasks.go) are the one copy, because F9's task
// tray needs the same fact to decide which tasks belong in it, and `chat`
// imports `app` so the dependency can only run this way.
//
// The fact itself is unchanged: the backend's real rule is
// `agent.max_steps > 1` (runtime.py:822's `is_bounded_loop`), ridden out on
// TaskStarted.background, which F5's own contract makes the sole authority on
// the lane. `max_steps` is not on the wire and ListAgents (B23) returns only
// {id, name, description}, so the one agent that satisfies it — research, at
// max_steps=8; general and filler-explainer are both max_steps=1 — is written
// down in app\tasks.go instead. See S11b's completed doc and F8's handoff
// note; the long-term replacement is a real registry read, in one place now
// rather than two.
//
// What stays here is the LANE, which is a chat rendering concept: `app` has
// no business knowing that a reply renders as a collapsed slot.

// isKnownAgent is the membership half — the only question every pre-S11b
// caller asked of the bool map this replaced.
func isKnownAgent(id string) bool { return app.IsKnownAgent(id) }

// agentLane is the lane an agent's replies belong in. An unknown agent falls
// to laneTurn (the zero value, and the deliberate default): an inline reply
// that should have been a slot only loses a fold, while a slot that should
// have been inline hides a whole answer behind a collapsed one-liner.
func agentLane(id string) lane {
	if app.IsBackgroundAgent(id) {
		return laneTask
	}
	return laneTurn
}

// agentTokenState reports how the leading @token relates to the registry:
// the exact agent it names, or whether it is still a viable prefix of one.
// Both are needed -- an exact match drives that agent's chip, a viable
// prefix drives modePending, and neither drives modeChat (so @report.pdf
// shows nothing, which a verification round confirmed as correct).
func agentTokenState(runes []rune, cursor int) (id string, exact bool, viablePrefix bool) {
	if len(runes) == 0 || runes[0] != '@' {
		return "", false, false
	}

	// 1. B11's own rule: the leading token, bounded by whitespace. This is
	//    what backend\cli.py's _strip_agent_prefix resolves, so it decides
	//    the EXACT match no matter where the cursor happens to be -- a
	//    finished "@research explain the runtime" still shows the chip.
	end := 1
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}
	if tok := strings.ToLower(string(runes[1:end])); isKnownAgent(tok) {
		return tok, true, false
	}

	// 2. The part of that token the user has actually TYPED, bounded by the
	//    cursor rather than by whatever text already sat after it.
	//
	//    A verification round found pending only worked on an empty buffer:
	//    typing '@' at position 0 of "hello world" made the whitespace-bounded
	//    token "hello", which names no agent and is no prefix of one, so the
	//    chip stayed on chat and the user got no signal the '@' had even
	//    registered. Bounded by the cursor the typed token is "", a viable
	//    prefix of every agent, and pending shows -- the same token the user
	//    believes they are typing.
	if cursor < 1 || cursor > len(runes) {
		return "", false, false
	}
	typed := runes[1:cursor]
	for _, r := range typed {
		if unicode.IsSpace(r) {
			return "", false, false // the cursor is past this token entirely
		}
	}
	tok := strings.ToLower(string(typed))
	if isKnownAgent(tok) {
		return tok, true, false
	}
	for _, known := range app.KnownAgentIDs() {
		if strings.HasPrefix(known, tok) {
			return "", false, true // the bare "@" case lands here too
		}
	}
	return "", false, false
}

// matchedAgentID is agentTokenState's exact-match half, kept separate
// because that is the part mirroring backend cli.py's _strip_agent_prefix
// rule: the token must name a registered agent exactly.
func matchedAgentID(runes []rune, cursor int) (string, bool) {
	id, exact, _ := agentTokenState(runes, cursor)
	return id, exact
}

// currentMode derives the mode chip's state from the buffer itself and
// matchedAgentID, above — reverting automatically the moment the leading
// @token stops naming "research", backspacing it out included.
// F04-query-input.md's Done criterion 8: "The mode chip changes on @, and
// the border colour follows." @general and @filler-explainer resolve as
// real agents (matchedAgentID returns true) but have no chip of their own
// yet — F4.md defines exactly ▸ chat and ▸ @research, nothing else to
// switch to — so they render as modeChat today, same as no match at all.
func (in *inputState) currentMode() editorMode {
	id, exact, viable := agentTokenState(in.buf.runes, in.buf.cursor)
	switch {
	case exact && id == "research":
		return modeResearch
	case viable:
		return modePending
	default:
		// An exact match on an agent with no chip of its own yet
		// (general, filler-explainer) lands here too -- see the doc above.
		return modeChat
	}
}

// AttachmentChip is one pinned chip above the typed text — F8's @ picker
// produces these; F4 only renders whatever it is handed (F04-query-input.md:
// "Do NOT implement the @ picker — render chips you are handed").
type AttachmentChip struct {
	// ID is the backend's attachment row id (AttachContext's reply) —
	// what DetachContext addresses. Empty only on a chip built before F8
	// existed, which never happened in product code.
	ID string
	// Path is the resolved source path — attachments.go's staleness
	// re-check (ResolveAttachment) is keyed on it, not on Name, since two
	// attachments can share a filename in different directories.
	Path    string
	Name    string
	Current bool // true: "✓ current". false: "⚠ changed" — will be re-sent.
	Tokens  int  // a token estimate, shown alongside the chip

	// RecordedMtime/RecordedSize are the (mtime, size) pair the backend
	// last recorded as SENT for this attachment — attach.py's own
	// change-detection pair, mirrored here so attachments.go's periodic
	// ResolveAttachment check can compare a fresh live stat against it
	// without asking the backend to also tell us what it thinks "current"
	// means.
	//
	// *int, not int: managers\attachment.py is explicit that a fresh row's
	// pair is NULL until the first turn that actually sends it — "a NULL
	// pair reads as 'never sent', the correct answer for a row nothing has
	// resolved yet". Decoding that null into a plain int silently produces
	// 0, indistinguishable from a real recorded mtime of zero, which is
	// what let a periodic sweep compare a real file's mtime against a
	// bogus zero baseline and report "changed" on a file nobody touched
	// (a real bug, caught in manual verification).
	RecordedMtime, RecordedSize *int
}

// SetChips replaces the pinned attachment chips. F8's job to call; unused
// by anything in this episode.
func (w *Workspace) SetChips(chips []AttachmentChip) {
	w.input.chips = chips
}

// modeChipPrefix is the exact text rendered before the first line of typed
// content — kept as one function so its width (used to compute the text
// wrap width) and its rendering can never disagree.
func modeChipPrefix(mode editorMode) string {
	switch mode {
	case modeResearch:
		return "▸ @research │ "
	case modePending:
		// Fixed width on purpose: showing the half-typed token here would
		// change the prefix width on every keystroke, re-wrapping the
		// text beside it as you type.
		return "▸ @agent… │ "
	default:
		return "▸ chat │ "
	}
}

// activePrefix picks the actual prefix text for the current state — the
// ONE place that decides between the mode chip and browseModePrefix, so
// render.go's splitGrowth (which needs its width for the growth
// calculation) and renderInputLines (which needs the string itself) can
// never disagree about which one is showing or how wide it is.
func activePrefix(mode editorMode, focused bool) string {
	if !focused {
		return browseModePrefix
	}
	return modeChipPrefix(mode)
}

// browseModePrefix replaces the mode chip while the input is unfocused
// (F3's j/k/y/G browsing the transcript — app\focus.go's EnterBrowseMsg).
//
// A verification round flagged this as a real design gap: browse mode
// discarded every key that was not j/k/y/G with no feedback at all, and
// nothing on screen said which mode was active or how to leave it. This is
// that feedback — it names the actual bindings (chat.browse and
// pane.focus.next, app\actions.go) rather than leaving the user to guess.
const browseModePrefix = "◌ BROWSE — j/k/y/G · Tab to type │ "

func renderChipRow(th theme.Theme, chips []AttachmentChip, width int) string {
	parts := make([]string, 0, len(chips))
	for _, c := range chips {
		state := th.Style(theme.Done).Render("✓ current")
		if !c.Current {
			state = th.Style(theme.Warning).Render("⚠ changed")
		}
		name := th.Style(theme.TextSecondary).Render("@" + c.Name)
		parts = append(parts, name+" "+state)
	}
	sep := th.Style(theme.TextDim).Render("  ·  ")
	return padCells(strings.Join(parts, sep), width)
}

// renderInputLines is the query input's own content — everything paneBox
// draws inside the Query Area box, aside from the box itself. rows is
// however many lines are actually available (already reduced for the chip
// row and clamped to the growth cap by the caller); scrollTop is the first
// wrapped line shown, for once the input has grown past its maximum and
// gone internally scrollable.
func renderInputLines(th theme.Theme, in *inputBuffer, mode editorMode, chips []AttachmentChip, focused bool, width, rows, scrollTop int) []string {
	if width < 1 || rows < 1 {
		return nil
	}

	var out []string
	if len(chips) > 0 {
		out = append(out, renderChipRow(th, chips, width))
		rows--
	}
	if rows <= 0 {
		return out
	}

	prefix := activePrefix(mode, focused)
	var prefixStyle lipgloss.Style
	switch {
	case !focused:
		prefixStyle = th.Style(theme.Warning)
	case mode == modeResearch:
		prefixStyle = th.Style(theme.AccentPrimary)
	case mode == modePending:
		// Visible, but deliberately NOT the accent: nothing has resolved
		// yet, and the accent is reserved for an agent that actually has.
		prefixStyle = th.Style(theme.TextSecondary)
	default:
		prefixStyle = th.Style(theme.TextDim)
	}
	styledPrefix := prefixStyle.Render(prefix)
	prefixW := visibleWidth(styledPrefix)

	textWidth := width - prefixW
	if textWidth < 1 {
		textWidth = 1
	}

	lines := in.lines(textWidth)

	selecting := in.hasSelection()
	selLo, selHi := in.selectionRange()

	// The cursor belongs to EXACTLY ONE visual line — lineAt's own
	// documented convention for a position sitting on a wrap boundary.
	// Deciding that once here, rather than letting each line's render call
	// independently guess from raw index comparisons, is Bug 6's fix: two
	// adjacent lines each drawing a caret for the SAME boundary position
	// (one as a trailing space after line i, the other on line i+1's own
	// first rune) is exactly what made Home/End look like they landed on
	// the wrong line, even though the cursor's rune index was already
	// correct — see MoveHome/MoveEnd's own tests.
	cursorLine := -1
	displayCursor := in.cursor
	if focused && !selecting {
		cursorLine, displayCursor = resolveCaretPosition(lines, in.cursor, in.trailing)
	}

	end := scrollTop + rows
	if end > len(lines) {
		end = len(lines)
	}
	for i := scrollTop; i < end; i++ {
		drawCaret := selecting || i == cursorLine
		text := renderTextLineWithCursor(th, in.runes, lines[i], displayCursor, selLo, selHi, selecting, drawCaret)
		var row string
		if i == 0 {
			row = styledPrefix + text
		} else {
			row = strings.Repeat(" ", prefixW) + text
		}
		out = append(out, padCells(row, width))
	}
	return out
}

// resolveCaretPosition is the ONE place renderInputLines decides which
// visual line owns the caret and where on it the caret is drawn. Splitting
// it out of renderInputLines makes it directly testable, which matters
// because this exact function is Bug 16's fix.
//
// cursorLine is lineForCursor's own answer — the SAME lookup movement
// (input_wrap.go) uses, so the line End moved the cursor to is always the
// line the caret is drawn on (Bug 6).
//
// displayCursor is a second, separate decision: in.cursor may sit inside a
// GAP (Bug 15's wrap-consumed whitespace run) even though lineForCursor
// correctly attributed it to cursorLine — splice, setText, Undo and Redo
// all set the cursor by plain arithmetic (lo+len(repl), a saved snapshot's
// index, ...) with no idea a wrap gap exists at all, unlike the movement
// functions, which deliberately skip over one. Typing a SPACE right at a
// wrap boundary is the clearest repro: it widens the existing gap by one
// rune, and splice's cursor = lo+1 lands one past the old gap's edge —
// still inside the new, wider one. renderTextLineWithCursor's per-rune
// i == cursorPos check only ever matches a rune actually inside a line's
// own [Start,End), so an unclamped gap-interior cursor matched nothing on
// ANY row and the caret simply did not render — same visible symptom as
// Bug 15, reached through editing instead of moving.
//
// Clamping the DISPLAY position here, once, is deliberately NOT the same
// fix as MoveRight/MoveLeft's gap-skip: those change where the cursor
// INDEX ends up, which matters for what the next keystroke does. This only
// changes where the SAME index is drawn. Every path that can ever set
// b.cursor converges on rendering through here, so fixing it once here is
// what stops a future edit path (a new mutation, a new undo-like
// operation) from reintroducing this by forgetting to normalize — the
// same reasoning that made setCursor the one place affinity is decided
// now makes this the one place an out-of-range display position is
// repaired.
func resolveCaretPosition(lines []inputLine, cursor int, trailing bool) (cursorLine, displayCursor int) {
	cursorLine = lineForCursor(lines, cursor, trailing)
	displayCursor = cursor
	if l := lines[cursorLine]; displayCursor < l.Start {
		displayCursor = l.Start
	} else if displayCursor > l.End {
		displayCursor = l.End
	}
	return cursorLine, displayCursor
}

// renderTextLineWithCursor paints one wrapped line's runes plain, except:
// the selection range (if any) or, lacking one, the single rune the cursor
// sits on, both drawn with theme.Fill(SelectText, SelectBg) — theme.go's
// own doc names that pairing for exactly this: "a selected list row, a
// focused input." A browse-mode (unfocused) input paints no caret at all —
// F4's chosen signal for "typing does not go here right now", now paired
// with browseModePrefix (above) actually saying so in words too.
//
// drawCaretOnThisLine is true for a selection on every line it spans (an
// unambiguous half-open range, i>=selLo && i<selHi, never double-drawn) and
// for the single line renderInputLines' cursorLine assigned the caret to
// when there is no selection — never both, and never neither, for a
// focused input.
func renderTextLineWithCursor(th theme.Theme, runes []rune, l inputLine, cursorPos, selLo, selHi int, selecting, drawCaretOnThisLine bool) string {
	plain := th.Style(theme.TextPrimary)
	caret := th.Fill(theme.SelectText, theme.SelectBg)

	var b strings.Builder
	for i := l.Start; i < l.End; i++ {
		r := runes[i]
		switch {
		case selecting && drawCaretOnThisLine && i >= selLo && i < selHi:
			b.WriteString(caret.Render(string(r)))
		case !selecting && drawCaretOnThisLine && i == cursorPos:
			b.WriteString(caret.Render(string(r)))
		default:
			b.WriteString(plain.Render(string(r)))
		}
	}
	if drawCaretOnThisLine && !selecting && cursorPos == l.End {
		b.WriteString(caret.Render(" "))
	}
	return b.String()
}

// queryBorderRole picks the Query Area pane's own border colour.
//
// The chat/research accent is F4.md's one deliberately "moving colour" —
// "make that the only moving colour on screen at that moment" — so it is
// reserved for the mode switch and only shows while the input is actually
// focused (composing is when knowing "this goes to @research" matters
// most). Focus itself (composing vs browsing the transcript) is a SEPARATE
// signal, carried by which of the two panes' borders is BorderFocus — see
// render.go's View, which picks the View Port's border the mirror-image way.
func queryBorderRole(mode editorMode, focused bool) theme.Role {
	switch {
	case focused && mode == modeResearch:
		return theme.AccentPrimary
	case focused && mode == modePending:
		// Pending keeps the ordinary focused border: the accent means an
		// agent resolved, and none has.
		return theme.BorderFocus
	case focused:
		return theme.BorderFocus
	default:
		return theme.BorderIdle
	}
}
