package app

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// CredentialPrompt is F10's credential entry (F10-settings-first-run.md's
// own section): a masked, single-line key field for a provider that needs
// one, validated by the backend before anything is kept.
//
// F10b (the Behaviour tab's three numeric rows — chat.queue_depth,
// limits.max_concurrent_tasks, research.stale_after_days) generalizes this
// rather than building a second input overlay: this codebase has exactly
// one text-entry mechanism, and a credential and a whole number differ only
// in whether the typed text is masked and in which message submit() ends up
// sending. behaviourKey set (instead of provider) is what selects the
// Behaviour-entry half of every branch below.
//
// It is a bespoke Overlay rather than a Popup, for the same reason Confirm
// is: a single input field with a status line is not a filterable list, and
// bending Popup's own filter box into a credential field would fight its
// "the filter is always live" rule (popup.go) rather than use it.
type CredentialPrompt struct {
	provider string // e.g. "openai" — never shown to the user; label is
	label    string // "OpenAI", or a Behaviour row's own label ("Queue depth")

	// behaviourKey set (provider left "") means this prompt is a Behaviour
	// numeric entry, not a credential: submit() parses the typed text as an
	// int and sends submitBehaviourValueMsg instead of submitCredentialMsg,
	// and the field renders unmasked (see unmasked's own doc).
	behaviourKey string

	// unmasked renders the typed text plainly instead of as bullets. Always
	// true together with behaviourKey != "" — a queue depth is not a
	// secret, and F10's "never echoed" rule is about credentials
	// specifically, not every field this type will ever grow.
	unmasked bool

	runes  []rune
	cursor int

	// lastRuneAt paces the same hazard chat\input_keys.go's pasteSettle
	// guards against, for the identical reason: on Windows a paste replays
	// as individual KeyRunes events with no Paste flag (app\paste.go's own
	// doc), and a clipboard key copied with a trailing newline ends that
	// replay with a real KeyEnter. Without this, that Enter would submit
	// the key one character early, cut by whatever had been typed up to
	// that point. pasteSettle is chat's own constant; credential entry
	// reuses the same window rather than inventing a second one to keep in
	// sync.
	lastRuneAt time.Time

	// status is one line of feedback: "", "validating…", the error the
	// backend actually returned, or the success line this episode's spec
	// names verbatim ("✓ valid — N models available"). Never the raw key,
	// in any of these — appconfig.mask_secret's rule enforced on this side
	// too: nothing this package renders may ever be a key's own bytes.
	status     string
	statusRole theme.Role
	validating bool

	// returnTo is the Settings popup this prompt was opened FROM — esc
	// backs out to it (F10: "esc backs out without setting a key —
	// allowed"), and a successful validation reopens it too, refreshed,
	// rather than leaving the user at a dead end with nothing on screen.
	// The same "d and esc both return to the tray" shape F9a's result card
	// uses (ResultCard.back), for the identical reason: reopening the SAME
	// instance keeps whatever tab/filter/selection it already had.
	returnTo *Popup
}

func newCredentialPrompt(provider, label string, returnTo *Popup) *CredentialPrompt {
	return &CredentialPrompt{provider: provider, label: label, returnTo: returnTo}
}

// newBehaviourValuePrompt opens the unmasked variant for one of the
// Behaviour tab's three numeric rows. prefill is the digits to start the
// field with — the current override's own value when one is already
// active, "" otherwise (F10b's Decision 2: never a guessed config.toml
// value, so there is nothing to prefill with when there is no override).
func newBehaviourValuePrompt(key, label, prefill string, returnTo *Popup) *CredentialPrompt {
	return &CredentialPrompt{
		behaviourKey: key, label: label, returnTo: returnTo, unmasked: true,
		runes: []rune(prefill), cursor: len([]rune(prefill)),
	}
}

func (c *CredentialPrompt) MinSize() (int, int) { return 56, 9 }

func (c *CredentialPrompt) Title() string {
	if c.behaviourKey != "" {
		return c.label
	}
	return c.label + " API Key"
}

func (c *CredentialPrompt) backCmd() tea.Cmd {
	rt := c.returnTo
	return func() tea.Msg { return openOverlayMsg{rt} }
}

func (c *CredentialPrompt) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return c, nil
	}
	if c.validating {
		// Nothing is editable while a round trip is in flight — there is
		// nothing meaningful to type over, and esc here would leave a
		// SetProviderApiKey reply with nowhere to land.
		return c, nil
	}
	switch k.Type {
	case tea.KeyEsc:
		return nil, c.backCmd()
	case tea.KeyEnter:
		// Swallowed as the paste's own trailing newline within the settle
		// window — see lastRuneAt's doc. Dropped outright, not inserted:
		// a credential is one line, and the field's own TrimSpace at
		// submit time would strip it anyway.
		if time.Since(c.lastRuneAt) < pasteSettleWindow {
			return c, nil
		}
		return c.submit()
	case tea.KeyBackspace:
		if c.cursor > 0 {
			c.runes = append(c.runes[:c.cursor-1], c.runes[c.cursor:]...)
			c.cursor--
		}
		return c, nil
	case tea.KeyLeft:
		if c.cursor > 0 {
			c.cursor--
		}
		return c, nil
	case tea.KeyRight:
		if c.cursor < len(c.runes) {
			c.cursor++
		}
		return c, nil
	case tea.KeyHome:
		c.cursor = 0
		return c, nil
	case tea.KeyEnd:
		c.cursor = len(c.runes)
		return c, nil
	case tea.KeySpace:
		c.insert(" ")
		return c, nil
	case tea.KeyRunes:
		// insertableRunes is popup.go's own guard against a bare modifier
		// keydown decoding as a zero-rune KeyRunes on Windows — the exact
		// hazard this field would otherwise be just as exposed to.
		c.insert(insertableRunes(k.Runes))
		return c, nil
	}
	return c, nil
}

// pasteSettleWindow mirrors chat\input_keys.go's pasteSettle exactly (200ms)
// — this package cannot import chat (chat already imports app), so the
// value is reproduced rather than shared, same as insertableRunes already
// is between the two packages for the identical reason.
const pasteSettleWindow = 200 * time.Millisecond

func (c *CredentialPrompt) insert(s string) {
	if s == "" {
		return
	}
	c.lastRuneAt = time.Now()
	r := []rune(s)
	c.runes = append(c.runes[:c.cursor], append(append([]rune{}, r...), c.runes[c.cursor:]...)...)
	c.cursor += len(r)
	c.status, c.statusRole = "", ""
}

// submit trims whitespace/newlines — F10's own named failure is a trailing
// \r\n from a Windows copy producing "a baffling 401 that looks like a
// wrong key" — and asks Root to validate it. Root owns the actual
// SetProviderApiKey round trip (settings.go), the same reason it owns the
// tray's and the picker's: an open Overlay never receives
// protocol.EnvelopeMsg directly (root.go routes every envelope to the
// active WORKSPACE only), so something outside this type has to carry the
// reply back to it.
//
// The Behaviour branch (behaviourKey != "") validates the typed text as a
// whole number BEFORE anything is sent — F10b Done-when #5: a round trip
// for "a" is a wasted request the backend would reject anyway via its own
// set_override shape check, not a case this field needs the network to
// catch.
func (c *CredentialPrompt) submit() (Overlay, tea.Cmd) {
	text := strings.TrimSpace(string(c.runes))
	if text == "" {
		c.status, c.statusRole = "enter a value first", theme.Warning
		return c, nil
	}
	if c.behaviourKey != "" {
		n, err := strconv.Atoi(text)
		if err != nil {
			c.status, c.statusRole = "enter a whole number", theme.Warning
			return c, nil
		}
		c.validating = true
		c.status, c.statusRole = "setting…", theme.TextDim
		key := c.behaviourKey
		return c, func() tea.Msg { return submitBehaviourValueMsg{key: key, value: n} }
	}
	c.validating = true
	c.status, c.statusRole = "validating…", theme.TextDim
	provider := c.provider
	return c, func() tea.Msg { return submitCredentialMsg{provider: provider, key: text} }
}

// applyResult is settings.go's own hook once the SetProviderApiKey (or, for
// a Behaviour row, the SetBehaviourOverride) reply lands — called directly
// on this instance (Root still holds the pointer via
// settingsState.credential), never through Update, since the message that
// carries the reply is protocol.EnvelopeMsg, routed to the ACTIVE
// WORKSPACE, not to this overlay.
func (c *CredentialPrompt) applyResult(ok bool, detail string) {
	c.validating = false
	if ok {
		c.status, c.statusRole = detail, theme.Done
		return
	}
	c.status, c.statusRole = detail, theme.Error
}

func (c *CredentialPrompt) View(th theme.Theme, w, h int) string {
	textW := w - 2
	if textW < 1 {
		textW = 1
	}

	prompt := "Paste or type the " + c.label + " API key. Never echoed."
	fieldLabel := "key: "
	footerHint := "⏎ validate   esc back"
	if c.behaviourKey != "" {
		prompt = "Enter a whole number for " + c.label + "."
		fieldLabel = "value: "
		footerHint = "⏎ set   esc back"
	}

	var b strings.Builder
	b.WriteString(th.Style(theme.TextSecondary).Render(padCells(prompt, textW)))
	b.WriteByte('\n')
	b.WriteByte('\n')

	// Masked entirely while composing — F10: "never echoed; only the last 4
	// shown after saving", and saving only ever happens inside Root, after
	// a HEALTHY reply, at which point this overlay is already closed. There
	// is therefore no state in a CREDENTIAL prompt that ever earns anything
	// but a bullet per rune. A Behaviour row (unmasked) is not a secret —
	// rendered plainly, so the value just typed is actually readable.
	var text string
	if c.unmasked {
		text = string(c.runes)
	} else {
		text = strings.Repeat("•", len(c.runes))
	}
	label := th.Style(theme.TextDim).Render(fieldLabel)
	field := th.Style(theme.TextPrimary).Render(text + "▏")
	b.WriteString(padCells(label+field, textW))
	b.WriteByte('\n')
	b.WriteByte('\n')

	if c.status != "" {
		role := c.statusRole
		if role == "" {
			role = theme.TextPrimary
		}
		b.WriteString(padCells(th.Style(role).Render(c.status), textW))
		b.WriteByte('\n')
	} else {
		b.WriteByte('\n')
	}
	b.WriteString(th.Style(theme.TextDim).Render(padCells(footerHint, textW)))
	return b.String()
}

// submitCredentialMsg asks Root to run SetProviderApiKey — the same
// ask-the-owner shape openOverlayMsg uses for the identical reason: this
// type's own Update call cannot reach protocol.Client or r.overlay.
type submitCredentialMsg struct {
	provider string
	key      string
}

// submitBehaviourValueMsg is submit()'s Behaviour-row counterpart to
// submitCredentialMsg — same reason, same shape: this type cannot reach
// protocol.Client itself, so Root (behaviour.go) runs the actual
// SetBehaviourOverride round trip once this message reaches it.
type submitBehaviourValueMsg struct {
	key   string
	value int
}

var _ Overlay = (*CredentialPrompt)(nil)
