package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"runtime"
	"strings"
)

// The lock holder key (F13b / A2, shape settled by B32's Decision 4).
//
// A holder is the WHOLE string `<username>@<full-id>`, composed once and
// stored in state.json as one opaque value. Three rules, and each one is
// load-bearing somewhere:
//
//  1. COMPOSED ONCE. Never re-derived at launch from a live USERNAME. The
//     backend compares holders by whole-string equality, so a username
//     that changes between launches — a renamed Windows account, a
//     different signed-in user on the same profile — would make Point a
//     different holder and leave it unable to release or re-acquire its
//     own lock.
//
//  2. THE WHOLE STRING IS THE IDENTITY. The username half is there so a
//     refusal can say "held by aryan" instead of "held by 7f3c…"; it is
//     decoration on a value that is compared in full. Nothing may compare
//     the halves separately.
//
//  3. SPLIT ON THE LAST '@' FOR DISPLAY. A Windows username may itself
//     contain '@' (a work or school account is literally
//     `name@contoso.com`) and may contain spaces. Splitting on the first
//     would show a truncated name against a nonsense id.

// holderIDBytes is the random half's length. Sixteen bytes is the same
// size protocol's request ids use, and the value never has to be typed by
// a person — the display form shows the first eight hex characters.
const holderIDBytes = 16

// NewHolder composes a fresh holder key.
//
// The username is whatever the OS reports, with the one transformation
// that matters for a value that ends up in a wire string and a status bar:
// surrounding whitespace is trimmed. It is NOT lowercased, stripped of
// spaces or otherwise normalised — it is shown back to a person, and
// B32 takes it as opaque.
func NewHolder() string {
	var b [holderIDBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Unreachable in practice (crypto/rand on Windows does not fail),
		// and still not a reason to return "" — an empty holder is
		// refused by WriteProjectFile's own `if not p.get("holder")`
		// check, which would turn a rare entropy failure into "save does
		// nothing and says nothing". A constant is a worse identity than
		// a random one and a better one than none.
		return userName() + "@fallback-" + strings.Repeat("0", holderIDBytes*2)
	}
	return userName() + "@" + hex.EncodeToString(b[:])
}

// userName is the display half.
func userName() string {
	var name string
	if runtime.GOOS == "windows" {
		name = os.Getenv("USERNAME")
	} else {
		name = os.Getenv("USER")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		// An unnamed holder is still a valid holder: the id half is what
		// identifies it. "someone" reads better in "held by someone
		// (12s ago)" than an empty string, which renders as "held by
		// (12s ago)" and looks like a bug.
		return "someone"
	}
	return name
}

// HolderDisplay is how a holder key is shown to a person: the username,
// then the first eight characters of the id.
//
//	aryan@7f3c1d2e4b5a6978...  ->  aryan (7f3c1d2e)
//
// Split on the LAST '@', per rule 3 above. A value with no '@' at all —
// which this package never produces, but another holder on the same
// project might — is shown whole rather than guessed at.
func HolderDisplay(holder string) string {
	i := strings.LastIndex(holder, "@")
	if i < 0 {
		return holder
	}
	name, id := holder[:i], holder[i+1:]
	if name == "" {
		name = "someone"
	}
	if len(id) > 8 {
		id = id[:8]
	}
	if id == "" {
		return name
	}
	return name + " (" + id + ")"
}
