// F10's Appearance tab: theme (live preview), background mode, and the
// mouse toggle — Settings' second tab, built the same way the Providers
// tab is (settings.go): a static item list, Decor computed fresh every
// render off Root's own live fields (r.theme, r.mouseEnabled), never baked
// into an item.
package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/array"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

var backgroundModes = []string{"transparent", "dark", "light"}

func newAppearanceTabItems(r *Root) []*array.Item {
	names := theme.Names()
	items := make([]*array.Item, 0, len(names)+len(backgroundModes)+1)
	for _, name := range names {
		items = append(items, array.NewItem("theme:"+name, titleCase(name)).SetSection("Theme"))
	}
	for _, mode := range backgroundModes {
		items = append(items, array.NewItem("bg:"+mode, titleCase(mode)).SetSection("Background"))
	}
	items = append(items, array.NewItem("mouse", "Mouse").SetSection("Mouse"))
	return items
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (r *Root) themeRowDecor(name string) RowDecor {
	if r.theme.Name() == name {
		return RowDecor{Icon: "●", IconRole: theme.AccentPrimary, Note: "current"}
	}
	return RowDecor{Icon: "○", IconRole: theme.TextDim}
}

func (r *Root) backgroundRowDecor(mode string) RowDecor {
	if string(r.theme.Background()) == mode {
		return RowDecor{Icon: "●", IconRole: theme.AccentPrimary, Note: "current"}
	}
	return RowDecor{Icon: "○", IconRole: theme.TextDim}
}

func (r *Root) mouseRowDecor() RowDecor {
	if r.mouseEnabled {
		return RowDecor{Note: "on — ⏎ turn off"}
	}
	return RowDecor{Note: "off — ⏎ turn on"}
}

// selectTheme applies the new palette immediately (live preview, F10
// Done-when #7) and keeps the popup open (closes=false in onSettingsSelect)
// so the user can keep comparing palettes without reopening Settings each
// time.
func (r *Root) selectTheme(name string) tea.Cmd {
	r.theme = theme.New(name, r.theme.Background())
	r.propagateTheme()
	r.status = "theme: " + name
	return func() tea.Msg { return UIOverrideSetMsg{Override: config.UIOverride{Theme: name}} }
}

func (r *Root) selectBackground(mode string) tea.Cmd {
	r.theme = theme.New(r.theme.Name(), theme.Background(mode))
	r.propagateTheme()
	r.status = "background: " + mode
	return func() tea.Msg { return UIOverrideSetMsg{Override: config.UIOverride{Background: mode}} }
}

// propagateTheme pushes r.theme into EVERY LOADED workspace.
//
// Root's own chrome (header, status bar, this very popup) reads r.theme
// directly on every render and needs no help. A workspace holds its OWN
// copy -- every Workspace.View call reads it, never one threaded in from
// outside -- and would not otherwise ever learn a theme changed. That much
// was always true; see app.ThemeAware's doc.
//
// What was wrong is that this pushed to r.current() ALONE, so a workspace
// that was loaded but not active never heard about it, and the only other
// caller of SetTheme is loadWorkspace -- i.e. construction. Switching to a
// workspace does not re-theme it, because it is already loaded.
//
// Reproduced: theme the Editor butterfly, switch to Chat, theme green from
// there, switch back. Root's header, title, connection word and status bar
// are green (they read r.theme live); the Editor's box borders, syntax
// colours, File Tree, Terminal and Task card are all still butterfly. The
// same in reverse, because the bug is in WHO gets told, not in which
// workspace.
//
// Nothing here caches a resolved lipgloss.Style -- the Editor builds its
// border and content styles per render from w.theme, and its highlight
// cache holds theme.Role values rather than styles precisely so a palette
// change cannot be baked into it. The copy was simply never refreshed.
//
// The vacant diagnostics screen is handled through r.current(): it is not
// in r.workspaces (it has no id in the map) but it is what is on screen
// when nothing is loaded, and it is ThemeAware. Re-setting the active
// workspace a second time is idempotent.
func (r *Root) propagateTheme() {
	for _, ws := range r.workspaces {
		if ta, ok := ws.(ThemeAware); ok {
			ta.SetTheme(r.theme)
		}
	}
	if ta, ok := r.current().(ThemeAware); ok {
		ta.SetTheme(r.theme)
	}
}

// toggleMouse flips mouse reporting live — tea.EnableMouseCellMotion and
// tea.DisableMouse are themselves valid tea.Cmd values (func() tea.Msg),
// Bubble Tea's own mechanism for changing terminal modes after the program
// has already started; main.go's tea.WithMouseCellMotion only sets the
// INITIAL mode.
func (r *Root) toggleMouse() tea.Cmd {
	r.mouseEnabled = !r.mouseEnabled
	enabled := r.mouseEnabled
	r.status = "mouse: off"
	var modeCmd tea.Cmd = tea.DisableMouse
	if enabled {
		r.status = "mouse: on"
		modeCmd = tea.EnableMouseCellMotion
	}
	return tea.Batch(modeCmd, func() tea.Msg {
		return UIOverrideSetMsg{Override: config.UIOverride{Mouse: &enabled}}
	})
}
