// F9a — the result card (UI-06), render and discard only.
//
// What a finished task produced, opened from the tray, scrollable, with an
// honest cost label. The card's attach and save axes are NOT here — see the
// note on the missing action row below.
package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// ResultCard is an Overlay, not a Popup: it renders one body of prose with a
// scroll offset, which is the opposite shape from a filterable list of rows.
//
// # Why the body is wrapped plain text and not rendered markdown
//
// F09's mockup says "rendered markdown result". The markdown renderer is
// chat\markdown.go, and `chat` imports `app` — so this package cannot reach
// it, and the only ways to get it here are to move it into a third package
// or to move this card into `chat`. Moving the renderer is a refactor of F3's
// shipped code in an F9 session; moving the card into `chat` contradicts the
// reason the tray is Root's at all (a task whose originating view is unloaded
// must still open, which is Done-when #1).
//
// So: wrapped plain text. Indentation IS preserved (see wrapParagraphs — that
// one is not a packaging problem, and losing it makes code read as DIFFERENT
// code), but there is no syntax colour and no box frame around a code block.
//
// **Deferred to F16 by decision, not left open.** `F16-polish.md` carries the
// item and the two packaging options: extract the renderer into
// `internal\markdown\` (preferred — F11's memory popup will want it too), or
// give `app` a minimal one of its own. The box frame is free once the card can
// call a renderer, since box.go's CodeBorder/CodeText roles already exist in
// every palette and the transcript already uses them.
//
// # Why there is no action row
//
// F09's mockup's footer is `a attach full · s attach summary · m save to
// memory · r re-run · d discard · esc close`. Four of those six have no
// backend:
//
//   - attach (either mode): context_attachments.source_type is `file`-only
//     and managers\context.py:564 refuses anything else outright. A task
//     result is not a file.
//   - save to memory: nothing writes memory_records. B11's agent_proposals
//     is insert-only with one status, and both its files say accept/reject
//     is B13's.
//   - re-run: re-sending a task's own question needs the question routed
//     back through the originating session's input, which is the workspace's
//     surface, not Root's — and the session may not be loaded.
//
// Rendering them as keys that do nothing would be worse than omitting them:
// F04's standing rule in this codebase is that a hint must never advertise a
// binding that does not currently work. So the footer shows the two that do.
type ResultCard struct {
	row      TaskRow
	question string
	body     string
	tokens   int

	// scroll is the first visible line of the wrapped body. Lines, not
	// rows: the wrap is recomputed per render (the overlay's width is not
	// known until View is called), so an offset into the SOURCE would move
	// under the user on a resize.
	scroll int

	// lastWrapW / wrapped cache the wrap for one width. Not an
	// optimization: View is called every frame and the scroll clamp in
	// Update needs to know how many wrapped lines exist, which is only
	// knowable after a wrap at the current width. Without the cache,
	// Update would have to guess a width or re-wrap on every key.
	lastWrapW int
	wrapped   []string

	// bodyRows is the page height the last render used. Remembered so
	// maxScroll clamps against the page actually on screen and PgUp/PgDn
	// move by a real page, rather than both guessing a constant.
	bodyRows int

	// back is the overlay d/esc returns to — the tray this card was opened
	// from. nil means "close outright", which is what a card opened from
	// anywhere else should do, and is the behaviour this field replaced for
	// the tray case specifically.
	//
	// Held as an Overlay rather than a bool-plus-a-rebuild: the tray is
	// still live (its items, its filter and its selection are all intact),
	// so returning the SAME instance puts the user back exactly where they
	// were. Rebuilding one would reset the filter they typed and re-run
	// openTaskTray's fit check and refresh for a keypress that chose
	// nothing.
	back Overlay
}

// newResultCard joins a task to its messages and builds the card.
//
// An empty body is a real outcome, not an error: a task can complete having
// persisted no assistant text (a cancelled turn the accumulator salvaged
// nothing from, or a row whose session predates the task_id column). The card
// opens anyway and says so — Done-when #1 is that the result OPENS, and a
// card that refuses to open cannot be distinguished from a tray that is
// broken.
func newResultCard(row TaskRow, msgs []trayMessage) *ResultCard {
	question, answer, tokens := splitTaskMessages(row.ID, msgs)
	if answer == "" {
		answer = "This task persisted no result text.\n\n" +
			"A task can finish without one — a cancelled or failed turn may " +
			"have salvaged nothing, and rows written before the task_id " +
			"column existed cannot be joined to their own reply at all. " +
			"Nothing has been deleted; this is what the transcript holds."
	}
	return &ResultCard{row: row, question: question, body: answer, tokens: tokens}
}

// MinSize is LAYOUT.md's overlay table: "result card 60×18" — the FLOOR.
func (c *ResultCard) MinSize() (int, int) { return 60, 18 }

// PreferredSize: whatever it is offered — the transcript pane's rect less a
// cell per side, the same answer the tray gives.
//
// Two reasons it is not 60×18. A result is prose, and prose in a 56-cell
// column inside a 160-column terminal is a worse read than the pane could
// give it for free. And since a code block in a result renders as plain
// indented text (see this file's own note on why markdown is not available
// here), width is the only thing standing between a wrapped code line and a
// legible one.
//
// The transcript pane's width specifically, rather than the terminal's,
// because a result IS transcript content read at transcript width — the
// reader's eye is already calibrated to that measure, and matching it is
// what stops the card feeling like a different application.
//
// Identical to the tray's on purpose: the two swap back and forth, and a
// size change across that swap reads as the panel jumping.
func (c *ResultCard) PreferredSize(availW, availH int) (int, int) {
	return matchTranscriptPane(availW, availH)
}

// Title is the card's own header. The TOPIC is the user's own question,
// which is recoverable here and not in the tray — the tray has only
// ListTasks, which carries no prompt field (see TaskLabel's doc), while this
// card has already fetched the session's messages.
func (c *ResultCard) Title() string {
	label := TaskLabel(c.row)
	if c.question == "" {
		return label
	}
	// One line only: a pasted multi-line question would otherwise push the
	// border apart. Truncation happens in View, against the real width.
	topic := strings.TrimSpace(strings.SplitN(c.question, "\n", 2)[0])
	if topic == "" {
		return label
	}
	return label + " · " + topic
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

// Update handles scrolling, discard and close.
//
// `d` and `esc` do the same thing in this scope, and that is deliberate
// rather than an oversight. F09 distinguishes them because a card in the full
// spec can be ATTACHED or SAVED — discard then means "I have taken what I
// want from this, stop showing it" as against esc's "close for now". With
// neither axis built there is nothing for the two to differ about: the card
// is not pinned into a transcript, not attached to a context, and not saved
// anywhere, so both simply stop showing it. Both are bound because `d` is in
// the spec's footer and a key the spec promises must work. `d` additionally
// reports which card went (discardedResultCmd), which is the seam F9c's
// discard extends once there IS attach state to reconcile.
//
// Neither deletes anything. That is F09's Done-when #8, and it holds for a
// structural reason rather than a careful one: this card has no delete path
// to call — there is no event for removing a task or a message, and the
// result lives in the `messages` rows the backend owns. Discarding stops
// showing the card; the row is untouched, so reopening from the tray
// re-fetches the same text.
func (c *ResultCard) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		switch m.Type {
		case tea.KeyEsc:
			return c.dismiss(), nil
		case tea.KeyUp:
			c.scrollBy(-1)
			return c, nil
		case tea.KeyDown:
			c.scrollBy(1)
			return c, nil
		case tea.KeyPgUp:
			c.scrollBy(-c.pageSize())
			return c, nil
		case tea.KeyPgDown:
			c.scrollBy(c.pageSize())
			return c, nil
		case tea.KeyHome:
			c.scroll = 0
			return c, nil
		case tea.KeyEnd:
			c.scroll = c.maxScroll()
			return c, nil
		case tea.KeyRunes:
			switch strings.ToLower(string(m.Runes)) {
			case "d":
				return c.dismiss(), discardedResultCmd(c.row.ID)
			}
		}
		return c, nil
	case tea.MouseMsg:
		switch m.Button {
		case tea.MouseButtonWheelUp:
			c.scrollBy(-1)
		case tea.MouseButtonWheelDown:
			c.scrollBy(1)
		}
		return c, nil
	}
	return c, nil
}

// dismiss is what d/esc return in place of this card: the tray it came
// from, or nil to close outright when it came from somewhere else.
//
// Returning a different Overlay from Update is the swap S13d built for
// UI-09's context card, and root.go's setOverlay is explicit that it is the
// one place r.overlay is assigned and that it re-applies the MinSize floor —
// which matters here, because the tray and the card have different floors and
// a terminal can be wide enough for one and not the other.
func (c *ResultCard) dismiss() Overlay {
	if c.back == nil {
		return nil
	}
	return c.back
}

// discardedResultCmd tells Root which card was discarded.
//
// Root does not need it to clean anything up — nothing is deleted — but it
// is what lets the status line name the task, and it is the seam F9c's
// discard (which WILL have state to reconcile, once a card can be attached)
// extends rather than invents.
func discardedResultCmd(taskID string) tea.Cmd {
	return func() tea.Msg { return resultDiscardedMsg{taskID} }
}

type resultDiscardedMsg struct{ taskID string }

// pageSize is one screenful. Before the first render nothing knows the real
// body height, and MinSize's 18 rows less the card's four chrome rows is the
// smallest page the card can ever actually have — so a PgDn pressed in that
// window scrolls by no more than one true page, never past it.
func (c *ResultCard) pageSize() int {
	if c.bodyRows > 0 {
		return c.bodyRows
	}
	return 14
}

func (c *ResultCard) scrollBy(n int) {
	c.scroll += n
	if c.scroll > c.maxScroll() {
		c.scroll = c.maxScroll()
	}
	if c.scroll < 0 {
		c.scroll = 0
	}
}

// maxScroll is 0 until the first render, which is correct: with no wrap
// computed there is nothing known to scroll past, and clamping to 0 means a
// key pressed in that window does nothing rather than scrolling into blank
// space. The first View fixes it.
func (c *ResultCard) maxScroll() int {
	if c.lastWrapW == 0 {
		return 0
	}
	max := len(c.wrapped) - c.bodyRows
	if max < 0 {
		return 0
	}
	return max
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func (c *ResultCard) View(th theme.Theme, w, h int) string {
	// box.go's fitCells adds one cell of padding each side inside the
	// border, so the text budget is w-2 — the same accounting Popup.View
	// documents.
	textW := w - 2
	if textW < 1 {
		textW = 1
	}

	header := c.headerLine(th, textW)
	footer := th.Style(theme.TextDim).Render(
		runewidth.Truncate("↑/↓ scroll   d discard   esc close", textW, "…"))
	rule := th.Style(theme.BorderSubtle).Render(strings.Repeat("─", textW))

	// header + blank + body + rule + footer
	bodyRows := h - 4
	if bodyRows < 1 {
		bodyRows = 1
	}
	c.rewrap(textW, bodyRows)

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n\n")
	for i := 0; i < bodyRows; i++ {
		src := c.scroll + i
		if src < len(c.wrapped) {
			b.WriteString(padCells(
				th.Style(theme.TextPrimary).Render(c.wrapped[src]), textW))
		} else {
			b.WriteString(strings.Repeat(" ", textW))
		}
		b.WriteByte('\n')
	}
	b.WriteString(rule)
	b.WriteByte('\n')
	b.WriteString(footer)
	return b.String()
}

// headerLine is the glyph, the state, and the cost — "done · 2:04 · 12.4k
// tok". The token count is real here (summed from the messages' own
// `usage` columns in splitTaskMessages) and is omitted when it is zero
// rather than printed as "0 tok": a row written before usage was populated
// does not cost nothing, it costs an unknown amount, and the two must not
// render the same.
func (c *ResultCard) headerLine(th theme.Theme, width int) string {
	parts := []string{c.stateWord()}
	if d := TaskDuration(c.row); d != "" {
		parts = append(parts, d)
	}
	if c.tokens > 0 {
		parts = append(parts, formatTokens(c.tokens)+" tok")
	}
	glyph := th.Style(TaskRole(c.row.State)).Render(TaskGlyph(c.row.State))
	meta := th.Style(theme.TextDim).Render(strings.Join(parts, " · "))
	left := glyph + " " + th.Style(theme.TextSecondary).Render(TaskLabel(c.row))

	gap := width - visibleWidth(left) - visibleWidth(meta)
	if gap < 1 {
		return padCells(truncateCells(left, width), width)
	}
	return left + strings.Repeat(" ", gap) + meta
}

func (c *ResultCard) stateWord() string {
	switch c.row.State {
	case TaskStateFailed:
		if c.row.ErrorCode != "" {
			return "failed: " + c.row.ErrorCode
		}
		return "failed"
	case TaskStateCancelled:
		return "cancelled"
	default:
		return "done"
	}
}

// formatTokens is the mockup's own "12.4k" shape. Below 1000 it is the plain
// number — "0.4k" would be a rounding of something small enough to state
// exactly.
func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// rewrap recomputes the wrapped body when the width changed, and re-clamps
// the scroll offset against the new line count.
//
// Re-clamping here rather than only in scrollBy is what keeps a resize from
// leaving the view scrolled past the end: a terminal widened from 60 to 120
// columns roughly halves the line count, and an offset that was valid before
// would render nothing but blank rows with no key pressed to correct it.
func (c *ResultCard) rewrap(width, bodyRows int) {
	c.bodyRows = bodyRows
	if width != c.lastWrapW {
		c.wrapped = wrapParagraphs(c.body, width)
		c.lastWrapW = width
	}
	if c.scroll > c.maxScroll() {
		c.scroll = c.maxScroll()
	}
	if c.scroll < 0 {
		c.scroll = 0
	}
}

// wrapParagraphs word-wraps to a DISPLAY-CELL width, PRESERVING line breaks
// and blank lines.
//
// Not placeholder.go's wrapPlain, and not a replacement for it. That one runs
// strings.Fields over the whole string, which collapses every newline — right
// for the one-paragraph placeholder and status text it was written for, and
// wrong here: paragraph breaks and list lines are the only structure plain
// text has, and flattening them turns a research result into one wall. The
// two are kept separate rather than one generalised, because changing
// wrapPlain would change F1's placeholder rendering in an F9 session.
//
// Display cells, not len(): rule 8 of the frontend's cross-cutting rules —
// CJK and emoji are two cells each, and a wrap computed on byte or rune
// counts tears every border in the app. A single word longer than the width
// (a URL, a long path) is hard-split at the cell boundary rather than
// allowed to overflow, because overflowing is what breaks the border.
func wrapParagraphs(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(para) == "" {
			out = append(out, "")
			continue
		}
		// LEADING WHITESPACE IS CONTENT, not formatting to be normalised
		// away. strings.Fields drops it, which turned an indented code
		// block in a result into left-flush lines — so
		//
		//	async def main():
		//	    await asyncio.gather(a(), b())
		//
		// rendered with `await` at the same level as `async def`, which
		// does not merely look worse, it reads as different code. With
		// markdown unavailable here (see this file's own note on why), the
		// indentation is the ONLY structure a code block has left, and
		// dropping it is what makes plain text actively misleading rather
		// than merely plain.
		//
		// Captured once per source line and re-applied to every wrapped
		// continuation of it, so a long indented line's tail stays inside
		// its own block instead of escaping to column zero.
		indent := para[:len(para)-len(strings.TrimLeft(para, " \t"))]
		// Tabs are expanded, not emitted: a terminal advances to its own
		// next tab stop, which no width calculation here can predict, so a
		// literal tab would make every downstream padCells wrong by however
		// much the terminal decided.
		indent = strings.ReplaceAll(indent, "\t", "    ")
		body := width - runewidth.StringWidth(indent)
		if body < 1 {
			// Indented further than the box is wide. Keep the text and drop
			// the indentation — the alternative is a column of single
			// characters.
			indent, body = "", width
		}

		line := ""
		flush := func() {
			out = append(out, indent+line)
			line = ""
		}
		for _, word := range strings.Fields(para) {
			switch {
			case line == "":
				line = word
			case runewidth.StringWidth(line)+1+runewidth.StringWidth(word) <= body:
				line += " " + word
			default:
				flush()
				line = word
			}
			for runewidth.StringWidth(line) > body {
				head := runewidth.Truncate(line, body, "")
				if head == "" { // pathological: a single rune wider than body
					break
				}
				rest := strings.TrimPrefix(line, head)
				line = head
				flush()
				line = rest
			}
		}
		if line != "" {
			flush()
		}
	}
	return out
}
