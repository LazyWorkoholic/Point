// F8: chip management once a chip already exists — detach (backspace, esc,
// or /detach) and the periodic staleness re-check that makes "touch an
// attached file -> the chip flips to changed" true without requiring the
// user to do anything else first. Attaching a NEW chip is app\picker.go's
// job (AttachContext, on selection); this file is everything that happens
// to a chip afterward.
//
// The staleness check runs on its OWN correlation ids, independent of this
// workspace's ordinary pending/queryKind map (protocol.go) — the same
// reason app\picker.go sends its own requests independent of it: a
// ResolveAttachment reply has to be matched back to WHICH chip it was
// pricing, and queryKind alone (one tag per request) cannot carry that.
package chat

import (
	"encoding/json"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// chipStaleCheckInterval: how often an already-attached chip's live stat is
// re-read while it is on screen. Short enough that "touch the file, then
// look" reads as live; long enough that it never approaches the cost of
// polling on every render. Idle-cost is still zero when there is nothing
// attached — see armChipTick's own guard.
const chipStaleCheckInterval = 5 * time.Second

type chipTickMsg struct{ gen int }

// armChipTick schedules the next staleness sweep, only while there is
// something to sweep — an untouched session with no attachments costs
// nothing, the same idle-CPU discipline stream.go's armTick already
// established for the watchdog tick.
func (w *Workspace) armChipTick() tea.Cmd {
	if w.chipTickArmed || len(w.input.chips) == 0 || w.client == nil {
		return nil
	}
	w.chipTickArmed = true
	w.chipTickGen++
	gen := w.chipTickGen
	return tea.Tick(chipStaleCheckInterval, func(time.Time) tea.Msg {
		return chipTickMsg{gen: gen}
	})
}

// onChipTick fires one ResolveAttachment per currently attached chip — a
// read-only price check (B12: "purely synchronous and side-effect free...
// lets the UI call this on every keystroke", so once per sweep costs
// nothing worth economizing further) — and re-arms itself.
func (w *Workspace) onChipTick(gen int) tea.Cmd {
	if gen != w.chipTickGen {
		return nil // superseded — a newer sweep is already scheduled or ran
	}
	w.chipTickArmed = false
	if w.resolvingPath == nil {
		w.resolvingPath = map[string]string{}
	}
	for _, c := range w.input.chips {
		if c.Path == "" || w.client == nil {
			continue
		}
		id, ok := w.sendResolveAttachment(c.Path)
		if ok {
			w.resolvingPath[id] = c.Path
		}
	}
	return w.armChipTick()
}

// sendResolveAttachment issues one ResolveAttachment, independent of the
// pending/queryKind map — see this file's own doc for why.
func (w *Workspace) sendResolveAttachment(path string) (string, bool) {
	raw, err := json.Marshal(map[string]any{"session": w.sessionID, "path": path})
	if err != nil {
		return "", false
	}
	id := localID()
	env := protocol.Envelope{ID: id, Type: protocol.CmdResolveAttachment, Payload: raw}
	if _, err := w.client.Request(env, 0); err != nil {
		return "", false
	}
	return id, true
}

// handleResolveAttachmentEnvelope claims a staleness sweep's own reply. It
// is tried from handleEnvelope BEFORE the ordinary pending-map lookup —
// this workspace's own correlation ids never collide with the ones
// doSend/pending mint (localID() is a fresh random id every call), but
// trying the smaller, purpose-built map first keeps the two mechanisms from
// ever needing to agree on precedence for real.
func (w *Workspace) handleResolveAttachmentEnvelope(env protocol.Envelope) bool {
	path, ok := w.resolvingPath[env.CorrID]
	if !ok {
		return false
	}
	delete(w.resolvingPath, env.CorrID)
	if w.client != nil {
		w.client.Release(env.CorrID)
	}
	if env.Type != protocol.EvAttachmentResolution {
		return true // refused or unreadable — leave the chip's last known state alone
	}
	var p struct {
		Mtime int `json:"mtime"`
		Size  int `json:"size"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		return true
	}
	for i, c := range w.input.chips {
		if c.Path != path {
			continue
		}
		// Bug D: a chip never yet sent (RecordedMtime/Size nil — see
		// AttachmentChip's own doc) has no baseline to compare against.
		// That is correctly "not current" (it WILL cost its full estimate
		// on the next turn), not an unknown to leave alone.
		if c.RecordedMtime == nil || c.RecordedSize == nil {
			w.input.chips[i].Current = false
			continue
		}
		w.input.chips[i].Current = p.Mtime == *c.RecordedMtime && p.Size == *c.RecordedSize
	}
	return true
}

// refreshAttachmentFreshness re-fetches every attachment's own recorded
// (mtime, size) pair — the baseline the periodic sweep above compares a
// live stat against. Triggered from onContextReport (context_report.go),
// at the same two moments that already pull/push a ContextReport: session
// load and after every completed turn — the only points the SERVER's
// recorded pair could have moved (managers\context.py's
// record_attachment_sent runs from inside build(), i.e. only from an
// actual send).
//
// Replaced a ContextReport-`detail`-string match ("unchanged since last
// sent") that this episode's own doc had already flagged as fragile
// (context.py: "a human rendering that is allowed to change") — and that
// design never even updated the chip's RecordedMtime/Size baseline, so the
// NEXT periodic sweep compared against the same stale (often NULL-turned-
// zero) pair and undid whatever the report had just corrected. Reading the
// attachment rows directly gives the real, current baseline instead of an
// inference from a string never meant to be parsed.
func (w *Workspace) refreshAttachmentFreshness() {
	if w.client == nil || w.sessionID == "" || len(w.input.chips) == 0 {
		return
	}
	w.send(protocol.CmdListAttachments, queryListAttachments, map[string]any{
		"session": w.sessionID,
	})
}

// wireAttachment mirrors managers\attachment.py's Attachment.to_dict().
type wireAttachment struct {
	ID            string `json:"id"`
	RecordedMtime *int   `json:"recorded_mtime"`
	RecordedSize  *int   `json:"recorded_size"`
}

// onAttachmentList applies refreshAttachmentFreshness's reply: each row's
// OWN recorded pair becomes this chip's new baseline. A row just refreshed
// this way is treated as current immediately — at the instant the backend
// reports a pair, that pair IS the live file's stat from whenever it was
// last sent, so nothing has had a chance to diverge yet; the next periodic
// sweep (onChipTick) is what would catch a REAL edit made since.
func (w *Workspace) onAttachmentList(env protocol.Envelope) {
	var p struct {
		Attachments []wireAttachment `json:"attachments"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		return
	}
	byID := make(map[string]wireAttachment, len(p.Attachments))
	for _, a := range p.Attachments {
		byID[a.ID] = a
	}
	for i, c := range w.input.chips {
		a, ok := byID[c.ID]
		if !ok {
			continue
		}
		w.input.chips[i].RecordedMtime = a.RecordedMtime
		w.input.chips[i].RecordedSize = a.RecordedSize
		w.input.chips[i].Current = a.RecordedMtime != nil && a.RecordedSize != nil
	}
}

// detachChip removes chip idx both locally and on the backend. F4's
// original backspace-at-position-0 removal only ever spliced the local
// slice — before B12/F8, chips were never non-empty, so nothing exercised
// this against a real attachment; now that they are real rows in
// `context_attachments`, a local-only removal would look like a detach
// while leaving the file bound server-side, still included in the next
// send. Every removal path (backspace, /detach) goes through here so there
// is one answer, not two that can drift.
func (w *Workspace) detachChip(idx int) tea.Cmd {
	if idx < 0 || idx >= len(w.input.chips) {
		return nil
	}
	chip := w.input.chips[idx]
	w.input.chips = append(w.input.chips[:idx], w.input.chips[idx+1:]...)
	if chip.ID != "" && w.sessionID != "" {
		w.send(protocol.CmdDetachContext, queryDetachAttachment, map[string]any{
			"session": w.sessionID, "attachment": chip.ID,
		})
	}
	return w.scheduleDraftSave()
}

// isDetachCommand reports whether raw is F08's typed detach command — a
// command, never text sent to the model. Detaches the LAST chip, the same
// "which chip" rule backspace-at-position-0 already uses (input_keys.go):
// one rule, not a second one for a second spelling of the same action.
func isDetachCommand(raw string) bool {
	return strings.TrimSpace(raw) == "/detach"
}
