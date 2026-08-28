package protocol

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// pump builds a StdioTransport around a plain reader, with no child process.
// readLoop, the drop policy and the protocol-error path are all process-free;
// only Spawn and waitLoop are not.
func pump(t *testing.T, stdout io.Reader, buffer int) *StdioTransport {
	t.Helper()
	tr := &StdioTransport{
		out:        make(chan Envelope, buffer),
		stderrTail: newRingBuffer(8),
		log:        discardLogger{},
		exited:     make(chan struct{}),
		stdout:     io.NopCloser(stdout),
	}
	go tr.readLoop()
	return tr
}

func line(e Envelope) string {
	raw, err := Encode(e)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func TestReaderDecodesEveryLine(t *testing.T) {
	body := line(Envelope{V: Version, ID: "1", Type: EvHello, Payload: json.RawMessage(`{"v":1}`)}) +
		line(Envelope{V: Version, ID: "2", Type: EvTaskStarted, TaskID: "t1"}) +
		line(Envelope{V: Version, ID: "3", Type: EvTaskDone, TaskID: "t1"})

	tr := pump(t, strings.NewReader(body), 16)

	var got []string
	for e := range tr.Recv() {
		got = append(got, e.ID)
	}
	if len(got) != 3 || got[0] != "1" || got[2] != "3" {
		t.Fatalf("got %v", got)
	}
}

// Blank lines between envelopes are not a protocol violation.
func TestBlankLinesAreIgnored(t *testing.T) {
	body := "\n\n" + line(Envelope{V: Version, ID: "1", Type: EvPing}) + "\n"
	tr := pump(t, strings.NewReader(body), 16)

	n := 0
	for range tr.Recv() {
		n++
	}
	if n != 1 {
		t.Fatalf("got %d envelopes, want 1", n)
	}
}

// A stray print() on the Python side. Stdout is the protocol channel, so this
// is a contract violation — and it must be NAMED, not silently skipped, or the
// symptom is a hang with no explanation.
func TestStrayPrintBecomesANamedProtocolError(t *testing.T) {
	body := line(Envelope{V: Version, ID: "1", Type: EvHello}) +
		"hi\n" +
		line(Envelope{V: Version, ID: "2", Type: EvPing})

	tr := pump(t, strings.NewReader(body), 16)

	var errs []ProtocolErrorMsg
	seen := 0
	for e := range tr.Recv() {
		if e.Type != EvProtocolError {
			seen++
			continue
		}
		var p ProtocolErrorMsg
		if err := e.UnmarshalPayload(&p); err != nil {
			t.Fatal(err)
		}
		errs = append(errs, p)
	}

	if len(errs) != 1 {
		t.Fatalf("got %d protocol errors, want 1", len(errs))
	}
	if errs[0].Text != "hi" {
		t.Errorf("the offending line was not reported: %q", errs[0].Text)
	}
	if errs[0].Line != 2 {
		t.Errorf("line number = %d, want 2", errs[0].Line)
	}
	// The stream keeps going: one bad line is not a broken connection.
	if seen != 2 {
		t.Errorf("%d good envelopes survived, want 2", seen)
	}
}

// The drop policy: token events go under backpressure, lifecycle events never
// do, so every task still reaches a terminal state.
func TestFloodDropsTokensButNeverLifecycleEvents(t *testing.T) {
	const tokens = 5000

	var b strings.Builder
	b.WriteString(line(Envelope{V: Version, ID: "start", Type: EvTaskStarted, TaskID: "t1"}))
	for i := 0; i < tokens; i++ {
		b.WriteString(line(Envelope{V: Version, ID: "tok", Type: EvTaskToken, TaskID: "t1"}))
	}
	b.WriteString(line(Envelope{V: Version, ID: "done", Type: EvTaskDone, TaskID: "t1"}))

	// A channel of 4 with a consumer that dawdles is as much backpressure as
	// this needs to make the policy fire.
	tr := pump(t, strings.NewReader(b.String()), 4)

	var (
		gotTokens int
		lifecycle []string
	)
	slow := 0
	for e := range tr.Recv() {
		switch {
		case IsDroppable(e.Type):
			gotTokens++
		default:
			lifecycle = append(lifecycle, e.Type)
		}
		// Dawdle for the first stretch so the buffer genuinely fills.
		if slow < 40 {
			slow++
			time.Sleep(time.Millisecond)
		}
	}

	if tr.Dropped() == 0 {
		t.Fatalf("no tokens were dropped under backpressure (received %d of %d)", gotTokens, tokens)
	}
	if gotTokens >= tokens {
		t.Fatalf("received every token; the drop policy did not engage")
	}
	if int64(gotTokens)+tr.Dropped() != tokens {
		t.Errorf("%d received + %d dropped != %d sent", gotTokens, tr.Dropped(), tokens)
	}

	// The one that actually matters.
	if len(lifecycle) != 2 || lifecycle[0] != EvTaskStarted || lifecycle[1] != EvTaskDone {
		t.Fatalf("lifecycle events = %v, want [TaskStarted TaskDone] — a dropped terminal event is a permanent spinner", lifecycle)
	}
}

// A payload larger than bufio.Scanner's 64 KiB default must survive. A single
// tool result or a pasted file exceeds it easily, and the failure mode is a
// truncated line reported as corruption.
func TestLongLinesSurvive(t *testing.T) {
	big := strings.Repeat("x", 512<<10)
	payload, _ := json.Marshal(map[string]string{"text": big})
	body := line(Envelope{V: Version, ID: "big", Type: EvTaskToken, TaskID: "t1", Payload: payload})

	tr := pump(t, strings.NewReader(body), 4)

	var got Envelope
	n := 0
	for e := range tr.Recv() {
		got = e
		n++
	}
	if n != 1 {
		t.Fatalf("got %d envelopes", n)
	}
	var p struct {
		Text string `json:"text"`
	}
	if err := got.UnmarshalPayload(&p); err != nil {
		t.Fatal(err)
	}
	if len(p.Text) != len(big) {
		t.Fatalf("payload truncated: %d bytes, want %d", len(p.Text), len(big))
	}
}

// stderr is drained unconditionally and never mixed with stdout — a full
// stderr pipe blocks Python's logging, which blocks whatever was logging.
func TestStderrIsDrainedToItsOwnSink(t *testing.T) {
	tr := &StdioTransport{stderrTail: newRingBuffer(3), log: discardLogger{}}
	var sink strings.Builder

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tr.drainStderr(strings.NewReader("one\ntwo\nthree\nfour\n"), &sink)
	}()
	wg.Wait()

	if got := sink.String(); !strings.Contains(got, "one") || !strings.Contains(got, "four") {
		t.Fatalf("sink = %q", got)
	}
	// The in-memory tail keeps only the last few lines, for explaining a
	// process that died before the handshake.
	tail := tr.StderrTail()
	if strings.Contains(tail, "one") {
		t.Errorf("tail did not bound itself: %q", tail)
	}
	if !strings.Contains(tail, "four") {
		t.Errorf("tail lost the most recent line: %q", tail)
	}
}

// The stderr log records every launch, even when Python says nothing.
//
// backend\logging_setup.py sets its stderr handler to WARNING (DEBUG only
// under --verbose), so a healthy run writes NOTHING to stderr. The file then
// keeps whatever timestamp it had when it was first created, across launches,
// which is indistinguishable from a drain that was never wired up.
func TestStderrLogIsStampedOnEveryLaunch(t *testing.T) {
	var sink strings.Builder
	tr := &StdioTransport{
		stderrTail: newRingBuffer(8),
		stderrSink: &sink,
		log:        discardLogger{},
	}

	tr.stamp("started · pid %d · %s", 1234, `D:\Projects\Point\backend`)
	tr.stamp("exited cleanly")

	out := sink.String()
	if strings.Count(out, "\n") != 2 {
		t.Fatalf("expected two stamped lines, got %q", out)
	}
	for _, want := range []string{"started", "pid 1234", `D:\Projects\Point\backend`, "exited cleanly"} {
		if !strings.Contains(out, want) {
			t.Errorf("stamp does not mention %q: %q", want, out)
		}
	}
	// A timestamp is the whole point: it is what makes LastWriteTime move.
	if !strings.Contains(out, time.Now().Format("2006-01-02")) {
		t.Errorf("stamp carries no date: %q", out)
	}
}

// A nil sink is not a crash — the app runs without a writable log directory.
func TestStampWithoutASinkIsHarmless(t *testing.T) {
	tr := &StdioTransport{stderrTail: newRingBuffer(4), log: discardLogger{}}
	tr.stamp("started")
}

// ---------------------------------------------------------------------------
// Backend directory resolution
// ---------------------------------------------------------------------------

func TestFindBackendDirWalksUpFromTheWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "frontend", "internal", "protocol")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	backend := filepath.Join(root, "backend")
	if err := os.MkdirAll(backend, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backend, "main.py"), []byte("#"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(BackendDirEnv, "")
	chdir(t, deep)

	got, err := FindBackendDir()
	if err != nil {
		t.Fatal(err)
	}
	if !sameDir(got, backend) {
		t.Fatalf("found %q, want %q", got, backend)
	}
}

func TestBackendDirEnvOverridesEverything(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("#"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(BackendDirEnv, dir)

	got, err := FindBackendDir()
	if err != nil {
		t.Fatal(err)
	}
	if !sameDir(got, dir) {
		t.Fatalf("found %q, want %q", got, dir)
	}
}

// A wrong override is named, not silently ignored in favour of a search.
func TestBadBackendDirEnvIsAnError(t *testing.T) {
	t.Setenv(BackendDirEnv, t.TempDir()) // no main.py in it

	_, err := FindBackendDir()
	if err == nil {
		t.Fatal("a POINT_BACKEND_DIR with no main.py was accepted")
	}
	if !strings.Contains(err.Error(), BackendDirEnv) {
		t.Errorf("error does not name the variable: %v", err)
	}
}

func TestMissingBackendIsExplained(t *testing.T) {
	t.Setenv(BackendDirEnv, "")
	chdir(t, t.TempDir())

	_, err := FindBackendDir()
	if err == nil {
		t.Skip("this machine has a backend\\main.py above the temp directory")
	}
	if !strings.Contains(err.Error(), BackendDirEnv) {
		t.Errorf("error does not say how to fix it: %v", err)
	}
}

// The real repo resolves, and it resolves to a directory the child can
// actually run from — main.py's imports only work from inside backend\.
func TestTheRealBackendResolves(t *testing.T) {
	t.Setenv(BackendDirEnv, "")

	dir, err := FindBackendDir()
	if err != nil {
		t.Skipf("not running inside the repo: %v", err)
	}
	if filepath.Base(dir) != "backend" {
		t.Errorf("resolved to %q", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.py")); err != nil {
		t.Errorf("no main.py in the resolved directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "runtime", "runtime.py")); err != nil {
		t.Errorf("resolved directory is not the backend package root: %v", err)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func sameDir(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}
