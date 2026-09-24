package app

// ContextExpandable is implemented by a workspace that can produce UI-09,
// the context composition overlay, from real ContextReport data (S13d) —
// chat.Workspace is the only implementer today.
//
// The (Overlay, bool) shape mirrors OverlayProvider's, plus a guard:
// ContextExpandOverlay returns (nil, false) when there is nothing real to
// show yet (no report pulled or pushed) or the backend is down, so every
// caller — the leader action below, a click on the Status card, and
// rightPaneOverlay's own enter/click handling — asks through the SAME
// function rather than each re-deriving "is there data" from w.contextReport
// and w.backendDown separately. One gate, three call sites, not three gates
// that can drift.
type ContextExpandable interface {
	ContextExpandOverlay() (Overlay, bool)
}

// MouseOverlayTarget is implemented by a workspace that can turn a raw
// left-click (root-space screen coordinates, same frame dispatchMouse
// already works in) into an overlay to open. dispatchMouse asks this before
// forwarding an unclaimed click to the workspace's own Update, so the
// workspace decides WHAT rect it owns and whether it has real data behind
// it, while root keeps sole ownership of WHETHER an overlay actually opens
// (OpenOverlay's fit check) — root never reads a workspace's rect or
// content fields directly to make that decision itself.
type MouseOverlayTarget interface {
	OverlayForClick(x, y int) (Overlay, bool)
}

// ContextView marks an Overlay that renders the workspace's ContextReport
// (chat's UI-09), so Root can tell it is on screen without knowing its type —
// the same marker shape as RightPaneFallback.
type ContextView interface {
	Overlay
	IsContextView() bool
}

// ContextRefresher is implemented by a workspace that can pull a fresh
// ContextReport. Root calls it from refreshContextIfShown — the one place
// that decides WHEN; the workspace only knows HOW.
type ContextRefresher interface {
	RefreshContextReport()
}
