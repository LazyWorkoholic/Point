package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// WorkspaceID is layout's, aliased so callers of this package do not have to
// import layout for one type. It lives below both packages because Compute
// branches on which workspace is active.
type WorkspaceID = layout.WorkspaceID

const (
	WorkspaceChat   = layout.WorkspaceChat
	WorkspaceEditor = layout.WorkspaceEditor
)

// Workspace is project_idea.md §8's interface, with one deliberate change.
//
// §8 writes View(Rect). A workspace owns MORE than one rect — Main, Secondary,
// and one per right-pane card — so a single Rect cannot express what it has to
// draw, and passing it one would force the workspace to derive the others,
// which is exactly the authority Compute is supposed to hold. It is handed the
// whole computed Layout instead and renders into the rects it recognises.
//
// The two rules that keep this from rotting are unchanged: the root never
// reads a workspace's internal fields, and workspaces never talk to each other
// — they emit messages the root routes.
type Workspace interface {
	Init() tea.Cmd
	Update(tea.Msg) (Workspace, tea.Cmd)
	View(layout.Layout) string

	// Suspend returns roughly two kilobytes: scroll position, draft text,
	// open files, cursor. Never the content — Resume rehydrates from the
	// database, which is the same code path as opening a week-old session.
	Suspend() ([]byte, error)
	Resume([]byte) error

	ID() WorkspaceID

	// Cards is what this workspace wants in the right pane. It is read by the
	// root and handed to Compute, which is how "one rect per card" stays a
	// layout decision made from data the workspace supplies rather than from
	// knowledge Compute should not have.
	Cards() []layout.CardSlot
}

// Overlay is LAYOUT.md's overlay contract.
//
// The root refuses to open one that does not fit and shows the ACTUAL numbers
// — "memory needs 60×16 — terminal is 48×20" — because a popup that silently
// does not appear is indistinguishable from a broken key binding.
//
// F7 builds the shared Popup on top of this. F1 owns only the interface, the
// fit check, and the scrim.
// View is handed the theme for the same reason every other component is: an
// overlay that returns unstyled text is not theme-neutral, it is hardcoded to
// the terminal's default foreground, and no palette can repaint it.
type Overlay interface {
	MinSize() (w, h int)
	Title() string
	View(th theme.Theme, w, h int) string
	Update(tea.Msg) (Overlay, tea.Cmd)
}

// ThemeAware is implemented by a workspace that keeps its own copy of the
// theme — every real Workspace does, since View takes a layout.Layout, not a
// theme.Theme, and renders using whatever it was constructed with. Settings'
// Appearance tab (F10) changes Root's own r.theme directly, which repaints
// Root's own chrome (header, status bar, overlays) for free; a workspace
// holding a stale copy would not otherwise ever find out. Root asks for this
// the same way it asks FocusAware/OverlayProvider for theirs — a narrow
// interface, type-asserted, never assumed.
type ThemeAware interface {
	SetTheme(theme.Theme)
}

// OverlayProvider is implemented by a workspace whose right-pane cards must
// stay reachable when the pane auto-hides — LAYOUT.md's "Auto-hides below 90
// columns; still openable as an overlay" (F06-right-pane.md). The same
// narrow-interface pattern Cards() and FocusAware already use: the root asks
// "can you give me an overlay for this", never reaches into the workspace's
// own card state to build one itself.
type OverlayProvider interface {
	RightPaneOverlay() Overlay
}

// RightPaneFallback marks an Overlay as OverlayProvider's narrow-width
// stand-in for the real pane. The root uses it for two things a plain
// Overlay cannot express on its own:
//
//  1. Retiring itself the moment a resize widens back past the breakpoint
//     and the real pane fits again — closeOverlayIfTooSmall only closes an
//     overlay that no longer FITS, which is the opposite condition, so
//     without this an overlay opened while narrow stayed open, side by
//     side with the real pane, once the terminal grew back.
//  2. Counting as "the pane is visible" for refreshTasks's gate — the
//     whole reason this overlay exists is to show the same cards the real
//     pane shows, so the data behind it needs the same live refresh, not a
//     one-time snapshot taken at open time.
type RightPaneFallback interface {
	Overlay
	IsRightPaneFallback() bool
}
