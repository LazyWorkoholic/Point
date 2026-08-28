package protocol

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The live test actually spawns `uv run python main.py`.
//
// It is OFF unless POINT_LIVE_BACKEND=1, and it must never be run against the
// real profile: the backend takes a single-instance lock, opens
// %LOCALAPPDATA%\Point\runtime.db and runs migrations against it. Point
// APPDATA and LOCALAPPDATA at a scratch directory first — both sides of the
// app read those two variables, so one pair of overrides moves everything.
//
//	$env:POINT_LIVE_BACKEND = "1"
//	$env:APPDATA      = "$env:TEMP\point-scratch\Roaming"
//	$env:LOCALAPPDATA = "$env:TEMP\point-scratch\Local"
//	go test .\frontend\internal\protocol\ -run Live -v
const liveEnv = "POINT_LIVE_BACKEND"

func requireLive(t *testing.T) string {
	t.Helper()
	if os.Getenv(liveEnv) != "1" {
		t.Skipf("set %s=1 to run this against the real backend", liveEnv)
	}
	dir, err := FindBackendDir()
	if err != nil {
		t.Fatalf("no backend to talk to: %v", err)
	}
	return dir
}

// The handshake, against the real Python process.
func TestLiveHandshake(t *testing.T) {
	dir := requireLive(t)

	var stderr strings.Builder
	c := NewClient(Options{
		Dial:             StdioDialer(dir, &stderr, testLogger{t}),
		HandshakeTimeout: 90 * time.Second, // `uv run` can be slow the first time
	})
	c.Start()
	defer c.Close()

	deadline := time.After(120 * time.Second)
	for {
		select {
		case msg := <-c.msgs:
			switch m := msg.(type) {
			case ConnectedMsg:
				t.Logf("connected: envelope v%d, providers %v, schema v%d",
					m.ProtocolVersion, m.Capabilities.Providers, m.Capabilities.SchemaVersion)
				if m.ProtocolVersion != Version {
					t.Fatalf("version %d, want %d", m.ProtocolVersion, Version)
				}
				if !m.Capabilities.Streaming || !m.Capabilities.Cancel {
					t.Errorf("capabilities = %+v", m.Capabilities)
				}
				return
			case BackendFailedMsg:
				t.Fatalf("backend refused to start: %s — %s\nstderr:\n%s",
					m.Reason, m.Detail, stderr.String())
			case VersionMismatchMsg:
				t.Fatalf("version mismatch: client v%d, backend v%d", m.Client, m.Backend)
			}
		case <-deadline:
			t.Fatalf("no handshake in time; stderr:\n%s", stderr.String())
		}
	}
}

// A real query, end to end: ListSessions in, SessionList back, correlated by
// corr_id.
func TestLiveQueryRoundTrip(t *testing.T) {
	dir := requireLive(t)

	c := NewClient(Options{
		Dial:             StdioDialer(dir, io.Discard, testLogger{t}),
		HandshakeTimeout: 90 * time.Second,
	})
	c.Start()
	defer c.Close()

	waitConnected(t, c, 120*time.Second)

	replies, err := c.Request(Envelope{Type: CmdListSessions,
		Payload: json.RawMessage(`{}`)}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case env, ok := <-replies:
		if !ok {
			t.Fatal("the query timed out with no reply")
		}
		if env.Type != EvSessionList && env.Type != EvError {
			t.Fatalf("unexpected reply %q", env.Type)
		}
		t.Logf("reply: %s %s", env.Type, string(env.Payload))
	case <-time.After(30 * time.Second):
		t.Fatal("no reply")
	}
}

// The ping arrives on its own, every five seconds, while the backend is idle —
// which is what the watchdog measures silence against.
func TestLivePingArrives(t *testing.T) {
	dir := requireLive(t)

	c := NewClient(Options{
		Dial:             StdioDialer(dir, io.Discard, testLogger{t}),
		HandshakeTimeout: 90 * time.Second,
	})
	c.Start()
	defer c.Close()

	waitConnected(t, c, 120*time.Second)

	deadline := time.After(30 * time.Second)
	for {
		select {
		case msg := <-c.msgs:
			if e, ok := msg.(EnvelopeMsg); ok && e.Envelope.Type == EvPing {
				if e.Route != RouteConnection {
					t.Errorf("ping routed as %q; it is caused by no request", e.Route)
				}
				return
			}
		case <-deadline:
			t.Fatal("no ping in 30s")
		}
	}
}

// The stderr log records every launch, against the real process.
//
// This is the reported bug reproduced end to end: backend-stderr.log's
// timestamp never moved across launches, because a healthy backend writes
// nothing to stderr (logging_setup.py's stderr handler is WARNING-level unless
// --verbose), so the file was created once and never touched again.
func TestLiveStderrLogGrowsOnEveryLaunch(t *testing.T) {
	dir := requireLive(t)

	logPath := filepath.Join(t.TempDir(), "backend-stderr.log")

	sizes := make([]int64, 0, 2)
	for launch := 0; launch < 2; launch++ {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Fatal(err)
		}

		c := NewClient(Options{
			Dial:             StdioDialer(dir, f, testLogger{t}),
			HandshakeTimeout: 90 * time.Second,
		})
		c.Start()
		waitConnected(t, c, 120*time.Second)
		if err := c.Close(); err != nil {
			t.Fatalf("launch %d: %v", launch, err)
		}
		_ = f.Close()

		info, err := os.Stat(logPath)
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, info.Size())
		t.Logf("after launch %d: %d bytes, mtime %s",
			launch+1, info.Size(), info.ModTime().Format(time.RFC3339))
	}

	if sizes[0] == 0 {
		t.Fatal("the first launch wrote nothing to the stderr log")
	}
	if sizes[1] <= sizes[0] {
		t.Fatalf("the second launch added nothing: %d then %d bytes", sizes[0], sizes[1])
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if strings.Count(got, "point backend started") != 2 {
		t.Errorf("expected two start stamps, got:\n%s", got)
	}
	if !strings.Contains(got, "point backend exited") {
		t.Errorf("no exit stamp:\n%s", got)
	}
	t.Logf("log contents:\n%s", got)
}

func waitConnected(t *testing.T, c *Client, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case msg := <-c.msgs:
			switch m := msg.(type) {
			case ConnectedMsg:
				return
			case BackendFailedMsg:
				t.Fatalf("backend refused to start: %s — %s", m.Reason, m.Detail)
			}
		case <-deadline:
			t.Fatal("never connected")
		}
	}
}

type testLogger struct{ t *testing.T }

func (l testLogger) Debug(msg string, args ...any) {}
func (l testLogger) Info(msg string, args ...any)  { l.t.Logf("INFO  %s %v", msg, args) }
func (l testLogger) Warn(msg string, args ...any)  { l.t.Logf("WARN  %s %v", msg, args) }
func (l testLogger) Error(msg string, args ...any) { l.t.Logf("ERROR %s %v", msg, args) }
