// F8's @ picker: this workspace's own half. Opening the picker and talking
// to the backend belongs to Root (app\picker.go's own doc explains why);
// what belongs here is the two things only this workspace can answer —
// what to scope the picker's requests against (PickerSession) — and the two
// things only this workspace can DO once the picker resolves: replace the
// leading '@' with a chosen agent's name, or turn it into an attachment
// chip.
package chat

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
)

// PickerSession answers app.PickerTarget. The picker scopes ListProjectFiles/
// ListDirectory/AttachContext against exactly what this session is already
// bound to — the same session.project_id the backend's own attachment guard
// reads (B12's _attachment_scope) — never a value resolved some other way,
// which is precisely the "two independently-decided things drift apart"
// failure this codebase has hit three times already.
func (w *Workspace) PickerSession() (sessionID, projectID string, hasProject bool) {
	return w.sessionID, w.projectID, w.projectID != ""
}

// pickerAtPos is where the '@' that opened the picker sits in the buffer,
// right now. Focus is trapped in the popup for as long as it is open (F07's
// own contract — root.go's dispatchKey never reaches this workspace while
// r.overlay != nil), so nothing has touched this workspace's input since
// that '@' was typed: it is still exactly one rune, sitting at cursor-1.
func (w *Workspace) pickerAtPos() (int, bool) {
	pos := w.input.buf.cursor - 1
	if pos < 0 || pos >= len(w.input.buf.runes) || w.input.buf.runes[pos] != '@' {
		return 0, false // the picker closed with nothing chosen, or state drifted — do nothing rather than guess
	}
	return pos, true
}

// applyAgentPicked replaces the leading '@' with "@<id> " — text, not a
// chip, exactly like typing the agent's name by hand: the existing mode
// chip (agentTokenState, input_render.go) picks it up on its own, the same
// way it already does for anything else typed there.
func (w *Workspace) applyAgentPicked(id string) tea.Cmd {
	pos, ok := w.pickerAtPos()
	if !ok {
		return nil
	}
	w.input.buf.beginEdit(editOther)
	w.input.buf.splice(pos, pos+1, []rune("@"+id+" "))
	return w.scheduleDraftSave()
}

// applyAttachmentPicked removes the leading '@' (a chip represents the
// attachment now, not text — F08's own mockup: the chip replaces what was
// typed) and adds Chip to the input's pinned chip row.
func (w *Workspace) applyAttachmentPicked(chip app.PickedAttachment) tea.Cmd {
	pos, ok := w.pickerAtPos()
	if !ok {
		return nil
	}
	w.input.buf.beginEdit(editOther)
	w.input.buf.splice(pos, pos+1, nil)
	w.input.chips = append(w.input.chips, AttachmentChip{
		ID: chip.ID, Path: chip.Path, Name: chip.Name,
		Current: chip.Current, Tokens: chip.Tokens,
		RecordedMtime: chip.RecordedMtime, RecordedSize: chip.RecordedSize,
	})
	return tea.Batch(w.scheduleDraftSave(), w.armChipTick())
}

var _ app.PickerTarget = (*Workspace)(nil)
