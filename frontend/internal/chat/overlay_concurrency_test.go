package chat

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// fakeBackend is a minimal protocol.Backend for driving a REAL
// protocol.Client end to end, including its actual concurrent tea.Cmd
// scheduling — the one thing every earlier test in this file's package
// sidestepped by feeding protocol.EnvelopeMsg into Root.Update directly.
// That earlier approach proved the DATA and its ordering are correct
// (Seq guard, immediate TaskStarted trigger); it cannot, by construction,
// catch a bug that only exists in how bubbletea's REAL cmd concurrency
// interacts with a live Client — which is exactly what this file exists
// to rule in or out.
type fakeBackend struct {
	mu     sync.Mutex
	sent   []protocol.Envelope
	out    chan protocol.Envelope
	exited chan struct{}
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{out: make(chan protocol.Envelope, 64), exited: make(chan struct{})}
}

func (f *fakeBackend) Send(e protocol.Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, e)
	return nil
}
func (f *fakeBackend) Recv() <-chan protocol.Envelope { return f.out }
func (f *fakeBackend) Close() error                   { return nil }
func (f *fakeBackend) Exited() <-chan struct{}        { return f.exited }
func (f *fakeBackend) ExitError() error               { return nil }
func (f *fakeBackend) StderrTail() string             { return "" }

func helloEnvelope() protocol.Envelope {
	payload, _ := json.Marshal(protocol.Hello{
		V:            protocol.Version,
		Capabilities: protocol.Capabilities{Providers: []string{"anthropic"}, SchemaVersion: 1},
	})
	return protocol.Envelope{V: protocol.Version, ID: "hello-1", Type: protocol.EvHello, Payload: payload}
}

// concurrentDriver runs a Root the way bubbletea's real Program does: every
// returned tea.Cmd is started in its OWN goroutine the instant it is
// produced, and whichever one completes first is what gets fed into the
// next Update call — there is no ordering between cmds beyond when they
// actually finish. A test harness that instead calls cmds synchronously,
// one at a time, imposes an ordering bubbletea never promises and can
// hide a bug that only manifests under real concurrent scheduling.
type concurrentDriver struct {
	r     *app.Root
	msgCh chan tea.Msg
}

// settleBudget is a CEILING on how long a wait may take before the test
// calls it a failure — never an assertion about speed. pumpUntil returns
// the instant its condition is met, so a generous ceiling costs a passing
// run nothing and only decides how long a genuinely broken one hangs
// before reporting. It is deliberately far above the ~0.5s these waits
// actually take: under -race on a loaded machine the same wait was
// measured at 2.07s, and a 3s ceiling made this suite flaky. A test that
// fails intermittently teaches you to ignore failures, which is worse
// than not having the test.
const settleBudget = 20 * time.Second

func newConcurrentDriver(r *app.Root) *concurrentDriver {
	return &concurrentDriver{r: r, msgCh: make(chan tea.Msg, 256)}
}

func (d *concurrentDriver) spawn(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if msg == nil {
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				d.spawn(c)
			}
			return
		}
		d.msgCh <- msg
	}()
}

// send delivers msg directly (a keypress, a resize — synchronous UI input,
// not something a backend goroutine produces) and spawns whatever cmd
// comes back.
func (d *concurrentDriver) send(msg tea.Msg) string {
	m, cmd := d.r.Update(msg)
	d.r = m.(*app.Root)
	d.spawn(cmd)
	return d.r.View()
}

// pumpUntil drains completed cmd-produced messages (real backend traffic)
// for up to timeout, feeding each into Update and spawning its own
// follow-up cmd — draining the SAME channel every message it produces
// lands on, exactly like bubbletea's single event loop does. want, when
// given, is re-checked after every message AND on every idle tick (never
// a caller's stale snapshot), and pumpUntil returns as soon as it is
// satisfied. This is the ONLY thing that actually advances the fake
// backend's messages through Update — a caller that polls fb.sent or
// d.r.View() without going through this drains nothing.
func (d *concurrentDriver) pumpUntil(timeout time.Duration, want func() bool) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case msg := <-d.msgCh:
			m, cmd := d.r.Update(msg)
			d.r = m.(*app.Root)
			d.spawn(cmd)
		case <-time.After(10 * time.Millisecond):
		}
		if want != nil && want() {
			return d.r.View()
		}
	}
	return d.r.View()
}

func TestOverlayTaskCardUpdatesLiveUnderRealClientConcurrency(t *testing.T) {
	th := testTheme()
	fb := newFakeBackend()
	client := protocol.NewClient(protocol.Options{
		Dial: func() (protocol.Backend, error) { return fb, nil },
	})

	r, err := app.New(app.Options{Theme: th, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkspace(app.WorkspaceChat, New(Options{Theme: th}))

	d := newConcurrentDriver(r)
	d.spawn(r.Init())

	// Handshake — drain for a fixed short window rather than matching on
	// rendered content, since the active workspace (chat, not Placeholder)
	// does not surface ConnState directly in its View().
	fb.out <- helloEnvelope()
	d.pumpUntil(300*time.Millisecond, nil)
	var view string

	// Narrow the terminal and open the fallback overlay.
	d.send(tea.WindowSizeMsg{Width: 80, Height: 30})
	d.send(key("ctrl+w"))
	view = d.send(key("c"))
	if !strings.Contains(view, "Right Pane") {
		t.Fatalf("overlay did not open:\n%s", stripANSI(view))
	}

	// Opening the overlay triggers the first ListTasks request over the
	// REAL client -> fake backend. Wait for it to actually be SENT —
	// pumping the driver the whole time, since that request is only sent
	// once refreshTasks runs, which only happens once Update has actually
	// processed whatever led here.
	nthSent := func(msgType string, n int) (string, bool) {
		fb.mu.Lock()
		defer fb.mu.Unlock()
		count := 0
		for _, e := range fb.sent {
			if e.Type == msgType {
				count++
				if count == n {
					return e.ID, true
				}
			}
		}
		return "", false
	}
	var reqID string
	d.pumpUntil(settleBudget, func() bool {
		id, ok := nthSent(protocol.CmdListTasks, 1)
		if ok {
			reqID = id
		}
		return ok
	})
	if reqID == "" {
		t.Fatal("opening the overlay never sent a ListTasks request to the backend")
	}

	fb.out <- taskListReply(reqID, nil)
	view = d.pumpUntil(settleBudget, func() bool {
		return strings.Contains(stripANSI(d.r.View()), "no background tasks")
	})
	if !strings.Contains(stripANSI(view), "no background tasks") {
		t.Fatalf("overlay did not settle to empty state:\n%s", stripANSI(view))
	}

	// A brand-new task starts — this is Config B from the field report:
	// the overlay is the visible surface, already settled, and ONE new
	// task starts. Over the real client, under real concurrent cmd
	// scheduling.
	fb.out <- protocol.Envelope{V: protocol.Version, Type: protocol.EvTaskStarted, TaskID: "new1"}

	var reqID2 string
	d.pumpUntil(settleBudget, func() bool {
		id, ok := nthSent(protocol.CmdListTasks, 2)
		if ok {
			reqID2 = id
		}
		return ok
	})
	if reqID2 == "" {
		t.Fatal("TaskStarted (real client) never triggered a second ListTasks request")
	}

	fb.out <- taskListReply(reqID2, []app.TaskRow{
		{ID: "new1", Agent: "research", State: app.TaskStateRunning},
	})
	view = d.pumpUntil(settleBudget, func() bool {
		return strings.Contains(stripANSI(d.r.View()), "⟳ research")
	})
	if !strings.Contains(stripANSI(view), "⟳ research") {
		t.Fatalf("overlay never showed the new task running under real client concurrency:\n%s", stripANSI(view))
	}
}

func taskListReply(corrID string, tasks []app.TaskRow) protocol.Envelope {
	payload, _ := json.Marshal(map[string]any{"tasks": tasks})
	return protocol.Envelope{V: protocol.Version, Type: protocol.EvTaskList, CorrID: corrID, Payload: payload}
}

// A token flood is the ONE thing that differs between "the task is
// streaming" and "the task has completed" — the exact boundary a field
// report drew ("nothing shows until the task fully completes"). This runs
// the same scenario against BOTH surfaces, so a difference between them
// would show up as one subtest failing while its twin passes, rather than
// as a claim about one surface with nothing to compare it to.
//
// Both surfaces read the SAME w.tasks through the SAME taskCardLines, and
// Root.View recomposes the whole frame — real pane and overlay alike —
// every time bubbletea calls it. There is no subscription model here and
// no per-component redraw: an overlay cannot be "not subscribed" to a
// state change, because nothing subscribes to anything.
func TestBothSurfacesShowARunningTaskWhileTokensFlood(t *testing.T) {
	for _, tc := range []struct {
		name    string
		width   int
		overlay bool
	}{
		{"real pane", 130, false},
		{"fallback overlay", 80, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th := testTheme()
			fb := newFakeBackend()
			client := protocol.NewClient(protocol.Options{
				Dial: func() (protocol.Backend, error) { return fb, nil },
			})
			r, err := app.New(app.Options{Theme: th, Client: client})
			if err != nil {
				t.Fatal(err)
			}
			r.SetWorkspace(app.WorkspaceChat, New(Options{Theme: th}))
			d := newConcurrentDriver(r)
			d.spawn(r.Init())

			fb.out <- helloEnvelope()
			d.pumpUntil(300*time.Millisecond, nil)

			d.send(tea.WindowSizeMsg{Width: tc.width, Height: 30})
			if tc.overlay {
				d.send(key("ctrl+w"))
				if v := d.send(key("c")); !strings.Contains(v, "Right Pane") {
					t.Fatalf("overlay did not open:\n%s", stripANSI(v))
				}
			}

			nthListTasks := func(n int) (string, bool) {
				fb.mu.Lock()
				defer fb.mu.Unlock()
				count := 0
				for _, e := range fb.sent {
					if e.Type == protocol.CmdListTasks {
						count++
						if count == n {
							return e.ID, true
						}
					}
				}
				return "", false
			}

			var id1 string
			d.pumpUntil(settleBudget, func() bool {
				id, ok := nthListTasks(1)
				if ok {
					id1 = id
				}
				return ok
			})
			if id1 == "" {
				t.Fatal("becoming visible never sent a ListTasks request")
			}
			fb.out <- taskListReply(id1, nil)
			d.pumpUntil(settleBudget, func() bool {
				return strings.Contains(stripANSI(d.r.View()), "no background tasks")
			})

			fb.out <- protocol.Envelope{V: protocol.Version, Type: protocol.EvTaskStarted, TaskID: "t1"}
			var id2 string
			d.pumpUntil(settleBudget, func() bool {
				id, ok := nthListTasks(2)
				if ok {
					id2 = id
				}
				return ok
			})
			if id2 == "" {
				t.Fatal("TaskStarted never triggered a ListTasks request")
			}

			// Tokens stream while the reply is still in flight.
			go func() {
				for i := 0; i < 250; i++ {
					fb.out <- protocol.Envelope{
						V: protocol.Version, Type: protocol.EvTextDelta, TaskID: "t1",
						Payload: json.RawMessage(`{"data":{"text":"x"}}`),
					}
				}
			}()
			fb.out <- taskListReply(id2, []app.TaskRow{
				{ID: "t1", Agent: "research", State: app.TaskStateRunning},
			})

			view := d.pumpUntil(settleBudget, func() bool {
				return strings.Contains(stripANSI(d.r.View()), "⟳ research")
			})
			if !strings.Contains(stripANSI(view), "⟳ research") {
				t.Errorf("running task not shown while tokens were flooding:\n%s", stripANSI(view))
			}
		})
	}
}
