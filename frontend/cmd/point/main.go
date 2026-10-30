// Command point is the Point terminal workspace.
//
//	cd D:\Projects\Point
//	go run .\frontend\cmd\point
//
// It spawns the Python backend as a child process with its working directory
// set to backend\, and talks to it over newline-delimited JSON on stdin and
// stdout.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/chat"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/editor"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "point:", err)
		os.Exit(1)
	}
}

func run() error {
	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("preparing %s: %w", config.StateDir(), err)
	}

	env := app.Environment{
		ConfigPath: config.ConfigPath(),
		StatePath:  config.StatePath(),
	}

	// A missing config is normal; a broken one is non-fatal but not silent.
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		env.ConfigError = cfgErr.Error()
	}
	state, stateErr := config.LoadState()
	if stateErr != nil {
		env.StateError = stateErr.Error()
	}

	logFile, err := os.OpenFile(filepath.Join(config.LogDir(), "frontend.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening the frontend log: %w", err)
	}
	defer logFile.Close()

	log := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelDebug}))
	log.Info("point starting",
		"config", config.ConfigPath(), "state", config.StatePath())

	// Both are no-ops unless their env var is set — see diagnostics.go.
	stopMemLog := startMemLog(log)
	defer stopMemLog()
	stopPprof := startPprof(log)
	defer stopPprof()

	// F10's Appearance tab writes a state.json override rather than
	// config.toml (Settings never writes the user's file) — applied here,
	// on top of config.toml's own [ui] values, before anything downstream
	// picks a theme or decides whether mouse reporting is on. An empty
	// override field means "untouched"; only Mouse needs a pointer to tell
	// "never touched" from "explicitly off".
	themeName, background := cfg.UI.Theme, cfg.UI.Background
	mouseEnabled := true
	if ov := state.UI; ov != nil {
		if ov.Theme != "" {
			themeName = ov.Theme
		}
		if ov.Background != "" {
			background = ov.Background
		}
		if ov.Mouse != nil {
			mouseEnabled = *ov.Mouse
		}
	}

	th := theme.New(themeName, theme.Background(background))
	if themeName != "" && th.Name() != themeName {
		env.Notes = append(env.Notes, fmt.Sprintf(
			"unknown theme %q — using %s (available: %v)",
			themeName, th.Name(), theme.Names()))
	}

	// Python's stderr goes to its own file. Stdout is the protocol channel and
	// the two are never mixed; an undrained stderr pipe blocks Python's
	// logging the moment the buffer fills.
	stderrPath := filepath.Join(config.LogDir(), "backend-stderr.log")
	stderrLog, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening the backend stderr log: %w", err)
	}
	defer stderrLog.Close()
	env.LogFile = stderrPath

	backendDir, err := protocol.FindBackendDir()
	if err != nil {
		// Not fatal: the shell still runs and says why there is no backend,
		// which is more useful than refusing to start at all.
		log.Error("backend not found", "err", err)
		env.Notes = append(env.Notes, err.Error())
	}
	env.BackendDir = backendDir

	var client *protocol.Client
	if backendDir != "" {
		client = protocol.NewClient(protocol.Options{
			Dial: protocol.StdioDialer(backendDir, stderrLog, slogAdapter{log}),
			Log:  slogAdapter{log},
		})
	}

	// F12: chat.Workspace is a FACTORY here, not an instance.
	//
	// It cannot be constructed inside app.New either way: its Update method
	// has to return app.Workspace, which means the chat package imports app
	// — so app cannot import chat back without a cycle. This is still the
	// one place that wires the two together. What changed is WHEN: this
	// closure runs on the first switch to the Chat workspace, which is
	// after the startup chooser is answered, and never at startup. Point
	// now boots with no workspace in memory at all.
	// F13b / A2: the lock holder, generated ONCE and persisted, before
	// chatState is taken below.
	//
	// The order here is the whole of the fix for the trap F13a's handoff
	// recorded. chat.Workspace serialises the WHOLE State from the
	// snapshot it is handed, so a holder generated AFTER that line would
	// be erased by chat's next debounced draft save -- on the very launch
	// that generated it -- and Point would mint a new identity every time
	// it started. Generated here, the snapshot carries it; cleared from
	// the snapshot below, chat's writes take saveStateTo's
	// keep-what-is-on-disk branch instead. Both halves are required.
	//
	// Written through SaveState rather than left in memory because the
	// value only does its job across restarts: an identity regenerated
	// per launch cannot release a lock its predecessor took.
	if state.Holder == "" {
		state.Holder = config.NewHolder()
		if err := config.SaveState(state); err != nil {
			// Not fatal. A holder that failed to persist still works for
			// this session -- the lock is taken and released by the same
			// in-memory value -- and the only cost is that a lock left
			// behind by a crash has to wait out B7's 60-second staleness
			// timeout instead of being re-acquired by name.
			log.Warn("persisting the editor lock holder", "err", err)
		}
	}

	chatState := state
	// The two fields app.Root owns are cleared from the copy chat gets, and
	// this is load-bearing rather than tidy. Both writers serialise the
	// WHOLE State from their own in-memory copy (chat\draft.go's
	// persistDraft, app\lifecycle.go's saveWorkspaceStateIfDue), so a
	// stale copy of a field you do not own erases the owner's newer value:
	// type one character and the scroll anchor is gone. Zeroed here, chat's
	// writes always take saveStateTo's keep-what-is-on-disk branch. See
	// config\state.go's saveStateTo for the whole rule.
	chatState.LastWorkspace = ""
	chatState.WorkspaceState = nil
	// F13b: and the holder, for exactly the same reason -- it is owned
	// here, not by chat, and a stale copy of it in chat's snapshot is how
	// it gets erased. See config\state.go's saveStateTo.
	chatState.Holder = ""

	newChat := func() app.Workspace {
		return chat.New(chat.Options{
			Theme:        th,
			Client:       client,
			Log:          log,
			Scrollback:   cfg.UI.Scrollback,
			HistoryBatch: cfg.UI.HistoryBatch,
			// F5: ui.stream_timeout, in seconds — the client-side
			// watchdog that stops a spinner no backend event is ever
			// going to stop.
			StreamTimeout: time.Duration(cfg.UI.StreamTimeout) * time.Second,
			// F4: chat.Workspace reads Drafts out of this and writes it
			// back, debounced, as the query input's buffer changes — see
			// draft.go.
			State: chatState,
		})
	}

	// F13: the same factory shape, for the same reason — editor.Workspace's
	// Update returns app.Workspace, so editor imports app and app cannot
	// import editor back.
	//
	// It still takes no State. F13b needs the holder key out of that
	// file, but it RECEIVES the composed value rather than reading or
	// writing the file itself: main.go generates and persists it above,
	// which keeps the two-writer rule config\state.go's saveStateTo
	// arbitrates exactly as it was for Holder.
	//
	// F14 is the exception, and a deliberate third writer rather than an
	// accidental one: editor.Workspace calls config.SaveTerminalHistory
	// directly (terminal.go), because command history is keyed per
	// PROJECT -- something only this workspace resolves -- and routing
	// every history write through main.go the way Holder is routed would
	// mean main.go learning the project binding just to pass it back.
	// saveStateTo's own keep-if-zero clause was extended for exactly this,
	// the same way it already was for Holder.
	newEditor := func() app.Workspace {
		return editor.New(editor.Options{
			Theme:  th,
			Client: client,
			Log:    log,
			Holder: state.Holder,
			// D90's two keys. Resolved here rather than inside the
			// editor so the workspace takes values, not a Config -- the
			// same shape chat's Scrollback/StreamTimeout/HistoryBatch
			// already use.
			Autosave:      cfg.AutosaveMode(),
			AutosaveDelay: cfg.AutosaveDelay(),
			// F14's own value-not-Config key, same shape as Autosave.
			Shell: cfg.TerminalShell(),
			// F15: the stored provider default, so the session the
			// Editor creates for its own agent invocations resolves the
			// provider the user actually picked rather than config.toml's
			// -- which in Demo mode is a different provider entirely. A
			// VALUE off the same State, read-only; this does not make the
			// editor a SECOND writer of state.json (F14's own writer is
			// the first; see above), and the two fields Root owns are
			// not involved.
			Provider: state.Provider,
		})
	}

	// Repaired BEFORE app.New (which copies env) and before
	// tea.NewProgram (which saves the console mode it will later try to
	// restore) -- see consolemode.go for what goes wrong otherwise. The
	// note rides on env.Notes so the diagnostics screen says it happened,
	// rather than it being a silent fix nobody can see.
	if note, err := sanitizeConsoleInput(); err != nil {
		// Not fatal: the only thing lost is the hatch, and refusing to
		// start over it would be worse than starting without it.
		log.Warn("could not repair the console input mode", "err", err)
		env.Notes = append(env.Notes, "console input mode could not be repaired: "+err.Error())
	} else if note != "" {
		log.Info("console: " + note)
		env.Notes = append(env.Notes, note)
	}

	// Declared here so the closure below can reach the Program that is
	// created further down: app.New needs the callback, the Program needs
	// the root, and only one of those orders is possible. The closure is
	// never called before the Program exists (it fires from a hatch
	// failure, which needs a running Program to have happened at all).
	var program *tea.Program

	root, err := app.New(app.Options{
		Theme:  th,
		Client: client,
		Log:    log,
		// F14: the only way back from a tea.ExecProcess whose
		// ReleaseTerminal failed -- see app.Options.RestoreTerminal.
		RestoreTerminal: func() error {
			if program == nil {
				return nil
			}
			return program.RestoreTerminal()
		},
		State:        state,
		Config:       cfg,
		Env:          env,
		MouseEnabled: mouseEnabled,
		Workspaces: []app.WorkspaceDef{{
			ID:   app.WorkspaceChat,
			Name: "Chat",
			// LeaderKey is deliberately empty. F12 asks for `c`, which
			// has been pane.right.toggle since F6; leader → w opens the
			// switcher instead. See WorkspaceDef.LeaderKey.
			New: newChat,
			// layout.MinWidth/MinHeight, named here rather than
			// inherited silently: the registry is where a workspace's
			// minimum belongs now, and the Editor's is larger.
			MinSize: app.Size{W: 50, H: 12},
		}, {
			ID:   app.WorkspaceEditor,
			Name: "Editor",
			// `e` is free in the registry — leader → e, which is what
			// F13's own manual verification asks for. Unlike Chat's,
			// this one could be given its spec'd key.
			LeaderKey: "e",
			New:       newEditor,
			// Larger than layout's floor, as WorkspaceDef.MinSize's own
			// doc predicted: a gutter and a text area beside a file tree
			// do not fit in fifty columns, and sixteen rows is the point
			// at which the tab strip, the 3-row Terminal rect Compute
			// always reserves for the Editor, and a usable number of code
			// lines all still exist. Refused with the ACTUAL numbers by
			// activeTooSmall, which is TEMPLATES.md's capability contract.
			MinSize: app.Size{W: 70, H: 16},
		}},
	})
	if err != nil {
		// A duplicate action ID or binding lands here: loudly, at boot, with
		// both offenders named — not silently at a keypress six weeks later.
		return err
	}

	programOpts := []tea.ProgramOption{
		tea.WithAltScreen(),
		// Bracketed paste is left enabled (the option to turn it off is
		// WithoutBracketedPaste), but on Windows it does NOT reach the model:
		// Bubble Tea's console-record reader clears ENABLE_VIRTUAL_TERMINAL_INPUT
		// and never sets KeyMsg.Paste, so a paste arrives as N key events.
		// frontend\internal\app\paste.go reconstructs it and explains why the
		// obvious alternative (WithInputTTY) cannot be used here.
	}
	// Mouse is on by default — nothing is mouse-only — unless F10's
	// Appearance tab turned it off via the state.json override above.
	if mouseEnabled {
		programOpts = append(programOpts, tea.WithMouseCellMotion())
	}
	program = tea.NewProgram(root, programOpts...)

	// Restore the terminal on the way out, whatever the reason.
	//
	// Bubble Tea catches panics inside its own loop and restores the terminal
	// before re-panicking (WithoutCatchPanics turns that off). This deferred
	// kill covers the rest: a panic in this function, or an early return, both
	// of which would otherwise leave the alt screen up and the cursor hidden,
	// and the only fix for that is closing the tab.
	defer program.Kill()

	if client != nil {
		// Closing the client closes the child's stdin, which is the shutdown
		// main.py was designed around: it drains in-flight handlers, closes
		// the database and releases the single-instance lock. Killing the
		// process instead leaves that lock held by a dead pid, and every later
		// start refuses.
		defer func() {
			if err := client.Close(); err != nil {
				log.Warn("closing the backend", "err", err)
			}
		}()
	}

	if _, err := program.Run(); err != nil {
		return err
	}

	log.Info("point stopped")
	return nil
}

// slogAdapter fits slog to the small interface the protocol package asks for,
// so that package depends on four methods rather than on a logging library.
type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Debug(msg string, args ...any) { a.l.Debug(msg, args...) }
func (a slogAdapter) Info(msg string, args ...any)  { a.l.Info(msg, args...) }
func (a slogAdapter) Warn(msg string, args ...any)  { a.l.Warn(msg, args...) }
func (a slogAdapter) Error(msg string, args ...any) { a.l.Error(msg, args...) }
