package editor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// F14's Tier 0 command runner: one command at a time, stdout and stderr
// interleaved, a typed line forwarded to the running process's stdin, and
// a capped scrollback. Three things this file is NOT, by the episode's own
// "Do NOT" section: a PTY, a VT100 emulator, or a second way for an agent
// to run a command -- nothing here is reachable from agents\ or tools\,
// only from a key the USER presses.
//
// # Three gaps resolved before this was built, not silently assumed
//
//  1. SSH guest refusal (Done-when #8): B16 is not started and there is no
//     guest/owner identity anywhere in the backend yet (checked
//     managers\usage.py, which explicitly declined to stub one for B14).
//     Nothing here branches on identity -- no hidden "if guest" anywhere
//     in this file or in app\hatch.go -- so there is nothing to bypass
//     once B16 adds the real check. Recorded as blocked on B16, not
//     silently marked passed; see the episode's own Handoff.
//
//  2. getpass.getpass() on Windows does not read sys.stdin at all -- it
//     calls msvcrt.getwch() directly against the shared console input
//     buffer, bypassing the pipe this file writes typed lines to
//     entirely. Bubble Tea already owns that console in raw input mode
//     for its own keys, so the child's getwch() call is racing a reader
//     that is already blocked waiting and wins essentially every time.
//     Net effect: a silent hang, not an error, a leak or a visible
//     fallback warning. Not fixable without emulating a console input
//     source for the child, which is PTY territory -- documented as a
//     known limitation instead. ctrl+c (kill, below) is the way out of
//     one; the hatch, which inherits a REAL console, is the way to avoid
//     hitting it at all.
//
//  3. Bare \r/\b progress redraws (pip, uv, tqdm) are NOT ANSI escape
//     sequences, so a stripper that only matches \x1b[...] would pass
//     them straight through and spam the capped buffer with near-
//     duplicate lines. Fixed below: feed treats \r as "discard the
//     pending line and restart at column 0" and \b as "delete the last
//     pending rune", so a hundred \r-redraws with no \n between them
//     commit as exactly ONE line.

// terminalLineCap is "output buffer capped ~5k lines, older dropped".
const terminalLineCap = 5000

// terminalHistoryCap bounds how many commands state.json keeps per
// project. Not specified by the episode doc, which only asks for ↑/↓
// recall -- chosen so the file cannot grow without bound from one
// long-lived project's history.
const terminalHistoryCap = 200

// ansiCSI strips ANSI CSI sequences (colour, cursor movement, clear-line --
// the overwhelming majority of what a batch command emits): ESC '[',
// optional parameter bytes, optional intermediate bytes, one final byte.
// Not a VT100 emulator -- it does not INTERPRET any of these, it deletes
// them, which is all "ANSI escape codes stripped on the way in" asks for.
var ansiCSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// ansiOSC strips OSC sequences (window title, etc.), terminated by BEL or
// ST.
var ansiOSC = regexp.MustCompile(`\x1b\][^\x07]*(\x07|\x1b\\)`)

// terminalPane is the Editor's F14 terminal: one runner, one history, one
// remembered size mode. A SINGLE long-lived value for the workspace's
// whole life, not recreated per command -- the output buffer, the history
// and the mode all have to survive the command that is currently running,
// and the command after it, and a Suspend/Resume in between.
type terminalPane struct {
	// mode is the user's own remembered choice (cycled by F8), persisted
	// via Suspend -- NOT necessarily what is on screen right now. See
	// effectiveTerminalMode.
	mode layout.TermMode
	// autoExpanded is transient and never persisted: new output arrived
	// while focus was on the code pane. Cleared the moment the terminal
	// is focused again -- "returning restores the remembered mode".
	autoExpanded bool

	// input is the one line being typed, as runes so a cursor index and
	// backspace/left/right are O(1) and multi-byte-safe.
	input  []rune
	cursor int

	// history is this project's command history, loaded from state.json
	// the moment historyKey resolves to something and kept here for
	// ↑/↓ recall. historyIdx is -1 while not navigating; historyScratch
	// is what was being typed before ↑ was first pressed, restored by
	// ↓ past the newest entry.
	history        []string
	historyIdx     int
	historyScratch string
	// historyKey is terminalHistoryKey's last result, used to detect a
	// project change (onProject) and to know which bucket to write back.
	// Empty until the Editor's own project resolution has run once, or
	// until the first command is run -- see recordTerminalHistory.
	historyKey string
	// cwd is this process's own working directory, resolved ONCE. It is
	// the no-project fallback for both Cmd.Dir and the pane header, and
	// it is cached because terminalDir is called on every frame (the
	// header names the directory) and a syscall per frame to answer a
	// question whose answer cannot change is waste.
	cwd string

	// lines is the committed scrollback, capped at terminalLineCap.
	// pendingRunes is the partial line being built -- see feed. It is
	// RENDERED as the newest row rather than held invisibly until a
	// newline arrives: a prompt written without one ("name: ", every
	// input() in existence) is exactly a partial line, and holding it
	// back is what made an interactive script look like a dead pane.
	lines        []string
	pendingRunes []rune
	// pendingCR is a CR held over to the NEXT rune, which may arrive in
	// the next chunk -- see feed. Windows line endings are CRLF, and the
	// CR and the LF genuinely do arrive in separate reads (measured: a
	// `Write-Output 'alpha'` lands as "alpha" then "\r\n"), so deciding
	// CRLF-vs-bare-CR needs state that survives a chunk boundary.
	pendingCR bool

	// scrollBack is how many lines ABOVE the live edge the view is parked.
	// Zero means pinned to the bottom, which is where output belongs; the
	// episode doc's "scrollable" is this plus pgup/pgdown and the wheel.
	scrollBack int

	// running, cmd and stdin describe the one live child, if any. gen
	// tags every event a command's own goroutines produce, so an event
	// from a KILLED or already-finished run can never be mistaken for the
	// current one's.
	running bool
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	gen     int
	// run is the live run's shared handle -- what the goroutines report
	// through, and what guarantees exactly one termExited per run even
	// when the kill watchdog and the real Wait both finish. nil when
	// nothing is running.
	run *termRun
	// killing is set by the first ctrl+c so a second press says so
	// instead of calling TerminateProcess against a handle that is
	// already gone -- see killRunning.
	killing bool
	// clearsScreen and bareClear are this run's classification, taken
	// from the command TEXT at start -- see the clearsScreen function.
	// They decide whether the console needs a repaint and whether the run
	// leaves any trace in the buffer.
	clearsScreen bool
	bareClear    bool
	// exitLine is the last "[exit N]" marker's text, kept only so the
	// pane header (terminalBox) can show the last outcome once the run
	// that produced it is no longer the live one.
	exitLine string

	// events is the ONE channel for this pane's whole life -- see next's
	// own doc for why it is not recreated per run.
	events chan termEvent
}

func newTerminalPane() *terminalPane {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return &terminalPane{
		mode:       layout.TermSmall,
		historyIdx: -1,
		events:     make(chan termEvent, 256),
		cwd:        cwd,
	}
}

// termEventKind distinguishes the two things a run can report.
type termEventKind int

const (
	termChunk termEventKind = iota
	termExited
)

// termEvent is gen-tagged exactly like the two tea.Tick loops' own msgs
// (F1's animTickMsg, F9a's trayTickMsg) are gen-tagged, and for the same
// reason: a goroutine started by a run that has since been killed or
// superseded may still have an event in flight, and it must be dropped on
// arrival rather than applied to the run that replaced it.
type termEvent struct {
	gen     int
	kind    termEventKind
	text    string // termChunk
	err     error  // termExited
	code    int    // termExited
	stopped bool   // termExited: ctrl+c, not the command's own exit
}

// terminalEventMsg wraps termEvent for Bubble Tea's Update switch.
//
// It is app.WorkspaceAddressed, and that is load-bearing rather than
// decorative: Root's fall-through delivers an unrecognised message to the
// ACTIVE workspace only, so without this, switching to Chat while a
// command runs sent the next event to Chat -- losing it, and with it the
// re-arm that keeps the pull loop alive. See app\contribute.go's
// WorkspaceAddressed for the full trace.
type terminalEventMsg struct{ ev termEvent }

func (terminalEventMsg) TargetWorkspace() layout.WorkspaceID {
	return layout.WorkspaceEditor
}

// next is the pull loop, started once from Init and re-armed from
// onTerminalEvent FOREVER -- it never stops, unlike animTick/trayTick,
// because there is no analogue of "nothing streaming" to stop it for: it
// is one goroutine blocking on one channel read for the whole life of the
// workspace, costing nothing while idle, and the alternative (starting and
// stopping it per run) would have to solve the exact race this avoids --
// a chunk arriving between "the last run's loop stopped" and "the next
// run's loop started".
func (t *terminalPane) next() tea.Cmd {
	return func() tea.Msg {
		ev := <-t.events
		return terminalEventMsg{ev: ev}
	}
}

// effectiveTerminalMode is what Compute should actually draw this frame --
// see TerminalMode's own doc on why this is not just w.term.mode.
func (w *Workspace) effectiveTerminalMode() layout.TermMode {
	if w.effectiveFocus() != focusTerminal {
		if w.term.autoExpanded {
			return layout.TermOverlay
		}
		return layout.TermSmall
	}
	return w.term.mode
}

// cycleTerminalMode is F8's binding: small -> overlay -> full -> small.
//
// It CLEARS autoExpanded, and that is the escape hatch rather than a
// detail. Auto-expand overrides the drawn mode while focus is elsewhere
// (effectiveTerminalMode), so with the flag left set, pressing the
// size key on a pane that had expanded itself over the code did
// nothing at all -- the one state in which a user is most likely to
// press it. An explicit size command beats an automatic expansion,
// always.
//
// It also reports the result, because the drawn size does not always
// change on the press: with the code pane focused, every mode draws as
// `small` (the focus-collapse rule), so an unreported press would look
// inert for a second reason. The status line names both the mode now
// remembered and what is actually on screen.
func (w *Workspace) cycleTerminalMode() tea.Cmd {
	switch w.term.mode {
	case layout.TermSmall:
		w.term.mode = layout.TermOverlay
	case layout.TermOverlay:
		w.term.mode = layout.TermFull
	default:
		w.term.mode = layout.TermSmall
	}
	w.term.autoExpanded = false

	drawn := w.effectiveTerminalMode()
	if drawn == w.term.mode {
		return w.say("terminal: " + string(w.term.mode))
	}
	return w.say(fmt.Sprintf("terminal: %s — showing %s until the pane has focus",
		w.term.mode, drawn))
}

// focusTerminalPane is ctrl+o's third stop and mouse.go's click target
// alike: focusing the terminal is also what "returning" to it means for
// auto-expand, so this is the one place that clears autoExpanded.
func (w *Workspace) focusTerminalPane() {
	w.focus = focusTerminal
	w.term.autoExpanded = false
}

// ---------------------------------------------------------------------------
// Running a command
// ---------------------------------------------------------------------------

// terminalHistoryKey is "per PROJECT, not per workspace" made concrete: the
// resolved project id when onProject found one, or the resolved working
// directory when it did not -- the same fallback hatchDir (app\hatch.go)
// uses for Cmd.Dir, so a project-less pane's history and its working
// directory always agree on what "this project" means.
func (w *Workspace) terminalHistoryKey() string {
	if w.projectID != "" {
		return "project:" + w.projectID
	}
	return "dir:" + w.terminalDir()
}

// terminalDir is "working directory = project root" with F14's own
// fallback for when onProject found no project at all: the process's own
// OS working directory, shown in the pane header either way so the user
// is never told a directory the pane is not actually running in.
func (w *Workspace) terminalDir() string {
	if w.projectRoot != "" {
		return w.projectRoot
	}
	return w.term.cwd
}

// reloadTerminalHistory re-keys and reloads the pane's history. Called
// from onProject (protocol.go) every time the Editor's project binding
// changes, including the very first resolution -- see TerminalHistory's
// own doc in config\state.go for why a project change must swap the whole
// bucket rather than carry the previous project's lines over.
func (w *Workspace) reloadTerminalHistory() {
	key := w.terminalHistoryKey()
	if key == w.term.historyKey {
		return
	}
	w.term.historyKey = key
	w.term.historyIdx = -1
	w.term.historyScratch = ""
	st, err := config.LoadState()
	if err != nil {
		w.term.history = nil
		return
	}
	w.term.history = st.TerminalHistory[key]
}

// recordTerminalHistory appends one command and writes the project's
// bucket back. Every run, even a repeat of the line just run -- F14's
// spec says nothing about deduping, and a real REPL-style history (↑
// recalling the exact sequence you typed) is the less surprising of the
// two readings.
func (w *Workspace) recordTerminalHistory(line string) {
	if line == "" {
		return
	}
	// Resolved HERE if onProject has not run yet, rather than skipped.
	// The first version returned early on an empty key, so a pane used
	// before the project resolved -- or in a directory with no `.point\`
	// at all, where onProject never produces one -- silently never
	// persisted a single command, and ↑ came back empty after every
	// restart with nothing explaining why.
	if w.term.historyKey == "" {
		w.reloadTerminalHistory()
	}
	w.term.history = append(w.term.history, line)
	if n := len(w.term.history); n > terminalHistoryCap {
		w.term.history = w.term.history[n-terminalHistoryCap:]
	}
	if err := config.SaveTerminalHistory(w.term.historyKey, w.term.history); err != nil {
		w.log.Warn("editor: saving terminal history", "err", err)
	}
}

// submitTerminalLine is Enter in the terminal pane. Two cases, decided by
// whether a process is already running -- there is only one input line in
// the pane, and which of "start a new command" or "answer this one's
// input() prompt" it means depends on that, not on anything the user
// chooses separately.
//
// An EMPTY line while a command is running is the one case the episode
// doc's "a second Enter while running is ignored with a hint" describes:
// sending a bare "\n" to a process that is not actually prompting for one
// would just spam its stdin, so this is declined with a status line
// instead of forwarded. A NON-empty line while running is the documented
// stdin feature ("covers input() prompts") and is always sent -- "one
// command at a time" is about starting a SECOND command, which Enter
// cannot do while one is already live, not about this.
func (w *Workspace) submitTerminalLine() tea.Cmd {
	line := string(w.term.input)
	w.term.input = nil
	w.term.cursor = 0
	w.term.historyIdx = -1
	w.term.historyScratch = ""

	// FIRST: make `running` mean what it says. See catchUpTerminalState --
	// ↑ then Enter is two keystrokes with nothing in between, which is
	// shorter than the time an exit takes to be delivered, and the whole
	// branch below turns on that one flag.
	w.catchUpTerminalState()

	if w.term.running {
		if line == "" {
			return w.say("a command is running — type input for it, or ctrl+c to stop it")
		}
		if w.term.stdin == nil {
			return w.say("nothing is reading input right now")
		}
		if _, err := io.WriteString(w.term.stdin, line+"\n"); err != nil {
			// A FAILED write means the child is gone, whatever the flag
			// still says -- a dead pipe is the most direct evidence
			// available. The line must not be lost to it: the reported
			// symptom was a recalled command vanishing into exactly this
			// error, so resolve the state and run it as the command it
			// was.
			w.resolveDeadRun("the process had already exited")
			return w.runTerminalLine(line)
		}
		// ECHOED AT THE CURSOR, which is where a real terminal puts it.
		//
		// The first version appended `» <line>` as its own COMMITTED
		// line, and that produced the wrong thing twice over. An
		// input() prompt is a PARTIAL line -- "your name: " with no
		// newline, sitting in pendingRunes -- so a committed echo landed
		// ABOVE it, and the child's reply then continued the still-open
		// prompt line, giving:
		//
		//	» Srijan
		//	your name: hi Srijan
		//
		// with the typed name missing from the line it was typed on. A
		// real terminal gives:
		//
		//	your name: Srijan
		//	hi Srijan
		//
		// which is what appending to the pending line and then
		// committing it produces: the typed text joins the prompt it
		// answered, and Enter ends that line, so the child's next write
		// starts a fresh one. With no prompt pending, this commits a
		// line holding just what was sent, which is still a record of
		// what was typed.
		w.term.pendingRunes = append(w.term.pendingRunes, []rune(line)...)
		w.term.commitPending()
		w.term.scrollBack = 0
		return nil
	}

	w.term.scrollBack = 0
	return w.runTerminalLine(line)
}

// runTerminalLine is the "this line is a COMMAND" half of submitting,
// factored out so the dead-pipe recovery above can reach it: a line that
// was mistakenly routed to a dead process has to end up here rather than
// being dropped.
func (w *Workspace) runTerminalLine(line string) tea.Cmd {
	if line == "" {
		return nil
	}
	if rest, ok := strings.CutPrefix(line, "!"); ok {
		// The hatch's OTHER door (app\hatch.go's own doc names both): a
		// `!`-prefixed line in the pane, alongside the global action.
		return func() tea.Msg { return app.OpenHatchMsg{Command: rest} }
	}
	w.recordTerminalHistory(line)
	cmd := w.runTerminalCommand(line)
	if note := statelessDirNote(line); note != "" {
		// Said OUT LOUD, because the alternative is a key that looks
		// broken -- see statelessDirNote.
		w.term.appendLine("[" + note + "]")
		return tea.Batch(cmd, w.say(statelessDirNoteShort))
	}
	return cmd
}

// resolveDeadRun takes the pane out of "running" when the process is
// provably gone but no exit event can arrive any more.
//
// # The state this exists to make impossible
//
// A run reports its exit exactly once (termRun.exitOnce), which is right:
// the real Wait and killTree's give-up timer both report, and only the
// first may be applied. But it means that once that one report has been
// SPENT, nothing can move the pane out of `running` -- and if the flag is
// stale at that moment, it is stale forever. Measured:
//
//	forced stale running=true      running=true  killing=false run=set
//	after first ctrl+c             running=true  killing=true
//	after second ctrl+c            running=true  killing=true
//	after killGrace + drain        running=true  killing=true   <-- stuck
//
// which is the reported "ctrl+c says stopping, then already stopping,
// and the title stays on stopping forever with no [stopped] and no exit
// code". The watchdog fired and its finish() was a no-op.
//
// So the pane resolves itself rather than waiting for a messenger that
// cannot come. Local, synchronous, and it reports an outcome line like
// any other end-of-run, because a run that ends with nothing written
// where the exit code goes is the thing that looked broken.
func (w *Workspace) resolveDeadRun(why string) {
	if !w.term.running {
		return
	}
	if len(w.term.pendingRunes) > 0 || w.term.pendingCR {
		w.term.pendingCR = false
		w.term.commitPending()
	}
	// The generation moves, so anything the old run's goroutines still
	// have in flight is dropped on arrival instead of re-resolving a run
	// that has already been accounted for.
	w.term.gen++
	w.term.running = false
	w.term.killing = false
	w.term.clearsScreen = false
	w.term.bareClear = false
	w.term.stdin = nil
	w.term.cmd = nil
	w.term.run = nil
	w.term.exitLine = "stopped"
	w.term.appendLine("[stopped — " + why + "]")
	w.term.scrollBack = 0
}

// clearCommands are the command names that wipe the screen. `clear` is a
// PowerShell alias for Clear-Host, and `cls` is both a PowerShell alias
// and cmd.exe's builtin, so all three are reachable in both shells
// `terminal.shell` offers.
var clearCommands = map[string]bool{
	"cls":        true,
	"clear":      true,
	"clear-host": true,
}

// clearsScreen reports whether any part of this command line clears the
// screen, and bareClear whether clearing is ALL it does.
//
// # Read off the command TEXT, not off how much it printed
//
// The first version inferred this from output volume: a command that
// produced no bytes on either pipe was assumed to be a console-scribbler,
// because `cls` measurably emits zero chunks. That is sound as far as it
// goes and far too wide in practice -- `$x = 5`, a successful `mkdir`, any
// assignment or any quiet success landed in the same bucket and paid for
// the same full-screen repaint. Reported as a blink after every silent
// command.
//
// Reading the text is not awkward here and is worth saying why, since the
// alternative was chosen once: runTerminalCommand is handed the exact
// line, before any shell has touched it, so there is no dispatch layer to
// infer backwards through. The classification is therefore exact for the
// cases it names, and simply false for anything it does not recognise --
// which is the right failure direction. An unrecognised program that
// clears the console gets no repaint and no wipe, exactly as before this
// episode; a recognised one gets both.
//
// Segments are split on the separators both shells share, so a chained
// `cls; pytest -q` still counts as clearing -- the clear really happens --
// while `echo cls` does not, because the first word of that segment is
// `echo`.
// # `clear all` -- the case that made this match on the NAME, measured
//
// The previous version required a segment to be a single token, so
// `clear all` was classified as not-a-clear. That was reported as the
// original corruption coming back: borders gone, only a mode cycle or
// ctrl+l recovering it.
//
// It was a false NEGATIVE, not the false positive it looked like.
// `Clear-Host` is a FUNCTION with no declared parameters:
//
//	(Get-Command Clear-Host).CommandType  -> Function
//	(Get-Command Clear-Host).Parameters   -> (none)
//
// so PowerShell drops extra positional arguments into `$args` and runs
// the body anyway. Measured in a real console, `clear all` really does
// clear it and really does exit 0:
//
//	MARKER-BEFORE-clearall            <- gone from the screen afterwards
//	DONE-clearall exit=0
//
// cmd.exe's builtin behaves the same way (`cmd /c "cls all"` -> exit 0,
// screen cleared). So there is no error to surface and nothing dishonest
// about the `[exit 0]`: the command succeeded. What was wrong was Point
// declining to repair the console it had just had wiped out from under
// it, and declining to clear its own buffer to match.
//
// Requiring an exact match on the whole segment -- the obvious reading --
// would have left that exactly as broken as it was, which is why this
// matches the command NAME instead.
//
// # The one argument form that does NOT clear
//
// A help request prints help and clears nothing, also measured:
//
//	MARKER-BEFORE-clearq              <- still on screen afterwards
//	NAME ...                          <- help text on stdout
//
// so a `-?`-style segment is excluded. That is the only exclusion with
// evidence behind it; a `clear -SomethingElse` is a parameterless
// function being handed a switch, which PowerShell also ignores.
func clearsScreen(line string) (clears, bareClear bool) {
	segments := strings.FieldsFunc(line, func(r rune) bool {
		return r == ';' || r == '&' || r == '|'
	})
	named := 0
	for _, seg := range segments {
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		named++
		// The NAME, whole and case-insensitively -- `clear-variable` is
		// not `clear`, so the Clear-* cmdlet family (Clear-Variable,
		// Clear-Item, Clear-Content, Clear-RecycleBin) cannot match.
		if !clearCommands[strings.ToLower(fields[0])] {
			continue
		}
		if isHelpRequest(fields[1:]) {
			continue
		}
		clears = true
	}
	return clears, clears && named == 1
}

// isHelpRequest reports whether these arguments ask for help rather than
// for the command to run -- the measured exception above.
func isHelpRequest(args []string) bool {
	for _, a := range args {
		switch strings.ToLower(a) {
		case "-?", "/?", "-h", "-help", "--help":
			return true
		}
	}
	return false
}

// statelessDirNote answers `cd` with the truth about Tier 0 instead of
// leaving it looking like a no-op.
//
// # Tier 0 is stateless per command, by design, and this is the record
//
// Every command is its own shell process with Cmd.Dir set to the resolved
// project root (runTerminalCommand). So `cd ..` runs, succeeds, changes
// that child's own directory, and the child exits -- taking the change
// with it. The NEXT command starts at the project root again. F14's Build
// section specifies exactly this ("working directory = project root,
// shown in the pane header") and nothing in it asks for a directory that
// persists between commands, so a persistent cwd is NOT built here.
//
// What was wrong was not the behaviour but the silence: `cd Backend`
// printed nothing, changed nothing visible, and left the header reading
// the same path, which is indistinguishable from a broken command. It
// also made a real error look like garbling -- `uv run python cli.py`
// fails with "can't open file 'D:\Projects\Point\cli.py'" precisely
// because cli.py is in backend\ and the command did not start there.
//
// The answer a user needs is the one-liner this returns, plus the
// workaround: put the cd in the SAME command (`cd backend; uv run python
// cli.py --help`), where it does exactly what they expect.
func statelessDirNote(line string) string {
	trimmed := strings.TrimSpace(line)
	// A cd CHAINED with something else is the idiom this note recommends,
	// so saying it there would be nagging a user who already knows. Only
	// a cd that is the whole command has nothing to show for itself.
	if strings.ContainsAny(trimmed, ";&|") {
		return ""
	}
	first := strings.ToLower(trimmed)
	if i := strings.IndexAny(first, " \t"); i >= 0 {
		first = first[:i]
	}
	switch first {
	case "cd", "chdir", "sl", "set-location", "pushd", "popd":
		return statelessDirNoteText
	}
	return ""
}

// The note, in two lengths.
//
// The PANE gets the long one: it wraps now (terminalVisibleLines), so
// there is room to say what to do instead.
//
// The STATUS BAR gets the short one, and that split is a bug fix. The bar
// is a single unboxed line sharing its width with the right-hand
// `connected · N task(s) · W×H`, and the long note was reported
// overflowing past the edge instead of wrapping -- which it cannot do
// there, because a status bar that wrapped would cost a row and shift the
// whole frame. Short enough that it fits any width Point will run at is
// the only thing that cannot overflow. ASCII-only too: the bar's fit is
// computed with runewidth and drawn by the terminal, and the two need not
// agree about a glyph like an em-dash.
const (
	statelessDirNoteText  = "each command starts in the project root -- put the cd in the same line, e.g. `cd backend; uv run python cli.py --help`"
	statelessDirNoteShort = "cd applies to this command only"
)

// runTerminalCommand starts Tier 0's one child process: a shell wrapping
// `line`, stdout and stderr piped and merged into the scrollback, stdin
// piped for the next typed line. "Windows specifics: run commands through
// cmd /c or PowerShell explicitly" -- config.TerminalShell, not a POSIX
// shell assumption.
func (w *Workspace) runTerminalCommand(line string) tea.Cmd {
	w.term.gen++
	gen := w.term.gen
	dir := w.terminalDir()

	var c *exec.Cmd
	switch w.terminalShell() {
	case config.ShellCmd:
		c = exec.Command("cmd", "/c", line)
	default:
		c = exec.Command("powershell", "-NoLogo", "-NoProfile", "-Command", line)
	}
	c.Dir = dir

	// A start failure goes into the PANE, not only onto the status line.
	//
	// It was status-line-only, and that is a real failure mode rather than
	// a theoretical one: Cmd.Dir is the resolved project root, so a root
	// that has been renamed, unmounted or never existed fails every
	// command here with "chdir ...: The system cannot find the file
	// specified". On the status line alone that line is transient and
	// competes with every other status writer, so the pane just sat there
	// looking like it had ignored the key. Found by a test whose fake
	// project root did not exist on disk.
	fail := func(err error) tea.Cmd {
		w.term.appendLine("$ " + line)
		w.term.appendLine("[could not start: " + err.Error() + "]")
		w.term.scrollBack = 0
		w.term.run = nil
		return w.say("could not start: " + err.Error())
	}

	stdin, err := c.StdinPipe()
	if err != nil {
		return fail(err)
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		return fail(err)
	}
	stderr, err := c.StderrPipe()
	if err != nil {
		return fail(err)
	}
	if err := c.Start(); err != nil {
		return fail(err)
	}

	// A clear WIPES the buffer, which is the whole point of running it --
	// a repaint alone fixed the corrupted frame and left every old line
	// exactly where it was, so `cls` appeared to do nothing but blink.
	// Done at START, where a real terminal does it, so a chained
	// `cls; echo hi` leaves `hi` on a cleared pane.
	clears, bare := clearsScreen(line)
	if clears {
		w.term.lines = nil
		w.term.pendingRunes = w.term.pendingRunes[:0]
		w.term.pendingCR = false
		w.term.scrollBack = 0
		w.term.exitLine = ""
	}
	w.term.clearsScreen = clears
	// A line whose ONLY job was clearing leaves nothing behind at all --
	// not its own echo, and not an exit marker when it succeeded. That is
	// what a cleared screen means, and it is what makes the buffer
	// genuinely empty afterwards rather than two lines shorter. A clear
	// CHAINED with real work keeps both, because the exit code of that
	// work still matters.
	w.term.bareClear = bare
	if !bare {
		w.term.appendLine("$ " + line)
	}
	w.term.running = true
	w.term.cmd = c
	w.term.stdin = stdin

	rn := &termRun{gen: gen, cmd: c, events: w.term.events}
	w.term.run = rn

	var wg sync.WaitGroup
	wg.Add(2)
	go drainTerminalPipe(gen, rn.events, stdout, &wg)
	go drainTerminalPipe(gen, rn.events, stderr, &wg)
	go waitTerminalCommand(rn, &wg)

	return nil
}

// termRun is one command's shared handle.
//
// exitOnce is the whole point: a run can finish two ways -- the real
// cmd.Wait() returning, or the kill watchdog giving up on it -- and
// whichever happens first has to be the only termExited event the pane
// ever sees for that run, or `running` would flap back on after the pane
// had already moved on.
type termRun struct {
	gen      int
	cmd      *exec.Cmd
	events   chan termEvent
	exitOnce sync.Once
	// stoppedByUser is set before the kill is issued, so whichever path
	// reports the exit says "stopped" rather than the exit code the kill
	// happened to produce. Without it, ctrl+c on a `powershell` wrapper
	// reported "[exit 1]" -- TerminateProcess's own code -- which reads
	// as "the command failed" rather than "you stopped it". Atomic
	// because killTree sets it on the Update goroutine while
	// waitTerminalCommand reads it on its own.
	stoppedByUser atomic.Bool
	// reported is set the one time finish() actually emits. It is the
	// honest answer to "can anything still report an exit for this run?"
	// -- see Workspace.runMayStillReport.
	reported atomic.Bool
}

// finish emits this run's single termExited event. Safe to call from any
// goroutine, any number of times.
func (rn *termRun) finish(err error) {
	rn.exitOnce.Do(func() {
		rn.reported.Store(true)
		rn.events <- termEvent{
			gen:     rn.gen,
			kind:    termExited,
			err:     err,
			code:    terminalExitCode(err),
			stopped: rn.stoppedByUser.Load(),
		}
	})
}

// drainTerminalPipe feeds raw bytes (not lines) into termChunk events --
// the \r/\b handling in feed needs the RAW stream, not something already
// split on \n, or a \r-redraw spanning two Read calls would be split
// across two "lines" instead of collapsing into one.
func drainTerminalPipe(gen int, events chan termEvent, r io.Reader, wg *sync.WaitGroup) {
	defer wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			events <- termEvent{gen: gen, kind: termChunk, text: string(buf[:n])}
		}
		if err != nil {
			return
		}
	}
}

// waitTerminalCommand calls cmd.Wait() only after BOTH pipe readers have
// returned -- os/exec's own Wait doc: "it is incorrect to call Wait before
// all reads from the pipe have completed," because Wait closes the pipes
// the moment the process exits, racing a reader still mid-Read.
//
// This ordering is also exactly why killing the wrapper alone wedged the
// pane: see killTree.
func waitTerminalCommand(rn *termRun, wg *sync.WaitGroup) {
	wg.Wait()
	rn.finish(rn.cmd.Wait())
}

// terminalExitCode reads what os/exec gives back. -1 (unknown) rather than
// a guess when it is not a plain *exec.ExitError.
func terminalExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// killGrace is how long killTree waits for the tree to actually die and
// the pipes to close before the pane stops believing the process is
// running. Generous enough that the ordinary case reports its real exit
// code, short enough that a pane cannot sit on "running" indefinitely.
const killGrace = 3 * time.Second

// errStoppedNoExit is what a run reports when the tree was killed and the
// pipes still had not closed within killGrace -- a state the pane has to
// be able to leave even if some descendant is unkillable.
var errStoppedNoExit = errors.New("stopped; the process did not report an exit")

// killTree ends the whole process tree rooted at the shell wrapper, and it
// is the fix for the single worst bug F14 shipped.
//
// # Why Process.Kill() is not enough, measured rather than assumed
//
// Tier 0 runs every command through a shell (`powershell -Command ...`),
// so w.term.cmd is the WRAPPER -- `pytest` is its grandchild. Three things
// then went wrong on a single ctrl+c, and all three came from killing the
// wrapper alone:
//
//  1. Kill() returns nil and the wrapper dies, but the grandchild keeps
//     running and keeps its own copies of the stdout/stderr pipe write
//     handles open. A pipe reaches EOF only when the LAST writer closes
//     it, so both drain goroutines stayed blocked in Read forever,
//     wg.Wait() never returned, cmd.Wait() was never called, and no
//     termExited event was ever emitted. The pane sat on "running"
//     permanently with nothing able to clear it. Reproduced directly: a
//     wrapper with one live grandchild, killed, still had not EOF'd six
//     seconds later.
//  2. The second ctrl+c then called TerminateProcess against the wrapper's
//     handle, which had already exited, and Windows answers that with
//     "Access is denied" -- reported verbatim by a user as the second
//     press's error.
//  3. The grandchild itself survived Point entirely.
//
// `taskkill /T /F` kills the tree by PID, is part of base Windows, and
// needs no new dependency, no job object and no syscall package. The
// watchdog is the backstop for the case where even that leaves a writer
// alive: the pane always gets an exit event within killGrace.
func (w *Workspace) killTree(note string) tea.Cmd {
	rn := w.term.run
	if rn == nil || rn.cmd == nil || rn.cmd.Process == nil {
		return nil
	}
	pid := rn.cmd.Process.Pid
	w.term.killing = true
	// Set BEFORE the kill, so whichever of Wait or the watchdog reports
	// the exit knows this was a ctrl+c and not a failure.
	rn.stoppedByUser.Store(true)

	// /T the tree, /F without asking. Failure is not fatal and not
	// reported as an error to the user: the fallback below is what
	// actually has to work, and taskkill's own stderr ("process not
	// found") is routine when the tree has just exited on its own.
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	if err := kill.Run(); err != nil {
		w.log.Debug("editor: taskkill fell back to Process.Kill", "pid", pid, "err", err)
		if err := rn.cmd.Process.Kill(); err != nil {
			w.log.Debug("editor: Process.Kill also failed", "pid", pid, "err", err)
		}
	}

	go func() {
		time.Sleep(killGrace)
		rn.finish(errStoppedNoExit)
	}()

	return w.say(note)
}

// ---------------------------------------------------------------------------
// Output: ANSI stripped, \r/\b collapsed, capped at terminalLineCap
// ---------------------------------------------------------------------------

// appendLine commits one whole line directly -- the echoed "$ command" and
// the "[exit N]" marker both go straight in, bypassing feed's \r/\b state
// machine because they are never partial.
func (t *terminalPane) appendLine(s string) {
	t.lines = append(t.lines, s)
	if n := len(t.lines); n > terminalLineCap {
		t.lines = t.lines[n-terminalLineCap:]
	}
}

// commitPending moves the line being built into the committed buffer and
// starts a new one.
func (t *terminalPane) commitPending() {
	t.appendLine(string(t.pendingRunes))
	t.pendingRunes = t.pendingRunes[:0]
}

// feed turns raw child output into committed lines. ANSI sequences are
// stripped first (never interpreted -- this is not an emulator), and what
// is left is walked rune by rune:
//
//	\r\n a line ending: commit the pending line
//	\n   a line ending on its own: the same
//	\r   NOT followed by \n -- a progress redraw: discard the pending line
//	     and restart at column 0. Not cursor math (that is VT100
//	     emulation, rejected), just enough that a hundred \r-redraws with
//	     no newline between them commit as ONE line instead of a hundred
//	     near-duplicates
//	\b   delete the last pending rune (spinners, \b-edited progress text)
//	\t   four spaces -- a column-aware tab stop is the same VT100
//	     territory \r's full version would be
//
// Other C0 control bytes are dropped rather than rendered as literal junk.
//
// # The CRLF bug this function shipped with, which blanked ALL output
//
// The first version treated EVERY \r as a redraw and so cleared the
// pending line on the CR of every CRLF, leaving the following LF to commit
// an empty string. On Windows -- where every line ends CRLF -- that turned
// every single line of every command's output into a blank row. The
// symptom was a pane that showed "$ pytest -q", a run of empty lines, and
// "[exit 1]", with none of pytest's actual output: it was all there, all
// of it blanked, one line at a time.
//
// It is a two-rune decision, so it needs one rune of lookahead, and the
// lookahead has to survive a CHUNK boundary because the CR and the LF
// really do arrive in separate reads. Hence pendingCR rather than
// strings.ReplaceAll("\r\n", "\n") on each chunk, which looks equivalent
// and silently fails on exactly that split.
func (t *terminalPane) feed(chunk string) {
	chunk = ansiOSC.ReplaceAllString(chunk, "")
	chunk = ansiCSI.ReplaceAllString(chunk, "")
	for _, r := range chunk {
		if t.pendingCR {
			t.pendingCR = false
			if r == '\n' {
				// CRLF: one line ending, not a redraw plus a blank.
				t.commitPending()
				continue
			}
			// A bare CR after all: the redraw case. The rune in hand is
			// the first of the replacement line, so it still has to be
			// processed below.
			t.pendingRunes = t.pendingRunes[:0]
		}
		switch r {
		case '\r':
			t.pendingCR = true
		case '\n':
			t.commitPending()
		case '\b':
			if n := len(t.pendingRunes); n > 0 {
				t.pendingRunes = t.pendingRunes[:n-1]
			}
		case '\t':
			t.pendingRunes = append(t.pendingRunes, ' ', ' ', ' ', ' ')
		default:
			if r >= 0x20 {
				t.pendingRunes = append(t.pendingRunes, r)
			}
		}
	}
}

// onTerminalEvent applies one event from the pane's channel and ALWAYS
// re-arms next() -- the loop never stops, see next's own doc.
func (w *Workspace) onTerminalEvent(ev termEvent) tea.Cmd {
	next := w.term.next()
	clear := w.applyTerminalEvent(ev)
	if clear != nil {
		return tea.Batch(next, clear)
	}
	return next
}

// catchUpTerminalState applies every event ALREADY sitting in the channel,
// without waiting for one and without touching the pull loop.
//
// # Why a flag that lags the process is not good enough
//
// `running` is only as current as the last delivered event, and delivery
// goes through Bubble Tea: the goroutines put the exit in the channel, the
// puller picks it up, and Update applies it -- some milliseconds later.
// Every keystroke processed inside that window reads a `running` that is
// TRUE for a process which has already finished.
//
// That is not theoretical. Measured, with the reported keystrokes:
//
//	second finished, events UNDRAINED      running=true  queued=3
//	after up-arrow                         input="echo second"
//	after immediate Enter  <-- THE BUG     input=""  (no new line, gen unchanged)
//
// The recalled command went down the stdin branch to a dead pipe, the
// write failed, and the command was LOST -- "↑ then Enter immediately"
// reproduces it every time because recall-and-submit is two keystrokes
// with nothing in between, which is exactly shorter than the delivery
// window. Typing a short command fast does the same thing, which is the
// other report (`hi`) reaching the same dead end by a different route.
//
// So anything that BRANCHES on whether a process is running catches up
// first. Reading the channel here does not starve the puller: the puller
// is blocked on the same channel and simply receives whatever arrives
// next, and each value is still delivered exactly once.
func (w *Workspace) catchUpTerminalState() {
	for {
		select {
		case ev := <-w.term.events:
			// The returned repaint is deliberately dropped: this runs
			// inside a key handler that is about to cause a frame
			// anyway, and the caller's own command is what matters.
			_ = w.applyTerminalEvent(ev)
		default:
			return
		}
	}
}

// applyTerminalEvent is the state change for one event, with no re-arm.
// It returns an optional command (the repaint a command exit asks for).
func (w *Workspace) applyTerminalEvent(ev termEvent) tea.Cmd {
	if ev.gen != w.term.gen {
		// A stale run: killed, or superseded by one started since. Its
		// own goroutines are still draining to EOF/Wait so they can exit
		// cleanly, but nothing they report is applied.
		return nil
	}
	switch ev.kind {
	case termChunk:
		w.term.feed(ev.text)
		// Done-when 7: output while focus is in the CODE pane auto-
		// expands to overlay, without stealing focus -- effectiveFocus,
		// not w.focus, so the tree does not trigger it (the episode doc
		// names the code pane specifically).
		if w.effectiveFocus() == focusBuffer {
			w.term.autoExpanded = true
		}

	case termExited:
		if !w.term.running {
			// Already resolved. A run can be told to finish twice now --
			// the real Wait and killTree's give-up path both report, and
			// either may arrive first -- so the second one must not
			// append a second outcome line.
			return nil
		}
		if len(w.term.pendingRunes) > 0 || w.term.pendingCR {
			// Output that ended without a newline is still output. A
			// `print(..., end="")` or a prompt left on screen when the
			// process died would otherwise be dropped on the floor at
			// exactly the moment the user wants to read it.
			w.term.pendingCR = false
			w.term.commitPending()
		}
		clears, bare := w.term.clearsScreen, w.term.bareClear
		w.term.running = false
		w.term.killing = false
		w.term.stdin = nil
		w.term.cmd = nil
		w.term.run = nil
		w.term.clearsScreen = false
		w.term.bareClear = false
		switch {
		case ev.stopped:
			w.term.exitLine = "stopped"
		default:
			w.term.exitLine = fmt.Sprintf("exit %d", ev.code)
		}
		// A bare clear that SUCCEEDED writes nothing; one that failed
		// reports like anything else, because a failure nobody can see is
		// the fault this episode has already been bitten by twice.
		if !bare || ev.stopped || ev.code != 0 {
			w.term.appendLine("[" + w.term.exitLine + "]")
		} else {
			w.term.exitLine = ""
		}
		if w.effectiveFocus() == focusBuffer {
			w.term.autoExpanded = true
		}

		// A full repaint, but ONLY for a command KNOWN to clear the
		// console.
		//
		// A child can write straight to the console Point is drawing on,
		// bypassing the pipes entirely. `cls` is the plain case:
		// measured through the real runner it produces ZERO chunks --
		// PowerShell's Clear-Host goes to the console API, not to our
		// stdout -- and it wipes Point's own frame underneath. Bubble
		// Tea's renderer only rewrites rows it believes changed, so the
		// wiped rows stay blank: the reported "borders and spacing
		// disappear, recoverable only by cycling to full and back".
		//
		// Two narrowings, in two rounds. The first fix repainted after
		// EVERY command (a blink on every `echo`); the second narrowed it
		// to commands that printed nothing, which still caught `$x = 5`
		// and every other quiet success. This one asks the command text
		// -- see clearsScreen for why that is both reliable here and the
		// right failure direction.
		if clears {
			return tea.ClearScreen
		}
	}
	return nil
}

// StopRunningProcess is app.ProcessStopper, called from unloadWorkspace and
// doQuit -- see that interface's own doc for why a Tier 0 child needs this
// at all. The TREE, for killTree's reasons: a wrapper-only kill at quit
// would leave the grandchild (the actual `npm run dev`) running after
// Point is gone, which is the worst version of the bug because there is no
// longer a pane to notice it from.
//
// Synchronous and silent: this fires during teardown, where there is no
// status line left to write to and no later frame to show it on.
func (w *Workspace) StopRunningProcess() {
	if !w.term.running {
		return
	}
	rn := w.term.run
	if rn == nil || rn.cmd == nil || rn.cmd.Process == nil {
		return
	}
	pid := rn.cmd.Process.Pid
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run(); err != nil {
		_ = rn.cmd.Process.Kill()
	}
}

// killRunning is ctrl+c in the terminal pane: the CHILD dies, not Point.
//
// A SECOND press while the first is still settling says so rather than
// firing another kill -- the old version re-entered the kill path and
// surfaced "TerminateProcess: Access is denied" from the already-dead
// wrapper, which read as "ctrl+c is broken" when the actual problem was
// that the first press had no way to report progress.
func (w *Workspace) killRunning() tea.Cmd {
	// Catch up before deciding, for submitTerminalLine's reason: ctrl+c
	// on a command that finished a few milliseconds ago would otherwise
	// go off and kill a pid that is already gone.
	w.catchUpTerminalState()
	if !w.term.running {
		return nil
	}

	// A SECOND press no longer just repeats "give it a moment" forever.
	// If the first kill has not produced an exit and the process is no
	// longer alive, there is nothing left that can report one -- so the
	// pane resolves itself rather than sitting on "stopping" (the
	// reported permanent stick; see resolveDeadRun).
	if w.term.killing {
		if w.runMayStillReport() {
			return w.say("already stopping it — give it a moment")
		}
		w.resolveDeadRun("it did not report an exit")
		return w.say("stopped")
	}

	// Even the FIRST press can arrive after the process is gone but
	// before its exit was delivered, in which case killing a dead pid
	// and arming a watchdog whose report may already be spent is exactly
	// how the stick was reached.
	if !w.runMayStillReport() {
		w.resolveDeadRun("the process had already exited")
		return w.say("stopped")
	}
	return w.killTree("stopping...")
}

// runMayStillReport answers the only question killRunning actually needs:
// is there anything left that could deliver an exit for this run?
//
// # Not a liveness probe, because that cannot be done this way on Windows
//
// This was Signal(syscall.Signal(0)), the portable "is it there" trick,
// and it was measured WRONG on the platform this project runs on:
//
//	alive while Start-Sleep 20 runs: false (pid 14140)
//	after ctrl+c: [stopped — the process had already exited]
//
// Go's os.Process.Signal rejects anything but Kill/Interrupt on Windows,
// so it errors for a live process exactly as it does for a dead one.
// Shipped, that would have made ctrl+c never kill anything: it would
// declare every command already finished and leave the real process
// running with nothing tracking it.
//
// A run's own exitOnce already holds the answer. Once finish() has
// emitted, no second report is possible (that is the whole point of the
// Once), so a pane still showing `running` at that point can only be
// freed locally. Before it has emitted, the waiter or the kill watchdog
// is still coming, and the kill is the right move.
func (w *Workspace) runMayStillReport() bool {
	rn := w.term.run
	if rn == nil || rn.cmd == nil || rn.cmd.Process == nil {
		return false
	}
	return !rn.reported.Load()
}

// terminalShell reads config.toml's [terminal] table through the same
// Options the editor already takes everything else config-shaped through
// -- a VALUE at construction time, not a live config.Config, matching how
// Autosave/AutosaveDelay already work in this same struct.
func (w *Workspace) terminalShell() string {
	if w.shell == config.ShellCmd {
		return config.ShellCmd
	}
	return config.ShellPowerShell
}

// ---------------------------------------------------------------------------
// Key handling -- reached from keys.go once the terminal pane has focus
// ---------------------------------------------------------------------------

// terminalKey is the pane's own keymap: line editing plus ↑/↓ history
// recall. ctrl+c is handled one level up in keys.go, before this is ever
// reached, because it means two different things depending on w.term.running
// and keys.go's own ctrl+c branch already has to make that call before
// deciding whether to fall through to copy.
func (w *Workspace) terminalKey(k tea.KeyMsg, key string) tea.Cmd {
	switch key {
	case "enter":
		return w.submitTerminalLine()
	case "left":
		if w.term.cursor > 0 {
			w.term.cursor--
		}
	case "right":
		if w.term.cursor < len(w.term.input) {
			w.term.cursor++
		}
	case "home":
		w.term.cursor = 0
	case "end":
		w.term.cursor = len(w.term.input)
	case "backspace":
		if w.term.cursor > 0 {
			w.term.input = append(w.term.input[:w.term.cursor-1], w.term.input[w.term.cursor:]...)
			w.term.cursor--
		}
	case "delete":
		if w.term.cursor < len(w.term.input) {
			w.term.input = append(w.term.input[:w.term.cursor], w.term.input[w.term.cursor+1:]...)
		}
	case "up":
		w.terminalHistoryUp()
	case "down":
		w.terminalHistoryDown()
	case "pgup":
		w.scrollTerminal(-w.terminalOutputRows())
	case "pgdown":
		w.scrollTerminal(+w.terminalOutputRows())
	default:
		if k.Type == tea.KeyRunes && !k.Alt {
			w.term.insertRunes(k.Runes)
		} else if k.Type == tea.KeySpace {
			w.term.insertRunes([]rune{' '})
		}
	}
	return nil
}

// terminalOutputRows is how many rows of scrollback are on screen, read
// off the rect View last drew -- the same "record it, never re-derive it"
// rule mouse.go's hit-testing follows. One row is the input line; two are
// the borders.
func (w *Workspace) terminalOutputRows() int {
	rows := w.termRect.H - 3
	if rows < 1 {
		return 1
	}
	return rows
}

// pasteIntoTerminal is app.PasteMsg with the terminal focused.
//
// F14's Build section does not mention paste at all -- it asks for "a
// typed line". It is wired anyway, because the pane IS a text input and
// the paste path already exists for the buffer: with it unwired, ctrl+v
// into the prompt silently did nothing, which is indistinguishable from a
// broken key. Newlines are flattened to spaces rather than submitted as
// several commands -- "one command at a time" is the episode's own rule,
// and a multi-line paste that fired four commands would break it in the
// most surprising way available.
func (w *Workspace) pasteIntoTerminal(text string) {
	flat := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(text)
	w.term.insertRunes([]rune(flat))
}

func (t *terminalPane) insertRunes(rs []rune) {
	// The tail is copied into a fresh slice FIRST, before anything appends
	// onto t.input's own backing array and potentially overwrites it in
	// place -- the standard hazard of "insert into a slice via append".
	tail := append([]rune{}, t.input[t.cursor:]...)
	t.input = append(t.input[:t.cursor], rs...)
	t.input = append(t.input, tail...)
	t.cursor += len(rs)
}

// terminalHistoryUp recalls an older command, saving the in-progress line
// (historyScratch) the first time so ↓ can get back to it rather than
// losing whatever was being typed before ↑ was pressed.
func (w *Workspace) terminalHistoryUp() {
	if len(w.term.history) == 0 {
		return
	}
	if w.term.historyIdx == -1 {
		w.term.historyScratch = string(w.term.input)
		w.term.historyIdx = len(w.term.history)
	}
	if w.term.historyIdx > 0 {
		w.term.historyIdx--
	}
	w.term.setInput(w.term.history[w.term.historyIdx])
}

func (w *Workspace) terminalHistoryDown() {
	if w.term.historyIdx == -1 {
		return
	}
	w.term.historyIdx++
	if w.term.historyIdx >= len(w.term.history) {
		w.term.historyIdx = -1
		w.term.setInput(w.term.historyScratch)
		return
	}
	w.term.setInput(w.term.history[w.term.historyIdx])
}

func (t *terminalPane) setInput(s string) {
	t.input = []rune(s)
	t.cursor = len(t.input)
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// terminalBox replaces render.go's F14 placeholder. Content is the
// scrollback tail plus one input row PINNED at the bottom -- in TermSmall
// (rows == 1) that is the input row ALONE, which is exactly the mockup in
// LAYOUT.md's Editor sketch ("$ pytest -q" is the only content row shown)
// and exactly why Done-when 7's auto-expand exists: with one row, there is
// no room to show output at all while small, so output while away has to
// claim more room or go unseen.
func (w *Workspace) terminalBox(r layout.Rect) string {
	if r.IsZero() {
		return ""
	}
	w.termRect = r
	focused := w.effectiveFocus() == focusTerminal
	rows := r.H - 2
	if rows <= 0 {
		return paneBox(w.edge(focused), w.label(focused), r, "Terminal", nil, 1)
	}

	title := "Terminal · " + w.terminalDir()
	switch {
	case w.term.killing:
		title += " · stopping"
	case w.term.running:
		title += " · running"
	case w.term.exitLine != "":
		title += " · " + w.term.exitLine
	}
	if w.term.scrollBack > 0 {
		title += fmt.Sprintf(" · ↑%d", w.term.scrollBack)
	}

	// The line the child is still writing to belongs ON the input row,
	// not above it -- see terminalInputRow. It is therefore excluded from
	// the output rows, or it would appear twice.
	inner := r.W - 4
	inline := string(w.term.pendingRunes)

	body := make([]string, 0, rows)
	if outputRows := rows - 1; outputRows > 0 {
		// r.W less two borders and paneBox's one cell of padding each
		// side -- the width a body row actually gets.
		visible := w.terminalVisibleLines(outputRows, inner)
		// Padded ABOVE, not below. The first version padded after the
		// tail, which pinned output to the TOP of the box and left a gap
		// between the last line and the prompt -- so a one-line result
		// sat three rows away from the command that produced it, with
		// blank rows in between that looked like missing output.
		for i := len(visible); i < outputRows; i++ {
			body = append(body, "")
		}
		style := w.theme.Style(theme.TextPrimary)
		for _, ln := range visible {
			body = append(body, style.Render(ln))
		}
	}
	body = append(body, w.terminalInputRow(focused, inline, inner))
	return paneBox(w.edge(focused), w.label(focused), r, title, body, 1)
}

// terminalVisibleLines is the window of scrollback to draw, newest last.
//
// The live PARTIAL line counts as a line here -- that is the whole fix for
// an invisible `input()` prompt. "name: " arrives with no trailing newline,
// so it never commits, and a renderer that draws only committed lines
// shows an empty pane while the child sits waiting for an answer. Measured
// against a real child: `Write-Host -NoNewline 'name: '` produces exactly
// one chunk and zero committed lines.
// WRAPPED, not truncated, and that was a real report rather than a
// refinement. `uv run python cli.py --help` from the project root fails
// with a 120-character message naming the interpreter, the file it could
// not open and the errno. The buffer held every character of it
// (verified against the raw chunks), and the pane showed
// "…\python.exe:ca" and stopped -- the row was cut to the box width with
// no marker and no way to see the rest, which read as garbled output
// from a broken pane rather than as a plain file-not-found. A terminal
// wraps; so does this.
func (w *Workspace) terminalVisibleLines(rows, width int) []string {
	// The pending partial line is NOT included: terminalBox draws it on
	// the input row instead, inline with what is being typed.
	all := w.term.lines

	end := len(all) - w.term.scrollBack
	if end < 0 {
		end = 0
	}
	all = all[:end]

	// Wrapped from the BOTTOM: only the tail is ever on screen, so only
	// the tail is worth wrapping, and wrapping the whole 5k-line buffer
	// on every frame to throw nearly all of it away would be the kind of
	// per-frame cost F13's render cache exists to avoid.
	out := make([]string, 0, rows)
	for i := len(all) - 1; i >= 0 && len(out) < rows; i-- {
		seg := wrapTerminalLine(all[i], width)
		for j := len(seg) - 1; j >= 0 && len(out) < rows; j-- {
			out = append(out, seg[j])
		}
	}
	// Reverse: collected newest-first, drawn oldest-first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// wrapTerminalLine splits one line into display-width pieces, measured in
// CELLS so a line of CJK wraps where it actually reaches the wall.
//
// NOT explainpanel.go's wrapCells, deliberately: that one is a WORD
// wrapper built on strings.Fields, which collapses runs of spaces and
// reflows at word boundaries. That is right for prose an agent wrote and
// wrong for program output, where the spaces are the layout -- a wrapped
// `--help` table or a progress bar would come back shredded. Terminal
// output wraps at the column, keeping every byte in order.
func wrapTerminalLine(s string, width int) []string {
	if width <= 0 || s == "" {
		return []string{s}
	}
	if visibleWidth(s) <= width {
		return []string{s}
	}
	var out []string
	var cur []rune
	col := 0
	for _, r := range s {
		c := runeCells(r, col)
		if col+c > width && len(cur) > 0 {
			out = append(out, string(cur))
			cur, col = cur[:0:0], 0
			c = runeCells(r, 0)
		}
		cur = append(cur, r)
		col += c
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

// stdinMarker distinguishes a line that was sent to the RUNNING process
// from `$`, which starts a new command. Two different things happen when
// you press Enter, so they cannot share one prompt.
const stdinMarker = "»"

// terminalInputRow draws the prompt plus the typed line, with the cursor
// shown as a reversed cell -- the same trick caretStyle already uses for
// the buffer's own caret.
func (w *Workspace) terminalInputRow(focused bool, inline string, width int) string {
	marker := "$ "
	if w.term.running {
		marker = stdinMarker
		if inline == "" {
			marker += " "
		}
	}

	typed := w.term.input
	cursor := w.term.cursor

	// Keep the CARET visible when prompt + marker + typed is wider than
	// the box: drop leading cells of the prompt rather than letting
	// padCells cut the tail, which would hide the thing being typed.
	if width > 8 {
		for visibleWidth(inline)+visibleWidth(marker)+visibleWidth(string(typed))+1 > width &&
			len([]rune(inline)) > 0 {
			ir := []rune(inline)
			inline = "…" + string(ir[2:])
			if len(ir) <= 2 {
				inline = ""
			}
		}
	}

	dim := w.theme.Style(theme.TextDim)
	text := w.theme.Style(theme.TextPrimary)
	head := text.Render(inline) + dim.Render(marker)

	if !focused {
		return head + text.Render(string(typed))
	}
	before := string(typed[:cursor])
	var at, after string
	if cursor < len(typed) {
		at = string(typed[cursor])
		after = string(typed[cursor+1:])
	} else {
		at = " "
	}
	return head +
		text.Render(before) +
		w.caretStyle(theme.TextPrimary).Render(at) +
		text.Render(after)
}

// scrollTerminal moves the view up (negative) or down through the
// scrollback -- the "scrollable" half of `small (3-4 rows, scrollable)`,
// which the first version left unimplemented entirely.
func (w *Workspace) scrollTerminal(by int) {
	w.term.scrollBack -= by
	if w.term.scrollBack < 0 {
		w.term.scrollBack = 0
	}
	if max := len(w.term.lines); w.term.scrollBack > max {
		w.term.scrollBack = max
	}
}
