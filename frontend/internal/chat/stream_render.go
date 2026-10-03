package chat

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// renderLive draws one live entry, memoised and throttled.
//
// The memo is what makes "only that message re-renders" true (F5): every
// OTHER message in the transcript comes from cache.go, and this one comes
// from here, so a token costs one entry's markdown parse and nothing else.
// The throttle is on top of that — inside streamThrottle of the last parse
// the previous lines are re-served unchanged, and the stream tick guarantees
// a repaint lands once the window passes.
func (w *Workspace) renderLive(e *liveEntry, width int, focused bool) []string {
	// A running slot shows a clock (`⟳ @research 0:42`), and a clock that
	// only redraws when an EVENT arrives is a stopped clock — the exact
	// misreading F5 is trying to prevent, since a frozen timer is what a
	// hung task looks like. So a non-terminal entry is also stale once the
	// second it was drawn in has passed; the stream tick (at most 500ms)
	// guarantees a frame lands to notice.
	// BEFORE any clock-based rule: within one frame an entry renders once,
	// full stop. Both staleness gates below are time-based, and a frame
	// renders each entry several times (bottomAnchored measures, then
	// visibleWindow composes; clampScroll and moveFocus measure too) with
	// real time passing in between. A frame that straddled the throttle
	// boundary therefore got a stale, SHORTER render for the measuring
	// pass and a real, taller one for the composing pass -- so (topAbs,
	// topRow), an address computed against the first height, was applied
	// to the second, and rows fell off the bottom of the window. The
	// window this opened is widest exactly where an entry's height jumps:
	// the moment a code fence closes and a raw tail becomes a boxed block.
	//
	// The throttle and the per-second staleness still do their jobs
	// BETWEEN frames, which is what they were for; this only stops them
	// from disagreeing with themselves inside one.
	// The guard is on what this frame already SERVED, not on what is
	// cached: a throttled call answers with lines rendered at an older
	// version, and every later call in that frame has to be given those
	// same lines or the heights disagree again. `servedVersion` is
	// therefore the version the answer was FOR; `renderedVersion` stays
	// the version the lines were BUILT at, and the two differ exactly
	// while the throttle is holding an update back.
	//
	// State still wins over the frame: a token, an opened slot or a
	// toggled thinking block bumps `version`, which lands here as a
	// mismatch and re-decides. Only the CLOCK is prevented from
	// re-deciding mid-frame. In the running app state cannot change
	// mid-frame at all (Update runs, then View), so this costs nothing
	// there and keeps the guard honest everywhere else.
	sameFrame := e.rendered != nil &&
		e.renderedFrame == w.frame &&
		e.servedVersion == e.version &&
		e.renderedWidth == width &&
		e.renderedFocused == focused &&
		e.renderedThemeGen == w.themeGen
	if sameFrame {
		return e.rendered
	}

	second := int(time.Since(e.startedAt).Seconds())
	fresh := e.rendered != nil &&
		e.renderedVersion == e.version &&
		e.renderedWidth == width &&
		e.renderedFocused == focused &&
		e.renderedThemeGen == w.themeGen &&
		(e.status.terminal() || e.renderedSecond == second)
	if fresh {
		// Stamped so the REST of this frame is bound to this same answer,
		// not just a real render would be. Whatever lines an entry gives
		// first in a frame are its lines for that whole frame -- if only
		// the real-render path stamped, a measuring pass served from cache
		// would leave the composing pass free to re-decide.
		e.renderedFrame, e.servedVersion = w.frame, e.version
		return e.rendered
	}
	throttled := e.rendered != nil &&
		e.renderedWidth == width &&
		e.renderedFocused == focused &&
		e.renderedThemeGen == w.themeGen &&
		!e.status.terminal() &&
		time.Since(e.renderedAt) < streamThrottle
	if throttled {
		e.renderedFrame, e.servedVersion = w.frame, e.version
		return e.rendered
	}

	lines := renderLiveEntry(w.theme, e, width, focused)
	e.rendered = lines
	e.renderedVersion = e.version
	e.renderedWidth = width
	e.renderedFocused = focused
	e.renderedThemeGen = w.themeGen
	e.renderedAt = time.Now()
	e.renderedSecond = second
	e.renderedFrame, e.servedVersion = w.frame, e.version
	return lines
}

func renderLiveEntry(th theme.Theme, e *liveEntry, width int, focused bool) []string {
	// Kind FIRST, then lane. A user message has a lane field like every
	// other entry (it is the zero value), so dispatching on lane alone
	// rendered what the user typed as a reply, under a "Point:" label.
	if e.kind == liveUserMsg {
		return renderUserEntry(th, e, width, focused)
	}
	if e.lane == laneTask {
		return renderTaskEntry(th, e, width, focused)
	}
	return renderTurnEntry(th, e, width, focused)
}

// renderUserEntry draws a message this session sent, through render.go's
// renderUserMessage — the same function the persisted copy will go through
// after the next session load, so the message does not change shape when it
// stops being live.
func renderUserEntry(th theme.Theme, e *liveEntry, width int, focused bool) []string {
	return renderUserMessage(th, wireMessage{
		Role:    roleUser,
		Content: wireContent{Type: "text", Text: e.userText},
	}, width, focused)
}

// ---------------------------------------------------------------------------
// The TASK lane — a collapsed slot that fills in place
// ---------------------------------------------------------------------------

// renderTaskEntry is F5's state transitions over F3's slot: `⟳ running 0:42`
// → `✓ done` → expandable. The slot is ONE line until it is opened, which is
// what keeps a task from displacing the chat that arrived after it.
func renderTaskEntry(th theme.Theme, e *liveEntry, width int, focused bool) []string {
	var role theme.Role
	var head string

	switch e.status {
	case statusSending, statusRunning:
		role = theme.Running
		head = fmt.Sprintf("⟳ %s %s", e.slotName(), elapsed(time.Since(e.startedAt)))
		if e.maxSteps > 0 {
			head += fmt.Sprintf("  step %d/%d", e.step, e.maxSteps)
		}
	case statusDone:
		role = theme.Done
		head = "✓ " + e.slotName() + " done " + elapsed(e.finishedAt.Sub(e.startedAt))
	case statusFailed:
		role, head = theme.Error, "✗ "+e.slotName()+" failed"
	case statusCancelled:
		role, head = theme.Warning, "⚠ "+e.slotName()+" interrupted"
	case statusTimedOut:
		role, head = theme.Warning, "⚠ "+e.slotName()+" — no response"
	default:
		role, head = theme.Queued, "◌ "+e.slotName()+" queued"
	}

	marker := "▸"
	if e.slotOpen {
		marker = "▾"
	}
	hint := ""
	if e.status.terminal() {
		hint = "   " + th.Style(theme.TextDim).Render(marker+" o")
	}

	// A one-line slot has nowhere to put a border, so focus was previously
	// signalled by recolouring the head — invisible on a red failed slot,
	// which already had a colour of its own. A leading bar is structural:
	// it holds a column the unfocused state leaves blank, so which slot is
	// focused is legible whatever colour the state is.
	lead := "  "
	if focused {
		lead = th.Style(theme.BorderFocus).Render("▌") + " "
	}
	out := []string{padCells(lead+th.Style(role).Render(head)+hint, width)}

	if e.slotOpen {
		inner := width - 4
		if inner < 4 {
			inner = 4
		}
		dim := th.Style(theme.TextDim)
		body := renderStreamBody(th, e.text, inner)
		if len(body) == 0 {
			// Expanding must always DO something. A failed slot has no
			// reply text at all, so the first cut flipped the marker to
			// "open" and opened nothing — the slot looked identical.
			// Whatever there is to say gets said here instead.
			body = []string{dim.Render(slotEmptyNote(e))}
		}
		for _, l := range body {
			out = append(out, lead+dim.Render("▎")+" "+l)
		}
	}
	if footer := liveFooter(th, e, width); footer != nil {
		for _, l := range footer {
			out = append(out, padCells(lead+l, width))
		}
	}
	return out
}

// slotEmptyNote is what an expanded slot says when the task produced no
// text — the normal case for a failure, and possible for a cancellation.
func slotEmptyNote(e *liveEntry) string {
	switch {
	case e.errText != "":
		return e.errText
	case e.status == statusCancelled:
		return "interrupted before it produced anything"
	case e.status == statusTimedOut:
		return "no response — nothing was received"
	default:
		return "no output"
	}
}

func (e *liveEntry) slotName() string {
	if e.agentID != "" {
		return "@" + e.agentID
	}
	return "task"
}

// elapsed formats a running duration as m:ss — the shape F5 writes as 0:42.
func elapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// ---------------------------------------------------------------------------
// The TURN lane — an inline streaming reply
// ---------------------------------------------------------------------------

// renderTurnEntry mirrors render.go's renderAssistantMessage exactly — same
// label, same accent bar, same width arithmetic — so a reply does not shift
// on screen the moment it stops streaming and becomes an ordinary persisted
// message on the next session load.
func renderTurnEntry(th theme.Theme, e *liveEntry, width int, focused bool) []string {
	label := th.Style(theme.TextSecondary).Render("Point:")
	if head := turnHeadNote(th, e); head != "" {
		label += "  " + head
	}

	bar := "▎ "
	barStyle := th.Style(theme.AIBar)
	if focused {
		barStyle = th.Style(theme.BorderFocus)
	}
	inner := width - visibleWidth(bar)
	if inner < 4 {
		inner = 4
	}

	body := renderThinking(th, e, inner)
	body = append(body, renderStreamBody(th, e.text, inner)...)
	if len(body) == 0 {
		body = []string{th.Style(theme.TextDim).Render("…")}
	}

	out := make([]string, 0, len(body)+2)
	out = append(out, padCells(label, width))
	for _, l := range body {
		out = append(out, barStyle.Render("▎")+" "+l)
	}
	if footer := liveFooter(th, e, width); footer != nil {
		out = append(out, footer...)
	}
	return out
}

// turnHeadNote is F5's `step 3/6` on the message header, plus whatever else
// progress had to say (a rate-limit backoff, which is the difference between
// "patiently waiting" and "hung").
func turnHeadNote(th theme.Theme, e *liveEntry) string {
	if e.status.terminal() {
		return ""
	}
	var parts []string
	if e.maxSteps > 0 {
		parts = append(parts, fmt.Sprintf("step %d/%d", e.step, e.maxSteps))
	}
	if e.progressNote != "" && !strings.HasPrefix(e.progressNote, "step ") {
		parts = append(parts, e.progressNote)
	}
	if len(parts) == 0 {
		return ""
	}
	return th.Style(theme.TextDim).Render(strings.Join(parts, " · "))
}

// renderThinking is the dim collapsible block, collapsed by default.
func renderThinking(th theme.Theme, e *liveEntry, width int) []string {
	if strings.TrimSpace(e.thinking) == "" {
		return nil
	}
	dim := th.Style(theme.TextDim)
	lines := strings.Split(expandTabs(strings.TrimRight(e.thinking, "\n")), "\n")
	if !e.expanded {
		return []string{dim.Render(fmt.Sprintf("▸ thinking (%d lines) — t", len(lines)))}
	}
	out := []string{dim.Render("▾ thinking — t")}
	for _, l := range lines {
		out = append(out, hardWrapCells(dim, l, width)...)
	}
	return append(out, "")
}

// ---------------------------------------------------------------------------
// Streaming markdown: the unclosed-fence rule
// ---------------------------------------------------------------------------

// renderStreamBody is renderMarkdown with F5's mid-stream fence rule applied:
// an unclosed fence must not flicker the code block, so everything from the
// opening fence onward is rendered as a plain raw tail until the closing
// fence arrives.
//
// Without this, goldmark treats an unterminated fence as a code block that
// grows a line at a time, so the boxed block (codeblock.go) appears, is
// re-drawn at a new height every token, and then — the moment the closing
// fence lands — is replaced by the real one. Holding the box back until the
// fence closes costs a few frames of plainer text and removes the flicker
// entirely.
func renderStreamBody(th theme.Theme, text string, width int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	stable, tail := splitOpenFence(text)

	var out []string
	if strings.TrimSpace(stable) != "" {
		out = append(out, renderMarkdown(th, stable, width)...)
	}
	if tail == "" {
		return out
	}
	if len(out) > 0 {
		out = append(out, "")
	}
	// The raw tail is preformatted by definition — it is the inside of a
	// fence that has not closed yet — so it carries tabs verbatim.
	style := th.Style(theme.TextPrimary)
	for _, l := range strings.Split(expandTabs(strings.TrimRight(tail, "\n")), "\n") {
		out = append(out, hardWrapCells(style, l, width)...)
	}
	return out
}

// splitOpenFence splits text at the start of an unclosed fenced code block.
//
// Returns the whole text as `stable` and an empty tail when every fence is
// balanced — the overwhelmingly common case, including all finished text, so
// a completed reply renders through exactly the same markdown path F3 uses
// for a persisted one.
func splitOpenFence(text string) (stable, tail string) {
	lines := strings.Split(text, "\n")
	openAt := -1
	for i, l := range lines {
		if !isFenceLine(l) {
			continue
		}
		if openAt < 0 {
			openAt = i
		} else {
			openAt = -1 // this one closed it
		}
	}
	if openAt < 0 {
		return text, ""
	}
	return strings.Join(lines[:openAt], "\n"), strings.Join(lines[openAt:], "\n")
}

// isFenceLine reports whether a line opens or closes a fence. Indented up to
// three spaces, per CommonMark; four would make it an indented code block,
// which has no fence to be unbalanced.
func isFenceLine(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return false
	}
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

// hardWrapCells breaks a line on cell boundaries rather than on words — what
// a raw code tail wants, since reflowing code by word would be a lie about
// what the model actually emitted.
func hardWrapCells(style interface{ Render(...string) string }, s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if s == "" {
		return []string{""}
	}
	var out []string
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if used+rw > width && used > 0 {
			out = append(out, style.Render(b.String()))
			b.Reset()
			used = 0
		}
		b.WriteRune(r)
		used += rw
	}
	if b.Len() > 0 {
		out = append(out, style.Render(b.String()))
	}
	return out
}

// ---------------------------------------------------------------------------
// Footers
// ---------------------------------------------------------------------------

// liveFooter is the honest ending. Every non-success terminal state gets one,
// and every one of them names the Retry chord — an affordance with no key
// behind it is worse than none, because it reads as a button.
func liveFooter(th theme.Theme, e *liveEntry, width int) []string {
	var role theme.Role
	var text string
	switch {
	case e.status == statusCancelled:
		role, text = theme.TextDim, "⚠ interrupted"
	case e.status == statusFailed && e.errCode == boundsExceededCode:
		// A bounded loop that reached ITS OWN configured limit and kept
		// what it had. Neither a fault nor silence — three states that the
		// first cut rendered with the same "⚠" and near-identical wording,
		// so a research run that answered as far as its step budget allowed
		// read as a crash. The backend's own message names the bound.
		role, text = theme.Warning, "⚠ stopped at its limit — "+e.errText
	case e.status == statusFailed:
		role, text = theme.Error, "✗ failed — "+e.errText
	case e.status == statusTimedOut:
		// Strictly silence: nothing at all arrived for the whole budget.
		role, text = theme.Warning, "⚠ no response — nothing arrived"
	default:
		return nil
	}
	line := th.Style(role).Render(text) + "  " +
		th.Style(theme.AccentPrimary).Render("[Retry ^R]")
	return []string{padCells(line, width)}
}

// ---------------------------------------------------------------------------
// The pending bubble
// ---------------------------------------------------------------------------

// renderPending is the dimmed bubble for the queued message: the user's own
// box (so it reads as theirs, not as a reply), dimmed, with what can be done
// to it written on it. It sits below the streaming reply because it is the
// last entry in the window — see entryAt.
func (w *Workspace) renderPending(width int, focused bool) []string {
	th := w.theme
	q := w.queued
	if q == nil {
		return nil
	}
	text := q.text
	if q.agentID != "" {
		text = "@" + q.agentID + " " + text
	}

	boxWidth := int(float64(width) * userBoxWidthRatio)
	if boxWidth < 8 {
		boxWidth = width
	}
	inner := boxWidth - 2*genericBoxPadding - 2
	if inner < 4 {
		inner = 4
	}
	dim := th.Style(theme.TextDim)

	body := wrapWords(splitWords(text, dim), inner)
	body = append(body, dim.Render("◌ queued · ctrl+e edit · esc discard"))

	edgeRole := theme.Queued
	if focused {
		edgeRole = theme.BorderFocus
	}
	box := messageBox(th.Style(edgeRole), boxWidth, body, genericBoxPadding)

	margin := strings.Repeat(" ", width-boxWidth)
	out := make([]string, 0, len(box))
	for _, l := range box {
		out = append(out, margin+l)
	}
	return out
}
