package app

// F4's focus arbitration between the Chat workspace's query input and F3's
// message-browsing hotkeys (j/k/y/G), which collide letter-for-letter with
// ordinary typing now that there is something to type into.
//
// The root never reaches into a workspace's internal focus state — it asks
// through FocusAware, the same narrow-interface pattern Cards() already
// established for the right pane, and it routes these two messages exactly
// like PasteMsg/EnvelopeMsg: an action's Run constructs one, and Root.Update's
// unhandled-message fallthrough (the code after the big switch in Update)
// delivers it to the active workspace without Root needing a case for it.

// EnterBrowseMsg asks the active workspace to move focus OFF its input and
// onto message browsing. Chat's input becomes unfocused; j/k/up/down/G/y
// resume moving message focus and copying, the same behaviour F3 verified
// before any input existed to conflict with.
type EnterBrowseMsg struct{}

// FocusInputMsg asks the active workspace to move focus back onto its input.
type FocusInputMsg struct{}

// FocusAware is implemented by a workspace whose Cards()-style self-report
// lets an action's When predicate decide availability without Root reading
// any field directly. Chat implements it; a workspace that has nothing to
// focus (the Placeholder, the future Editor) simply does not, and a type
// assertion against it fails harmlessly.
type FocusAware interface {
	InputFocused() bool
}
