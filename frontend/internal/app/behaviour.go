// F10b: the Behaviour tab (Settings' third tab) — wires five
// config.toml-sourced keys to B26's SetBehaviourOverride/
// BehaviourOverrideSet. Built the same shape appearance.go already is: a
// static item list (newBehaviourTabItems), with Decor computed fresh every
// render from Root's own live state (r.behaviourOverrides) — never baked
// into an item at build time, F9a's render-time rule, which F10's own
// handoff flagged Settings as the next consumer of.
//
// Decision 2 (F10b-settings-behaviour-doctor.md): a row shows override
// STATE only, never a value read back from config.toml — Go cannot read
// that table (config.Load parses only the [ui] table) and B26 deliberately
// built no GetBehaviourOverrides pull event. "default" or
// "overridden → X", never a guessed number.
//
// Decision 1: r.behaviourOverrides is replayed via SetBehaviourOverride on
// every protocol.ConnectedMsg — first connect AND every later reconnect,
// including one caused by the BACKEND restarting while this process keeps
// running — since B26's own override layer is deliberately
// process-lifetime-only on that side. See replayBehaviourOverrides. The
// map replayed is whichever one is CURRENTLY confirmed, so a reconnect
// after several overrides have been set replays all of them in one pass,
// not just the most recently changed key.
package app

import (
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/array"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// behaviourKeyInfo is one row's fixed shape. Bool rows toggle immediately
// on selection; everything else opens the unmasked numeric entry
// (credential.go's newBehaviourValuePrompt).
type behaviourKeyInfo struct {
	Key   string // the dotted config key, e.g. "chat.queue_depth"
	Label string
	Bool  bool
}

// behaviourKeys is this tab's whole row set — B26's ALLOWED_KEYS minus the
// per-provider concurrency-cap family (F10b's Decision 3: only the general
// cap is exposed; a row per registered provider is a real feature the
// backend already supports but is not this episode's scope). Scrollback
// and idle-unload timeout are deliberately absent too — neither is a real
// config_value()-backed key (B26's own Do-NOT), and a disabled placeholder
// row would promise a feature no backend key exists for.
var behaviourKeys = []behaviourKeyInfo{
	{Key: "chat.queue_depth", Label: "Queue depth"},
	{Key: "limits.max_concurrent_tasks", Label: "Concurrency cap"},
	{Key: "project.allow_outside_project", Label: "Allow outside project", Bool: true},
	{Key: "index.respect_gitignore", Label: "Respect .gitignore", Bool: true},
	{Key: "research.stale_after_days", Label: "Stale after (days)"},
}

func behaviourKeyByID(key string) (behaviourKeyInfo, bool) {
	for _, k := range behaviourKeys {
		if k.Key == key {
			return k, true
		}
	}
	return behaviourKeyInfo{}, false
}

func newBehaviourTabItems(r *Root) []*array.Item {
	items := make([]*array.Item, 0, len(behaviourKeys))
	for _, k := range behaviourKeys {
		items = append(items, array.NewItem("behaviour:"+k.Key, k.Label))
	}
	return items
}

// behaviourRowDecor is computed fresh every render from r.behaviourOverrides
// — never cached on the item, never a guess at what config.toml holds (see
// this file's own doc on Decision 2).
// behaviourInertKeys are rows that take a real override (BehaviourOverrideSet
// confirms it, GetDoctorReport echoes it back) but have NO reachable,
// observable effect anywhere in the shipped application today —
// chat.queue_depth specifically: every plain-chat send in this frontend
// shares one capacity-1 local gate (chat\stream.go's w.queued) that never
// lets a session's turn lane hold more than one occupant, so
// _check_turn_queue's ceiling (backend\runtime\runtime.py's own
// `1 + depth`) never has a reason to differ by depth from this client —
// see F10b-settings-behaviour-doctor.md's Decision 4 for the full,
// file:line-traced finding. Rendering this row identically to the four
// that DO work (limits.max_concurrent_tasks, research.stale_after_days,
// project.allow_outside_project, index.respect_gitignore — each
// confirmed live) would be a silently inert control, which is worse
// than an honestly labelled one.
var behaviourInertKeys = map[string]bool{
	"chat.queue_depth": true,
}

func (r *Root) behaviourRowDecor(key string) RowDecor {
	k, ok := behaviourKeyByID(key)
	if !ok {
		return RowDecor{}
	}
	val, overridden := r.behaviourOverrides[key]

	note := "default"
	if overridden {
		note = "overridden → " + formatBehaviourValue(k, val)
	}
	if behaviourInertKeys[key] {
		// A fixed icon, in Warning's role, regardless of override state —
		// this row never earns the working rows' ●/○ pair, because setting
		// it changes nothing observable yet (see behaviourInertKeys' own
		// doc). "!" rather than "⚠": a verification round reported the
		// warning sign rendering with NO visible gap before the label,
		// unlike every other icon here — go-runewidth's width-1 table entry
		// for U+26A0 does not match how at least one real terminal font
		// actually draws it, the same class of glyph-vs-terminal mismatch
		// this project already paid for once (F9a's "⋯" rendering as three
		// literal dots, replaced with "◌"). Plain ASCII has no such risk.
		//
		// The NOTE carries no extra suffix on purpose — a verification
		// round found "overridden → 15 · inert" genuinely truncates at
		// this popup's real width: rowWidths' note-column budget is
		// avail/3 when that is BELOW the 24-cell ceiling (MinW: 74 here
		// computes to 23, not 24), so a fixed-ceiling mental count is the
		// wrong thing to check against — only a real render at the real
		// width is (TestBehaviourInertRowNeverOverflowsAtRealWidth does
		// exactly that). Rather than chase a suffix short enough to
		// survive every width AND never eat into the number — the one
		// piece of information this row exists to show — the icon's own
		// colour and glyph carry the "this is different" signal, and
		// settingsHint's hint line carries the why, once this row is
		// actually selected.
		return RowDecor{Icon: "!", IconRole: theme.Warning, Note: note}
	}
	if !overridden {
		return RowDecor{Icon: "○", IconRole: theme.TextDim, Note: note}
	}
	return RowDecor{Icon: "●", IconRole: theme.AccentPrimary, Note: note}
}

// formatBehaviourValue renders an override's value for display. Decoded
// off protocol.Envelope.UnmarshalPayload into a bare map[string]any (see
// BehaviourOverrideSet's own handling in settings.go for why this payload
// is left untyped rather than given a struct the way ListProviders' is),
// so a JSON number arrives as float64, never int — handled explicitly
// rather than left to a generic %v, which would render "7" as "7" but "0"
// read back from a bool-shaped key as "false" with no hint of the mix-up.
func formatBehaviourValue(k behaviourKeyInfo, val any) string {
	if k.Bool {
		if b, ok := val.(bool); ok {
			return strconv.FormatBool(b)
		}
		return fmt.Sprintf("%v", val)
	}
	switch v := val.(type) {
	case float64:
		return strconv.Itoa(int(v))
	case int:
		return strconv.Itoa(v)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// onBehaviourSelect is Enter on a "behaviour:"-prefixed row (settings.go's
// onSettingsSelect dispatches here by prefix).
func (r *Root) onBehaviourSelect(key string) tea.Cmd {
	k, ok := behaviourKeyByID(key)
	if !ok {
		return nil
	}
	if k.Bool {
		cur, _ := r.behaviourOverrides[key].(bool)
		return r.sendBehaviourOverride(key, !cur)
	}
	prefill := ""
	if v, overridden := r.behaviourOverrides[key]; overridden {
		prefill = formatBehaviourValue(k, v)
	}
	cp := newBehaviourValuePrompt(key, k.Label, prefill, r.settings.popup)
	r.settings.credential = cp
	return func() tea.Msg { return openOverlayMsg{cp} }
}

// clearBehaviourOverride is the "ctrl+x clear" ItemAction's Run — see
// newSettingsPopup's Actions entry for the When guard that only shows this
// hint while the selected row actually has something to clear.
func (r *Root) clearBehaviourOverride(key string) tea.Cmd {
	return r.sendBehaviourClear(key)
}

// ---------------------------------------------------------------------------
// Outgoing requests
// ---------------------------------------------------------------------------

// sendBehaviourOverride sends SetBehaviourOverride{key, value} — used by
// both the bool toggle (onBehaviourSelect) and the numeric prompt's submit
// (settingsSubmitBehaviourValue, settings.go).
func (r *Root) sendBehaviourOverride(key string, value any) tea.Cmd {
	reqID, ok := r.SettingsSendFn(protocol.CmdSetBehaviourOverride,
		map[string]any{"key": key, "value": value})
	if !ok {
		if cp := r.settings.credential; cp != nil && cp.behaviourKey == key {
			cp.applyResult(false, "✗ could not reach the backend")
		} else {
			r.status = "could not reach the backend"
		}
		return nil
	}
	r.settingsTrackBehaviourRequest(key, reqID)
	return nil
}

// sendBehaviourClear sends SetBehaviourOverride{key} with no "value" field
// — B26 §1b's clear shape: same key, the field's presence (not its value)
// decides the verb, mirroring DetachContext.
func (r *Root) sendBehaviourClear(key string) tea.Cmd {
	reqID, ok := r.SettingsSendFn(protocol.CmdSetBehaviourOverride, map[string]any{"key": key})
	if !ok {
		r.status = "could not reach the backend"
		return nil
	}
	r.settingsTrackBehaviourRequest(key, reqID)
	return nil
}

func (r *Root) settingsTrackBehaviourRequest(key, reqID string) {
	if r.settings.behaviourReqID == nil {
		r.settings.behaviourReqID = map[string]string{}
	}
	r.settings.behaviourReqID[key] = reqID
}

// replayBehaviourOverrides re-sends every entry Root currently holds as
// confirmed in r.behaviourOverrides — called from Root's ConnectedMsg
// case, every connect and every reconnect alike (Decision 1). The map
// replayed is whichever one is CURRENTLY confirmed: on a fresh launch that
// is whatever state.json seeded r.behaviourOverrides with at New(); on a
// backend-only restart (this process never stopped) it is whatever the
// last BehaviourOverrideSet reply actually confirmed — either way, every
// key present is resent in the same pass, which is what makes this correct
// for MULTIPLE simultaneous overrides and not just the most recent one.
func (r *Root) replayBehaviourOverrides() tea.Cmd {
	if len(r.behaviourOverrides) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(r.behaviourOverrides))
	for key, value := range r.behaviourOverrides {
		cmds = append(cmds, r.sendBehaviourOverride(key, value))
	}
	return tea.Batch(cmds...)
}

// ---------------------------------------------------------------------------
// Incoming replies
// ---------------------------------------------------------------------------

// onBehaviourOverrideSet handles settings.go's handleSettingsEnvelope case
// for a reqID found in r.settings.behaviourReqID — a SetBehaviourOverride
// reply, matched by the key it was sent for.
func (r *Root) onBehaviourOverrideSet(key string, env protocol.Envelope) tea.Cmd {
	cp := r.settings.credential
	fromPrompt := cp != nil && cp.behaviourKey == key

	if env.Type == protocol.EvError {
		var p struct {
			Error string `json:"error"`
		}
		_ = env.UnmarshalPayload(&p)
		msg := p.Error
		if msg == "" {
			msg = "rejected"
		}
		if fromPrompt {
			cp.applyResult(false, "✗ "+msg)
		} else {
			r.status = "behaviour: " + msg
		}
		return nil // r.behaviourOverrides is left exactly as it was — Decision 2
	}

	// BehaviourOverrideSet: the backend's own echo of the FULL current
	// override map (B26 §1b) — trusted wholesale, not merged, since it is
	// already the complete resulting state, the same "echo back full
	// resulting state" precedent ProviderApiKeySet set.
	var p struct {
		Overrides map[string]any `json:"overrides"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		return nil
	}
	if p.Overrides == nil {
		p.Overrides = map[string]any{}
	}
	r.behaviourOverrides = p.Overrides

	if fromPrompt {
		cp.applyResult(true, "✓ set")
		popup := r.settings.popup
		return func() tea.Msg { return openOverlayMsg{popup} }
	}
	return func() tea.Msg { return BehaviourOverridesSetMsg{Overrides: p.Overrides} }
}

// settingsSubmitBehaviourValue is submitBehaviourValueMsg's handler
// (root.go) — CredentialPrompt's own Update cannot reach protocol.Client,
// the same reason settingsSubmitKey exists for the credential case.
func (r *Root) settingsSubmitBehaviourValue(key string, value int) tea.Cmd {
	return r.sendBehaviourOverride(key, value)
}

// ---------------------------------------------------------------------------
// Messages chat.Workspace consumes (the sole state.json writer — F10)
// ---------------------------------------------------------------------------

// BehaviourOverridesSetMsg carries the FULL current override map, not a
// patch — unlike UIOverrideSetMsg (which patches one field at a time
// because theme/background/mouse are independent), BehaviourOverrideSet's
// own wire reply already IS the complete resulting state (B26 §1b), so
// there is nothing to merge: chat\providers.go's applyBehaviourOverrides
// simply replaces w.state.BehaviourOverrides with exactly what the backend
// just confirmed.
type BehaviourOverridesSetMsg struct{ Overrides map[string]any }
