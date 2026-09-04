package app

// DraftFlusher is implemented by a workspace that debounces some unsaved
// state to disk and needs a synchronous escape hatch from that debounce —
// the same FocusAware-style narrow interface (focus.go), read through a
// type assertion so Root never needs to know chat's own draft-save details.
//
// Bug 17 (a verification round, F4): app.quit returned tea.Quit with
// nothing flushing chat's debounced draft save first. A still-sleeping
// tea.Tick from the last keystroke never gets delivered once the program
// has already been told to quit — Update simply never runs again — so
// whatever the PREVIOUS successful debounced write held stayed on disk,
// stale by however much was typed since. app.quit's Run calls FlushDraft
// here, synchronously, before returning tea.Quit. A workspace with nothing
// to flush (the Placeholder, the future Editor) simply does not implement
// this, and the type assertion fails harmlessly, same as FocusAware's.
type DraftFlusher interface {
	FlushDraft()
}
