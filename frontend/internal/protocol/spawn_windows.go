//go:build windows

package protocol

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW. `uv run python main.py` is a console application, and
// launching one from a TUI that already owns the console can flash a second
// console window. The flag suppresses it; the pipes are inherited regardless,
// because handle inheritance and window creation are unrelated concerns.
const createNoWindow = 0x08000000

func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
