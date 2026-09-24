package protocol

import (
	"os"
	"strings"
	"testing"
)

func TestBuildCommandVerboseEnv(t *testing.T) {
	cases := []struct {
		name     string
		envUnset bool
		envVal   string
		want     string
	}{
		{name: "unset", envUnset: true, want: "uv run python main.py"},
		{name: "1", envVal: "1", want: "uv run python main.py --verbose"},
		{name: "true", envVal: "true", want: "uv run python main.py"},
		{name: "0", envVal: "0", want: "uv run python main.py"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envUnset {
				old, had := os.LookupEnv(BackendVerboseEnv)
				os.Unsetenv(BackendVerboseEnv)
				if had {
					t.Cleanup(func() { os.Setenv(BackendVerboseEnv, old) })
				}
			} else {
				t.Setenv(BackendVerboseEnv, tc.envVal)
			}

			cmd := buildCommand("backend")
			got := strings.Join(cmd.Args, " ")
			if got != tc.want {
				t.Fatalf("Args = %q, want %q", got, tc.want)
			}
		})
	}
}
