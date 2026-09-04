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

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/chat"
	"github.com/lazyworkoholic/point/frontend/internal/config"
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

	th := theme.New(cfg.UI.Theme, theme.Background(cfg.UI.Background))
	if cfg.UI.Theme != "" && th.Name() != cfg.UI.Theme {
		env.Notes = append(env.Notes, fmt.Sprintf(
			"unknown theme %q — using %s (available: %v)",
			cfg.UI.Theme, th.Name(), theme.Names()))
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

	root, err := app.New(app.Options{
		Theme:  th,
		Client: client,
		Log:    log,
		State:  state,
		Env:    env,
	})
	if err != nil {
		// A duplicate action ID or binding lands here: loudly, at boot, with
		// both offenders named — not silently at a keypress six weeks later.
		return err
	}

	// chat cannot be constructed inside app.New: its Update method has to
	// return app.Workspace, which means the chat package imports app — so
	// app cannot import chat back without a cycle. This is the one place
	// that wires the two together, replacing F1's placeholder for the chat
	// workspace with the real thing.
	root.SetWorkspace(app.WorkspaceChat, chat.New(chat.Options{
		Theme:      th,
		Client:     client,
		Log:        log,
		Scrollback: cfg.UI.Scrollback,
		// F4: chat.Workspace reads Drafts out of this and writes it back,
		// debounced, as the query input's buffer changes — see draft.go.
		State: state,
	}))

	program := tea.NewProgram(root,
		tea.WithAltScreen(),
		// Mouse is on; nothing is mouse-only, and F16 adds the toggle.
		tea.WithMouseCellMotion(),
		// Bracketed paste is left enabled (the option to turn it off is
		// WithoutBracketedPaste), but on Windows it does NOT reach the model:
		// Bubble Tea's console-record reader clears ENABLE_VIRTUAL_TERMINAL_INPUT
		// and never sets KeyMsg.Paste, so a paste arrives as N key events.
		// frontend\internal\app\paste.go reconstructs it and explains why the
		// obvious alternative (WithInputTTY) cannot be used here.
	)

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
