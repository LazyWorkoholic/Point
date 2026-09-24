package protocol

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// BackendDirEnv overrides where backend\ is found, so a packaged build can
// point somewhere the source tree layout does not describe.
const BackendDirEnv = "POINT_BACKEND_DIR"

// BackendVerboseEnv, when set to exactly "1", makes buildCommand launch the
// backend with --verbose, surfacing its DEBUG lines in point.log.
const BackendVerboseEnv = "POINT_BACKEND_VERBOSE"

// backendMarker is the file whose presence proves a directory is backend\.
const backendMarker = "main.py"

// FindBackendDir locates backend\.
//
// The search order, and why:
//
//  1. POINT_BACKEND_DIR, if set. An explicit answer always wins.
//  2. Upward from the executable's directory. This is the packaged case.
//  3. Upward from the working directory. This is the DEVELOPMENT case, and it
//     is not redundant with (2): `go run .\frontend\cmd\point` builds the
//     binary into a temp directory under %LOCALAPPDATA%\go-build, so walking
//     up from the executable finds nothing at all during normal work.
//
// "Upward" stops at the volume root. A directory qualifies when it contains
// backend\main.py.
func FindBackendDir() (string, error) {
	if dir := os.Getenv(BackendDirEnv); dir != "" {
		if err := validateBackendDir(dir); err != nil {
			return "", fmt.Errorf("%s=%q: %w", BackendDirEnv, dir, err)
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		return abs, nil
	}

	var tried []string

	if exe, err := os.Executable(); err == nil {
		if dir, ok := searchUpward(filepath.Dir(exe)); ok {
			return dir, nil
		}
		tried = append(tried, filepath.Dir(exe))
	}

	if wd, err := os.Getwd(); err == nil {
		if dir, ok := searchUpward(wd); ok {
			return dir, nil
		}
		tried = append(tried, wd)
	}

	return "", fmt.Errorf(
		"could not find backend\\%s by searching upward from %v — set %s to point at it",
		backendMarker, tried, BackendDirEnv)
}

// searchUpward walks from start toward the volume root looking for
// <dir>\backend\main.py.
func searchUpward(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		candidate := filepath.Join(dir, "backend")
		if validateBackendDir(candidate) == nil {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func validateBackendDir(dir string) error {
	marker := filepath.Join(dir, backendMarker)
	info, err := os.Stat(marker)
	if err != nil {
		return fmt.Errorf("no %s there", backendMarker)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", backendMarker)
	}
	return nil
}

// buildCommand constructs the child process.
//
// The working directory MUST be backend\: main.py does
// `from runtime.runtime import Runtime`, which resolves only from inside that
// folder. This is not a Python bug to fix — it is the layout every backend
// episode was built against.
//
// `uv` is resolved through PATH, so no .exe suffix handling is needed on
// Windows; exec.LookPath applies PATHEXT for us.
//
// BackendVerboseEnv, when exactly "1", appends --verbose to the argv.
func buildCommand(backendDir string) *exec.Cmd {
	args := []string{"run", "python", "main.py"}
	if os.Getenv(BackendVerboseEnv) == "1" {
		args = append(args, "--verbose")
	}
	cmd := exec.Command("uv", args...)
	cmd.Dir = backendDir
	configureChild(cmd)
	return cmd
}
