// S13d — UI-09, the context composition card. Expanded from the Status
// card via the context.expand leader chord, a click on the Status card, or
// (below 90 columns) enter/a click on rightPaneOverlay's own embedded
// Status section — see context_report.go and cards.go's rightPaneOverlay.
//
// A bespoke app.Overlay, not a Popup: this content is ambient data, not a
// selectable item list, the same call rightPaneOverlay itself already made
// for the same reason (cards.go's own doc comment on it).
package chat

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// contextOverlayMinW/H were sized against this overlay's own real content
// (six section rows, a rule, free, a blank, and comfortable room for a
// handful of eviction lines before "+N more" kicks in) rather than reused
// from Popup's 70×16 or squeezed to fit inside rightPaneOverlay's fixed
// 36×20 box — root.setOverlay (app/root.go) fit-checks a self-returned swap
// exactly like a fresh OpenOverlay, so this does not need to fit inside the
// fallback overlay's own footprint. 16 rows tall (14 content rows once the
// border is subtracted) gives the eviction section a real budget of about 5
// rows even after the six section rows + rule + free + blank take their 9 —
// found short at 14 during manual verification: the eviction area's real
// budget was only 3 rows, room for one item plus "+N more", nowhere near
// the "couple of eviction lines" this was originally sized for.
const (
	contextOverlayMinW = 42
	contextOverlayMinH = 16
)

// contextOverlay is UI-09. w.contextReport is guaranteed non-nil for the
// lifetime of this value — it is only ever constructed from inside
// Workspace.ContextExpandOverlay's guarded true branch (context_report.go),
// never directly, so View never needs a nil-report fallback of its own for
// THAT case. w.backendDown is a separate, later state that can turn true
// AFTER this overlay is already open (nothing closes it — see View below),
// so it is checked fresh on every render, not assumed away by the
// constructor guard.
type contextOverlay struct {
	w *Workspace
}

func newContextOverlay(w *Workspace) *contextOverlay {
	return &contextOverlay{w: w}
}

func (o *contextOverlay) MinSize() (int, int) { return contextOverlayMinW, contextOverlayMinH }

// IsContextView lets Root see UI-09 is on screen (app.ContextView) and keep
// it current — see root.go's refreshContextIfShown.
func (o *contextOverlay) IsContextView() bool { return true }

var _ app.ContextView = (*contextOverlay)(nil)

// Title carries the used/budget numbers on the box edge itself, matching
// F06-right-pane.md's own mockup ("Context · 12.4k / 32k") -- root.go's
// withOverlay calls this fresh on every render, so it stays live as the
// report updates without View needing to repeat the same numbers as its
// own first content row. Degrades to the bare title while the backend is
// down, for the same reason View's own rows do: the numbers on the box
// edge are exactly as stale as the numbers inside it once the connection
// that produced them is gone.
func (o *contextOverlay) Title() string {
	cr := o.w.contextReport
	if o.w.backendDown || cr == nil {
		return "Context"
	}
	return fmt.Sprintf("Context · %s/%s", formatTokens(cr.UsedTokens), formatTokens(cr.BudgetTokens))
}

// Update closes on esc only. Backend-down is deliberately NOT a reason to
// close here -- see View's own doc comment: the fix for a lost connection
// is degrading what is drawn, the same choice every other card in this
// package already makes, never dropping the surface the user had open.
func (o *contextOverlay) Update(msg tea.Msg) (app.Overlay, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
		// Closes entirely -- does NOT step back to rightPaneOverlay even if
		// that is what this overlay swapped in for (S13d's accepted
		// consequence of a same-slot swap over a back-stack: r.overlay is
		// one field, and returning nil is the one way any bespoke overlay
		// in this codebase already closes).
		return nil, nil
	}
	return o, nil
}

// View receives w,h already adjusted for the outer box's border by
// root.go's withOverlay (rect.W-2, rect.H-2) -- F07-popup-framework.md's
// own gotcha #3, which rightPaneOverlay's doc comment already explains in
// full: box()'s fitCells pads/crops to the interior AFTER this returns, so
// contentW here is w's OWN further -2 for this box's border-to-content
// padding, the same convention every bordered box in this codebase uses.
//
// Backend-down degrades to the SAME single dim "—" placeholder
// statusCardLines/taskCardLines/sessionsCardLines already use, rather than
// closing the overlay or leaving stale numbers on screen -- found live
// during manual verification: nothing here checked w.backendDown at all, so
// killing the backend while this overlay was open left the last real
// numbers frozen on screen indefinitely, the exact "stale data outlives the
// connection" failure F06's Done-when #7 exists to rule out for the other
// three cards. Recovery needs no special handling: View runs fresh every
// frame, so the placeholder clears back to real numbers the moment
// w.backendDown goes false again, without the overlay ever closing.
func (o *contextOverlay) View(th theme.Theme, w, h int) string {
	contentW := w - 2
	if contentW < 1 {
		contentW = 1
	}

	if o.w.backendDown {
		return strings.Join(clip([]string{th.Style(theme.TextDim).Render("—")}, h), "\n")
	}

	cr := o.w.contextReport
	if cr == nil {
		return "" // unreachable -- see the type's own doc comment
	}

	primary := th.Style(theme.TextPrimary)
	dim := th.Style(theme.TextDim)

	barWidth := contentW - 22
	switch {
	case barWidth < 4:
		barWidth = 4
	case barWidth > 20:
		barWidth = 20
	}

	// Section rows are NOT run through runewidth.Truncate: `bar` already
	// carries embedded ANSI from tokenBar's own two-tone styling, and
	// truncating a string that already contains escape codes (rather than
	// plain text styled AFTER truncation, the order every other card in
	// this package uses -- e.g. sessionsCardLines) can cut mid-escape-
	// sequence. Unlike the eviction lines below, this row's shape is fully
	// our own -- six fixed labels plus a "(count)" suffix, never
	// backend-supplied free text -- so its width is bounded by construction
	// and does not need generic truncation.
	var out []string
	for _, key := range contextSectionOrder {
		label := contextSectionLabel[key]
		if count := cr.Counts[key]; count > 1 {
			label = fmt.Sprintf("%s (%d)", label, count)
		}
		tokens := cr.Sections[key]
		bar := tokenBar(th, tokens, cr.UsedTokens, barWidth)
		line := fmt.Sprintf("%-18s %7d  ", label, tokens)
		out = append(out, primary.Render(line)+bar)
	}

	out = append(out, dim.Render(strings.Repeat("─", contentW)))
	out = append(out, primary.Render(fmt.Sprintf("%-18s %7d", "free", cr.FreeTokens)))
	out = append(out, "")

	// Whatever is left after the fixed rows above is the eviction section's
	// REAL budget -- not a fixed constant guessed at design time. This is
	// what bug 1 from manual verification actually was: a fixed cap of 3
	// eviction rows, checked independently of how much room the surrounding
	// content had already used, meant the outer clip(out, h) below did the
	// ACTUAL truncating -- silently, well past evictionSection's own count,
	// with no "+N more" for what it cut. evictionSection is now handed the
	// true remaining budget and is the only thing allowed to drop rows, and
	// it always says how many it dropped.
	budget := h - len(out)
	out = append(out, evictionSection(th, cr.NextEvictions, contentW, budget)...)

	return strings.Join(clip(out, h), "\n")
}

// evictionSection renders next_evictions within an exact row budget,
// mirroring taskCardLines/capTerminalTail's own convention (cards.go): the
// number of rows dropped is always reported, never silently absorbed by an
// outer clip. `budget` includes the "next turn evicts:" header row itself.
func evictionSection(th theme.Theme, items []wireContextItem, contentW, budget int) []string {
	dim := th.Style(theme.TextDim)

	switch {
	case budget < 1:
		// No room for even the header. Dropping the whole section here is
		// not the silent-truncation bug this function exists to prevent --
		// that bug was a PARTIAL drop (some real entries shown, some cut,
		// nothing said). A budget this tight is an unreachable MinSize
		// misconfiguration, not normal operation; see contextOverlayMinH's
		// own doc comment for the numbers that keep this from happening in
		// practice.
		return nil
	case len(items) == 0:
		// Mirrors context.py describe()'s own "evicted: nothing" -- B8's
		// own wording for the same fact, never a second phrase for it.
		return []string{dim.Render("next turn evicts: nothing")}
	}

	primary := th.Style(theme.TextPrimary)
	itemBudget := budget - 1 // one row reserved for the header
	if itemBudget < 1 {
		// Room for the header but not even one item plus a possible
		// "+N more" line -- fold the count into the header itself rather
		// than showing a header with nothing under it.
		noun := "items"
		if len(items) == 1 {
			noun = "item"
		}
		return []string{dim.Render(fmt.Sprintf("next turn evicts: %d %s", len(items), noun))}
	}

	keep := len(items)
	hidden := 0
	if keep > itemBudget {
		keep = itemBudget - 1 // one item row becomes the "+N more" line instead
		if keep < 0 {
			keep = 0
		}
		hidden = len(items) - keep
	}

	out := make([]string, 0, 2+keep)
	out = append(out, dim.Render("next turn evicts:"))
	for _, item := range items[:keep] {
		line := fmt.Sprintf("  %s %s — %s", item.Kind, item.Ref, item.Reason)
		out = append(out, primary.Render(runewidth.Truncate(line, contentW, "…")))
	}
	if hidden > 0 {
		out = append(out, dim.Render(fmt.Sprintf("  +%d more", hidden)))
	}
	return out
}
