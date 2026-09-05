package app

// F5's seams between the root and whichever workspace is streaming.
//
// The same narrow-interface pattern focus.go (F4) and draft.go (F4's Bug 17)
// already established: the root never reads a workspace's internal fields,
// it asks a one-method interface through a type assertion. A workspace with
// nothing to stream simply does not implement these, and every branch below
// falls through to the behaviour that existed before F5.

import tea "github.com/charmbracelet/bubbletea"

// StreamAware is "is something running right now".
//
// The root needs this for exactly one thing: the header animation, which
// LAYOUT.md draws and F05-streaming-queue.md turns into an activity
// indicator — it runs while streaming and FREEZES when idle. A frozen
// animation is not a cosmetic choice; a tick that keeps firing on an
// untouched app is the idle-CPU failure F5's verification measures.
type StreamAware interface {
	// Streaming reports whether any task this workspace owns is in a
	// non-terminal state.
	Streaming() bool
}

// QueueAware exposes only whether a queued message is waiting.
//
// esc is already bound to chat.browse (F4). At queue capacity it has to
// discard the pending message instead — F05's "editable (ctrl+e) and
// discardable (esc) until it fires" — and the root cannot choose between
// the two without knowing this one bit. It gets the bit, not the queue.
type QueueAware interface {
	HasPending() bool
}

// CancelStreamMsg asks the active workspace to cancel whatever it is
// streaming. Ctrl+C's whole payload.
//
// The no-op-when-idle rule lives in the WORKSPACE, not in the action's
// When predicate: a predicate that goes false when nothing streams would
// make ctrl+c report "Cancel Stream is not available right now", and F5
// requires that nothing happens — not that something is said.
type CancelStreamMsg struct{}

// DiscardPendingMsg drops the queued message (esc, at capacity).
type DiscardPendingMsg struct{}

// EditPendingMsg pulls the queued message back into the input (ctrl+e).
type EditPendingMsg struct{}

// RetryMsg re-sends whatever was interrupted, failed, or timed out — the
// `[Retry]` affordance F5 attaches to all three states.
type RetryMsg struct{}

func cancelStreamCmd() tea.Cmd   { return func() tea.Msg { return CancelStreamMsg{} } }
func discardPendingCmd() tea.Cmd { return func() tea.Msg { return DiscardPendingMsg{} } }
func editPendingCmd() tea.Cmd    { return func() tea.Msg { return EditPendingMsg{} } }
func retryCmd() tea.Cmd          { return func() tea.Msg { return RetryMsg{} } }

// streaming reports whether the active workspace has anything running.
func (r *Root) streaming() bool {
	sa, ok := r.current().(StreamAware)
	return ok && sa.Streaming()
}

// hasPending reports whether the active workspace holds a queued message.
func (r *Root) hasPending() bool {
	qa, ok := r.current().(QueueAware)
	return ok && qa.HasPending()
}
