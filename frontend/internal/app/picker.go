// F8: the @ picker. One key opens agents, files and memory (Memory stays
// empty until B13 — see F08-at-picker.md's "Do NOT"). This file is Root's
// own controller for it: the backend calls it drives (ListAgents,
// ListProjectFiles, ListDirectory, AttachContext), the mode decisions ("what
// does the first character after @ mean"), and the Popup it assembles.
//
// Why Root, not chat.Workspace, owns this: an open Overlay never receives
// protocol.EnvelopeMsg (Root routes every envelope to the ACTIVE WORKSPACE
// only — see root.go's Update). Filesystem-browse mode needs fresh
// ListDirectory calls as the user descends, so something has to own that
// request/reply cycle and push results into the open popup. Root already
// does exactly this for the Task card (refreshTasks/handleTaskListEnvelope,
// its own correlation id, independent of any workspace); the picker follows
// the identical shape.
//
// ---------------------------------------------------------------------------
// Mode triggering — the complete rule set, in one place
// ---------------------------------------------------------------------------
//
// This took several rounds of manual discovery to map out; written down
// here once so the next reader does not have to re-derive it.
//
//   - Bare '@' (openAttachmentPicker): fuzzy mode (Agents + the bound
//     project's ListProjectFiles index) if the SESSION already has a
//     project bound (PickerTarget.PickerSession's hasProject — read ONCE,
//     from the backend's own session.project_id, never guessed); browse
//     mode rooted at this process's own os.Getwd() otherwise. hasProject
//     can flip from false to true, PERMANENTLY, within a session's own
//     lifetime — see openAttachmentPicker's own doc for exactly how
//     (B12's _adopt_project_for, triggered by attaching a file that
//     resolves to a project). Not a bug: the identical scratch profile's
//     bare '@' can legitimately browse on one launch and fuzzy-search on
//     the next, once that has happened.
//
//   - '@?', typed right after '@' with nothing dismissed in between: ALWAYS
//     forces browse mode, unconditionally — pickerLeadingCommand's '?' case
//     runs before hasProject decides anything else. hasProject only changes
//     WHERE it roots (the bound project's root vs. cwd), never whether it
//     browses.
//
//   - A leading '?' typed into an ALREADY-OPEN FUZZY picker's filter box
//     (not freshly after '@' — the picker was already showing fuzzy
//     results) is the SAME mechanism as '@?' above, reached through the
//     same onPickerFilterChange/pickerLeadingCommand code path: it forces
//     browse mode too, rooted the same way, and respects the SAME
//     leading rule for Agents (below) — Bug 6 was this coupling being
//     violated, now fixed.
//
//   - Once browse mode is engaged, TYPED TEXT ONLY FILTERS — it never
//     navigates, in any spelling. See "THE MODEL" above
//     pickerLeadingCommand for the full statement and for the five
//     symptoms the old typed-navigation branch produced. Location changes
//     only from a chosen row (Enter/click), including the ".." row.
//
//   - Agents' visibility is governed SOLELY by r.picker.leading — set
//     ONCE, at open, from OpenAttachmentPickerMsg.Leading (itself: was the
//     triggering '@' at buffer index 0 — B12's rule, only the leading
//     @token can ever name an agent). This is completely independent of
//     fuzzy-vs-browse: a leading '@' keeps offering Agents whether Files
//     end up shown via the fuzzy index OR a forced directory listing; a
//     mid-sentence '@' never offers Agents, in either mode. Engaging
//     browse mode (via '@?', a leading '?' in the filter, a drive letter,
//     or any other browse prefix) must never touch agentItems — see
//     onPickerFilterChange's own comment at the point Bug 6 lived.
//
//   - EVERY command-like character is LEADING-POSITION ONLY, matching '@”s
//     own F04 discipline: pickerLeadingCommand is all exact-matches and
//     HasPrefix, so '?' or a separator anywhere other than the start of
//     the filter is ordinary text. "backend\runtime" filters; "src/main"
//     filters; `\foo` is a command.
//
//   - NEITHER bare '/' NOR bare '\' is a mode-switch trigger, anywhere,
//     in any position — a revision of F08's own original table (which
//     listed both as aliases), made across two verification rounds. '/'
//     went first: it is ordinary, realistic fuzzy-search text, and
//     claiming it as a command made that text unreachable. '\' followed
//     once it was found jumping to a drive root on its own, which is
//     neither what F08 meant by '@\' nor something a bare separator can
//     name unambiguously. Reaching a drive root now requires naming the
//     drive — an exact `D:\`. '.\', './', '..\', '../', '~\', '~/' are
//     unaffected, each distinguished by its FIRST character rather than
//     by the separator.
//
//   - The command evaluator runs ENTIRELY inside onPickerFilterChange's
//     `!r.picker.browsing` branch. So a command is a command only the
//     FIRST time, from still-fuzzy mode: once browsing has engaged (by
//     any means), '?' — even freshly typed at filter position 0 again,
//     since SetItems clears the filter back to "" on every mode/directory
//     switch — is ordinary filter text, not a repeat command. This
//     mirrors F04's own leading-'@' rule (an exact match wins once; it
//     does not keep re-triggering) and is intentional, not a gap.
//
//   - FUZZY vs BROWSE SYMMETRY — the choice, and why. A verification round
//     found these two modes disagreeing: typing 'D:\' instantly jumped
//     while still fuzzy, but did nothing once already browsing. Offered
//     (a) symmetric instant path completion in both modes, or (b) remove
//     it entirely so every multi-character path needs Enter. CHOSE (a),
//     narrowed to SELF-CONTAINED targets:
//
//     A BARE drive root or a '~'-rooted path names exactly ONE location
//     however the picker got there. It cannot be read two ways and cannot
//     be corrupted by ambient state, so it is honoured IDENTICALLY in
//     both modes (cmdAbsolute) — which is both what (a) asked for and
//     what keeps F08 Done-when #3 ("@D:\ ... with no extra keystroke")
//     true. (b) was rejected because it would have broken that Done-when
//     outright and made other drives unreachable from the picker at all.
//
//     THE DRIVE ROOT IS AN EXACT MATCH, NOT A PREFIX. The filter must be
//     precisely `<letter>:\` or `<letter>:/` — three characters, nothing
//     more — to jump. This was a prefix check and that was a bug with a
//     precise symptom: typing `C:\Windows\` character by character fired
//     the moment the third keystroke completed `C:\`, navigating to the
//     drive root before "Windows\" could be typed. Under exact equality a
//     longer path sharing the prefix is never EQUAL to the bare pattern,
//     only ever longer, so `C:\Windows\`, `D:\Projects\` and `C:\W` all
//     stay ordinary filter text throughout, in both modes — Item 1's rule
//     correctly applied. Only a complete, bare drive-root entry jumps.
//
//     '~\' / '~/' are DELIBERATELY still prefix matches, confirmed in
//     verification rather than overlooked: `~\Desktop\` firing at `~\`
//     lands in the home directory with the filter cleared, and "Desktop"
//     then filters that listing, which is what a home shortcut is for.
//     `C:\` differs only because it is itself typed as the prefix of a
//     longer path someone is still in the middle of writing.
//
//     '?' (cmdExplorer) and the base-relative prefixes '\', '.\', './',
//     '..\', '../' (cmdBaseRelative) stay FUZZY-MODE ONLY, because
//     neither is self-contained: '?' means "start browsing, at the
//     default root", which is meaningless once already browsing (and
//     keeping it fuzzy-only is what preserves the '?'-only-once rule a
//     previous round locked in and tested); and a base-relative prefix
//     names a location only together with a base, which is exactly the
//     "relative to what?" ambiguity that made item 4's stale-state bug
//     possible. While browsing, the ".." row is the unambiguous way up.
//
//     So: Desktop/Downloads/another drive are reachable by typing from
//     EITHER mode. Going up one level while browsing is the ".." row, not
//     a typed '..\'. One rule per command kind, the same in both modes.
//
//   - Esc dismisses the popup WITHOUT touching the chat input buffer at
//     all — the triggering '@' (and anything typed into the popup's own
//     filter, which is a SEPARATE buffer) is simply gone; nothing is
//     written back. The '@' that opened it stays in the chat input as
//     ordinary text, exactly as if the picker never existed. Typing
//     further text after a dismissal does NOT reopen it — only a fresh
//     '@' keystroke re-arms the trigger (input_keys.go). Decided, not a
//     gap: see that file's own comment at the trigger site for why a live
//     recompute was rejected.
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/array"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// OpenAttachmentPickerMsg asks Root to open the @ picker. chat's
// input_keys.go emits it the moment a bare '@' is typed — F4's seam. It
// lives here, not in chat, so Root (which owns OpenOverlay and the backend
// plumbing this needs) can react to it directly — the same reason
// EnterBrowseMsg/FocusInputMsg live here instead of chat.
type OpenAttachmentPickerMsg struct {
	// Leading is true when the '@' that triggered this open sits at index 0
	// of the WHOLE input buffer — the only position B12's own invocation
	// parser ever treats as capable of naming an agent
	// (managers\invocation.py: "Only the FIRST @-token can name the
	// agent"). A picker opened from any other position shows Files (and
	// Memory) only — choosing an agent there would insert syntax
	// parse_invocation can never route as agent selection, since it only
	// ever inspects the leading token.
	Leading bool
}

// PickerTarget is implemented by a workspace whose input the @ picker can
// act on — the same narrow-interface pattern FocusAware/OverlayProvider/
// DraftFlusher already establish, so Root asks "what do you need this
// scoped against" rather than reaching into chat.Workspace's own fields.
type PickerTarget interface {
	// PickerSession reports the session id (AttachContext/ResolveAttachment
	// require one) and the bound project, if any. hasProject is a separate
	// bool rather than an empty-string check on projectID: "unbound" and
	// "bound to an empty id" are not the same claim, even though only the
	// first is reachable today.
	PickerSession() (sessionID, projectID string, hasProject bool)
}

// AgentPickedMsg: the user chose an agent from the picker's Agents section.
// chat replaces the leading @token in its input buffer with "@<ID> ".
type AgentPickedMsg struct{ ID string }

// AttachmentPickedMsg: AttachContext succeeded for a file the user chose.
// chat removes the leading @token from its input buffer and adds Chip to
// the chip row.
type AttachmentPickedMsg struct{ Chip PickedAttachment }

// PickedAttachment mirrors chat's AttachmentChip without importing chat
// (chat already imports app; the reverse would cycle). chat's own handler
// converts it on receipt.
//
// RecordedMtime/RecordedSize are *int, not int: managers\attachment.py's
// own doc is explicit that a fresh row's pair is NULL until the first turn
// that actually sends it ("a NULL pair reads as 'never sent' -- the correct
// answer for a row nothing has resolved yet"). Decoding that null into a
// plain int silently produces 0, indistinguishable from a real recorded
// mtime of zero -- which is exactly Bug D: a periodic live-stat sweep then
// compares a REAL file's mtime against a bogus 0 baseline, never matches,
// and reports "changed" on a file nobody touched.
type PickedAttachment struct {
	ID                          string
	Path                        string
	Name                        string
	Current                     bool
	Tokens                      int
	RecordedMtime, RecordedSize *int
}

// pickerAgent mirrors AgentList's per-agent shape.
type pickerAgent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// pickerDirEntry mirrors DirectoryList's per-entry shape.
type pickerDirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
}

// pickerState is Root's own bookkeeping for one open (or in-flight-to-open)
// picker. Cleared to its zero value on close.
type pickerState struct {
	popup   *Popup
	target  PickerTarget
	leading bool

	sessionID  string
	projectID  string
	hasProject bool
	cwd        string

	// browsing is true once the picker committed to filesystem-browse mode
	// (an explicit prefix, the no-project fallback, or the '?' explorer).
	// false is fuzzy mode: Agents + the project's file index.
	browsing bool
	// root is the absolute directory `dirItems` currently reflects, once
	// browsing. "" until the first DirectoryList reply lands.
	root string

	agentItems []*array.Item
	dirItems   []*array.Item
	// dirKind maps an item's id (its absolute path) to whether it is a
	// directory — OnSelect's only use for it is deciding whether to
	// descend (stay open, fetch again) or attach (close, AttachContext).
	dirKind map[string]bool

	agentsReqID string
	listReqID   string
	attachReqID string
	attachPath  string

	// awaitingConnection is Bug B's fix: set when a fetch could not even be
	// SENT (the backend handshake had not completed yet — '@' pressed at
	// startup, before ConnectedMsg). Without this, the popup opened with
	// its "Loading…" placeholder and NOTHING was ever actually in flight
	// to replace it, so it stayed on "Loading" forever; retryPickerIfReady
	// (called from Root's own ConnectedMsg case) uses this to know a
	// retry, not a fresh open, is what is needed once the connection is up.
	awaitingConnection bool
}

func (r *Root) resetPicker() {
	r.picker = pickerState{}
	r.overlay = nil
}

// openAttachmentPicker is OpenAttachmentPickerMsg's handler.
//
// Mode selection for a bare '@' — exactly when each one fires, and the one
// place the two can silently swap between launches of the SAME profile:
//
//   - hasProject (PickerTarget.PickerSession's third return) is read ONCE,
//     here, from chat.Workspace.projectID — which is only ever what the
//     BACKEND'S session row says session.project_id is (B23's own wire
//     field, decoded in chat\model.go). This package never computes or
//     guesses it.
//   - hasProject == true  -> fuzzy mode: Agents + ListProjectFiles against
//     that project's index.
//   - hasProject == false -> browse mode rooted at Go's own os.Getwd() (the
//     frontend process's cwd — see pickerFetchDirectory/_resolve_browse_
//     target's own doc for why that must be THIS process's cwd and not the
//     backend's, which always runs from inside backend\).
//
// The swap a verification round found — a bare '@' browsing cwd folder by
// folder on one launch, then fuzzy-searching a flat project file list on
// the NEXT launch of the exact same scratch profile — is real, and is
// B12's _adopt_project_for (runtime.py), not an F8 bug. A session created
// by this app's own bootstrap (chat\protocol.go's onSessionList ->
// CreateSession with an EMPTY payload) always starts with project_id NULL:
// _handle_create_session only ever takes project_id from the payload, with
// no cwd-resolve fallback of its own. hasProject is therefore false and
// '@' browses — UNTIL the user ATTACHES a file (AttachContext) whose path,
// walked upward, finds a `.point\` marker: _attachment_scope finds the
// session still unbound and calls _adopt_project_for, which resolves that
// project and calls SessionManager.set_project — a real, DURABLE write to
// the session's own database row, "only ever fills a NULL... never
// re-pointed" (B12's own words). That row is what the SECOND launch's
// GetSession reads back, with project_id now set — so the identical
// profile's bare '@' now resolves to fuzzy mode, permanently, for that
// session, with no picker-side toggle to undo it (consistent with B12's
// "adopted once, never re-pointed" design — it is not meant to be
// reversible from here).
//
// '@?' is NOT subject to any of the above: it forces browse mode
// unconditionally, whether or not a project is bound (see
// onPickerFilterChange — pickerBrowsePrefix's '?' case runs before
// hasProject is even consulted). hasProject only changes WHERE '@?' roots
// itself (the bound project's root vs. cwd), never whether it browses.
func (r *Root) openAttachmentPicker(m OpenAttachmentPickerMsg) tea.Cmd {
	pt, ok := r.current().(PickerTarget)
	if !ok || r.client == nil {
		return nil
	}
	sessionID, projectID, hasProject := pt.PickerSession()
	cwd, _ := os.Getwd()

	loading := array.NewItem("__loading__", "Loading…").SetDisabled(true)
	popup := NewPopup(PopupConfig{
		Title: "@",
		Tabs:  []PopupTab{{Items: []*array.Item{loading}}},
		// LAYOUT.md's table has no entry for the @ picker specifically;
		// 60×16 matches Memory (F11), the closest existing shape — a
		// filterable, sectioned content list, not an action list like the
		// palette (40×10).
		MinW: 60, MinH: 16,
		OnSelect:       r.onPickerSelect,
		QueryFor:       r.pickerQueryFor,
		OnFilterChange: r.onPickerFilterChange,
		Hint:           r.pickerHint,
	})

	r.picker = pickerState{
		popup:      popup,
		target:     pt,
		leading:    m.Leading,
		sessionID:  sessionID,
		projectID:  projectID,
		hasProject: hasProject,
		cwd:        cwd,
		dirKind:    map[string]bool{},
	}
	r.OpenOverlay(popup)
	if r.overlay != popup {
		// Too small for even the loading placeholder — OpenOverlay already
		// refused and set the status line; nothing left to fetch for.
		r.picker = pickerState{}
		return nil
	}

	if hasProject {
		return tea.Batch(r.pickerFetchAgents(), r.pickerFetchProjectFiles())
	}
	// No project: browse mode rooted at cwd, per F08's own table — "without
	// one it opens browse mode rooted at cwd", never a dead key.
	r.picker.browsing = true
	return tea.Batch(r.pickerFetchAgents(), r.pickerFetchDirectory(cwd))
}

// ---------------------------------------------------------------------------
// Outgoing requests
// ---------------------------------------------------------------------------

func (r *Root) pickerSend(cmdType string, payload map[string]any) (string, bool) {
	if r.client == nil {
		return "", false
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	env := protocol.Envelope{ID: rootRequestID(), Type: cmdType, Payload: raw}
	if _, err := r.client.Request(env, 0); err != nil {
		// Bug B: most commonly "backend is not connected" — '@' pressed
		// before the handshake completes. Remembered so ConnectedMsg can
		// retry once the connection is actually usable; without this the
		// popup's "Loading…" placeholder had nothing left to ever replace
		// it.
		r.picker.awaitingConnection = true
		return "", false
	}
	return env.ID, true
}

// retryPickerIfAwaiting re-issues whatever fetch never made it out because
// the backend was not yet connected when it was first attempted — Bug B.
// Called from Root's own ConnectedMsg case, the same moment refreshTasks
// already re-asks for its own snapshot.
func (r *Root) retryPickerIfAwaiting() tea.Cmd {
	if r.picker.popup == nil || !r.picker.awaitingConnection {
		return nil
	}
	r.picker.awaitingConnection = false
	var cmds []tea.Cmd
	if r.picker.agentsReqID == "" && len(r.picker.agentItems) == 0 {
		cmds = append(cmds, r.pickerFetchAgents())
	}
	if r.picker.listReqID == "" && len(r.picker.dirItems) == 0 {
		if r.picker.browsing {
			root := r.picker.root
			if root == "" {
				root = r.picker.cwd
				if r.picker.hasProject {
					root = "."
				}
			}
			cmds = append(cmds, r.pickerFetchDirectory(root))
		} else {
			cmds = append(cmds, r.pickerFetchProjectFiles())
		}
	}
	return tea.Batch(cmds...)
}

func (r *Root) pickerFetchAgents() tea.Cmd {
	id, ok := r.pickerSend(protocol.CmdListAgents, map[string]any{})
	if ok {
		r.picker.agentsReqID = id
	}
	return nil
}

func (r *Root) pickerFetchProjectFiles() tea.Cmd {
	id, ok := r.pickerSend(protocol.CmdListProjectFiles, map[string]any{
		"project_id": r.picker.projectID, "limit": 200,
	})
	if ok {
		r.picker.listReqID = id
	}
	return nil
}

func (r *Root) pickerFetchDirectory(path string) tea.Cmd {
	payload := map[string]any{"path": path, "cwd": r.picker.cwd}
	if r.picker.hasProject {
		payload["project_id"] = r.picker.projectID
	}
	id, ok := r.pickerSend(protocol.CmdListDirectory, payload)
	if ok {
		r.picker.listReqID = id
	}
	return nil
}

func (r *Root) pickerFetchAttach(path string) tea.Cmd {
	id, ok := r.pickerSend(protocol.CmdAttachContext, map[string]any{
		"session": r.picker.sessionID, "path": path,
	})
	if ok {
		r.picker.attachReqID = id
		r.picker.attachPath = path
	}
	return nil
}

// ---------------------------------------------------------------------------
// Incoming replies
// ---------------------------------------------------------------------------

// handlePickerEnvelope claims Root's own picker replies (matched by corr_id,
// same pattern handleTaskListEnvelope uses) and returns a command to deliver
// a write-back message to the active workspace, if this reply produced one.
//
// Bug A's fix: env.CorrID == "" is refused BEFORE the switch, unconditionally.
// A cleared reqID field (agentsReqID/listReqID/attachReqID, all "" once
// their real reply has already landed) is a valid switch case value, and
// the backend's 5-second idle heartbeat (runtime.py's start_heartbeat)
// arrives with corr_id null -- decoded here as "" -- forever after. Without
// this guard, EVERY heartbeat once both initial fetches had already
// cleared their ids matched `case r.picker.agentsReqID:` (both sides "")
// and was handled as if it were a fresh, empty ListAgents reply, silently
// wiping the Agents section a few seconds after it had correctly
// populated. handleTaskListEnvelope already guards the identical case
// (`env.CorrID != "" && ...`) for exactly this reason; this one did not,
// and should have.
func (r *Root) handlePickerEnvelope(env protocol.Envelope) tea.Cmd {
	if r.picker.popup == nil || env.CorrID == "" {
		return nil
	}
	switch env.CorrID {
	case r.picker.agentsReqID:
		r.picker.agentsReqID = ""
		return r.onPickerAgents(env)
	case r.picker.listReqID:
		r.picker.listReqID = ""
		return r.onPickerListing(env)
	case r.picker.attachReqID:
		r.picker.attachReqID = ""
		return r.onPickerAttach(env)
	}
	return nil
}

func (r *Root) onPickerAgents(env protocol.Envelope) tea.Cmd {
	if env.Type == protocol.EvError {
		// Best-effort: an Agents section that failed to load is not a
		// reason to refuse the whole picker — Files/browsing still work.
		return nil
	}
	var p struct {
		Agents []pickerAgent `json:"agents"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		return nil
	}
	items := make([]*array.Item, 0, len(p.Agents))
	// Only the LEADING position can ever name an agent (B12's own rule —
	// see OpenAttachmentPickerMsg's doc). A picker opened elsewhere in the
	// buffer shows Files only.
	if r.picker.leading {
		for _, a := range p.Agents {
			items = append(items, array.NewItem(a.ID, a.ID).
				SetDescription(a.Description).SetSection("Agents"))
		}
	}
	r.picker.agentItems = items
	r.pickerApplyItems()
	return nil
}

func (r *Root) onPickerListing(env protocol.Envelope) tea.Cmd {
	if env.Type == protocol.EvError {
		var p struct {
			Error string `json:"error"`
			Offer string `json:"offer"`
		}
		_ = env.UnmarshalPayload(&p)
		r.pickerShowRefusal(p.Error, p.Offer)
		return nil
	}
	if r.picker.browsing {
		var p struct {
			Path    string           `json:"path"`
			Entries []pickerDirEntry `json:"entries"`
		}
		if err := env.UnmarshalPayload(&p); err != nil {
			return nil
		}
		r.picker.root = p.Path
		items := make([]*array.Item, 0, len(p.Entries)+1)
		r.picker.dirKind = make(map[string]bool, len(p.Entries)+1)
		// ".." is real navigation, not decoration — otherwise a picker that
		// descends several levels via arrow+Enter (@?'s own "arrow-key
		// navigation") has no way back except closing and retyping the
		// whole path. filepath.Dir at an actual filesystem root (e.g.
		// "C:\") returns the same path unchanged, which is what stops this
		// from ever showing at the top.
		if parent := filepath.Dir(p.Path); parent != p.Path {
			items = append(items, array.NewItem(parent, `..\`).SetSection("Files"))
			r.picker.dirKind[parent] = true
		}
		for _, e := range p.Entries {
			full := filepath.Join(p.Path, e.Name)
			label := e.Name
			if e.IsDir {
				label += `\`
			}
			items = append(items, array.NewItem(full, label).SetSection("Files"))
			r.picker.dirKind[full] = e.IsDir
		}
		r.picker.dirItems = items
	} else {
		var p struct {
			Files []string `json:"files"`
		}
		if err := env.UnmarshalPayload(&p); err != nil {
			return nil
		}
		items := make([]*array.Item, 0, len(p.Files))
		for _, f := range p.Files {
			items = append(items, array.NewItem(f, f).SetSection("Files"))
		}
		r.picker.dirItems = items
	}
	r.pickerApplyItems()
	return nil
}

func (r *Root) onPickerAttach(env protocol.Envelope) tea.Cmd {
	path := r.picker.attachPath
	if env.Type == protocol.EvError {
		var p struct {
			Error string `json:"error"`
			Offer string `json:"offer"`
		}
		_ = env.UnmarshalPayload(&p)
		r.pickerShowRefusal(p.Error, p.Offer)
		return nil
	}
	var p struct {
		Attachment struct {
			ID            string `json:"id"`
			RecordedMtime *int   `json:"recorded_mtime"`
			RecordedSize  *int   `json:"recorded_size"`
		} `json:"attachment"`
		Resolution struct {
			TokensEstimate int `json:"tokens_estimate"`
		} `json:"resolution"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		return nil
	}
	chip := PickedAttachment{
		ID:   p.Attachment.ID,
		Path: path,
		Name: filepath.Base(path),
		// Bug D: NOT current. managers\attachment.py's own row is created
		// with recorded_mtime/recorded_size NULL -- attach() never passes a
		// stat to insert() -- and only a real SEND records one
		// (managers\context.py's record_attachment_sent, called from
		// build(), never from attach). B12's own verification: "attach
		// reported 713 [tokens]... After a send the attachment correctly
		// reports 0". A file that has never been sent WILL cost its full
		// estimate on the next turn, which is what "changed / will send"
		// means, not "current".
		Current:       false,
		Tokens:        p.Resolution.TokensEstimate,
		RecordedMtime: p.Attachment.RecordedMtime,
		RecordedSize:  p.Attachment.RecordedSize,
	}
	r.resetPicker()
	return r.deliverToWorkspace(AttachmentPickedMsg{Chip: chip})
}

// deliverToWorkspace hands msg to the active workspace exactly the way
// Root's own fallthrough does for anything it does not recognise — used
// here because the picker's replies arrive on Root's OWN correlation ids,
// outside the generic EnvelopeMsg fallthrough that would otherwise carry
// them there.
func (r *Root) deliverToWorkspace(msg tea.Msg) tea.Cmd {
	ws, cmd := r.current().Update(msg)
	r.workspaces[r.active] = ws
	return cmd
}

// pickerShowRefusal keeps the popup open and shows a guard's refusal
// (too_large, binary, is_directory, path_denied, ...) IN the popup itself —
// F08's own rule: "Guards — messages, not errors". The popup is on top of
// the whole screen while it is open, so routing this to chat's own status
// line (behind the scrim) would show it nowhere the user could see; a
// disabled item at the top of the SAME list the guard was reached from
// keeps the message where the user is actually looking, and "here's the
// reason, try something else" is exactly what a guard refusal, as opposed
// to a crash, promises.
// It changes NO location state — not r.picker.root, not dirItems, not
// browsing. An unresolvable path shows its reason and the picker stays
// exactly where it was, which is item 3's guarantee: an error is never
// also a navigation.
//
// The message is WRAPPED across several disabled rows rather than passed
// as one long title. renderRow truncates a row's name at 40 display cells
// (rowWidths' own cap), so a real backend refusal — which routinely names
// a full resolved path — was being cut mid-sentence with an ellipsis, and
// read as a cryptic fragment sitting above a list that was (correctly)
// still showing the previous directory. That mismatch is item 5's render
// half: the list was never stale, the MESSAGE was unreadable.
func (r *Root) pickerShowRefusal(errText, offer string) {
	if r.picker.popup == nil {
		return
	}
	label := errText
	if offer != "" {
		label += " — " + offer
	}
	notice := wrapPickerNotice("⚠ "+label, pickerNoticeWidth)
	if r.picker.browsing && r.picker.root != "" {
		// Wrapped through the same helper — a resolved Windows path is
		// routinely wider than the row on its own, and this line being
		// the one thing that got truncated would be its own small
		// version of the bug it exists to explain.
		notice = append(notice,
			wrapPickerNotice("(still showing "+r.picker.root+")", pickerNoticeWidth)...)
	}

	combined := make([]*array.Item, 0, len(notice)+len(r.picker.agentItems)+len(r.picker.dirItems))
	for i, line := range notice {
		combined = append(combined,
			array.NewItem(fmt.Sprintf("__refused__%d", i), line).SetDisabled(true))
	}
	combined = append(combined, r.picker.agentItems...)
	combined = append(combined, r.picker.dirItems...)
	r.picker.popup.SetItems(combined)
}

// pickerNoticeWidth is renderRow's own name-column ceiling (rowWidths'
// maxName), minus the two cells renderRow prefixes every row with. A
// notice line longer than this is what gets truncated with an ellipsis,
// so this is the width to wrap at — not a guess.
const pickerNoticeWidth = 38

// wrapPickerNotice breaks text into lines of at most width display cells,
// on word boundaries where it can and mid-word only when a single word is
// itself longer than the line (a resolved Windows path usually is).
func wrapPickerNotice(text string, width int) []string {
	if width < 8 {
		width = 8
	}
	var lines []string
	for _, word := range strings.Fields(text) {
		switch {
		case len(lines) == 0:
			lines = append(lines, word)
		case runewidth.StringWidth(lines[len(lines)-1]+" "+word) <= width:
			lines[len(lines)-1] += " " + word
			continue
		default:
			lines = append(lines, word)
		}
		// The line just appended may itself be over-wide (one long path);
		// hard-break it so nothing reaches renderRow's truncation.
		for runewidth.StringWidth(lines[len(lines)-1]) > width {
			long := lines[len(lines)-1]
			cut := runewidth.Truncate(long, width, "")
			lines[len(lines)-1] = cut
			lines = append(lines, long[len(cut):])
		}
	}
	if len(lines) == 0 {
		return []string{text}
	}
	return lines
}

// pickerApplyItems recombines whatever sections are currently known
// (Agents + Files/directory entries) and hands them to the popup.
//
// Gated on BOTH agentsReqID and listReqID being settled ("" — cleared once
// their reply, success or refusal, has landed): the two initial fetches
// (ListAgents, ListProjectFiles/ListDirectory) race, and calling SetItems
// from whichever lands FIRST would reset the menu's selection to row 0
// (SetItems' own documented behaviour) while the OTHER fetch is still in
// flight. A user who started arrow-navigating in that narrow window would
// have their selection silently snapped back under them the instant the
// second reply arrived — a real, if narrower, contributor to the same
// "selecting a file lands on whatever's at row 0 instead" symptom Bug A's
// heartbeat collision caused more visibly. Waiting for both means the
// FIRST SetItems call already has the complete picture, and every
// SUBSEQUENT one (directory descent, path-typing) is a deliberate, single-
// source dataset swap that earns its own reset.
func (r *Root) pickerApplyItems() {
	if r.picker.popup == nil {
		return
	}
	if r.picker.agentsReqID != "" || r.picker.listReqID != "" {
		return // still waiting on the other half of the initial fan-in
	}
	combined := make([]*array.Item, 0, len(r.picker.agentItems)+len(r.picker.dirItems))
	combined = append(combined, r.picker.agentItems...)
	combined = append(combined, r.picker.dirItems...)
	r.picker.popup.SetItems(combined)
}

// ---------------------------------------------------------------------------
// Selection
// ---------------------------------------------------------------------------

func (r *Root) onPickerSelect(it *array.Item) (tea.Cmd, bool) {
	if it.GetSection() == "Agents" {
		id := it.GetID()
		r.resetPicker()
		return agentPickedCmd(id), true
	}
	path := it.GetID()
	if r.picker.dirKind[path] {
		// Descend (or ascend — the ".." row is just a directory row whose
		// id is the parent). THE ONLY way a location ever changes: a row
		// the user actually chose, carrying an ABSOLUTE target built from
		// the reply that listed it, through the one navigation entry
		// point. Stays open — swap items, never close-then-reopen.
		return r.browseTo(path), false
	}
	// A file: price and bind it. AttachContext's own reply decides whether
	// the popup closes (see onPickerAttach) — a guard refusal must leave
	// the picker open so the user can try something else, and OnSelect has
	// to answer "close?" before that round trip has even started.
	return r.pickerFetchAttach(path), false
}

func agentPickedCmd(id string) tea.Cmd {
	return func() tea.Msg { return AgentPickedMsg{ID: id} }
}

// ---------------------------------------------------------------------------
// The filter box: one interaction model, stated once
// ---------------------------------------------------------------------------
//
// THE MODEL. Four rounds of verification each found a new edge case in this
// one mechanism, because the mechanism had two jobs mixed together: typed
// text was BOTH a filter AND a navigation instruction, and the navigation
// half resolved relative paths against the current browse location. That is
// what produced, in order: typing "backend\" silently descending with no
// Enter; typing "..\" silently ascending; a mid-string separator acting like
// a command; a freshly typed absolute "D:\" being JOINED onto the previous
// location (filepath.Join(root, "D:\")) and landing somewhere nonsensical;
// and a truncated refusal rendered over a list that still showed the old
// directory. One cause, five symptoms.
//
// The model now, in full:
//
//  1. TYPED TEXT NEVER NAVIGATES. Once browse mode is engaged, every
//     keystroke in the filter box only ever filters the directory listing
//     that is already on screen. There is no path parsing, no separator
//     handling, no join — onPickerFilterChange returns nil, always, in that
//     state. Type to narrow, Enter (or a click) to act, exactly like every
//     other Popup consumer in this codebase.
//  2. LOCATION CHANGES ONLY FROM A CHOSEN ROW. Enter/click on a directory
//     row descends; Enter/click on the ".." row ascends. Both carry an
//     ABSOLUTE target (onPickerListing builds every item id as
//     filepath.Join(reply.path, name)), so no navigation ever has to be
//     resolved against ambient state — which is what made the stale-state
//     bug possible in the first place.
//  3. ONE LEADING-SLOT COMMAND EVALUATOR, FUZZY MODE ONLY. While the picker
//     is still showing fuzzy results, and only there, the filter's leading
//     slot is checked ONCE for a browse command: '?' (explorer) or one of
//     F08's path prefixes. That is the whole of "@?" and "@D:\" — the
//     characters land in the filter box, the leading slot recognises them,
//     and the picker commits to browse mode rooted there. Leading-slot
//     only, matching '@''s own F04 discipline and the '?' rule a previous
//     round locked in: once browsing has engaged, the evaluator is never
//     consulted again, so a later '?' or '\' is ordinary filter text.
//  4. EVERY COMMAND TARGET IS SELF-CONTAINED. pickerLeadingCommand returns
//     a target the BACKEND can resolve on its own — absolute, '~'-rooted,
//     or relative to the session's fixed base (project root when bound,
//     else this process's cwd, which is what pickerFetchDirectory already
//     sends as `cwd`). It is never relative to the current browse
//     location, and nothing in this file joins it against r.picker.root.
//
// SINGLE SOURCE OF TRUTH for "where is browse mode rooted": r.picker.root,
// written in EXACTLY ONE place — onPickerListing, from the reply's own
// `path` field, i.e. the location the backend actually resolved and listed.
// Nothing else assigns it. Both the render side (the ".." row, every item
// id) and the navigation side (Enter's absolute targets) are built from
// that same reply in that same function, so they cannot disagree. A failed
// navigation leaves it untouched: pickerShowRefusal changes no location
// state at all, so an unresolvable path shows a message and stays exactly
// where it was.
//
// THE ONE EXCEPTION to rule 1, added deliberately a round later when the
// two modes were found disagreeing: a SELF-CONTAINED absolute target
// (drive letter or '~') typed into the leading slot navigates in BOTH
// modes. It is still "typing navigated", which rule 1 otherwise forbids,
// and it is allowed only because such a target names exactly one location
// regardless of where the picker already is — so it cannot be joined onto,
// or misread against, any ambient state. A base-relative prefix or '?'
// gets no such exception; see the SYMMETRY entry in the file header for
// the full reasoning and for what (b) would have cost.

// pickerQueryFor is Popup's QueryFor hook. The raw typed text IS the query,
// in both modes — there is no longer a path/leaf split to make, because
// typed text is no longer a path (see THE MODEL above). Kept as an explicit
// identity rather than deleted: PopupConfig.QueryFor exists for consumers
// that need it, and a future picker mode may; leaving the hook wired, with
// its reasoning, is cheaper than rediscovering why it was removed.
func (r *Root) pickerQueryFor(raw string) string { return raw }

// onPickerFilterChange is Popup's OnFilterChange hook: fired with the raw
// typed text on every keystroke.
//
// It does exactly one thing — evaluate the LEADING SLOT for a browse
// command, and only while still in fuzzy mode. In browse mode it returns
// nil unconditionally: typed text filters, it does not navigate.
func (r *Root) onPickerFilterChange(raw string) tea.Cmd {
	target, kind := r.pickerLeadingCommand(raw)
	switch kind {
	case cmdNone:
		return nil // ordinary filter text, in either mode
	case cmdAbsolute:
		// Self-contained: a drive letter or '~' names one location and
		// cannot be read two ways. Honoured in BOTH modes — the symmetry
		// item 2 asked for, and F08 Done-when #3's own "@D:\ with no
		// extra keystroke".
	case cmdExplorer, cmdBaseRelative:
		// Both mean "somewhere relative to this session's fixed base":
		// '?' is mode ENTRY (meaningless once already browsing, which is
		// also what keeps the '?'-only-once rule a previous round locked
		// in), and '\' / '.\' / '..\' name a location only in terms of a
		// base, which is exactly the ambiguity that let item 4 happen.
		// Fuzzy mode only; while browsing, the ".." row is the
		// unambiguous way up.
		if r.picker.browsing {
			return nil
		}
	}
	r.picker.browsing = true
	// Bug 6: Agents' visibility is governed SOLELY by r.picker.leading (set
	// once, at open, from OpenAttachmentPickerMsg — only the buffer's
	// leading @token can ever name an agent, B12's rule). Engaging browse
	// mode must not couple into that at all. This used to nil agentItems
	// here, which is exactly why a leading '?' made Agents vanish
	// regardless of leading-ness. agentItems is left untouched.
	return r.browseTo(target)
}

// pickerCmdKind classifies what a leading-slot command IS, which is what
// decides whether it is honoured in browse mode as well as fuzzy mode.
// The distinction that matters is not "how many characters" but "can this
// be read two ways" — see onPickerFilterChange's switch.
type pickerCmdKind int

const (
	cmdNone pickerCmdKind = iota
	// cmdExplorer: '?'. Mode ENTRY, not a location — "start browsing, at
	// the default root". Fuzzy mode only.
	cmdExplorer
	// cmdAbsolute: a drive letter or '~'. Names exactly one location
	// whatever the picker is currently showing, so it is safe — and
	// useful — in both modes.
	cmdAbsolute
	// cmdBaseRelative: '\', '.\', './', '..\', '../'. Names a location
	// only together with a base. Fuzzy mode only; the ".." row covers
	// going up once browsing.
	cmdBaseRelative
)

// pickerLeadingCommand is THE one evaluator for "is the filter's leading
// slot a browse command, and if so what location does it name and of what
// kind" — F08's own prefix table, consulted from exactly one call site
// (onPickerFilterChange). Leading-position only, by construction: every
// check below is an exact match or a HasPrefix, so a separator or '?'
// anywhere other than the start is ordinary text, the same discipline '@'
// itself follows in F04's agentTokenState.
//
// Every target it returns is resolvable by the backend on its own, from
// what pickerFetchDirectory already sends (`cwd`/`project_id`). None of
// them is ever relative to the CURRENT browse location, which is the
// property that made item 4 impossible rather than merely unlikely.
func (r *Root) pickerLeadingCommand(raw string) (target string, kind pickerCmdKind) {
	// '?' — the explorer. Roots at the bound project's own root when there
	// is one (the backend joins a relative path onto project_id's root),
	// else at this process's cwd.
	if raw == "?" {
		if r.picker.hasProject {
			return ".", cmdExplorer
		}
		return r.picker.cwd, cmdExplorer
	}
	// A BARE drive root, and only that: the filter must be EXACTLY
	// `<letter>:\` or `<letter>:/`, three characters, nothing more.
	//
	// This was `len(raw) >= 3` — a prefix check — and that is a bug with a
	// precise symptom: typing `C:\Windows\` character by character, the
	// moment the third keystroke completed the buffer to `C:\`, the prefix
	// check already matched and fired, navigating to C:\ root before
	// "Windows\" could be typed at all. Exact equality is what makes a
	// longer path sharing the prefix never match: `C:\Windows\` is never
	// EQUAL to the bare pattern, only ever longer than it, so it stays
	// ordinary filter text throughout — which is Item 1's rule, correctly
	// applied. Only a complete, bare drive-root entry jumps.
	if len(raw) == 3 && isDriveLetter(raw[0]) && raw[1] == ':' && isPathSep(raw[2]) {
		return raw, cmdAbsolute
	}
	// '~' is absolute too once the backend expands it (pathguard's own
	// os.path.expanduser) — it does not depend on where the picker is.
	//
	// DELIBERATELY left as a PREFIX match, unlike the drive root above —
	// a decision confirmed in verification, not an oversight. `~\Desktop\`
	// firing at `~\` lands you in the home directory with the filter
	// cleared, and "Desktop" then filters that listing, which is exactly
	// what a home shortcut is for. The drive-root case is different only
	// because `C:\` is itself a destination people type as a prefix of a
	// longer path they are still typing; `~\` is not typed as a prefix of
	// anything one navigates to by hand.
	for _, prefix := range []string{`~\`, "~/"} {
		if strings.HasPrefix(raw, prefix) {
			return raw, cmdAbsolute
		}
	}
	// F08's remaining prefixes, all base-relative.
	//
	// Bare '/' AND bare '\' are both deliberately absent — a revision of
	// F08's own table, made across two verification rounds. '/' went
	// first: it is ordinary, realistic fuzzy-search text, and claiming it
	// as a command made that text unreachable. '\' followed for the same
	// reason once it was found jumping to a drive root on its own, which
	// is neither what F08's table meant by '@\' nor something a bare
	// separator can name unambiguously. Reaching a drive root now
	// requires naming the drive: an exact `D:\`, per the rule above.
	//
	// '.\', './', '..\', '../' stay, each distinguished by its FIRST
	// character rather than by the separator, so dropping the bare
	// separators does not touch them.
	for _, prefix := range []string{`.\`, "./", `..\`, "../"} {
		if strings.HasPrefix(raw, prefix) {
			return raw, cmdBaseRelative
		}
	}
	return "", cmdNone
}

// pickerHint is the picker's own dim footer line — PopupConfig.Hint, read
// at render time. The path shortcuts all work (and are tested), but
// nothing in the UI said so, which made them discoverable only by trial
// and error.
//
// Mode-aware on purpose, and this is the reason Hint is a func rather than
// a string: '?' is a command only from fuzzy mode (pickerLeadingCommand's
// cmdExplorer is gated there), so advertising it while browsing would be
// exactly F04's Bug 12 — a hint naming a binding that does not currently
// do anything. The drive-root and '~' jumps work in both modes, so they
// are always listed.
//
// Kept to one short line, dim, and placed above the key-binding footer:
// the list region is where refusals and the "still showing <path>" line
// live, and this must not compete with them.
func (r *Root) pickerHint() string {
	parts := []string{`~\ home`, `C:\ D:\ drive root`}
	if !r.picker.browsing {
		parts = append(parts, "? browse")
	}
	return strings.Join(parts, "   ")
}

// browseTo is THE one navigation entry point: every location change in
// browse mode goes through it — a leading-slot command, a chosen directory
// row, and the ".." row alike. It deliberately does NOT touch
// r.picker.root: the location is only ever adopted from the reply that
// actually resolved it (onPickerListing), so a fetch that fails or never
// arrives cannot move the picker somewhere it is not.
func (r *Root) browseTo(target string) tea.Cmd {
	return r.pickerFetchDirectory(target)
}

func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isPathSep(b byte) bool { return b == '\\' || b == '/' }
