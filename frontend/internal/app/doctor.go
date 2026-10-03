// F10b: the About tab's one action — "Run doctor" — and the scrollable
// card it opens. Calls GetDoctorReport (B26) and renders
// build_doctor_report()'s dict, the same facts `point doctor` prints,
// grouped the same way (not byte-identical text — that guarantee is
// cli.py's own, B26 Done-when #4 — but the same sections in the same
// order, so a report that looks right on one side looks right on the
// other).
//
// DoctorCard is its own small type rather than a reuse of F9a's
// ResultCard: that type is built around a TaskRow (state glyph, duration,
// token count) this report has none of, and bending it to a second shape
// would cost more than the ~60 lines of scroll/wrap plumbing duplicated
// here. What IS reused is resultcard.go's own wrapParagraphs — the
// indentation-preserving word wrap, not the type wrapped around it.
package app

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/array"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

func newAboutTabItems(r *Root) []*array.Item {
	return []*array.Item{array.NewItem("about:doctor", "Run doctor")}
}

// onAboutSelect is Enter on "about:doctor" (settings.go's onSettingsSelect
// dispatches here by prefix).
func (r *Root) onAboutSelect() tea.Cmd {
	reqID, ok := r.SettingsSendFn(protocol.CmdGetDoctorReport, map[string]any{})
	if !ok {
		r.status = "could not reach the backend"
		return nil
	}
	r.settings.doctorReqID = reqID
	r.status = "running doctor…"
	return nil
}

// onDoctorReport handles settings.go's handleSettingsEnvelope case for
// r.settings.doctorReqID.
func (r *Root) onDoctorReport(env protocol.Envelope) tea.Cmd {
	if env.Type == protocol.EvError {
		var p struct {
			Error string `json:"error"`
		}
		_ = env.UnmarshalPayload(&p)
		msg := p.Error
		if msg == "" {
			msg = "doctor failed"
		}
		r.status = "✗ " + msg
		return nil
	}
	var report map[string]any
	if err := env.UnmarshalPayload(&report); err != nil {
		r.status = "✗ doctor report did not decode"
		return nil
	}
	back := r.settings.popup
	card := newDoctorCard(formatDoctorReport(report), back)
	return func() tea.Msg { return openOverlayMsg{card} }
}

// ---------------------------------------------------------------------------
// Report formatting
// ---------------------------------------------------------------------------

// formatDoctorReport mirrors cli.py's _print_doctor_report section order
// (version info, go frontend status, python version, dependency report,
// database path/schema state, config/credentials/state/log paths and
// existence, per-provider key source masked, ollama report, mock provider
// status, behaviour_overrides) over the same dict GetDoctorReport ships —
// build_doctor_report()'s own keys, read defensively: every field is typed
// loosely (map[string]any decoded off JSON, never a fixed Go struct,
// because this dict is Python's own evolving fact sheet, not a shape this
// package owns or should have to keep in lockstep with).
func formatDoctorReport(r map[string]any) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s); b.WriteByte('\n') }
	str := func(key string) string {
		s, _ := r[key].(string)
		return s
	}
	boolv := func(key string) bool {
		v, _ := r[key].(bool)
		return v
	}

	line("Point doctor")
	line(strings.Repeat("─", 40))
	line(fmt.Sprintf("version             %s  (%s)", str("version"), str("version_file")))
	line(fmt.Sprintf("go frontend         %s", str("go_frontend_status")))
	line(fmt.Sprintf("protocol envelope   v%s", formatNumber(r["envelope_version"])))
	line(fmt.Sprintf("python              %s  (%s)", str("python_version"), str("python_executable")))
	line("")
	line("dependencies        " + str("dependency_report"))
	line("")
	line("database            " + str("database_path"))
	line("                    " + str("database_state"))
	if legacy := str("legacy_db_path"); legacy != "" {
		line("                    legacy copy still present: " + legacy)
	}
	line("")
	line(fmt.Sprintf("config              %s  %s", str("config_path"), existsText(boolv("config_exists"))))
	if cfgErr := str("config_error"); cfgErr != "" {
		line("                    UNREADABLE: " + cfgErr)
	}
	line(fmt.Sprintf("credentials         %s  %s", str("credentials_path"), existsText(boolv("credentials_exists"))))
	if boolv("credentials_permissions_inherited") {
		line("                    permissions inherited from the user profile directory")
	}
	line(fmt.Sprintf("state               %s  %s", str("state_path"), existsText(boolv("state_exists"))))
	line(fmt.Sprintf("logs                %s  %s", str("log_path"), existsText(boolv("log_exists"))))
	line("")
	line("providers")
	if providers, ok := r["providers"].([]any); ok {
		for _, raw := range providers {
			p, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := p["name"].(string)
			if configured, _ := p["configured"].(bool); configured {
				source, _ := p["source"].(string)
				masked, _ := p["masked_key"].(string)
				line(fmt.Sprintf("  %-12skey from %s  (%s)", name, source, masked))
			} else {
				line(fmt.Sprintf("  %-12sno credentials", name))
			}
		}
	}
	line(fmt.Sprintf("  %-12s%s", "ollama", str("ollama_report")))
	mock := "not enabled"
	if boolv("mock_enabled") {
		mock = "enabled (POINT_MOCK=1)"
	}
	line(fmt.Sprintf("  %-12s%s", "mock", mock))

	line("")
	line("behaviour overrides")
	overrides, _ := r["behaviour_overrides"].(map[string]any)
	if len(overrides) == 0 {
		line("  none active")
	} else {
		keys := make([]string, 0, len(overrides))
		for k := range overrides {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			line(fmt.Sprintf("  %-32s%v", k, overrides[k]))
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func existsText(exists bool) string {
	if exists {
		return "exists"
	}
	return "missing"
}

// formatNumber renders a JSON-decoded numeric field without the ".0" a
// bare %v would print for a float64 — build_doctor_report()'s
// envelope_version is a plain int on the Python side, but every number in
// a map[string]any decoded off JSON arrives as float64 (encoding/json's
// own rule for an untyped destination).
func formatNumber(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.Itoa(int(n))
	case int:
		return strconv.Itoa(n)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ---------------------------------------------------------------------------
// DoctorCard
// ---------------------------------------------------------------------------

// DoctorCard is an Overlay, not a Popup — one scrollable body of
// pre-formatted text, the same shape ResultCard is and for the same reason
// (F9a's own doc on why a filterable list is the wrong shape for prose).
type DoctorCard struct {
	body string

	scroll    int
	lastWrapW int
	wrapped   []string
	bodyRows  int

	// back is the Settings popup this card was opened from — esc returns
	// to the SAME live instance (its tab and selection intact), the same
	// "d/esc both return to the tray" shape ResultCard.back already uses.
	back Overlay
}

func newDoctorCard(body string, back Overlay) *DoctorCard {
	return &DoctorCard{body: body, back: back}
}

func (c *DoctorCard) MinSize() (int, int) { return 60, 18 }

func (c *DoctorCard) PreferredSize(availW, availH int) (int, int) {
	return matchTranscriptPane(availW, availH)
}

func (c *DoctorCard) Title() string { return "Doctor Report" }

func (c *DoctorCard) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		switch m.Type {
		case tea.KeyEsc:
			return c.dismiss(), nil
		case tea.KeyUp:
			c.scrollBy(-1)
		case tea.KeyDown:
			c.scrollBy(1)
		case tea.KeyPgUp:
			c.scrollBy(-c.pageSize())
		case tea.KeyPgDown:
			c.scrollBy(c.pageSize())
		case tea.KeyHome:
			c.scroll = 0
		case tea.KeyEnd:
			c.scroll = c.maxScroll()
		}
		return c, nil
	case tea.MouseMsg:
		switch m.Button {
		case tea.MouseButtonWheelUp:
			c.scrollBy(-1)
		case tea.MouseButtonWheelDown:
			c.scrollBy(1)
		}
		return c, nil
	}
	return c, nil
}

func (c *DoctorCard) dismiss() Overlay {
	if c.back == nil {
		return nil
	}
	return c.back
}

func (c *DoctorCard) pageSize() int {
	if c.bodyRows > 0 {
		return c.bodyRows
	}
	return 14
}

func (c *DoctorCard) scrollBy(n int) {
	c.scroll += n
	if c.scroll > c.maxScroll() {
		c.scroll = c.maxScroll()
	}
	if c.scroll < 0 {
		c.scroll = 0
	}
}

func (c *DoctorCard) maxScroll() int {
	if c.lastWrapW == 0 {
		return 0
	}
	max := len(c.wrapped) - c.bodyRows
	if max < 0 {
		return 0
	}
	return max
}

func (c *DoctorCard) View(th theme.Theme, w, h int) string {
	textW := w - 2
	if textW < 1 {
		textW = 1
	}

	header := th.Style(theme.TextSecondary).Render(c.Title())
	footer := th.Style(theme.TextDim).Render(
		runewidth.Truncate("↑/↓ scroll   esc close", textW, "…"))
	rule := th.Style(theme.BorderSubtle).Render(strings.Repeat("─", textW))

	bodyRows := h - 4
	if bodyRows < 1 {
		bodyRows = 1
	}
	c.rewrap(textW, bodyRows)

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n\n")
	for i := 0; i < bodyRows; i++ {
		src := c.scroll + i
		if src < len(c.wrapped) {
			b.WriteString(padCells(th.Style(theme.TextPrimary).Render(c.wrapped[src]), textW))
		} else {
			b.WriteString(strings.Repeat(" ", textW))
		}
		b.WriteByte('\n')
	}
	b.WriteString(rule)
	b.WriteByte('\n')
	b.WriteString(footer)
	return b.String()
}

func (c *DoctorCard) rewrap(width, bodyRows int) {
	c.bodyRows = bodyRows
	if width != c.lastWrapW {
		c.wrapped = wrapParagraphs(c.body, width)
		c.lastWrapW = width
	}
	if c.scroll > c.maxScroll() {
		c.scroll = c.maxScroll()
	}
	if c.scroll < 0 {
		c.scroll = 0
	}
}

var _ Overlay = (*DoctorCard)(nil)
