//go:build !windows

package protocol

import "os/exec"

// Nothing to configure off Windows: there is no console window to suppress.
// The file exists so spawn.go stays platform-free and `go vet ./...` passes on
// a Linux CI box, which is where a Windows-only syscall reference would
// otherwise fail first.
func configureChild(cmd *exec.Cmd) {}
