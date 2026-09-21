package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// Diagnostics is everything the placeholder renders. It is passed IN, never
// reached for: the placeholder holds no reference to the root, the client, or
// any global, which is the rule every later component has to follow too.
type Diagnostics struct {
	// Seq is root.go's pushDiagnostics doc comment: a monotonic counter a
	// receiver compares against the last one it applied, because arrival
	// order is not guaranteed once two pushes are in flight as separate
	// tea.Cmd goroutines.
	Seq int

	ConnState       protocol.ConnState
	ProtocolVersion int
	ClientVersion   int
	Capabilities    protocol.Capabilities
	Handshaked      bool
	Restarts        int
	Banner          string
	BannerDetail    string
	BackendDir      string
	LogFile         string

	InFlight    []string
	Unknown     []string
	Interrupted []string

	// Tasks is F6's global snapshot — see tasks.go's doc comment on why Root
	// fetches it rather than the workspace. Nil until the first ListTasks
	// reply lands (the pane has to be visible to trigger one at all).
	Tasks []TaskRow

	ProtocolErrors []string

	ThemeName   string
	Background  string
	NoColor     bool
	ActionCount int

	PasteEvents int
	PasteLines  int

	ConfigPath  string
	ConfigError string
	StatePath   string
	StateError  string

	Notes []string
}

// DiagnosticsMsg is how the root hands a workspace a fresh snapshot. The
// root emits it; a workspace consumes what it needs and ignores the rest.
// Exported (F6): chat.Workspace reads Tasks and ConnState off it too, which
// means it has to be nameable outside package app — Placeholder no longer
// has the only case for it.
type DiagnosticsMsg struct{ D Diagnostics }

// Placeholder is F1's stub workspace.
//
// F1 says to expect an empty-looking screen, and the chat viewport, input and
// cards are F3–F6. What this renders instead is the connection: state,
// handshake, capabilities, restarts and task lifecycle. Without it neither
// episode's gate is checkable — F2's verification asks for a header showing
// connected, a banner on crash, and a partial task marked interrupted, none of
// which has a renderer until F3.
type Placeholder struct {
	id    WorkspaceID
	theme theme.Theme
	d     Diagnostics
}

func NewPlaceholder(id WorkspaceID, th theme.Theme) *Placeholder {
	return &Placeholder{id: id, theme: th}
}

func (p *Placeholder) ID() WorkspaceID { return p.id }
func (p *Placeholder) Init() tea.Cmd   { return nil }

func (p *Placeholder) Update(msg tea.Msg) (Workspace, tea.Cmd) {
	// Seq guard: see chat/protocol.go's DiagnosticsMsg case for why arrival
	// order cannot be trusted — the same race applies here, since this is
	// the other consumer of the same message.
	if m, ok := msg.(DiagnosticsMsg); ok && m.D.Seq > p.d.Seq {
		p.d = m.D
	}
	return p, nil
}

// Suspend keeps nothing: there is no position, draft or open file yet. The
// method exists because the lifecycle is the interface's, not because F1 has
// state to persist — that is F12's.
func (p *Placeholder) Suspend() ([]byte, error) { return nil, nil }
func (p *Placeholder) Resume([]byte) error      { return nil }

// Cards is the Chat right pane from LAYOUT.md: Status short, Task medium,
// Sessions taking the remainder.
func (p *Placeholder) Cards() []layout.CardSlot {
	return []layout.CardSlot{
		{MinHeight: 5},
		{MinHeight: 8},
		{MinHeight: 6, Flex: true},
	}
}

func (p *Placeholder) SetTheme(th theme.Theme) { p.theme = th }

func (p *Placeholder) View(l layout.Layout) string {
	th := p.theme

	left := stack(
		box(th, l.Main, "View Port", p.viewport(l.Main), true),
		box(th, l.Secondary, "Query Area", p.query(l.Secondary), false),
	)

	if !l.ShowRightPane || len(l.RightPane) == 0 {
		return left
	}

	titles := []string{"Status", "Task", "Sessions"}
	bodies := []func(layout.Rect) string{p.statusCard, p.taskCard, p.sessionsCard}

	cards := make([]string, 0, len(l.RightPane))
	for i, r := range l.RightPane {
		if r.IsZero() || i >= len(titles) {
			continue
		}
		cards = append(cards, box(th, r, titles[i], bodies[i](r), false))
	}

	return sideBySide(left, l.Main.W, stack(cards...), l.RightPaneWidth, layout.PaneGapX)
}

// ---------------------------------------------------------------------------
// Panels
// ---------------------------------------------------------------------------

func (p *Placeholder) viewport(r layout.Rect) string {
	th := p.theme
	dim := th.Style(theme.TextDim)
	txt := th.Style(theme.TextPrimary)

	var lines []string
	add := func(s string) { lines = append(lines, s) }

	add(th.Style(theme.AccentPrimary).Render("F1 + F2 — foundations only"))
	add(dim.Render("No transcript, no input, no cards. F3–F6 build those."))
	add("")

	add(txt.Render("Backend"))
	add("  " + p.connLine())
	if p.d.Handshaked {
		add(dim.Render(fmt.Sprintf("  envelope v%d (client speaks v%d)",
			p.d.ProtocolVersion, p.d.ClientVersion)))
		caps := p.d.Capabilities
		providers := strings.Join(caps.Providers, ", ")
		if providers == "" {
			providers = dim.Render("none registered")
		}
		add(dim.Render("  providers    ") + txt.Render(providers))
		add(dim.Render(fmt.Sprintf("  schema v%d · streaming %v · cancel %v",
			caps.SchemaVersion, caps.Streaming, caps.Cancel)))
	}
	add(dim.Render("  restarts     ") + txt.Render(fmt.Sprint(p.d.Restarts)))
	if p.d.BackendDir != "" {
		add(dim.Render("  backend dir  ") + dim.Render(p.d.BackendDir))
	}
	if p.d.LogFile != "" {
		add(dim.Render("  stderr log   ") + dim.Render(p.d.LogFile))
	}

	if p.d.Banner != "" {
		add("")
		add(th.Style(theme.Error).Render("  " + p.d.Banner))
		if p.d.BannerDetail != "" {
			for _, l := range wrapPlain(p.d.BannerDetail, r.W-6) {
				add(th.Style(theme.Warning).Render("  " + l))
			}
		}
	}

	if len(p.d.ProtocolErrors) > 0 {
		add("")
		add(th.Style(theme.Error).Render("Protocol errors"))
		for _, e := range p.d.ProtocolErrors {
			add(th.Style(theme.Warning).Render("  " + e))
		}
	}

	add("")
	add(txt.Render("Shell"))
	add(dim.Render("  theme        ") + txt.Render(p.d.ThemeName) +
		dim.Render("  background ") + txt.Render(p.d.Background))
	if p.d.NoColor {
		add(dim.Render("  NO_COLOR is set — plain text, borders intact"))
	}
	add(dim.Render("  actions      ") + txt.Render(fmt.Sprintf("%d registered", p.d.ActionCount)))
	add(dim.Render("  this rect    ") + txt.Render(fmt.Sprintf("%d×%d", r.W, r.H)))
	add(dim.Render("  pastes       ") + txt.Render(
		fmt.Sprintf("%d event(s), %d line(s)", p.d.PasteEvents, p.d.PasteLines)))

	if p.d.ConfigError != "" {
		add(th.Style(theme.Warning).Render("  config.toml ignored: " + p.d.ConfigError))
	}
	if p.d.StateError != "" {
		add(th.Style(theme.Warning).Render("  state.json ignored: " + p.d.StateError))
	}

	for _, n := range p.d.Notes {
		add(dim.Render("  " + n))
	}

	return clip(lines, r.H-2)
}

func (p *Placeholder) connLine() string {
	th := p.theme
	switch p.d.ConnState {
	case protocol.StateConnected:
		return th.Style(theme.Done).Render("● connected")
	case protocol.StateStarting:
		return th.Style(theme.Running).Render("◐ starting")
	case protocol.StateLost:
		return th.Style(theme.Warning).Render("◌ lost — restarting")
	case protocol.StateFailed:
		return th.Style(theme.Error).Render("✕ unavailable")
	default:
		return th.Style(theme.TextDim).Render("· idle")
	}
}

func (p *Placeholder) query(r layout.Rect) string {
	return clip([]string{
		p.theme.Style(theme.TextDim).Render("query input arrives in F4"),
	}, r.H-2)
}

func (p *Placeholder) statusCard(r layout.Rect) string {
	th := p.theme
	dim := th.Style(theme.TextDim)
	lines := []string{
		p.connLine(),
		dim.Render(fmt.Sprintf("restarts %d", p.d.Restarts)),
		dim.Render(fmt.Sprintf("in flight %d", len(p.d.InFlight))),
	}
	return clip(lines, r.H-2)
}

func (p *Placeholder) taskCard(r layout.Rect) string {
	th := p.theme
	var lines []string

	for _, id := range p.d.InFlight {
		lines = append(lines, th.Style(theme.Running).Render("▶ "+short(id))+
			th.Style(theme.TextDim).Render(" running"))
	}
	for _, id := range p.d.Unknown {
		lines = append(lines, th.Style(theme.Warning).Render("? "+short(id))+
			th.Style(theme.TextDim).Render(" unknown"))
	}
	for _, id := range p.d.Interrupted {
		lines = append(lines, th.Style(theme.Error).Render("✕ "+short(id))+
			th.Style(theme.TextDim).Render(" interrupted"))
	}
	if len(lines) == 0 {
		lines = []string{th.Style(theme.TextDim).Render("no tasks")}
	}
	return clip(lines, r.H-2)
}

func (p *Placeholder) sessionsCard(r layout.Rect) string {
	return clip([]string{
		p.theme.Style(theme.TextDim).Render("sessions arrive in F3"),
	}, r.H-2)
}

// ---------------------------------------------------------------------------

func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func clip(lines []string, rows int) string {
	if rows <= 0 {
		return ""
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return strings.Join(lines, "\n")
}

// wrapPlain wraps unstyled text at a display width. Styled text is never
// wrapped here — the escape sequences would be split mid-word.
func wrapPlain(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var (
		out  []string
		line strings.Builder
		used int
	)
	for _, word := range strings.Fields(s) {
		w := visibleWidth(word)
		if used > 0 && used+1+w > width {
			out = append(out, line.String())
			line.Reset()
			used = 0
		}
		if used > 0 {
			line.WriteByte(' ')
			used++
		}
		line.WriteString(word)
		used += w
	}
	if line.Len() > 0 {
		out = append(out, line.String())
	}
	return out
}
