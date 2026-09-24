package chat

import "github.com/lazyworkoholic/point/frontend/internal/protocol"

// connectionDown is the ONE check behind both halves of "the backend is not
// connected": the viewport's banner (render.go) and a send refused because of
// it (stream.go's writeSend).
//
// Before this, the banner was a side effect of WHICH failure path a send
// happened to take. A send made after the client noticed the exit failed in
// Client.Send, and writeSend stored that error as w.loadErr — the banner,
// drawn in place of the whole transcript. A send made a moment earlier, while
// the exit was not yet noticed, wrote into the dead process's pipe without
// error, was ended by BackendLostMsg a moment later — footer only — and
// never set a banner at all. Identical repros, different screens, decided by
// milliseconds. (connection_banner_test.go reproduces both windows.)
//
// The input is the connection state Root already owns and pushes, Seq-
// guarded, on every transition (connecting, connected, lost, failed). Before
// the first session has loaded it is never "down": that screen is the
// loading state's, not an outage.
func (w *Workspace) connectionDown() bool {
	return w.bootstrapped && w.connState != "" && w.connState != protocol.StateConnected
}

// connectionNotice is the banner's text, and the reason recorded on a send
// refused while connectionDown is true — the same words in both places.
func (w *Workspace) connectionNotice() string {
	if w.connState == protocol.StateFailed {
		return "backend not connected — retry from the status bar"
	}
	return "backend not connected — restarting…"
}
