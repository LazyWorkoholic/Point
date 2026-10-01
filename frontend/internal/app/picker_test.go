package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/array"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// fakePickerWorkspace records whatever message it is handed — standing in
// for chat.Workspace (this package cannot import chat), the same pattern
// fakeFocusWorkspace/fakeDraftWorkspace already use for their own narrow
// interfaces. onPickerAttach/onPickerAgentPickedMsg deliver their write-back
// through deliverToWorkspace, which calls Update directly rather than
// returning a Cmd that produces the message later — so what a test needs
// to capture is THIS, not cmd()'s return value.
type fakePickerWorkspace struct{ last tea.Msg }

func (f *fakePickerWorkspace) ID() WorkspaceID           { return WorkspaceChat }
func (f *fakePickerWorkspace) Init() tea.Cmd             { return nil }
func (f *fakePickerWorkspace) View(layout.Layout) string { return "" }
func (f *fakePickerWorkspace) Suspend() ([]byte, error)  { return nil, nil }
func (f *fakePickerWorkspace) Resume([]byte) error       { return nil }
func (f *fakePickerWorkspace) Cards() []layout.CardSlot  { return nil }
func (f *fakePickerWorkspace) Update(m tea.Msg) (Workspace, tea.Cmd) {
	f.last = m
	return f, nil
}

// PickerSession lets this fake stand in for openAttachmentPicker's own
// PickerTarget assertion (r.current().(PickerTarget)), not just the
// deliverToWorkspace half.
func (f *fakePickerWorkspace) PickerSession() (sessionID, projectID string, hasProject bool) {
	return "s1", "", false
}

var _ Workspace = (*fakePickerWorkspace)(nil)
var _ PickerTarget = (*fakePickerWorkspace)(nil)

// pickerLeadingCommand is THE one leading-slot evaluator — F08's own mode
// table, consulted from exactly one call site and only while still fuzzy.
//
// Bare '/' is a deliberate REVISION of F08's own original table, made
// during manual verification: the spec originally listed '@/' alongside
// '@\' as an equivalent trigger, but '/' is realistic fuzzy-search text (a
// path-shaped query has every reason to start with one), and claiming it
// as a mode switch made that text unreachable. '\' keeps its special
// meaning — see pickerLeadingCommand's own doc for why the two are not
// actually symmetric despite the spec originally treating them as aliases.
func TestPickerLeadingCommandMatchesF08sTable(t *testing.T) {
	cases := []struct {
		raw        string
		wantKind   pickerCmdKind
		wantTarget string // "" means "don't care"
	}{
		{raw: "", wantKind: cmdNone},
		{raw: "r", wantKind: cmdNone},          // a viable agent prefix, not a path
		{raw: "research", wantKind: cmdNone},   // an agent name, not a path
		{raw: "report.pdf", wantKind: cmdNone}, // attachment-shaped, not a command
		{raw: "D:", wantKind: cmdNone},         // no trailing separator yet
		// A drive root is an EXACT three-character match, never a prefix:
		// `C:\Projects\` is only ever LONGER than the bare pattern, so it
		// stays ordinary text and cannot fire mid-typing.
		{raw: `D:\`, wantKind: cmdAbsolute, wantTarget: `D:\`},
		{raw: "D:/", wantKind: cmdAbsolute, wantTarget: "D:/"},
		{raw: `c:\`, wantKind: cmdAbsolute, wantTarget: `c:\`}, // case does not matter
		{raw: `C:\Projects\`, wantKind: cmdNone},
		{raw: `D:\Projects`, wantKind: cmdNone},
		{raw: `C:\W`, wantKind: cmdNone},
		// '~' is deliberately still a PREFIX match — see
		// pickerLeadingCommand's own note for why the two differ.
		{raw: `~\`, wantKind: cmdAbsolute, wantTarget: `~\`},
		{raw: "~/", wantKind: cmdAbsolute, wantTarget: "~/"},
		{raw: `~\Desktop\`, wantKind: cmdAbsolute, wantTarget: `~\Desktop\`},
		// Bare separators are never commands — '/' first, then '\'.
		{raw: `\`, wantKind: cmdNone},
		{raw: `\foo`, wantKind: cmdNone},
		{raw: `.\`, wantKind: cmdBaseRelative, wantTarget: `.\`},
		{raw: "./", wantKind: cmdBaseRelative, wantTarget: "./"},
		{raw: `..\`, wantKind: cmdBaseRelative, wantTarget: `..\`},
		{raw: "../", wantKind: cmdBaseRelative, wantTarget: "../"},
		{raw: "/", wantKind: cmdNone}, // REVISED: not a trigger — see this test's doc
		{raw: "~", wantKind: cmdNone}, // no separator — spec lists @~\ / @~/, not bare @~
		{raw: "?", wantKind: cmdExplorer},
	}
	r := &Root{picker: pickerState{cwd: `D:\somewhere`}}
	for _, c := range cases {
		target, kind := r.pickerLeadingCommand(c.raw)
		if kind != c.wantKind {
			t.Errorf("pickerLeadingCommand(%q) kind = %v, want %v", c.raw, kind, c.wantKind)
			continue
		}
		if kind != cmdNone && c.wantTarget != "" && target != c.wantTarget {
			t.Errorf("pickerLeadingCommand(%q) target = %q, want %q", c.raw, target, c.wantTarget)
		}
	}
}

// '?' roots at the bound project's own root when there is one (a relative
// "." the backend joins onto project_id's root), else at this process's
// cwd. This is the one command whose target depends on session state.
func TestPickerLeadingCommandExplorerRooting(t *testing.T) {
	withProject := &Root{picker: pickerState{hasProject: true, cwd: `D:\somewhere`}}
	if target, kind := withProject.pickerLeadingCommand("?"); kind != cmdExplorer || target != "." {
		t.Errorf("with a project: '?' -> (%q, %v), want (\".\", cmdExplorer) — the project's own root", target, kind)
	}

	noProject := &Root{picker: pickerState{cwd: `D:\somewhere`}}
	if target, kind := noProject.pickerLeadingCommand("?"); kind != cmdExplorer || target != `D:\somewhere` {
		t.Errorf("without a project: '?' -> (%q, %v), want this process's cwd", target, kind)
	}
}

// pickerQueryFor is the identity in BOTH modes now: typed text is a filter,
// never a path, so there is no leaf to split out (see THE MODEL in
// picker.go). The old leaf-splitting behaviour existed only to serve
// typed-text navigation, which is gone.
func TestPickerQueryForIsAlwaysIdentity(t *testing.T) {
	r := &Root{}
	const raw = `D:\Projects\Poi`
	if got := r.pickerQueryFor(raw); got != raw {
		t.Errorf("fuzzy mode: QueryFor(%q) = %q, want unchanged", raw, got)
	}
	r.picker.browsing = true
	if got := r.pickerQueryFor(raw); got != raw {
		t.Errorf("browse mode: QueryFor(%q) = %q, want unchanged — typed text is a filter, not a path", raw, got)
	}
}

// Only the LEADING position can ever name an agent (B12's own rule). A
// picker opened elsewhere in the buffer must show Files only — never an
// Agents section a chosen entry could not actually route as agent
// selection.
func TestOnPickerAgentsRespectsLeading(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"agents": []map[string]string{{"id": "research", "name": "Research", "description": "d"}},
	})

	leading := &Root{picker: pickerState{popup: NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16})}}
	leading.picker.leading = true
	leading.onPickerAgents(protocol.Envelope{Type: protocol.EvAgentList, Payload: payload})
	if len(leading.picker.agentItems) != 1 {
		t.Fatalf("leading picker: got %d agent items, want 1", len(leading.picker.agentItems))
	}

	notLeading := &Root{picker: pickerState{popup: NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16})}}
	notLeading.picker.leading = false
	notLeading.onPickerAgents(protocol.Envelope{Type: protocol.EvAgentList, Payload: payload})
	if len(notLeading.picker.agentItems) != 0 {
		t.Fatalf("non-leading picker: got %d agent items, want 0 (agents only make sense at index 0)", len(notLeading.picker.agentItems))
	}
}

// Protected case, end to end through openAttachmentPicker itself (not just
// onPickerAgents in isolation): '@' typed mid-sentence must open with
// Leading carried through correctly from OpenAttachmentPickerMsg into
// pickerState, so a later change to item 5's trigger design (whether the
// picker should ALSO reopen live off buffer content) cannot silently widen
// who gets offered Agents along the way.
func TestOpenAttachmentPickerCarriesNonLeadingThrough(t *testing.T) {
	r, err := New(Options{Client: protocol.NewClient(protocol.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	fw := &fakePickerWorkspace{}
	r.SetWorkspace(WorkspaceChat, fw)
	r.active = WorkspaceChat
	size(t, r, 120, 40) // establishes r.layout.OverlaysAllowed; a zero-value Root refuses every overlay

	r.openAttachmentPicker(OpenAttachmentPickerMsg{Leading: false})

	if r.picker.popup == nil {
		t.Fatal("openAttachmentPicker did not open a popup")
	}
	if r.picker.leading {
		t.Fatal("pickerState.leading = true, want false — it must carry OpenAttachmentPickerMsg.Leading through unchanged")
	}
}

// Bug 6: Agents' visibility is governed SOLELY by r.picker.leading — the
// same rule already correctly enforced for a leading vs. mid-sentence '@'.
// Engaging browse mode (a leading '?' typed inside an already-open fuzzy
// picker, or any other browse-prefix) must not couple into that at all. A
// leading '@' that then forces browse should still offer Agents once the
// new directory listing lands — found live: typing '?' first thing wiped
// the Agents section outright, regardless of leading-ness.
func TestAgentsSurviveEngagingBrowseModeWhenLeading(t *testing.T) {
	r := &Root{picker: pickerState{
		popup:   NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}),
		leading: true,
		cwd:     `D:\Projects\Point`,
	}}
	agentsPayload, _ := json.Marshal(map[string]any{
		"agents": []map[string]string{{"id": "research", "name": "Research"}},
	})
	r.onPickerAgents(protocol.Envelope{Type: protocol.EvAgentList, Payload: agentsPayload})
	filesPayload, _ := json.Marshal(map[string]any{"files": []string{"README.md"}})
	r.onPickerListing(protocol.Envelope{Type: protocol.EvProjectFileList, Payload: filesPayload})
	if len(r.picker.agentItems) != 1 {
		t.Fatalf("before engaging browse: agentItems = %d, want 1", len(r.picker.agentItems))
	}

	// The leading '?' trigger, exactly as typed into the picker's own
	// filter box.
	r.onPickerFilterChange("?")

	if len(r.picker.agentItems) != 1 {
		t.Errorf("agentItems = %d after engaging browse mode via leading '?', want 1 (unchanged) — Bug 6", len(r.picker.agentItems))
	}
	if !r.picker.browsing {
		t.Fatal("leading '?' must still engage browse mode")
	}
}

// The mirror case: a NON-leading picker never had Agents to begin with
// (onPickerAgents' own gate), so engaging browse changes nothing about
// that — confirming Bug 6's fix does not accidentally start offering
// Agents somewhere B12's rule says they cannot apply.
func TestAgentsStayAbsentEngagingBrowseModeWhenNotLeading(t *testing.T) {
	r := &Root{picker: pickerState{
		popup:   NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}),
		leading: false,
		cwd:     `D:\Projects\Point`,
	}}
	agentsPayload, _ := json.Marshal(map[string]any{
		"agents": []map[string]string{{"id": "research", "name": "Research"}},
	})
	r.onPickerAgents(protocol.Envelope{Type: protocol.EvAgentList, Payload: agentsPayload})
	if len(r.picker.agentItems) != 0 {
		t.Fatalf("non-leading picker should never have agent items, got %d", len(r.picker.agentItems))
	}

	r.onPickerFilterChange("?")

	if len(r.picker.agentItems) != 0 {
		t.Errorf("agentItems = %d after engaging browse, want 0 — non-leading must stay Files-only", len(r.picker.agentItems))
	}
}

// DOCUMENT + TEST: the filter box's leading '?' behaves exactly like F04's
// main-input leading-'@' rule — checked as a command FIRST, only once,
// only from the still-fuzzy state. A '?' anywhere else, or a SECOND '?'
// typed in that same leading slot once browsing has already engaged, is
// ordinary text — because the command check lives entirely inside
// onPickerFilterChange's `!r.picker.browsing` branch, which a first '?'
// already exited. This is intentional and must not change.
func TestLeadingQuestionMarkIsACommandOnlyOnceFromFuzzyMode(t *testing.T) {
	r := &Root{
		client: protocol.NewClient(protocol.Options{}),
		picker: pickerState{
			popup: NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}),
			cwd:   `D:\Projects\Point`,
		},
	}

	// First '?' from fuzzy mode: a command. Engages browse, and attempts
	// to fetch a directory listing. The client here is never Start()ed (a
	// unit test cannot spin up a real backend), so the SEND itself fails —
	// pickerSend's own fix for Bug B marks awaitingConnection on exactly
	// that failure, which is this test's proof an attempt was made.
	r.onPickerFilterChange("?")
	if !r.picker.browsing {
		t.Fatal("the FIRST leading '?' from fuzzy mode must engage browse mode")
	}
	if !r.picker.awaitingConnection {
		t.Fatal("engaging browse mode must attempt to fetch a directory listing")
	}
	r.picker.awaitingConnection = false // reset, so the second '?' below proves it on its own

	// A SECOND '?', in that same leading slot, now that browsing is
	// already engaged: ordinary text — a leaf filter over the current
	// directory, not a command. No new fetch attempt at all;
	// pickerQueryFor reads it as the leaf "?" to match against entry
	// names instead.
	r.onPickerFilterChange("?")
	if r.picker.awaitingConnection {
		t.Error("a second leading '?' while already browsing must not attempt any fetch — it is ordinary filter text, not a command")
	}
	if got := r.pickerQueryFor("?"); got != "?" {
		t.Errorf("pickerQueryFor(%q) while browsing = %q, want %q (plain filter text)", "?", got, "?")
	}
}

// ---------------------------------------------------------------------------
// THE MODEL (picker.go): typed text never navigates. One regression test
// per numbered item from the systematic round that produced it.
// ---------------------------------------------------------------------------

// browsingPicker is a picker already in browse mode, with a real listing
// adopted from a reply — the state every item below starts from. The
// client is a never-Start()ed one, so any fetch ATTEMPT is observable via
// awaitingConnection (pickerSend's own Bug B marker) without needing a
// live backend.
func browsingPicker(t *testing.T, root string, entries []map[string]any) *Root {
	t.Helper()
	r := &Root{
		client: protocol.NewClient(protocol.Options{}),
		picker: pickerState{
			popup:    NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}),
			browsing: true,
			cwd:      `D:\Projects\Point`,
		},
	}
	payload, err := json.Marshal(map[string]any{"path": root, "entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	r.onPickerListing(protocol.Envelope{Type: protocol.EvDirectoryList, Payload: payload})
	if r.picker.root != root {
		t.Fatalf("fixture: root = %q, want %q", r.picker.root, root)
	}
	return r
}

// ITEM 1: ordinary typed text, while ALREADY browsing, must never
// navigate. The reported spellings: a folder name plus a trailing
// separator ("backend\", "backend/") used to auto-descend, and ".." plus a
// separator used to auto-ascend — no Enter, no arrow key, no click.
//
// The list below is everything that is NOT a self-contained location:
// folder-name fragments, the base-relative prefixes (whose meaning depends
// on a base the two modes read differently — the ".." row is the
// unambiguous way up while browsing), and '?' (mode ENTRY, meaningless
// once already browsing, which is also what keeps the '?'-only-once rule
// intact). Self-contained absolute targets are the deliberate exception
// and have their own test — see TestItem2AbsolutePathsBehaveTheSameInBothModes.
func TestItem1OrdinaryTypedTextNeverNavigatesWhileBrowsing(t *testing.T) {
	for _, raw := range []string{
		`backend\`, "backend/", "..", `sub\dir\`, "backend", "read",
		`..\`, "../", `\`, `.\`, "./", // base-relative: fuzzy mode only
		"?", // mode entry: fuzzy mode only
	} {
		r := browsingPicker(t, `D:\Projects\Point`, []map[string]any{
			{"name": "backend", "is_dir": true},
			{"name": "README.md", "is_dir": false},
		})
		rootBefore, itemsBefore := r.picker.root, len(r.picker.dirItems)

		if cmd := r.onPickerFilterChange(raw); cmd != nil {
			t.Errorf("typing %q while browsing returned a command — it must only filter", raw)
		}
		if r.picker.awaitingConnection {
			t.Errorf("typing %q while browsing attempted a fetch — it must only filter", raw)
		}
		if r.picker.root != rootBefore {
			t.Errorf("typing %q moved the location: root = %q, want %q", raw, r.picker.root, rootBefore)
		}
		if len(r.picker.dirItems) != itemsBefore {
			t.Errorf("typing %q changed the listing: %d items, want %d", raw, len(r.picker.dirItems), itemsBefore)
		}
	}
}

// ITEM 2's resolution, chosen as (a) SYMMETRIC for self-contained targets:
// a drive-letter or '~' path is the SAME command in both modes. It names
// exactly one location however the picker got there, so it cannot be read
// two ways and cannot be corrupted by ambient state — which is what makes
// it safe to honour while browsing, unlike a base-relative prefix.
//
// This is the multi-character both-modes case the round asked for: the
// identical input, from fuzzy mode and from browse mode, must produce the
// identical outcome.
func TestItem2AbsolutePathsBehaveTheSameInBothModes(t *testing.T) {
	for _, raw := range []string{`D:\`, `C:\`, "D:/", `~\`} {
		// From FUZZY mode.
		fuzzy := &Root{
			client: protocol.NewClient(protocol.Options{}),
			picker: pickerState{
				popup: NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}),
				cwd:   `D:\Projects\Point`,
			},
		}
		fuzzy.onPickerFilterChange(raw)
		if !fuzzy.picker.browsing {
			t.Errorf("fuzzy mode: typing %q did not engage browse mode", raw)
		}
		if !fuzzy.picker.awaitingConnection {
			t.Errorf("fuzzy mode: typing %q did not attempt to navigate", raw)
		}

		// From BROWSE mode, already somewhere else entirely.
		browse := browsingPicker(t, `D:\Projects\Point\backend`, []map[string]any{
			{"name": "runtime", "is_dir": true},
		})
		browse.onPickerFilterChange(raw)
		if !browse.picker.browsing {
			t.Errorf("browse mode: typing %q dropped out of browse mode", raw)
		}
		if !browse.picker.awaitingConnection {
			t.Errorf("browse mode: typing %q did not attempt to navigate — it must behave the same as in fuzzy mode", raw)
		}
		// And item 4's property still holds: the target was NOT joined
		// onto the previous location — root stays until a reply adopts a
		// new one, and nothing prefixed the old root onto the request.
		if browse.picker.root != `D:\Projects\Point\backend` {
			t.Errorf("browse mode: typing %q moved root to %q before any reply", raw, browse.picker.root)
		}
	}
}

// The drive-root match is EXACT, not a prefix — the bug this closes.
// Typing `C:\Windows\` character by character used to fire the instant the
// third keystroke completed `C:\`, navigating to the drive root before
// "Windows\" could be typed at all. A longer path sharing the prefix is
// never EQUAL to the bare pattern, only ever longer, so it stays ordinary
// text in BOTH modes — and bare '\' is not a command either, the same
// correction bare '/' already had.
func TestLongerPathsSharingADriveRootPrefixNeverJump(t *testing.T) {
	for _, raw := range []string{
		`C:\Windows\`, `D:\Projects\`, `C:\W`, `D:\Projects\Point`,
		`\`, `\foo`, // bare separator: never a command on its own
	} {
		// FUZZY mode: must stay fuzzy, must not navigate.
		fuzzy := &Root{
			client: protocol.NewClient(protocol.Options{}),
			picker: pickerState{
				popup: NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}),
				cwd:   `D:\Projects\Point`,
			},
		}
		if cmd := fuzzy.onPickerFilterChange(raw); cmd != nil {
			t.Errorf("fuzzy mode: typing %q returned a command — it must be ordinary filter text", raw)
		}
		if fuzzy.picker.browsing {
			t.Errorf("fuzzy mode: typing %q engaged browse mode — only an EXACT bare drive root may", raw)
		}
		if fuzzy.picker.awaitingConnection {
			t.Errorf("fuzzy mode: typing %q attempted to navigate — it must only filter", raw)
		}

		// BROWSE mode: must not move.
		browse := browsingPicker(t, `D:\Projects\Point\backend`, []map[string]any{
			{"name": "runtime", "is_dir": true},
		})
		if cmd := browse.onPickerFilterChange(raw); cmd != nil {
			t.Errorf("browse mode: typing %q returned a command — it must be ordinary filter text", raw)
		}
		if browse.picker.awaitingConnection {
			t.Errorf("browse mode: typing %q attempted to navigate — it must only filter", raw)
		}
		if browse.picker.root != `D:\Projects\Point\backend` {
			t.Errorf("browse mode: typing %q moved the location to %q", raw, browse.picker.root)
		}
	}
}

// Command-like characters are LEADING-POSITION ONLY, matching '@”s own F04
// discipline: mid-string they are ordinary text, the exact defect bare '/'
// had. Checked in fuzzy mode, where the evaluator actually runs.
func TestItem2CommandCharsAreLeadingPositionOnly(t *testing.T) {
	r := &Root{picker: pickerState{cwd: `D:\Projects\Point`}}

	// Leading: a command.
	for _, raw := range []string{"?", `..\`, `D:\`, `~\`} {
		if _, kind := r.pickerLeadingCommand(raw); kind == cmdNone {
			t.Errorf("pickerLeadingCommand(%q) = cmdNone, want a command (leading position)", raw)
		}
	}
	// Anywhere else: ordinary text. Every one of these contains a
	// command character, none of them START with one.
	for _, raw := range []string{
		`backend\`, `backend\runtime`, `src/main`, "a?", "what?",
		`run\time\x`, "..", "report.pdf", "D:",
		`\`, `\foo`, // bare separator: never a command, same as bare '/'
		`C:\Windows\`, `D:\Projects\`, // longer than the bare drive root
	} {
		if target, kind := r.pickerLeadingCommand(raw); kind != cmdNone {
			t.Errorf("pickerLeadingCommand(%q) = command %v -> %q, want ordinary text (not leading)", raw, kind, target)
		}
	}
}

// ITEM 3: an unresolvable path shows its reason and does NOT move. The
// error path must never also be a navigation — no reset to a root, no
// cleared listing.
func TestItem3RefusalNeverChangesLocation(t *testing.T) {
	r := browsingPicker(t, `D:\Projects\Point\backend`, []map[string]any{
		{"name": "runtime", "is_dir": true},
		{"name": "cli.py", "is_dir": false},
	})
	rootBefore, itemsBefore := r.picker.root, len(r.picker.dirItems)

	payload, _ := json.Marshal(map[string]any{
		"error": `'D:\Projects\Point\backend\nope' refused: no such path`,
		"offer": "",
	})
	cmd := r.onPickerListing(protocol.Envelope{Type: protocol.EvError, Payload: payload})

	if cmd != nil {
		t.Error("a refusal must not produce a command")
	}
	if r.picker.root != rootBefore {
		t.Errorf("a refusal moved the location: root = %q, want %q (unchanged)", r.picker.root, rootBefore)
	}
	if len(r.picker.dirItems) != itemsBefore {
		t.Errorf("a refusal changed the listing: %d items, want %d (unchanged)", len(r.picker.dirItems), itemsBefore)
	}
	if !r.picker.browsing {
		t.Error("a refusal must not drop out of browse mode")
	}
}

// ITEM 4: the stale-state bug. A freshly chosen absolute target must be
// used verbatim, never joined onto the previous location — the old code did
// filepath.Join(r.picker.root, typed), which turned a typed "D:\" into
// "D:\Projects\Point\backend\D:" and landed nowhere near D:\.
//
// Navigation now only happens from a chosen ROW, and every row's id is an
// absolute path built from the reply that listed it, so this asserts the
// property directly: the target handed to the backend is the row's own id,
// with no trace of the current root prefixed onto it.
func TestItem4NavigationTargetsAreAbsoluteNotJoinedOntoCurrentRoot(t *testing.T) {
	r := browsingPicker(t, `D:\Projects\Point\backend`, []map[string]any{
		{"name": "runtime", "is_dir": true},
	})

	// The ".." row and the subdirectory row: both must carry absolute,
	// self-contained ids.
	for _, it := range r.picker.dirItems {
		id := it.GetID()
		if !filepath.IsAbs(id) {
			t.Errorf("row id %q is not absolute — navigation would have to resolve it against ambient state", id)
		}
	}

	// Choosing the parent row navigates to exactly that absolute path,
	// not to root+parent.
	parent := filepath.Dir(`D:\Projects\Point\backend`)
	var parentItem *array.Item
	for _, it := range r.picker.dirItems {
		if it.GetID() == parent {
			parentItem = it
		}
	}
	if parentItem == nil {
		t.Fatalf("no '..' row for parent %q", parent)
	}
	r.picker.attachPath = "" // make sure we observe the directory path, not an attach
	_, closes := r.onPickerSelect(parentItem)
	if closes {
		t.Error("choosing a directory must keep the picker open")
	}
	// browseTo -> pickerFetchDirectory sent (and failed, no live backend),
	// but the path it tried is what matters. root must still be untouched
	// until a reply adopts it.
	if r.picker.root != `D:\Projects\Point\backend` {
		t.Errorf("root = %q, want it UNCHANGED until the reply adopts the new location", r.picker.root)
	}
}

// ITEM 5: the render half. r.picker.root and the rendered listing are both
// adopted from the SAME reply in the SAME function, so they cannot
// disagree; and a refusal's message must be READABLE rather than cut at
// renderRow's 40-cell name column, which is what made a long backend error
// look like a stale fragment over a list that was actually correct.
func TestItem5RefusalMessageIsWrappedNotTruncated(t *testing.T) {
	r := browsingPicker(t, `D:\Projects\Point\backend`, []map[string]any{
		{"name": "runtime", "is_dir": true},
	})

	long := `'D:\Projects\Point\backend\does\not\exist' refused: no such path (ListDirectory)`
	payload, _ := json.Marshal(map[string]any{"error": long, "offer": ""})
	r.onPickerListing(protocol.Envelope{Type: protocol.EvError, Payload: payload})

	rows := r.picker.popup.cfg.Tabs[0].Items
	var notices []string
	for _, it := range rows {
		if it.IsDisabled() {
			notices = append(notices, it.GetTitle())
		}
	}
	if len(notices) < 2 {
		t.Fatalf("a %d-char refusal produced %d notice row(s) — it must wrap, not truncate", len(long), len(notices))
	}
	for _, line := range notices {
		if w := runewidth.StringWidth(line); w > pickerNoticeWidth {
			t.Errorf("notice line %q is %d cells wide, over the %d renderRow would truncate at", line, w, pickerNoticeWidth)
		}
	}
	// The still-current location is stated, so the list below it is never
	// ambiguous about where it belongs.
	joined := strings.Join(notices, " ")
	if !strings.Contains(joined, "still showing") {
		t.Errorf("refusal notices %q should name the location that is still being shown", joined)
	}
	// And the listing itself survived.
	if len(r.picker.dirItems) == 0 {
		t.Error("the current directory's entries must still be listed under the refusal")
	}
}

// Discoverability: the path shortcuts all work, but nothing in the UI said
// so. One dim line above the key-binding footer names them — and it is
// mode-aware, because '?' is a command only from fuzzy mode and F04's Bug
// 12 is this codebase's standing rule against advertising a binding that
// does not currently work.
func TestPickerHintNamesTheShortcutsAndTracksMode(t *testing.T) {
	r := &Root{picker: pickerState{cwd: `D:\Projects\Point`}}

	fuzzy := r.pickerHint()
	for _, want := range []string{`~\`, `C:\`, `D:\`, "?"} {
		if !strings.Contains(fuzzy, want) {
			t.Errorf("fuzzy-mode hint %q does not mention %q", fuzzy, want)
		}
	}

	r.picker.browsing = true
	browsing := r.pickerHint()
	if strings.Contains(browsing, "?") {
		t.Errorf("browse-mode hint %q still advertises '?', which is fuzzy-mode only there", browsing)
	}
	for _, want := range []string{`~\`, `C:\`, `D:\`} {
		if !strings.Contains(browsing, want) {
			t.Errorf("browse-mode hint %q does not mention %q, which DOES work in both modes", browsing, want)
		}
	}
}

// The hint must render, fit, and sit in a different region from the list —
// it must not crowd out or compete with a refusal notice or the
// "still showing <path>" line, which both live in the list itself.
func TestPickerHintRendersWithoutCrowdingTheRefusalDisplay(t *testing.T) {
	r := browsingPicker(t, `D:\Projects\Point\backend`, []map[string]any{
		{"name": "runtime", "is_dir": true},
	})
	r.picker.popup.cfg.Hint = r.pickerHint

	payload, _ := json.Marshal(map[string]any{
		"error": `'D:\Projects\Point\backend\nope' refused: no such path`, "offer": "",
	})
	r.onPickerListing(protocol.Envelope{Type: protocol.EvError, Payload: payload})

	out := stripANSI(r.picker.popup.View(theme.New("turquoise", theme.BackgroundTransparent), 60, 16))

	// All three coexist: the refusal, the location, and the hint.
	for _, want := range []string{"refused", "still showing", `~\ home`} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered popup is missing %q:\n%s", want, out)
		}
	}
	// The list's own content and the hint are on DIFFERENT lines — the hint
	// is its own row above the footer, not jammed into the list.
	var hintLine, refusalLine int = -1, -1
	for i, line := range strings.Split(out, "\n") {
		if strings.Contains(line, `~\ home`) {
			hintLine = i
		}
		if strings.Contains(line, "refused") {
			refusalLine = i
		}
	}
	if hintLine < 0 || refusalLine < 0 {
		t.Fatalf("could not locate both rows (hint=%d refusal=%d):\n%s", hintLine, refusalLine, out)
	}
	if hintLine == refusalLine {
		t.Error("the hint and the refusal are sharing a row — the hint must be its own line")
	}
	if hintLine < refusalLine {
		t.Error("the hint must sit BELOW the list, above the footer — not above the refusal")
	}
	// Still fits, and the key-binding footer still survives beneath it.
	if !strings.Contains(out, "esc close") {
		t.Errorf("the key-binding footer was crowded out by the hint:\n%s", out)
	}
	// And the entry itself is still listed.
	if !strings.Contains(out, "runtime") {
		t.Errorf("the directory listing was crowded out:\n%s", out)
	}
}

// The palette and Help supply no Hint, so they reserve no row and render
// exactly as they did before this existed.
func TestNoHintMeansNoReservedRow(t *testing.T) {
	r := newRoot(t)
	th := theme.New("turquoise", theme.BackgroundTransparent)

	for name, p := range map[string]*Popup{"palette": newPaletteOverlay(r), "help": newHelpOverlay(r)} {
		if p.cfg.Hint != nil {
			t.Errorf("%s must not supply a Hint", name)
		}
		out := stripANSI(p.View(th, 70, 16))
		if strings.Contains(out, "drive root") {
			t.Errorf("%s rendered a hint row it never asked for:\n%s", name, out)
		}
	}
}

// Fuzzy mode's Files section comes from ListProjectFiles' plain path list.
func TestOnPickerListingFuzzyMode(t *testing.T) {
	r := &Root{picker: pickerState{popup: NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16})}}
	payload, _ := json.Marshal(map[string]any{"files": []string{`backend\runtime\task.py`, "README.md"}})

	r.onPickerListing(protocol.Envelope{Type: protocol.EvProjectFileList, Payload: payload})

	if len(r.picker.dirItems) != 2 {
		t.Fatalf("got %d file items, want 2", len(r.picker.dirItems))
	}
	if r.picker.dirItems[0].GetID() != `backend\runtime\task.py` {
		t.Errorf("item id = %q, want the path verbatim", r.picker.dirItems[0].GetID())
	}
	if len(r.picker.dirKind) != 0 {
		t.Error("fuzzy mode must not populate dirKind — nothing here is a directory to descend into")
	}
}

// Browse mode's Files section comes from DirectoryList's entries, a
// directory suffix marks which ones are directories, and dirKind records
// which item ids are descendable.
func TestOnPickerListingBrowseMode(t *testing.T) {
	r := &Root{picker: pickerState{popup: NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}), browsing: true}}
	payload, _ := json.Marshal(map[string]any{
		"path": `D:\Projects`,
		"entries": []map[string]any{
			{"name": "Point", "is_dir": true},
			{"name": "README.md", "is_dir": false},
		},
	})

	r.onPickerListing(protocol.Envelope{Type: protocol.EvDirectoryList, Payload: payload})

	if r.picker.root != `D:\Projects` {
		t.Errorf("root = %q, want D:\\Projects", r.picker.root)
	}
	// ".." (parent navigation) + Point + README.md.
	if len(r.picker.dirItems) != 3 {
		t.Fatalf("got %d items, want 3 (.. + Point + README.md)", len(r.picker.dirItems))
	}
	parentPath := r.picker.dirItems[0].GetID()
	if parentPath != `D:\` || !r.picker.dirKind[parentPath] {
		t.Errorf("dirItems[0] = %q (dirKind=%v), want the parent D:\\, marked as a directory",
			parentPath, r.picker.dirKind[parentPath])
	}
	dirPath := r.picker.dirItems[1].GetID()
	if !r.picker.dirKind[dirPath] {
		t.Errorf("dirKind[%q] = false, want true (Point is a directory)", dirPath)
	}
	filePath := r.picker.dirItems[2].GetID()
	if r.picker.dirKind[filePath] {
		t.Errorf("dirKind[%q] = true, want false (README.md is a file)", filePath)
	}
}

// Already at a filesystem root: there is no parent to navigate to, so no
// ".." entry appears — filepath.Dir("C:\") returns "C:\" unchanged, which
// is exactly the signal onPickerListing uses to know to omit it.
func TestOnPickerListingBrowseModeAtFilesystemRootHasNoParentEntry(t *testing.T) {
	r := &Root{picker: pickerState{popup: NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}), browsing: true}}
	payload, _ := json.Marshal(map[string]any{
		"path":    `C:\`,
		"entries": []map[string]any{{"name": "Windows", "is_dir": true}},
	})

	r.onPickerListing(protocol.Envelope{Type: protocol.EvDirectoryList, Payload: payload})

	if len(r.picker.dirItems) != 1 {
		t.Fatalf("got %d items, want 1 (no '..' at an actual filesystem root)", len(r.picker.dirItems))
	}
	if r.picker.dirItems[0].GetID() != `C:\Windows` {
		t.Errorf("dirItems[0] = %q, want C:\\Windows — a '..' entry leaked in at the root", r.picker.dirItems[0].GetID())
	}
}

// Bug B: '@' pressed before the backend handshake completes. pickerSend
// must record that a retry is owed rather than leaving the popup's
// "Loading…" placeholder with nothing ever in flight to replace it.
func TestPickerSendMarksAwaitingConnectionOnFailure(t *testing.T) {
	r := &Root{client: protocol.NewClient(protocol.Options{}), picker: pickerState{
		popup: NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}),
	}}
	// A Client that was never Start()ed has no backend yet — Request fails
	// exactly the way it does against a real one before ConnectedMsg.
	id, ok := r.pickerSend(protocol.CmdListAgents, map[string]any{})
	if ok || id != "" {
		t.Fatalf("pickerSend against a disconnected client returned (%q, %v), want (\"\", false)", id, ok)
	}
	if !r.picker.awaitingConnection {
		t.Error("awaitingConnection must be set once a send could not go out at all")
	}
}

// Once ConnectedMsg arrives, retryPickerIfAwaiting must actually attempt
// the fetch again — not just clear the flag and leave the popup stuck.
// Verified here by observing awaitingConnection flip back to true: the
// retry's OWN attempt fails against the same still-disconnected client
// (this test cannot spin up a real backend), so seeing the flag re-armed
// is proof the retry fired, not proof it succeeded — a real ConnectedMsg
// means the client IS connected by the time this runs for real.
func TestRetryPickerIfAwaitingFiresAnotherAttempt(t *testing.T) {
	r := &Root{client: protocol.NewClient(protocol.Options{}), picker: pickerState{
		popup:              NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}),
		hasProject:         true,
		awaitingConnection: true,
	}}

	r.retryPickerIfAwaiting()

	if !r.picker.awaitingConnection {
		t.Error("retryPickerIfAwaiting did not attempt anything — awaitingConnection should have been re-armed by the retry's own failed send")
	}
}

// No popup open, or nothing owed: a no-op, not a spurious fetch.
func TestRetryPickerIfAwaitingNoOpsWhenNotNeeded(t *testing.T) {
	r := &Root{client: protocol.NewClient(protocol.Options{})}
	if cmd := r.retryPickerIfAwaiting(); cmd != nil {
		t.Error("no popup open: must be a no-op")
	}

	r.picker.popup = NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16})
	r.picker.awaitingConnection = false
	if cmd := r.retryPickerIfAwaiting(); cmd != nil {
		t.Error("nothing owed (awaitingConnection false): must be a no-op")
	}
}

// Bug A's exact reproduction: the backend's 5-second idle heartbeat
// (corr_id null -> "" once decoded) must never be mistaken for a picker
// reply just because both agentsReqID and listReqID have already cleared
// to "" themselves. Without handlePickerEnvelope's env.CorrID == "" guard,
// this test fails by wiping the Agents section back to empty the instant
// the ping is delivered — reproduced here with a REAL ping envelope, not a
// synthetic one, at exactly the point in time it actually happened: after
// both real replies had already landed.
func TestPickerSurvivesHeartbeatPingAfterBothRepliesSettle(t *testing.T) {
	r := &Root{picker: pickerState{
		popup: NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}), leading: true,
	}}
	r.picker.agentsReqID = "agents-1"
	r.picker.listReqID = "files-1"

	agentsPayload, _ := json.Marshal(map[string]any{
		"agents": []map[string]string{{"id": "research", "name": "Research"}},
	})
	r.handlePickerEnvelope(protocol.Envelope{Type: protocol.EvAgentList, CorrID: "agents-1", Payload: agentsPayload})
	filesPayload, _ := json.Marshal(map[string]any{"files": []string{"README.md"}})
	r.handlePickerEnvelope(protocol.Envelope{Type: protocol.EvProjectFileList, CorrID: "files-1", Payload: filesPayload})

	if len(r.picker.agentItems) != 1 || len(r.picker.dirItems) != 1 {
		t.Fatalf("before the ping: agentItems=%d dirItems=%d, want 1 and 1",
			len(r.picker.agentItems), len(r.picker.dirItems))
	}

	// The real event: runtime.py's start_heartbeat, request_id=None.
	r.handlePickerEnvelope(protocol.Envelope{Type: protocol.EvPing, CorrID: ""})

	if len(r.picker.agentItems) != 1 {
		t.Errorf("agentItems = %d after a heartbeat ping, want 1 (unchanged) — Bug A", len(r.picker.agentItems))
	}
	if len(r.picker.dirItems) != 1 {
		t.Errorf("dirItems = %d after a heartbeat ping, want 1 (unchanged)", len(r.picker.dirItems))
	}
	if r.picker.popup.menu.TotalLen() != 2 {
		t.Errorf("popup shows %d items after the ping, want 2 (both sections still present)", r.picker.popup.menu.TotalLen())
	}
}

// The two initial fetches (ListAgents, ListProjectFiles) race in the real
// app; whichever lands second must not have discarded the first's section.
// Fired here in BOTH orders explicitly, per the request to prove this does
// not depend on arrival order.
func TestPickerBothSectionsSurviveEitherArrivalOrder(t *testing.T) {
	agentsPayload, _ := json.Marshal(map[string]any{
		"agents": []map[string]string{{"id": "research", "name": "Research"}},
	})
	filesPayload, _ := json.Marshal(map[string]any{"files": []string{"README.md"}})

	run := func(agentsFirst bool) {
		r := &Root{picker: pickerState{
			popup: NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}), leading: true,
		}}
		r.picker.agentsReqID = "agents-1"
		r.picker.listReqID = "files-1"

		first := protocol.Envelope{Type: protocol.EvAgentList, CorrID: "agents-1", Payload: agentsPayload}
		second := protocol.Envelope{Type: protocol.EvProjectFileList, CorrID: "files-1", Payload: filesPayload}
		if !agentsFirst {
			first, second = second, first
		}
		r.handlePickerEnvelope(first)
		r.handlePickerEnvelope(second)

		if len(r.picker.agentItems) != 1 {
			t.Errorf("agentsFirst=%v: agentItems = %d, want 1", agentsFirst, len(r.picker.agentItems))
		}
		if len(r.picker.dirItems) != 1 {
			t.Errorf("agentsFirst=%v: dirItems = %d, want 1", agentsFirst, len(r.picker.dirItems))
		}
		if r.picker.popup.menu.TotalLen() != 2 {
			t.Errorf("agentsFirst=%v: popup shows %d items, want 2", agentsFirst, r.picker.popup.menu.TotalLen())
		}
	}
	run(true)
	run(false)
}

// A guard refusal (ListDirectory/AttachContext both surface theirs the same
// way) shows the message IN the still-open popup rather than routing
// anywhere the popup itself would hide — see pickerShowRefusal's own doc.
func TestOnPickerListingRefusalStaysInThePopup(t *testing.T) {
	popup := NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16})
	r := &Root{overlay: popup, picker: pickerState{popup: popup, browsing: true}}
	payload, _ := json.Marshal(map[string]any{"error": "not a directory", "offer": ""})

	cmd := r.onPickerListing(protocol.Envelope{Type: protocol.EvError, Payload: payload})
	if cmd != nil {
		t.Error("a guard refusal must not produce a workspace write-back command")
	}
	if r.overlay == nil {
		t.Fatal("a guard refusal must not close the popup")
	}
	// Notices are indexed now — a long refusal wraps across several rows
	// rather than being truncated at renderRow's name column (item 5).
	it, ok := popup.menu.Selected()
	if !ok || !strings.HasPrefix(it.GetID(), "__refused__") {
		t.Errorf("popup's first item = %v, want a refusal notice row", it)
	}
}

// Choosing an agent item closes the picker and hands back an AgentPickedMsg
// naming it — no backend round trip, since the agent list is already known.
func TestOnPickerSelectAgent(t *testing.T) {
	r := &Root{picker: pickerState{popup: NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16})}}
	it := array.NewItem("research", "research").SetSection("Agents")

	cmd, closes := r.onPickerSelect(it)
	if !closes {
		t.Error("choosing an agent must close the picker")
	}
	if cmd == nil {
		t.Fatal("choosing an agent must produce a command")
	}
	msg, ok := cmd().(AgentPickedMsg)
	if !ok || msg.ID != "research" {
		t.Errorf("cmd() = %#v, want AgentPickedMsg{ID: \"research\"}", cmd())
	}
}

// Choosing a directory row stays open — F08 session's own decision: descend
// in place, never close-then-reopen.
func TestOnPickerSelectDirectoryStaysOpen(t *testing.T) {
	r := &Root{picker: pickerState{
		popup:    NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}),
		browsing: true,
		dirKind:  map[string]bool{`D:\Projects\Point`: true},
	}}
	it := array.NewItem(`D:\Projects\Point`, `Point\`).SetSection("Files")

	_, closes := r.onPickerSelect(it)
	if closes {
		t.Error("choosing a directory must keep the picker open")
	}
}

// Choosing a file also stays open at the OnSelect boundary — AttachContext
// is a round trip, and only ITS reply (onPickerAttach) can know whether the
// guard accepted it. Closing here unconditionally would close the popup out
// from under a refusal the user still needs to see.
func TestOnPickerSelectFileStaysOpenUntilAttachResolves(t *testing.T) {
	r := &Root{picker: pickerState{
		popup:   NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16}),
		dirKind: map[string]bool{},
	}}
	it := array.NewItem(`backend\runtime\task.py`, `backend\runtime\task.py`).SetSection("Files")

	_, closes := r.onPickerSelect(it)
	if closes {
		t.Error("choosing a file must not close before AttachContext's reply lands")
	}
}

// A successful AttachContext reply closes the picker and hands back the
// chip. Current is false, deliberately — Bug D's fix: managers\attachment.py
// inserts a fresh row with recorded_mtime/recorded_size NULL and only a
// real SEND ever records a pair (managers\context.py's
// record_attachment_sent), so a file that has never been sent WILL cost
// its full estimate on the next turn, which is what "changed" means here,
// not "current". Even though THIS payload's own recorded_mtime/size happen
// to be non-null (a resolved AttachmentResolution's live stat mirrored back
// for illustration), the chip's Current must not be inferred from them —
// see PickedAttachment's own doc.
func TestOnPickerAttachSuccessClosesAndProducesChip(t *testing.T) {
	popup := NewPopup(PopupConfig{Title: "@", MinW: 60, MinH: 16})
	fw := &fakePickerWorkspace{}
	r, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkspace(WorkspaceChat, fw)
	r.active = WorkspaceChat
	r.overlay = popup
	r.picker = pickerState{popup: popup, attachPath: `backend\runtime\task.py`}

	// A fresh attach's row, verbatim: recorded_mtime/size NULL, exactly as
	// managers\attachment.py's own doc says ("a NULL pair reads as 'never
	// sent' -- the correct answer for a row nothing has resolved yet").
	payload, _ := json.Marshal(map[string]any{
		"attachment": map[string]any{"recorded_mtime": nil, "recorded_size": nil},
		"resolution": map[string]any{"tokens_estimate": 713},
	})
	r.onPickerAttach(protocol.Envelope{Type: protocol.EvAttachment, Payload: payload})
	if r.overlay != nil {
		t.Error("a successful attach must close the picker")
	}
	msg, ok := fw.last.(AttachmentPickedMsg)
	if !ok {
		t.Fatalf("workspace received %#v, want AttachmentPickedMsg", fw.last)
	}
	if msg.Chip.Name != "task.py" || msg.Chip.Current || msg.Chip.Tokens != 713 {
		t.Errorf("chip = %+v, want {Name: task.py, Current: false, Tokens: 713}", msg.Chip)
	}
	if msg.Chip.RecordedMtime != nil || msg.Chip.RecordedSize != nil {
		t.Errorf("chip.RecordedMtime/Size = %v/%v, want both nil — a NULL pair means never sent", msg.Chip.RecordedMtime, msg.Chip.RecordedSize)
	}
}
