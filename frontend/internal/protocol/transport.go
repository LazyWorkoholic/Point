package protocol

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Transport is the seam a socket implementation drops into later. It exists so
// cmd.Stdin never appears anywhere but in this file.
type Transport interface {
	Send(Envelope) error
	Recv() <-chan Envelope
	Close() error
}

// The three transport rules from F2, each of which is a real bug if broken:
//
//  1. A dedicated reader goroutine. Reading on the render goroutine lets a
//     slow render fill the OS pipe buffer, which blocks Python's write and
//     freezes the backend.
//  2. A bounded channel with a drop policy. Token events may be dropped under
//     backpressure; a lifecycle event never may, because a dropped one is a
//     permanent spinner.
//  3. Python's stderr goes to a log file, never mixed with stdout. Stdout is
//     the protocol channel, and an undrained stderr pipe blocks Python's
//     logging the moment the buffer fills.

const (
	// recvBuffer is the bounded channel's depth. Deep enough that a normal
	// render pause does not cost tokens, shallow enough that backpressure is
	// felt rather than accumulated into a lag.
	recvBuffer = 256

	// maxLineBytes caps one JSON line. bufio.Scanner's 64 KiB default is far
	// too small — a single tool result or a pasted file easily exceeds it, and
	// the failure mode is a truncated line reported as corruption.
	maxLineBytes = 16 << 20

	// stderrTailLines is how much of Python's stderr is kept in memory, so a
	// process that dies before the handshake can be explained with its own
	// last words rather than "exit status 1".
	stderrTailLines = 40
)

// StdioTransport wraps the child process.
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	out chan Envelope

	// stampMu keeps a stamp from interleaving with a Python line mid-write.
	stampMu sync.Mutex

	// writeMu serialises Send. Two goroutines writing to a pipe can interleave
	// mid-line, which produces JSON that is valid on neither side.
	writeMu sync.Mutex

	stderrTail *ringBuffer
	stderrSink io.Writer
	log        Logger

	dropped atomic.Int64
	lines   atomic.Int64

	exited  chan struct{}
	exitErr error

	closeOnce sync.Once
	closed    atomic.Bool
}

// Logger is the minimum this package needs. main supplies a slog-backed one;
// tests supply nothing.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// Spawn starts the backend and begins reading it.
//
// stderrSink receives Python's stderr verbatim. It is never the same writer as
// anything the protocol reads.
func Spawn(backendDir string, stderrSink io.Writer, log Logger) (*StdioTransport, error) {
	if log == nil {
		log = discardLogger{}
	}

	cmd := buildCommand(backendDir)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("backend stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("backend stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("backend stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting `uv run python main.py` in %s: %w", backendDir, err)
	}

	t := &StdioTransport{
		cmd:        cmd,
		stdin:      stdin,
		stdout:     stdout,
		out:        make(chan Envelope, recvBuffer),
		stderrTail: newRingBuffer(stderrTailLines),
		log:        log,
		exited:     make(chan struct{}),
		stderrSink: stderrSink,
	}

	// Stamp the launch into the stderr log.
	//
	// Without this the file looks broken: backend\logging_setup.py sets its
	// stderr handler to WARNING (DEBUG only under --verbose), so a healthy run
	// writes NOTHING to stderr, and the log keeps whatever timestamp it had
	// when it was first created — across launches, for as long as nothing goes
	// wrong. An empty file is the correct outcome for a clean run, but it is
	// indistinguishable from a drain that was never wired up, and F2's
	// verification asks you to look at this file to confirm it was.
	//
	// Python's own warnings still land here, interleaved, when there are any.
	t.stamp("started · pid %d · %s", cmd.Process.Pid, backendDir)

	go t.drainStderr(stderr, stderrSink)
	go t.readLoop()
	go t.waitLoop()

	return t, nil
}

// Send writes one envelope. Safe to call from any goroutine.
func (t *StdioTransport) Send(e Envelope) error {
	if t.closed.Load() {
		return errors.New("transport is closed")
	}
	raw, err := Encode(e)
	if err != nil {
		return err
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if _, err := t.stdin.Write(raw); err != nil {
		return fmt.Errorf("writing %s to the backend: %w", e.Type, err)
	}
	return nil
}

// Recv is the bounded channel. It closes when the backend's stdout ends,
// which is the signal that the child is gone.
func (t *StdioTransport) Recv() <-chan Envelope { return t.out }

// Close ends the child.
//
// Closing stdin is the shutdown the backend was designed for: main.py treats
// EOF on stdin as "Go is gone", drains its in-flight handlers, closes the
// database and releases the single-instance lock. Killing it instead leaves
// that lock held by a dead pid, and every later start refuses.
//
// The kill is the fallback for a backend that does not take the hint.
func (t *StdioTransport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		t.closed.Store(true)
		err = t.stdin.Close()

		select {
		case <-t.exited:
		case <-time.After(5 * time.Second):
			t.log.Warn("backend did not exit on stdin close; killing it")
			if t.cmd.Process != nil {
				_ = t.cmd.Process.Kill()
			}
			<-t.exited
		}
	})
	return err
}

// Exited closes when the child process has been reaped.
func (t *StdioTransport) Exited() <-chan struct{} { return t.exited }

// ExitError is the child's exit status, valid once Exited has closed.
func (t *StdioTransport) ExitError() error { return t.exitErr }

// StderrTail is Python's last words. Used to explain a process that died
// before the handshake — the single-instance lock refusal above all, which
// exits 1 with an explanation nobody would otherwise see.
func (t *StdioTransport) StderrTail() string { return t.stderrTail.String() }

// Dropped counts token events discarded under backpressure. Never lifecycle
// events; that is the invariant the count exists to make checkable.
func (t *StdioTransport) Dropped() int64 { return t.dropped.Load() }

// readLoop is rule 1: one dedicated goroutine, never the render goroutine.
func (t *StdioTransport) readLoop() {
	defer close(t.out)

	reader := bufio.NewReaderSize(t.stdout, 64<<10)
	lineNo := 0

	for {
		line, err := readLine(reader, maxLineBytes)
		if len(line) > 0 {
			lineNo++
			t.lines.Add(1)
			t.handleLine(lineNo, line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.log.Warn("backend stdout ended", "err", err)
			}
			return
		}
	}
}

func (t *StdioTransport) handleLine(lineNo int, line []byte) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return
	}

	env, err := Decode([]byte(trimmed))
	if err != nil {
		// A stray print() on the Python side lands here. Stdout is the
		// protocol channel, so anything that is not an envelope is a contract
		// violation — and it must be named, not silently skipped, or the
		// symptom is a hang with no explanation.
		t.log.Error("protocol violation on backend stdout",
			"line", lineNo, "text", truncate(trimmed, 200), "err", err)
		t.emit(protocolErrorEnvelope(lineNo, trimmed, err))
		return
	}
	t.emit(env)
}

// emit is rule 2: the drop policy.
func (t *StdioTransport) emit(e Envelope) {
	select {
	case t.out <- e:
		return
	default:
	}

	if IsDroppable(e.Type) {
		n := t.dropped.Add(1)
		if n == 1 || n%100 == 0 {
			t.log.Debug("dropping token events under backpressure", "total", n)
		}
		return
	}

	// Everything else blocks until the consumer catches up. A lifecycle event
	// that never arrives is a spinner that never stops, which is strictly
	// worse than a frame of lag.
	t.out <- e
}

// drainStderr is rule 3. It must run unconditionally: a full stderr pipe
// blocks Python's logging call, which blocks whatever was logging, which is
// usually the thing you were waiting on.
func (t *StdioTransport) drainStderr(r io.Reader, sink io.Writer) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for scanner.Scan() {
		line := scanner.Text()
		t.stderrTail.Add(line)
		if sink != nil {
			fmt.Fprintln(sink, line)
		}
	}

	// Scan() returns false for EOF and for failure alike, and without this
	// check the two are indistinguishable: a broken pipe would end the drain
	// silently, which is the exact condition rule 3 exists to prevent. From
	// that point on Python's stderr has nobody reading it, and the next time
	// the pipe buffer fills, the logging call that fills it blocks — taking
	// whatever was logging down with it.
	//
	// io.EOF is not reported by Scan(), so any error here is a real one.
	if err := scanner.Err(); err != nil {
		t.log.Error("stopped draining the backend's stderr", "err", err)
		t.stamp("stderr drain failed: %v", err)
	}
}

func (t *StdioTransport) waitLoop() {
	t.exitErr = t.cmd.Wait()
	if t.exitErr != nil {
		t.stamp("exited: %v", t.exitErr)
	} else {
		t.stamp("exited cleanly")
	}
	close(t.exited)
}

// stamp writes one framed line to the backend's stderr log, so the file
// records every launch and shutdown even when Python itself stays quiet.
func (t *StdioTransport) stamp(format string, args ...any) {
	if t.stderrSink == nil {
		return
	}
	t.stampMu.Lock()
	defer t.stampMu.Unlock()
	fmt.Fprintf(t.stderrSink, "---- point backend %s · %s\n",
		fmt.Sprintf(format, args...), time.Now().Format(time.RFC3339))
}

// readLine reads up to and including '\n', with a hard cap.
func readLine(r *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == nil {
			return buf, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			if len(buf) > limit {
				return buf[:limit], fmt.Errorf("line exceeded %d bytes", limit)
			}
			continue
		}
		return buf, err
	}
}

func protocolErrorEnvelope(lineNo int, text string, cause error) Envelope {
	payload, _ := json.Marshal(map[string]any{
		"line":    lineNo,
		"text":    truncate(text, 400),
		"message": cause.Error(),
	})
	return Envelope{
		V:       Version,
		Type:    EvProtocolError,
		TS:      float64(time.Now().UnixNano()) / 1e9,
		Payload: payload,
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ringBuffer keeps the last n lines and nothing more.
type ringBuffer struct {
	mu    sync.Mutex
	lines []string
	n     int
}

func newRingBuffer(n int) *ringBuffer { return &ringBuffer{n: n} }

func (b *ringBuffer) Add(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, line)
	if len(b.lines) > b.n {
		b.lines = b.lines[len(b.lines)-b.n:]
	}
}

func (b *ringBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.lines, "\n")
}

type discardLogger struct{}

func (discardLogger) Debug(string, ...any) {}
func (discardLogger) Info(string, ...any)  {}
func (discardLogger) Warn(string, ...any)  {}
func (discardLogger) Error(string, ...any) {}
