package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// The explain panel: a right-side panel INSIDE the Editor.
//
// F15's Build: "Rendered in a right-side overlay panel inside the Editor --
// a rounded box with its title on the edge, scrollable, dismissed with
// `esc`, with a `send to chat` action that copies the exchange into the
// current chat session. Rationale: switching workspaces to read an
// explanation about code you are looking at defeats the purpose."
//
// # Why it is a SPLIT of the Code File pane, not an app.Overlay
//
// `app.Overlay` would have been less code and would have got `esc` for free
// (Root gives an open overlay every key, esc included). It is wrong for
// this surface on both counts the spec cares about:
//
//   - an Overlay is centred over the whole transcript/main area by
//     `overlayRect`, so it would cover the code it is explaining, which is
//     the thing the panel exists not to do;
//   - the explanation is about the file in the pane beside it. Reading them
//     side by side is the feature.
//
// So the panel is composed by `View`: the Code File box takes the left
// share of `layout.Main` and the panel takes the right, joined by the same
// `sideBySide` the right pane already uses. Nothing overlays styled cells
// (which would mean cell-accurate surgery on rendered rows), and
// `layout.Compute` is untouched -- the workspace is subdividing a rect it
// was already handed, which is exactly the authority a workspace has over
// its own rects.
//
// # It holds the keyboard while it is open
//
// A reading surface that did not would scroll with keys that also type into
// the buffer. So while the panel is open it takes the keys, and `esc`
// closes it and hands them back. That is also what makes `c` (send to chat)
// unambiguous rather than a letter that would otherwise be inserted into
// the code.
//
// This is the one reason `CapturesKey` had to grow a conditional claim --
// see its own doc in editor.go.
type explainPanel struct {
	// title and question are the header: the file and what was asked.
	title    string
	question string
	// scope and lines are the other two header FIELDS: which block was
	// explained, and which line range.
	//
	// Three distinct fields rather than one decorative sentence, which is
	// what this shipped with ("About app.py - explain this"). A reader
	// checking whether the right code was explained is answering a
	// different question from a reader re-reading what they asked, and a
	// combined string makes both of them parse a sentence to find one
	// fact. The scope and the range come from the invocation, which took
	// them from the same lines and range that were actually sent.
	scope string
	lines string

	// body is the reply, soft-wrapped at render time rather than stored
	// wrapped: the panel's width changes with the terminal and a stored
	// wrap would be the wrong one after any resize.
	body string

	// streaming is true until the task reached a terminal state. Shown,
	// because a panel that stops growing looks finished whether or not it
	// is.
	streaming bool
	// failure, when set, is rendered instead of the body.
	failure string

	// scroll is the first visible wrapped row, and rows the body height
	// the last render used -- kept so paging moves by the page actually on
	// screen, `ResultCard`'s own reason for keeping bodyRows.
	scroll int
	rows   int
	// lastWrap is the wrapped body from the last render, so scrolling can
	// clamp against a real row count without re-wrapping on every keypress.
	lastWrap []string
}

func newExplainPanel(inv *invocation) *explainPanel {
	return &explainPanel{
		title:     inv.name,
		question:  inv.question,
		scope:     inv.scope,
		lines:     fmt.Sprintf("lines %d-%d", inv.shownStart, inv.shownEnd),
		streaming: true,
	}
}

// setText replaces the body with everything that has arrived.
//
// Whole-replace, not append: the caller hands over `invocation.text`, which
// is the accumulated reply, so the panel is a pure function of it. Same
// property the ghost has and for the same reason (Done-when 3b).
func (p *explainPanel) setText(s string, done bool) {
	p.body = s
	if done {
		p.streaming = false
	}
}

func (p *explainPanel) fail(reason string) {
	p.failure = reason
	p.streaming = false
}

// exchange is what `send to chat` hands over: the question and the answer,
// quoted, as one block of text.
//
// Markdown blockquote syntax, because the destination is Chat's input and
// Chat renders markdown (chat\markdown.go). The file and the region are
// included: an explanation pasted into a session with no indication of what
// it was about is a paragraph about nothing.
func (p *explainPanel) exchange() string {
	var b strings.Builder
	b.WriteString("About ")
	b.WriteString(p.title)
	b.WriteString(" — ")
	b.WriteString(p.question)
	b.WriteString("\n\n")
	for _, line := range strings.Split(strings.TrimRight(p.body, "\n"), "\n") {
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// key handles one keystroke while the panel is open, reporting whether it
// is dismissed and any command it produced.
func (w *Workspace) panelKey(k tea.KeyMsg) tea.Cmd {
	p := w.panel
	if p == nil {
		return nil
	}
	switch k.String() {
	case "esc":
		// esc cancels a task that is still running, and merely closes one
		// that has finished.
		//
		// esc ONLY: ctrl+c is COPY in this workspace and never reaches
		// here (handleKey takes it first -- see copy.go for why a scoped
		// ctrl+c was rejected).
		//
		// esc used to only HIDE the panel, which left the task running
		// to completion in the background -- so the Task card showed an
		// explain the user had visibly dismissed as "completed", while
		// the equivalent gesture on a fill (esc while the ghost grows)
		// showed "cancelled". Two modes, two answers, for the same key
		// and the same intent. F15 pairs the keys explicitly for fill
		// ("`esc` or `ctrl+c`"), and there is no reason the explain
		// panel should read that pairing differently.
		//
		// Dismissing a reply you are not going to read IS cancelling the
		// work: nothing downstream wants the rest of it, and the turn is
		// still persisted by the backend either way.
		//
		// taskRunning, NOT liveInvocation: the narrow question, asked by
		// its own name. A fill stays "live" across the gap between its
		// terminal event and its proposal (see liveInvocation), and
		// sending a CancelTask inside that gap would be cancelling a
		// task the backend has already finished.
		if w.taskRunning() {
			// cancelInvocation closes the panel itself.
			return w.cancelInvocation()
		}
		w.panel = nil
		return nil
	case "c":
		return w.sendPanelToChat()
	case "up", "k":
		p.scrollBy(-1)
	case "down", "j":
		p.scrollBy(+1)
	case "pgup":
		p.scrollBy(-p.pageRows())
	case "pgdown":
		p.scrollBy(+p.pageRows())
	case "home":
		p.scroll = 0
	case "end":
		p.scrollBy(len(p.lastWrap))
	}
	return nil
}

func (p *explainPanel) pageRows() int {
	if p.rows > 1 {
		return p.rows
	}
	return 10
}

func (p *explainPanel) scrollBy(delta int) {
	p.scroll += delta
	max := len(p.lastWrap) - 1
	if p.scroll > max {
		p.scroll = max
	}
	if p.scroll < 0 {
		p.scroll = 0
	}
}

// panelWidth is how much of the Code File pane the panel takes.
//
// Two fifths, bounded: never under 30 cells (below that an explanation is
// one word per line) and never more than what leaves the code 24 cells to
// show. Under 56 cells of main rect there is no useful split at all and the
// panel takes the whole pane -- the explanation is what the user asked for,
// and a 20-cell code column beside a 30-cell panel shows neither.
func panelWidth(mainW int) int {
	if mainW < 56 {
		return mainW
	}
	w := mainW * 2 / 5
	if w < 30 {
		w = 30
	}
	if w > mainW-24 {
		w = mainW - 24
	}
	return w
}

// panelBox renders the panel into its rect.
func (w *Workspace) panelBox(r layout.Rect) string {
	p := w.panel
	if p == nil || r.IsZero() {
		return ""
	}
	th := w.theme
	inner := r.W - 2 - 2 // borders, then paneBox's one cell of padding
	rows := r.H - 2
	if inner <= 0 || rows <= 0 {
		return paneBox(w.edge(true), w.label(true), r, "Explain", nil, 1)
	}

	// The question WRAPS rather than being cut.
	//
	// It was one truncated row, and F15's gate reported the result as text
	// running past the panel's border with "the full text is never visible
	// no matter how it's scrolled". The cut was real (measured: a 32-cell
	// panel showed "explain why this function vali" and nothing more of
	// it) and scrolling could never have revealed the rest, because the
	// scroll offset belongs to the BODY -- the head is not part of what
	// scrolls.
	//
	// Wrapping rather than horizontal scrolling, because this is a static
	// label and not an input: there is no caret whose position has to stay
	// visible, so there is nothing for a horizontal offset to be anchored
	// to. The fill prompt is the opposite case and keeps its own
	// horizontal scroll for exactly that reason (prompt.go).
	//
	// Capped at a third of the panel, so a long instruction cannot push
	// the answer off the surface it was asked on. The cap keeps the FIRST
	// rows -- the beginning is what identifies the question -- and marks
	// the cut.
	// Field 1: the scope and the range, on one row because they answer the
	// same question ("what was explained") and are both short. Field 2:
	// the question, quoted, on its own row(s) and never folded into field
	// 1 -- see the struct's own note.
	scopeRow := p.scope
	if p.lines != "" {
		if scopeRow != "" {
			scopeRow += " · " + p.lines
		} else {
			scopeRow = p.lines
		}
	}
	question := wrapCells(`"`+p.question+`"`, inner)
	if maxQ := rows / 3; maxQ >= 1 && len(question) > maxQ {
		question = question[:maxQ]
		question[maxQ-1] = truncateCells(question[maxQ-1], inner-1) + "…"
	}
	head := make([]string, 0, len(question)+2)
	if scopeRow != "" {
		// AccentSecondary, not the question's role: the two fields are
		// different kinds of fact and must not read as one wrapped
		// sentence.
		head = append(head, th.Style(theme.AccentSecondary).Render(
			padCells(truncateCells(scopeRow, inner), inner)))
	}
	for _, q := range question {
		head = append(head, th.Style(theme.TextSecondary).Render(padCells(q, inner)))
	}
	head = append(head,
		th.Style(theme.BorderSubtle).Render(strings.Repeat(borderHorizontal, inner)))
	footer := th.Style(theme.TextDim).Render(
		padCells(truncateCells("esc close   c send to chat   ↑/↓ scroll", inner), inner))

	// The budget, spent in priority order, exactly as the diff card's View
	// does and for the same reason F13b's stale dialog records: the footer
	// is the keys and is dropped last, the head is trimmed from the front,
	// and the total is exactly `rows` at every height.
	body := rows - len(head) - 1
	if body < 1 {
		head = nil
		body = rows - 1
	}
	if body < 1 {
		head, body = nil, rows
	}

	text := p.body
	role := theme.TextPrimary
	if p.failure != "" {
		text, role = p.failure, theme.Error
	}
	wrapped := wrapCells(text, inner)
	if p.streaming {
		wrapped = append(wrapped, th.Style(theme.Running).Render("…"))
	}
	if len(wrapped) == 0 {
		wrapped = []string{th.Style(theme.TextDim).Render("waiting for the first token…")}
	}
	p.lastWrap, p.rows = wrapped, body
	if p.scroll > len(wrapped)-1 {
		p.scroll = len(wrapped) - 1
	}
	if p.scroll < 0 {
		p.scroll = 0
	}

	content := make([]string, 0, rows)
	content = append(content, head...)
	for i := 0; i < body; i++ {
		n := p.scroll + i
		if n >= len(wrapped) {
			content = append(content, "")
			continue
		}
		content = append(content, th.Style(role).Render(padCells(wrapped[n], inner)))
	}
	if rows > len(content) {
		content = append(content, footer)
	}

	title := "Explain · " + p.title
	if p.streaming {
		title = "Explaining · " + p.title
	}
	return paneBox(w.edge(true), w.label(true), r, title, content, 1)
}

// wrapCells soft-wraps text to `width` display cells, preserving its own
// line breaks and its leading indentation.
//
// Indentation is preserved rather than collapsed for F9a's own reason about
// its result card: "losing it makes code read as DIFFERENT code", and an
// explanation of code quotes code.
func wrapCells(s string, width int) []string {
	if width < 1 {
		return nil
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		words := strings.Fields(line)
		cur := indent
		for _, word := range words {
			candidate := word
			if strings.TrimSpace(cur) != "" {
				candidate = cur + " " + word
			} else {
				candidate = cur + word
			}
			if visibleWidth(candidate) > width && strings.TrimSpace(cur) != "" {
				out = append(out, cur)
				cur = indent + word
				continue
			}
			cur = candidate
		}
		// A single word longer than the width is hard-cut rather than
		// dropped: a 200-character path is exactly the thing an
		// explanation quotes.
		for visibleWidth(cur) > width {
			out = append(out, truncateCells(cur, width))
			cut := []rune(cur)
			kept := 0
			for i := range cut {
				if visibleWidth(string(cut[:i+1])) > width {
					break
				}
				kept = i + 1
			}
			cur = string(cut[kept:])
		}
		out = append(out, cur)
	}
	return out
}
