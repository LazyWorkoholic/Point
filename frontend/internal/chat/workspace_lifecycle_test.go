package chat

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// F12's wiring, against the REAL chat workspace rather than a fake one.
//
// app\lifecycle_test.go proves the lifecycle with test-only workspaces
// through the real registry, which is the only way to get more than one
// workspace before F13 lands. What it cannot prove is that the one real
// workspace that exists actually survives being loaded late — and that half
// is where lazy loading's one genuinely new failure mode lives. These tests
// are in this package for the same reason diagnostics_race_test.go is: this
// is the side of the import edge where both a real Root and a real
// chat.Workspace can be built.

// chatRegistered builds a Root wired exactly the way main.go wires it: chat
// as a FACTORY, not an instance.
func chatRegistered(t *testing.T, st config.State) (*app.Root, *[]*Workspace) {
	t.Helper()
	th := testTheme()
	// Every instance the factory builds, in order. The closure keeping its
	// own references is what lets these tests reach the real workspace
	// without app.Root growing an accessor for it -- "the root never reads
	// a workspace's internal fields" would be a strange rule to enforce
	// inside the package and then undo with an exported getter for a test.
	// It is also how a reload is distinguished from a workspace that was
	// never released.
	var built []*Workspace
	// The same client both sides get in main.go. It is never Start()ed, so
	// a send always fails -- which is enough to prove an ATTEMPT was made
	// (picker.go's own tests already rely on this) and is exactly what the
	// handshake-replay test needs to observe.
	client := protocol.NewClient(protocol.Options{})
	r, err := app.New(app.Options{
		Theme:  th,
		Client: client,
		State:  st,
		Workspaces: []app.WorkspaceDef{{
			ID:   app.WorkspaceChat,
			Name: "Chat",
			New: func() app.Workspace {
				w := New(Options{Theme: th, Client: client, State: st})
				built = append(built, w)
				return w
			},
			MinSize: app.Size{W: 50, H: 12},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Never the real %LOCALAPPDATA%\Point\state.json — CLAUDE.md is
	// explicit that nothing automated may touch it outside a scratch
	// profile.
	r.SaveWorkspaceStateFn = func(string, map[string]json.RawMessage) error { return nil }
	return r, &built
}

// unloadViaLeader presses the real binding rather than reaching for the
// action registry: leader -> u is what the episode shipped and what the
// verification gate types.
func unloadViaLeader(t *testing.T, r *app.Root) string {
	t.Helper()
	updateAndDrain(t, r, key(actions.Leader))
	return updateAndDrain(t, r, key("u"))
}

// Startup builds no chat workspace at all, and the chooser is what the user
// sees. The factory's own call count is the proof: an instance created and
// then not shown would still have paid for its construction, which is the
// cost F12 exists to avoid.
func TestChatIsNotConstructedUntilItIsChosen(t *testing.T) {
	r, built := chatRegistered(t, testProviderDefault)

	if len(*built) != 0 {
		t.Fatalf("chat was constructed %d times before anything was chosen", len(*built))
	}
	view := updateAndDrain(t, r, tea.WindowSizeMsg{Width: 100, Height: 30})
	if len(*built) != 0 {
		t.Fatalf("the first frame constructed chat %d times", len(*built))
	}
	if !strings.Contains(stripANSI(view), "Resume previous session?") {
		t.Fatalf("the startup chooser is not on screen:\n%s", stripANSI(view))
	}

	// Enter on the pre-selected button loads it, once.
	view = updateAndDrain(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if len(*built) != 1 {
		t.Fatalf("choosing Chat constructed it %d times, want 1", len(*built))
	}
	if !strings.Contains(stripANSI(view), "Query Area") {
		t.Fatalf("the real chat workspace did not render after being chosen:\n%s",
			stripANSI(view))
	}
}

// The one genuinely new failure mode lazy loading introduces.
//
// chat.Workspace does not bootstrap in Init(): its own doc says Update
// watches for protocol.ConnectedMsg and starts the
// ListSessions/GetSession/CreateSession sequence from there, because a query
// fails while the client is not connected. The backend handshake almost
// always completes while the chooser is still on screen, so the real
// ConnectedMsg arrives before the workspace exists and no second one is ever
// coming — the workspace would sit at "loading" forever with no session and
// no transcript. Before F12 this was unreachable, because a workspace
// existed before the first frame.
func TestChatBootstrapsWhenTheHandshakeHappenedBeforeItWasChosen(t *testing.T) {
	r, built := chatRegistered(t, testProviderDefault)
	updateAndDrain(t, r, tea.WindowSizeMsg{Width: 100, Height: 30})

	// The handshake lands while the chooser is up and nothing is loaded.
	updateAndDrain(t, r, protocol.ConnectedMsg{ProtocolVersion: protocol.Version})

	// Choosing Chat now has to replay it, or nothing ever queries.
	updateAndDrain(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	ws := last(t, built)
	// The client is not Start()ed, so the send itself fails and what is
	// observable is the ATTEMPT: loadErr set by doSend. Without the
	// handshake replay nothing is attempted at all and all three of these
	// stay at their zero values -- which is the workspace sitting at
	// "loading" forever with no session and no transcript.
	if len(ws.pending) == 0 && ws.sessionID == "" && ws.loadErr == "" {
		t.Fatalf("chat never began its bootstrap: pending=%d session=%q loadErr=%q",
			len(ws.pending), ws.sessionID, ws.loadErr)
	}
}

// last is the most recently constructed instance.
func last(t *testing.T, built *[]*Workspace) *Workspace {
	t.Helper()
	if len(*built) == 0 {
		t.Fatal("no workspace was ever constructed")
	}
	return (*built)[len(*built)-1]
}

// Suspend's bytes are F18's blob, and Root hands them back unread. This is
// the F12 half of F18's own Done-when #8, which that episode could only
// prove by calling the two methods directly because nothing called them.
func TestTheRealChatBlobSurvivesAnUnloadAndComesBackOnResume(t *testing.T) {
	r, built := chatRegistered(t, testProviderDefault)
	updateAndDrain(t, r, tea.WindowSizeMsg{Width: 100, Height: 30})
	updateAndDrain(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	before := last(t, built)
	blob, err := before.Suspend()
	if err != nil {
		t.Fatal(err)
	}
	// F18's shape, not a redefinition of it: version plus the anchor.
	var parsed struct {
		Version int `json:"version"`
		Anchor  *struct {
			AnchorSeq      int  `json:"anchor_seq"`
			RowOffset      int  `json:"row_offset"`
			PinnedToBottom bool `json:"pinned_to_bottom"`
		} `json:"anchor"`
	}
	if err := json.Unmarshal(blob, &parsed); err != nil {
		t.Fatalf("the blob is not F18's shape: %q: %v", blob, err)
	}
	if parsed.Version != 1 {
		t.Fatalf("blob version is %d, want F18's 1", parsed.Version)
	}
	if parsed.Anchor == nil {
		t.Fatalf("the blob carries no anchor: %q", blob)
	}

	// Unload, then reload. A second construction proves the first instance
	// was genuinely released rather than parked somewhere.
	view := unloadViaLeader(t, r)
	if !strings.Contains(stripANSI(view), "Resume previous session?") {
		t.Fatalf("unloading the only workspace did not bring the chooser back:\n%s",
			stripANSI(view))
	}
	updateAndDrain(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if len(*built) != 2 {
		t.Fatalf("chat was constructed %d times across unload+reload, want 2", len(*built))
	}
	after := last(t, built)
	if after == before {
		t.Fatal("the same instance came back; nothing was released")
	}
	// The anchor reached the new instance, which is all Resume can do at
	// Resume time -- there is no session, no window and no transcript to
	// address yet, so applyResumeAnchor consumes it at the end of
	// setSession. Observing the stored anchor is therefore the honest
	// assertion here.
	if after.resumeAnchor == nil {
		t.Fatal("the reloaded workspace has no stored resume anchor")
	}
}

// An empty blob is one of F18's five unusable-blob cases -- and the one a
// pre-F18 state file actually holds, since F3's own Suspend returned nil.
// It must load the session and resume at the bottom, never fail the load.
func TestAnUnusableStoredBlobStillLoadsChat(t *testing.T) {
	st := testProviderDefault
	st.WorkspaceState = map[string]json.RawMessage{
		"chat": json.RawMessage(`{"version":99,"anchor":{"anchor_seq":7}}`),
	}
	r, built := chatRegistered(t, st)
	updateAndDrain(t, r, tea.WindowSizeMsg{Width: 100, Height: 30})
	view := updateAndDrain(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	ws := last(t, built)
	if ws.resumeAnchor != nil {
		t.Errorf("a version this build does not know was accepted: %+v", ws.resumeAnchor)
	}
	if !ws.pinned {
		t.Error("an unusable blob did not degrade to the bottom")
	}
	if !strings.Contains(stripANSI(view), "Query Area") {
		t.Fatalf("the workspace did not load:\n%s", stripANSI(view))
	}
}

// F12's Done-when #7, deterministically: "Go heap returns near its pre-load
// level over 20 load/unload cycles."
//
// The gate asks for this with `go tool pprof` against a running app after
// twenty manual cycles through the chooser. That route works -- the server
// listens on 127.0.0.1:6061, see cmd\point\diagnostics.go -- but twenty
// cycles by hand is not a repeatable measurement and nobody will do it
// twice. This is the same claim measured the way F18 measured its own
// (TestRepeatedGReRepinsAndDoesNotLeak, workspace_test.go): force a GC,
// read MemStats, do the work, force a GC, read again.
//
// It drives the REAL path and nothing else: the chooser's own Enter to load,
// leader -> u to unload, the real factory, and a 400-message transcript per
// cycle. So what it proves is that releasing a workspace releases its
// transcript, not that a stub struct is cheap. F12's claim is specifically
// that Go DOES release here, unlike CPython (B15's own note), so a real
// reduction is expected rather than merely a bounded growth.
func TestTwentyLoadUnloadCyclesReturnTheHeapNearBaseline(t *testing.T) {
	// A COUNTING factory, not chatRegistered's collecting one.
	//
	// chatRegistered keeps every instance it builds, which is what lets
	// the tests above tell a reload from a reused instance -- and which
	// makes it useless here: holding twenty-one workspaces is itself the
	// leak, and the first run of this test reported +349 KB/cycle for
	// exactly that reason. This keeps the count and only the CURRENT
	// instance, so the one reference that outlives a cycle is constant
	// rather than per-cycle.
	th := testTheme()
	client := protocol.NewClient(protocol.Options{})
	var builtCount int
	var current *Workspace
	r, err := app.New(app.Options{
		Theme:  th,
		Client: client,
		State:  testProviderDefault,
		Workspaces: []app.WorkspaceDef{{
			ID:   app.WorkspaceChat,
			Name: "Chat",
			New: func() app.Workspace {
				builtCount++
				current = New(Options{Theme: th, Client: client, State: testProviderDefault})
				return current
			},
			MinSize: app.Size{W: 50, H: 12},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.SaveWorkspaceStateFn = func(string, map[string]json.RawMessage) error { return nil }
	updateAndDrain(t, r, tea.WindowSizeMsg{Width: 100, Height: 30})

	// Bare Update, not updateAndDrain: the cycle needs no DiagnosticsMsg,
	// and drainOnce waits out its idle window on every call -- which over
	// sixty calls would make this test slower than the manual version it
	// exists to replace.
	press := func(msg tea.Msg) string {
		m, _ := r.Update(msg)
		return stripANSI(m.(*app.Root).View())
	}

	cycle := func(i int) {
		// The chooser is on screen (at startup, and again after every
		// unload, because unloading the last workspace has nothing left
		// to show). Enter takes the pre-selected Chat button.
		view := press(tea.KeyMsg{Type: tea.KeyEnter})
		if !strings.Contains(view, "Query Area") {
			t.Fatalf("cycle %d: Chat did not load:\n%s", i, view)
		}
		w := current

		// A transcript worth releasing: without one, "the heap returned"
		// says nothing, because nothing of size was ever held.
		w.setSession(wireSession{ID: "s1", Messages: cycleMessages(400), TotalMessageCount: 400})
		_ = w.viewportLines(layout.Rect{W: 100, H: 26})

		press(key(actions.Leader))
		view = press(key("u"))
		if !strings.Contains(view, "Resume previous session?") {
			t.Fatalf("cycle %d: the workspace did not unload:\n%s", i, view)
		}
	}

	// One warm-up cycle first, so the measurement is not dominated by
	// first-use allocation -- the render cache, the theme's style table,
	// the client's buffers. What is under test is the STEADY state across
	// cycles, which is the only thing that can grow without bound.
	cycle(0)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	const cycles = 20
	for i := 1; i <= cycles; i++ {
		cycle(i)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	// KeepAlive is load-bearing, not defensive.
	//
	// Go's GC is precise about liveness: after the loop above, nothing
	// reads r or current again, so the whole Root graph -- every workspace
	// it ever held, and anything hanging off them -- is collectable before
	// this GC runs, inside a function that has not returned. Without these
	// two lines the measurement was a constant ~930 KB whatever the app
	// did: injecting a deliberate retention of all 21 workspaces changed
	// nothing, and so did hanging 20 MiB of ballast off the Root. It was
	// the "green while verifying almost nothing" trap, aimed at the one
	// Done-when that is purely a measurement.
	runtime.KeepAlive(r)
	runtime.KeepAlive(current)

	delta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("heap_alloc before=%d after=%d delta=%+d bytes over %d cycles (%+d/cycle)",
		before.HeapAlloc, after.HeapAlloc, delta, cycles, delta/cycles)
	t.Logf("heap_objects before=%d after=%d", before.HeapObjects, after.HeapObjects)

	// Nothing is loaded at the end, so a workspace still being held is a
	// leak however the bytes happen to land. A reused instance would also
	// mean the unload never released it.
	if builtCount != cycles+1 {
		t.Fatalf("the factory ran %d times for %d cycles -- a workspace was reused",
			builtCount, cycles+1)
	}

	// A generous ceiling rather than a tight budget, exactly as F18's own
	// heap test argues: this has to catch a per-cycle retention (a
	// workspace, its window, its 400-message transcript) without becoming
	// a flaky assertion about precise GC timing. One retained cycle is
	// hundreds of KiB on its own, so this still fails long before a real
	// leak could hide inside it.
	const maxGrowth = 4 << 20
	if delta > maxGrowth {
		t.Fatalf("heap grew by %d bytes across %d load/unload cycles, want <= %d "+
			"-- a workspace or its transcript is being retained after unload",
			delta, cycles, maxGrowth)
	}
}

// cycleMessages is a transcript of a size worth measuring the release of.
func cycleMessages(n int) []wireMessage {
	msgs := make([]wireMessage, 0, n)
	for i := 0; i < n; i++ {
		role := roleUser
		if i%2 == 1 {
			role = roleAssistant
		}
		msgs = append(msgs, wireMessage{
			ID:   "m" + itoa(i),
			Role: role,
			Seq:  i + 1,
			Content: wireContent{Type: "text", Text: strings.Repeat(
				"load/unload cycle filler, long enough to wrap several rows ", 6)},
			Timestamp: float64(1700000000 + i),
		})
	}
	return msgs
}
