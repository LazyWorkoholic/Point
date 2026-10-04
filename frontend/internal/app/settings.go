// F10: the Settings popup (UI-03) — the Providers and Appearance tabs only.
// The Behaviour/Keys/About tabs, the point-doctor action and demo mode are
// explicitly out of this episode's narrowed scope (see its own handoff).
//
// This is also the first-run provider chooser: "the Providers section of
// Settings, not a separate wizard screen — same component, three entry
// points" (F10-settings-first-run.md). openSettingsPopup is the one
// construction path either entry point uses.
package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/array"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

const (
	providersTabIndex  = 0
	appearanceTabIndex = 1
	behaviourTabIndex  = 2
	aboutTabIndex      = 3
)

// providerCatalogEntry is one row the chooser can ever offer.
//
// There is no wire event that reports "known but not yet registered"
// providers — ListProviders only reports what bootstrap.py's
// build_registry_from_env already registered (ollama unconditionally, a
// cloud provider only once it already has a key, backend\providers\
// bootstrap.py), and backend\appconfig.py's known_providers() is a Python
// function, never sent over the wire. So the chooser's own list of WHAT CAN
// BE OFFERED has to be a frontend constant, mirroring appconfig.py's
// _KEY_ENV_VARS keys plus "ollama" by hand. If the backend ever adds a sixth
// provider, it is invisible here until this list is updated to match.
//
// Demo mode (F10c) is the one row that needs no key: DemoProvider.health()
// always reports healthy (B28 §1) and is registered unconditionally, so it
// becomes selectable here the moment ListProviders' reply includes it, with
// no branching needed elsewhere in the selection/health-check machinery
// below. Its one extra side effect (binding the shipped sample project) is
// in applyModelChoice, the one place that does need to know its id.
type providerCatalogEntry struct {
	ID       string
	Label    string
	NeedsKey bool
}

var providerCatalog = []providerCatalogEntry{
	{ID: "ollama", Label: "Ollama", NeedsKey: false},
	{ID: "openai", Label: "OpenAI", NeedsKey: true},
	{ID: "anthropic", Label: "Anthropic", NeedsKey: true},
	{ID: "gemini", Label: "Gemini", NeedsKey: true},
	{ID: "openrouter", Label: "OpenRouter", NeedsKey: true},
	{ID: "demo", Label: "Demo mode", NeedsKey: false},
}

func providerCatalogByID(id string) (providerCatalogEntry, bool) {
	for _, p := range providerCatalog {
		if p.ID == id {
			return p, true
		}
	}
	return providerCatalogEntry{}, false
}

func providerLabel(id string) string {
	if p, ok := providerCatalogByID(id); ok {
		return p.Label
	}
	return id
}

type providerHealthInfo struct {
	status string
	detail string
}

type providerModelInfo struct {
	id             string
	displayName    string
	supportsEffort bool
	// sizeBytes is Ollama-only (providers\ollama\provider.py's list_models
	// now forwards /api/tags' own size field as metadata.size_bytes) — 0
	// for every cloud provider, whose metadata never carries it. formatSize
	// turns 0 into "" rather than "0 B".
	sizeBytes int64
}

// settingsState is Root's controller for the Settings popup — the same
// shape pickerState and trayState already use: the popup itself, whatever
// is in flight, and enough to turn a reply into a row update.
type settingsState struct {
	popup *Popup

	providersReqID string
	// health is nil until the first ListProviders reply lands (rendered as
	// "checking…"), then one entry per provider the backend has actually
	// REGISTERED — a provider absent from this map has no key yet.
	health map[string]providerHealthInfo

	// models caches each provider's last known ListProviderModels reply,
	// sorted by id. Populated eagerly for ollama (the mockup's "detected ·
	// N models" needs it on first paint) and lazily for everything else, on
	// selection — see onSettingsProviderList/finalizeProviderSelection.
	models      map[string][]providerModelInfo
	modelsReqID map[string]string // provider id -> in-flight corr id

	// finalizing marks a provider whose ListProviderModels reply should,
	// once it lands, actually become the active default — set the moment
	// the user selects a provider whose models are not cached yet (the
	// common case for a cloud provider whose key was JUST validated).
	finalizing map[string]bool

	// expandedProvider is the ONE provider whose per-model rows are
	// currently shown (newProvidersTabItems) — "" shows none. A
	// verification finding: models stayed cached (and so visible) for
	// EVERY provider ever looked at in this popup's lifetime, so switching
	// from Ollama to OpenAI left Ollama's whole model list sitting on
	// screen underneath it too. Single-valued on purpose: selecting a
	// DIFFERENT provider's row overwrites this, which is what collapses
	// the previous one without any separate "collapse" action needing to
	// exist. r.settings.models itself still caches every provider's list
	// it has ever fetched — only which ONE gets rendered expanded changes.
	expandedProvider string

	setKeyReqID    string
	setKeyProvider string
	// credential is whichever CredentialPrompt is (or was) open, so the
	// SetProviderApiKey reply (handled here, in Root, since an open Overlay
	// never receives protocol.EnvelopeMsg) can update ITS status line.
	credential *CredentialPrompt

	// activeModelSupportsEffort is the active default's own model
	// capability, cached at the moment it became the default — read by the
	// Providers tab's Effort row (newProvidersTabItems) to decide whether
	// that row is disabled, per F10 Done-when #8.
	activeModelSupportsEffort bool

	// awaitingConnection is picker.go's own "Bug B" fix, reproduced here —
	// set when settingsFetchProviders could not even be SENT because the
	// backend handshake had not completed yet (the actual first-launch
	// hang; see settingsSend's doc). retrySettingsIfAwaiting, called from
	// Root's ConnectedMsg case, is what gives it a second chance.
	awaitingConnection bool

	// behaviourReqID is F10b's own map, keyed by the Behaviour key a
	// SetBehaviourOverride request was sent for — plural, unlike
	// providersReqID/setKeyReqID, because a bool toggle and a numeric
	// prompt's submit can each be in flight for a DIFFERENT key at once
	// (e.g. toggling "Allow outside project" while a numeric prompt for
	// "Stale after (days)" is still open from a moment ago).
	behaviourReqID map[string]string

	// doctorReqID is the in-flight GetDoctorReport request, if any.
	doctorReqID string

	// demoProjectReqID is F10c's own: the in-flight ProjectInit request
	// fired the moment Demo mode is selected (applyModelChoice), binding
	// the shipped sample project. Single-valued like providersReqID/
	// setKeyReqID, not a map like behaviourReqID — only Demo ever sends
	// this, and only ever one at a time (re-selecting Demo while one is
	// still in flight is not guarded against separately: ProjectInit is
	// idempotent, so a second one landing after the first just confirms
	// the same project id).
	demoProjectReqID string
}

// openSettingsPopup builds and opens the Settings popup, then asks for the
// current provider state — the one construction path for both the
// auto-opened first-run chooser (root.go's WindowSizeMsg case) and the
// settings.open action (actions.go), so the two can never drift into
// looking like different screens.
func (r *Root) openSettingsPopup() tea.Cmd {
	popup := newSettingsPopup(r)
	r.OpenOverlay(popup)
	if r.overlay != popup {
		// Too small even for the empty chooser — OpenOverlay already
		// refused and set the status line naming the actual numbers.
		r.settings = settingsState{}
		return nil
	}
	return r.settingsFetchProviders()
}

func newSettingsPopup(r *Root) *Popup {
	p := NewPopup(PopupConfig{
		Title: "Settings",
		Tabs: []PopupTab{
			{Label: "Providers", Items: newProvidersTabItems(r)},
			{Label: "Appearance", Items: newAppearanceTabItems(r)},
			{Label: "Behaviour", Items: newBehaviourTabItems(r)},
			{Label: "About", Items: newAboutTabItems(r)},
		},
		// 74x16: the palette/Help/picker's established 70x16 (overlay.go's
		// doc: raised twice during verification, landing on
		// layout.OverlayFloor), widened 2 cells each side — a verification
		// finding against the narrower size: the Providers tab's note
		// column (popup.go's rowWidths caps it at 24 cells regardless of
		// overlay width) was never the bottleneck, but the extra room
		// still helps the per-model rows below read comfortably.
		MinW: 74, MinH: 16,
		OnSelect: r.onSettingsSelect,
		Decor:    r.settingsDecor,
		Hint:     r.settingsHint,
		// StickySections: the Appearance tab's Theme/Background/Mouse
		// groups are exactly the long-sectioned-list case F9a's own doc
		// names by example — without this, scrolling past a section's
		// heading and back makes it vanish while its rows stay selectable
		// (popup.go's own StickySections doc). A no-op for the Providers
		// tab, which uses plain indentation instead of sections for its
		// per-model rows (see newProvidersTabItems).
		StickySections: true,
		Actions: []ItemAction{
			{
				Key:   "ctrl+k",
				Label: "set/change key",
				When: func(it *array.Item) bool {
					id, ok := strings.CutPrefix(it.GetID(), "provider:")
					if !ok {
						return false
					}
					cat, known := providerCatalogByID(id)
					return known && cat.NeedsKey
				},
				Run: func(it *array.Item) tea.Cmd {
					id, _ := strings.CutPrefix(it.GetID(), "provider:")
					return r.openCredentialPrompt(id)
				},
			},
			{
				Key:   "ctrl+x",
				Label: "clear override",
				When: func(it *array.Item) bool {
					key, ok := strings.CutPrefix(it.GetID(), "behaviour:")
					if !ok {
						return false
					}
					_, overridden := r.behaviourOverrides[key]
					return overridden
				},
				Run: func(it *array.Item) tea.Cmd {
					key, _ := strings.CutPrefix(it.GetID(), "behaviour:")
					return r.clearBehaviourOverride(key)
				},
			},
		},
	})
	r.settings.popup = p
	return p
}

// ---------------------------------------------------------------------------
// Providers tab — items and decoration
// ---------------------------------------------------------------------------

func newProvidersTabItems(r *Root) []*array.Item {
	items := make([]*array.Item, 0, len(providerCatalog)+1)
	for _, p := range providerCatalog {
		items = append(items, array.NewItem("provider:"+p.ID, p.Label))
		// Per-model rows, indented by two spaces into the title itself
		// rather than SetSection — F10's own mockup draws these as a plain
		// indented sub-list under the provider row, not a labelled section
		// (a "Ollama" section heading above a row that already says
		// "Ollama" would double the label for no reason).
		//
		// Shown ONLY for r.settings.expandedProvider — a verification
		// finding: r.settings.models caches every provider ever looked at
		// for the rest of the popup's lifetime (so re-expanding is instant,
		// no refetch), but rendering ALL of them unconditionally meant
		// Ollama's whole model list stayed on screen under it even after
		// switching to look at OpenAI. Selecting the PROVIDER row itself
		// still picks the lexicographically-first model (applyProviderChoice)
		// as a quick default, and expands it; these rows are what let you
		// pick a SPECIFIC one instead — added after a verification round
		// found no other way existed to choose, say, "qwen2.5-coder:3b"
		// over whichever model happened to sort first.
		if p.ID == r.settings.expandedProvider {
			for _, m := range r.settings.models[p.ID] {
				items = append(items, array.NewItem("model:"+p.ID+":"+m.id, "  "+m.displayName))
			}
		}
	}
	// The Effort row only exists once there IS an active default — nothing
	// to set an effort ON before then, and F10's own rule (About's honesty
	// requirement, generalised) is to never show a control with nothing to
	// act on. Disabled per the ACTIVE MODEL's own capability, recomputed
	// every time the default changes (rebuildSettingsProvidersTab), not
	// baked in once — the F9a render-time lesson applied to a value that
	// changes less often than a clock but is still state, not a fixed fact.
	if r.activeProvider != nil {
		items = append(items, array.NewItem("effort", "Effort: "+effortLabel(r.activeProvider.Effort)).
			SetDisabled(!r.settings.activeModelSupportsEffort))
	}
	return items
}

func effortLabel(e string) string {
	if e == "" {
		return "off"
	}
	return e
}

// nextEffort cycles off -> low -> medium -> high -> off. "" and "off" are
// the same rung — config\state.go's ProviderDefault.Effort stores "" for
// "not set", which resolves identically to an explicit "off" through
// backend\agents\executor.py's resolve_provider_model_effort chain, so
// there is no third state to distinguish here.
func nextEffort(e string) string {
	order := []string{"", "low", "medium", "high"}
	for i, v := range order {
		if v == e {
			return order[(i+1)%len(order)]
		}
	}
	return ""
}

// settingsDecor is the ONE Decor func a Popup holds, dispatching by the
// item id's own prefix to whichever tab's rows it belongs to — Popup has no
// notion of "this Decor is for tab 0 only" (popup.go's PopupConfig.Decor is
// one func for the whole popup), so the dispatch lives here instead.
func (r *Root) settingsDecor(it *array.Item) RowDecor {
	id := it.GetID()
	switch {
	case strings.HasPrefix(id, "model:"):
		rest, _ := strings.CutPrefix(id, "model:")
		pid, mid, ok := strings.Cut(rest, ":")
		if !ok {
			return RowDecor{}
		}
		return r.modelRowDecor(pid, mid)
	case strings.HasPrefix(id, "provider:"):
		pid, _ := strings.CutPrefix(id, "provider:")
		return r.providerRowDecor(pid)
	case strings.HasPrefix(id, "theme:"):
		name, _ := strings.CutPrefix(id, "theme:")
		return r.themeRowDecor(name)
	case strings.HasPrefix(id, "bg:"):
		mode, _ := strings.CutPrefix(id, "bg:")
		return r.backgroundRowDecor(mode)
	case id == "mouse":
		return r.mouseRowDecor()
	case strings.HasPrefix(id, "behaviour:"):
		key, _ := strings.CutPrefix(id, "behaviour:")
		return r.behaviourRowDecor(key)
	}
	return RowDecor{}
}

// settingsHint is PopupConfig.Hint: one dim line above the footer, live —
// see popup.go's own doc on why a func and not a stored string. Honest
// about the one state this popup cannot promise a time for: the backend
// has not answered ListProviders at all yet, so every provider still reads
// "checking…"/"needs an API key" purely by default, not because anything
// has actually been ruled out. A verification round asked for exactly
// this, after a fresh profile's first health check took minutes once with
// no way to tell "about to resolve" from "never going to" apart — it
// disappears the moment r.settings.health is populated, even partially.
func (r *Root) settingsHint() string {
	// The Queue depth row's own "⚠ · inert" decor (behaviour.go) flags
	// that it is not yet reachable, but a glyph and a note-column suffix
	// cannot carry the WHY — this is the one line that does, the moment
	// that specific row is actually selected. Checked first and
	// unconditionally: it has nothing to do with whether providers have
	// answered yet, and must not be hidden behind that unrelated wait.
	if p := r.settings.popup; p != nil {
		if it, ok := p.menu.Selected(); ok && behaviourInertKeys[strings.TrimPrefix(it.GetID(), "behaviour:")] {
			return "not yet enforced for plain chat sends (see F10b's Decision 4)"
		}
	}
	if r.settings.providersReqID == "" && r.settings.health == nil {
		// Never asked (e.g. no backend) — nothing to explain a wait for.
		return ""
	}
	if r.settings.health == nil {
		return "waiting on the backend's first answer — can take longer on a brand-new profile"
	}
	return ""
}

// providerRowDecor is computed fresh every render (RowDecor's whole reason
// to exist, per F9a's render-time lesson) from r.settings.health/models and
// r.activeProvider — none of which are baked into the item itself.
//
// Note text is kept SHORT deliberately — popup.go's rowWidths caps the note
// column at 24 cells regardless of how wide the overlay is, and a
// verification round found "detected · 3 models · local" (28 cells) always
// truncated, at ANY overlay width, with the model count besides: it is
// redundant now that each model gets its own row (newProvidersTabItems), so
// it is simply gone rather than shortened and left one more model away from
// truncating again — the exact lesson F7's own description column already
// paid for once (popup.go's renderRow doc).
func (r *Root) providerRowDecor(id string) RowDecor {
	h, known := r.settings.health[id]
	isActive := r.activeProvider != nil && r.activeProvider.Provider == id

	var icon string
	var role theme.Role
	var note string

	switch {
	case !known:
		icon, role = "○", theme.TextDim
		note = "needs an API key"
		if id == "ollama" {
			note = "checking…"
		}
	case h.status == "healthy":
		icon, role = "●", theme.Done
		note = "configured"
		if id == "ollama" {
			note = "detected"
		}
	default:
		icon, role = "●", theme.Warning
		// h.detail is the BACKEND's own text (ProviderHealth.detail,
		// arbitrary length) — truncated to a fixed budget here rather than
		// left to Popup's own ellipsis truncation, so this note's own
		// worst case is provably bounded instead of merely usually short.
		// "configured · " is 13 cells; 13 + 6 + 1(ellipsis) + the " ★"
		// active suffix (2) = 22, under rowWidths' 24-cell ceiling with a
		// margin to spare.
		note = "configured · " + truncateToRunes(h.detail, 6)
		if id == "ollama" {
			note = "not running"
		}
	}
	if isActive {
		role = theme.AccentPrimary
		// A short suffix, not " · active" (9 cells) — the unhealthy-detail
		// branch above already spends most of the 24-cell budget on the
		// backend's own text, and the icon's colour (AccentPrimary) already
		// carries most of "this one is active" on its own.
		note += " ★"
	}
	// A ListProviderModels fetch for THIS provider is in flight — said
	// plainly, overriding whatever the note above would otherwise show,
	// rather than leaving the row looking unchanged while a real network
	// round trip is happening. A verification round asked directly whether
	// "switching providers feels slow" was a real backend delay or a
	// missing indicator; ListProviderModels genuinely is a network call
	// (Ollama's own /api/tags, or a cloud provider's real /models
	// endpoint) and was never going to be instant — this is the fix for
	// the MISSING-INDICATOR half of that question.
	if r.settings.modelsReqID[id] != "" {
		note = "loading models…"
	}
	return RowDecor{Icon: icon, IconRole: role, Note: note}
}

// truncateToRunes cuts s to at most max runes, appending "…" when it does —
// providerRowDecor's own bound on backend-supplied text, independent of
// Popup's own column-width truncation (popup.go's renderRow), which this
// exists to never have to rely on.
func truncateToRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// modelRowDecor marks whichever model is the active default for its
// provider and shows its size on disk when the backend reported one
// (Ollama only — providers\ollama\provider.py's list_models forwards
// /api/tags' own size field; every cloud provider's metadata is empty).
func (r *Root) modelRowDecor(providerID, modelID string) RowDecor {
	isActive := r.activeProvider != nil &&
		r.activeProvider.Provider == providerID && r.activeProvider.Model == modelID

	var sizeBytes int64
	for _, m := range r.settings.models[providerID] {
		if m.id == modelID {
			sizeBytes = m.sizeBytes
			break
		}
	}

	if isActive {
		note := "active"
		if s := formatSize(sizeBytes); s != "" {
			note = s + " · active"
		}
		return RowDecor{Icon: "✓", IconRole: theme.AccentPrimary, Note: note}
	}
	return RowDecor{Note: formatSize(sizeBytes)}
}

// formatSize renders bytes as a human-scaled size ("4.6 GB"), or "" for 0 —
// the honest answer for every cloud provider, whose metadata never carries
// one, rather than a fabricated "0 B".
func formatSize(bytes int64) string {
	if bytes <= 0 {
		return ""
	}
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// ---------------------------------------------------------------------------
// Selection
// ---------------------------------------------------------------------------

func (r *Root) onSettingsSelect(it *array.Item) (tea.Cmd, bool) {
	id := it.GetID()
	switch {
	case strings.HasPrefix(id, "model:"):
		rest, _ := strings.CutPrefix(id, "model:")
		pid, mid, ok := strings.Cut(rest, ":")
		if !ok {
			return nil, false
		}
		return r.selectSpecificModel(pid, mid), false
	case strings.HasPrefix(id, "provider:"):
		pid, _ := strings.CutPrefix(id, "provider:")
		return r.selectProvider(pid), false
	case strings.HasPrefix(id, "theme:"):
		name, _ := strings.CutPrefix(id, "theme:")
		return r.selectTheme(name), false
	case strings.HasPrefix(id, "bg:"):
		mode, _ := strings.CutPrefix(id, "bg:")
		return r.selectBackground(mode), false
	case id == "mouse":
		return r.toggleMouse(), false
	case id == "effort":
		return r.cycleEffort(), false
	case strings.HasPrefix(id, "behaviour:"):
		key, _ := strings.CutPrefix(id, "behaviour:")
		return r.onBehaviourSelect(key), false
	case id == "about:doctor":
		return r.onAboutSelect(), false
	}
	return nil, false
}

// selectProvider is Enter on a provider row — "Detect silently, select
// explicitly" (F10): nothing here is ever reached by detection alone, only
// by the user choosing this exact row.
func (r *Root) selectProvider(id string) tea.Cmd {
	cat, ok := providerCatalogByID(id)
	if !ok {
		return nil
	}
	h, known := r.settings.health[id]
	usable := known && h.status == "healthy"
	if !usable {
		if cat.NeedsKey {
			return r.openCredentialPrompt(id)
		}
		// Ollama, registered but not reachable right now (the daemon is
		// not running) — nothing to select until it is.
		r.status = cat.Label + " is not reachable right now"
		return nil
	}
	// Expand THIS provider's models and nothing else's — see
	// settingsState.expandedProvider's own doc for why a single field is
	// what collapses whichever one was expanded before.
	r.settings.expandedProvider = id
	r.rebuildSettingsProvidersTab()

	// Already the active default: Enter here means "show me its models",
	// not "pick one for me". Falling through to finalizeProviderSelection
	// unconditionally would re-run applyProviderChoice's quick pick
	// (always the alphabetically-first model, by its own doc) and PERSIST
	// that as a new choice — silently reverting whatever SPECIFIC model
	// selectSpecificModel had set, the moment the user re-opens Settings
	// and expands the row to check what is active. Found via a live
	// report of exactly this symptom (model reverted to
	// alphabetically-first after restart) and confirmed NOT cosmetic: the
	// revert happens in r.activeProvider itself and is emitted as a real
	// ProviderDefaultSetMsg, which chat.Workspace persists to state.json
	// — the bug was never in restart/replay, it was in this row's own
	// Enter handler treating "look" and "choose" as the same action.
	//
	// Still fetches if the models genuinely are not cached yet (a race
	// with onSettingsProviderList's own eager fetch for the active
	// default), but WITHOUT finalizing — so a fetch landing afterward
	// shows the list without also overwriting the already-correct choice.
	if r.activeProvider != nil && r.activeProvider.Provider == id {
		if _, cached := r.settings.models[id]; !cached {
			return r.settingsFetchModels(id)
		}
		return nil
	}
	return r.finalizeProviderSelection(id)
}

// finalizeProviderSelection applies the cached model list if there is one,
// or fetches it first and applies it once it lands (onSettingsModelList).
func (r *Root) finalizeProviderSelection(id string) tea.Cmd {
	if models, ok := r.settings.models[id]; ok {
		return r.applyProviderChoice(id, models)
	}
	if r.settings.finalizing == nil {
		r.settings.finalizing = map[string]bool{}
	}
	r.settings.finalizing[id] = true
	r.status = "fetching " + providerLabel(id) + "'s models…"
	cmd := r.settingsFetchModels(id)
	// Rebuilt here too, defensively, so this function is self-contained
	// regardless of what a future caller does — but this is NOT what makes
	// "loading models…" visible. RowDecor is re-evaluated fresh on every
	// single render, with no dependency on when (or whether) the item LIST
	// was last rebuilt — only the SET of rows (newProvidersTabItems) needs
	// a rebuild to change; a NOTE a row already has needs none. By the time
	// settingsFetchModels returns, r.settings.modelsReqID[id] is already
	// set (the request is sent synchronously, inline), so the very next
	// frame Bubble Tea draws — which happens automatically after this
	// Update call returns, before any reply can possibly have arrived —
	// already reads it correctly. A verification round asked whether a
	// provider's models appearing "instantly, no loading indicator" on a
	// TRUE first-ever fetch was this mechanism failing, or just Ollama's
	// own localhost round trip being fast enough that the one frame the
	// indicator is genuinely owed resolves faster than a human can see it.
	// Checked by tracing the call chain rather than guessed: the mechanism
	// is correct by construction, independent of this rebuild call — so if
	// it reproduces on a CLOUD provider's genuine first fetch (a real
	// network round trip, not localhost), that is a different, not-yet-
	// found bug, not this one.
	r.rebuildSettingsProvidersTab()
	return cmd
}

// selectSpecificModel is Enter on one of a provider's own per-model rows —
// the exact choice applyProviderChoice's "lexicographically-first" default
// cannot make for you. Looked up from the cached list rather than trusted
// from the item id alone, so a model that has since disappeared from a
// refreshed list (Ollama model removed, re-pulled under router control)
// cannot be set as a default this process never actually confirmed exists.
func (r *Root) selectSpecificModel(providerID, modelID string) tea.Cmd {
	for _, m := range r.settings.models[providerID] {
		if m.id == modelID {
			return r.applyModelChoice(providerID, m)
		}
	}
	r.status = "that model is no longer listed for " + providerLabel(providerID)
	return nil
}

func (r *Root) applyModelChoice(id string, model providerModelInfo) tea.Cmd {
	def := config.ProviderDefault{Provider: id, Model: model.id}
	r.activeProvider = &def
	r.settings.activeModelSupportsEffort = model.supportsEffort
	r.rebuildSettingsProvidersTab()
	r.status = fmt.Sprintf("%s (%s) is now the active provider", providerLabel(id), model.id)
	cmds := []tea.Cmd{func() tea.Msg { return ProviderDefaultSetMsg{Default: def} }}
	// F10c: becoming active does not wait on this — the badge and
	// r.activeProvider update above with no perceptible delay regardless
	// of how long ProjectInit's round trip takes. See onDemoProjectInit
	// for the reply half, and the task doc's Decision 1 for why the
	// sequencing is split this way.
	if id == "demo" {
		if cmd := r.initDemoProject(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// initDemoProject fires ProjectInit against the shipped sample project —
// F10c's Decision 1/Question 2: the path is a join onto env.BackendDir,
// which protocol.FindBackendDir() already resolved at startup (spawn.go),
// not a second, independently-guessed relative path. ProjectManager.init()
// is idempotent (project_manager.py:70-96 — "if the folder is already
// initialized, this is just resolve() plus a touch"), so firing this every
// time Demo is (re-)selected is deliberately cheap.
func (r *Root) initDemoProject() tea.Cmd {
	if r.env.BackendDir == "" {
		return nil // no backend found at all — nothing to bind against
	}
	path := filepath.Join(r.env.BackendDir, "demo", "sample_project")
	reqID, ok := r.SettingsSendFn(protocol.CmdProjectInit, map[string]any{"path": path})
	if !ok {
		return nil
	}
	r.settings.demoProjectReqID = reqID
	return nil
}

// onDemoProjectInit is the ProjectInit reply's handler (handleSettingsEnvelope).
func (r *Root) onDemoProjectInit(env protocol.Envelope) tea.Cmd {
	if env.Type != protocol.EvProject {
		return nil // refused — Demo stays selected, just without a binding
	}
	var p struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	if err := env.UnmarshalPayload(&p); err != nil || p.Project.ID == "" {
		return nil
	}
	// The user may have switched away from Demo before this landed —
	// Decision 1's own guard against patching a default that is no
	// longer the active one.
	if r.activeProvider == nil || r.activeProvider.Provider != "demo" {
		return nil
	}
	def := *r.activeProvider
	def.ProjectID = p.Project.ID
	r.activeProvider = &def
	return func() tea.Msg { return ProviderDefaultSetMsg{Default: def} }
}

// applyProviderChoice picks the lexicographically-first model — a
// deterministic, if arbitrary, quick default for Enter on the PROVIDER row
// itself. Precise choice among a provider's installed models is
// selectSpecificModel's own per-model row, immediately above/below this one
// in the same list once the models have been fetched.
func (r *Root) applyProviderChoice(id string, models []providerModelInfo) tea.Cmd {
	if len(models) == 0 {
		r.status = providerLabel(id) + " has no models available right now"
		return nil
	}
	return r.applyModelChoice(id, models[0])
}

func (r *Root) cycleEffort() tea.Cmd {
	if r.activeProvider == nil || !r.settings.activeModelSupportsEffort {
		return nil // the row is disabled in this case — defensive, not reachable via Enter
	}
	def := *r.activeProvider
	def.Effort = nextEffort(def.Effort)
	r.activeProvider = &def
	r.rebuildSettingsProvidersTab()
	return func() tea.Msg { return ProviderDefaultSetMsg{Default: def} }
}

// rebuildSettingsProvidersTab refreshes the Providers tab's item list —
// needed only when the SET of rows changes (the Effort row appearing, or
// its disabled flag flipping); every other dynamic bit lives in Decor and
// needs no rebuild at all.
//
// Popup.RefreshItems only ever targets the ACTIVE tab (p.cfg.Tabs[p.active]
// — popup.go) — there is no "refresh tab N regardless of which is showing"
// method, because every consumer before this one only ever had rows that
// could change while THEY were on screen. Settings is the first with two
// tabs where either can be open while the OTHER one's data changes in the
// background (a ListProviderModels reply landing while the user is looking
// at Appearance) — so a plain RefreshItems call here would silently
// overwrite whichever tab happens to be active with the wrong rows. The
// guard below is why this reaches into Popup's own (unexported, same-
// package) field instead: there is nothing unsafe about replacing an
// INACTIVE tab's item slice directly, since array.Menu only ever reflects
// p.cfg.Tabs[p.active] — switchTab (popup.go) re-reads it fresh the next
// time the user tabs over.
func (r *Root) rebuildSettingsProvidersTab() {
	p := r.settings.popup
	if p == nil {
		return
	}
	items := newProvidersTabItems(r)
	if p.active == providersTabIndex {
		p.RefreshItems(items)
		return
	}
	p.cfg.Tabs[providersTabIndex].Items = items
}

// ---------------------------------------------------------------------------
// Credential entry
// ---------------------------------------------------------------------------

func (r *Root) openCredentialPrompt(id string) tea.Cmd {
	cp := newCredentialPrompt(id, providerLabel(id), r.settings.popup)
	r.settings.credential = cp
	// Set here, not just on success: if the user cancels partway through
	// (esc backs out "without setting a key — allowed", F10's own words)
	// and later actually does validate a key for this SAME provider, its
	// models should expand without a second, separate action deciding so.
	// Collapses whatever was expanded before, same as selectProvider.
	r.settings.expandedProvider = id
	return func() tea.Msg { return openOverlayMsg{cp} }
}

// settingsSubmitKey is submitCredentialMsg's handler (root.go) — the
// CredentialPrompt itself cannot reach protocol.Client.
func (r *Root) settingsSubmitKey(provider, key string) tea.Cmd {
	reqID, ok := r.SettingsSendFn(protocol.CmdSetProviderAPIKey,
		map[string]any{"provider": provider, "api_key": key})
	if !ok {
		if cp := r.settings.credential; cp != nil {
			cp.applyResult(false, "✗ could not reach the backend")
		}
		return nil
	}
	r.settings.setKeyReqID = reqID
	r.settings.setKeyProvider = provider
	return nil
}

// ---------------------------------------------------------------------------
// Outgoing requests
// ---------------------------------------------------------------------------

// settingsSend mirrors pickerSend (picker.go) exactly — same reason, same
// shape: a one-shot query with Root's own correlation id, independent of
// any workspace's pending map.
func (r *Root) settingsSend(cmdType string, payload map[string]any) (string, bool) {
	if r.client == nil {
		return "", false
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	env := protocol.Envelope{ID: rootRequestID(), Type: cmdType, Payload: raw}
	if _, err := r.client.Request(env, 0); err != nil {
		// THE actual first-launch hang, found by hard evidence rather than a
		// fourth guess: Request returns this exact error — "backend is not
		// connected" — synchronously and WITHOUT QUEUING, whenever c.backend
		// is still nil (protocol\client.go's Request, Start/supervise — the
		// handshake has not completed, which on a brand-new profile has to
		// wait out the FULL migration chain first). The auto-opened
		// first-run chooser (root.go's WindowSizeMsg case) fires from the
		// terminal's own first resize, which arrives from the OS essentially
		// immediately — almost certainly before that handshake on a cold
		// profile, rarely on a warm one (migrations are then a no-op), which
		// is exactly the "fresh profile only, every later launch instant"
		// pattern three earlier theories (a slow call, an uncaught
		// exception, DNS/IPv6 resolution) each failed to explain: this
		// request was never SENT at all, so nothing about how long Ollama
		// itself takes to answer was ever the question. Esc + reopening
		// Settings "resolved instantly" because by THEN the connection was
		// already up and this exact call finally went out.
		//
		// picker.go hit the identical shape once already (its own "Bug B"):
		// an action attempted before the handshake completes, remembered so
		// ConnectedMsg can retry it once the connection is actually usable.
		// This is that same fix, not a new one.
		r.settings.awaitingConnection = true
		return "", false
	}
	return env.ID, true
}

// retrySettingsIfAwaiting re-issues the Providers fetch that could not even
// be SENT at first-run auto-open time — see settingsSend's own doc. Called
// from Root's ConnectedMsg case, the same moment retryPickerIfAwaiting
// already does for the identical reason.
func (r *Root) retrySettingsIfAwaiting() tea.Cmd {
	if r.settings.popup == nil || !r.settings.awaitingConnection {
		return nil
	}
	r.settings.awaitingConnection = false
	return r.settingsFetchProviders()
}

func (r *Root) settingsFetchProviders() tea.Cmd {
	reqID, ok := r.SettingsSendFn(protocol.CmdListProviders, map[string]any{})
	if ok {
		r.settings.providersReqID = reqID
	}
	return nil
}

func (r *Root) settingsFetchModels(id string) tea.Cmd {
	reqID, ok := r.SettingsSendFn(protocol.CmdListProviderModels, map[string]any{"provider": id})
	if !ok {
		return nil
	}
	if r.settings.modelsReqID == nil {
		r.settings.modelsReqID = map[string]string{}
	}
	r.settings.modelsReqID[id] = reqID
	return nil
}

// ---------------------------------------------------------------------------
// Incoming replies
// ---------------------------------------------------------------------------

// handleSettingsEnvelope claims Root's own Settings replies by corr_id —
// the same shape handlePickerEnvelope uses, including picker.go's own Bug A
// guard: env.CorrID == "" is refused BEFORE anything else, unconditionally,
// because a cleared reqID field is a valid map/switch value and the
// backend's 5-second idle heartbeat arrives with corr_id null (decoded here
// as "") forever after. Without this a heartbeat landing after every
// request had already cleared its id would match the first zero-value
// comparison it found.
func (r *Root) handleSettingsEnvelope(env protocol.Envelope) tea.Cmd {
	if env.CorrID == "" {
		return nil
	}
	if env.CorrID == r.settings.providersReqID {
		r.settings.providersReqID = ""
		return r.onSettingsProviderList(env)
	}
	for id, reqID := range r.settings.modelsReqID {
		if env.CorrID == reqID {
			delete(r.settings.modelsReqID, id)
			return r.onSettingsModelList(id, env)
		}
	}
	if env.CorrID == r.settings.setKeyReqID {
		r.settings.setKeyReqID = ""
		return r.onSettingsKeySet(env)
	}
	for key, reqID := range r.settings.behaviourReqID {
		if env.CorrID == reqID {
			delete(r.settings.behaviourReqID, key)
			return r.onBehaviourOverrideSet(key, env)
		}
	}
	if env.CorrID == r.settings.doctorReqID {
		r.settings.doctorReqID = ""
		return r.onDoctorReport(env)
	}
	if env.CorrID == r.settings.demoProjectReqID {
		r.settings.demoProjectReqID = ""
		return r.onDemoProjectInit(env)
	}
	return nil
}

func (r *Root) onSettingsProviderList(env protocol.Envelope) tea.Cmd {
	if env.Type != protocol.EvProviderList {
		return nil // a refused ListProviders leaves health exactly as it was
	}
	var p struct {
		Providers []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"providers"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		return nil
	}
	health := make(map[string]providerHealthInfo, len(p.Providers))
	for _, entry := range p.Providers {
		health[entry.Name] = providerHealthInfo{status: entry.Status, detail: entry.Detail}
	}
	r.settings.health = health
	r.rebuildSettingsProvidersTab()

	// Ollama's model list is shown inline (the mockup's "detected · N
	// models") -- worth the round trip on every open since it is the one
	// provider guaranteed registered on a fresh profile. Everything else
	// fetches lazily, only on selection — see finalizeProviderSelection.
	if h, ok := health["ollama"]; ok && h.status == "healthy" {
		if _, cached := r.settings.models["ollama"]; !cached {
			return r.settingsFetchModels("ollama")
		}
	}
	// The ACTIVE default's own model list, if it is not ollama (already
	// covered above) and nothing has fetched it THIS session yet. Without
	// this, a default restored from state.json at startup (a previous
	// session's choice, not a fresh selection — applyProviderChoice is the
	// only place that sets activeModelSupportsEffort) would leave the
	// Providers tab's Effort row stuck disabled until the user reselected
	// the same provider, even on a model that genuinely supports effort.
	if ap := r.activeProvider; ap != nil && ap.Provider != "ollama" {
		if h, ok := health[ap.Provider]; ok && h.status == "healthy" {
			if _, cached := r.settings.models[ap.Provider]; !cached {
				return r.settingsFetchModels(ap.Provider)
			}
		}
	}
	return nil
}

func (r *Root) onSettingsModelList(id string, env protocol.Envelope) tea.Cmd {
	if env.Type != protocol.EvProviderModelList {
		if r.settings.finalizing[id] {
			delete(r.settings.finalizing, id)
			r.status = "could not list " + providerLabel(id) + "'s models"
		}
		return nil
	}
	var p struct {
		Models []struct {
			ID           string `json:"id"`
			DisplayName  string `json:"display_name"`
			Capabilities struct {
				SupportsEffort bool `json:"supports_effort"`
			} `json:"capabilities"`
			// Metadata.SizeBytes is Ollama-only — providers\ollama\
			// provider.py's list_models forwards /api/tags' own size
			// field here; every other provider's metadata is {}, which
			// decodes to the zero value (0) cleanly.
			Metadata struct {
				SizeBytes int64 `json:"size_bytes"`
			} `json:"metadata"`
		} `json:"models"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		return nil
	}
	models := make([]providerModelInfo, 0, len(p.Models))
	for _, m := range p.Models {
		models = append(models, providerModelInfo{
			id: m.ID, displayName: m.DisplayName, supportsEffort: m.Capabilities.SupportsEffort,
			sizeBytes: m.Metadata.SizeBytes,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].id < models[j].id })
	if r.settings.models == nil {
		r.settings.models = map[string][]providerModelInfo{}
	}
	r.settings.models[id] = models
	r.rebuildSettingsProvidersTab()

	if r.settings.finalizing[id] {
		delete(r.settings.finalizing, id)
		return r.applyProviderChoice(id, models)
	}
	// Not a fresh selection — still worth syncing activeModelSupportsEffort
	// if this reply happens to be for the provider ALREADY active (a
	// default restored from state.json, or ollama's own eager fetch landing
	// after Ollama was already the default from a previous session). See
	// onSettingsProviderList's own comment on why this gap exists at all.
	if ap := r.activeProvider; ap != nil && ap.Provider == id {
		for _, m := range models {
			if m.id == ap.Model {
				r.settings.activeModelSupportsEffort = m.supportsEffort
				r.rebuildSettingsProvidersTab()
				break
			}
		}
	}
	return nil
}

func (r *Root) onSettingsKeySet(env protocol.Envelope) tea.Cmd {
	provider := r.settings.setKeyProvider
	r.settings.setKeyProvider = ""
	cp := r.settings.credential

	if env.Type == protocol.EvError {
		var p struct {
			Error string `json:"error"`
		}
		_ = env.UnmarshalPayload(&p)
		msg := p.Error
		if msg == "" {
			msg = "key rejected"
		}
		if cp != nil {
			cp.applyResult(false, "✗ "+msg)
		}
		return nil // stays on the credential prompt — esc backs out, or try again
	}

	// ProviderApiKeySet: the key is now durable (B25) and registered for
	// this process. Refresh health so the Providers tab stops showing
	// "needs an API key", then finalize — the whole reason someone just
	// typed a key was to make this provider the active default.
	if cp != nil {
		cp.applyResult(true, "✓ valid")
	}
	if r.settings.health == nil {
		r.settings.health = map[string]providerHealthInfo{}
	}
	r.settings.health[provider] = providerHealthInfo{status: "healthy"}
	if r.settings.finalizing == nil {
		r.settings.finalizing = map[string]bool{}
	}
	r.settings.finalizing[provider] = true
	r.rebuildSettingsProvidersTab()
	r.status = providerLabel(provider) + " key saved — fetching its models…"

	popup := r.settings.popup
	return tea.Batch(
		r.settingsFetchModels(provider),
		func() tea.Msg { return openOverlayMsg{popup} },
	)
}

// ---------------------------------------------------------------------------
// Messages chat.Workspace consumes (the sole state.json writer — F10)
// ---------------------------------------------------------------------------

// ProviderDefaultSetMsg carries a complete new default to chat, which
// persists it and uses it for every CreateSession from here on — see
// chat\providers.go's applyProviderDefault and config\state.go's own doc on
// why Root does not write state.json itself.
type ProviderDefaultSetMsg struct{ Default config.ProviderDefault }

// UIOverrideSetMsg carries a PATCH, not a replacement: only the field being
// changed is set, the rest left at their zero value, and
// chat\providers.go's applyUIOverride merges it into whatever state.json
// already holds. A full-replacement message would have the theme and mouse
// toggles clobber each other's half of the override every time either one
// fires alone.
type UIOverrideSetMsg struct{ Override config.UIOverride }
