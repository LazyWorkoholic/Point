package theme

import (
	"os"
	"testing"
)

// Clear NO_COLOR for the package's tests.
//
// theme.New honours it by dropping every colour, so a developer who has it set
// — and F1's own verification tells you to set it — would see this package's
// colour assertions fail for a reason that has nothing to do with the code.
// TestNoColorDropsColourButKeepsBorders sets it deliberately, with t.Setenv.
func TestMain(m *testing.M) {
	os.Unsetenv("NO_COLOR")
	os.Exit(m.Run())
}
