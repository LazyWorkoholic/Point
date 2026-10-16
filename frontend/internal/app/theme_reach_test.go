package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// A theme change has to reach EVERY loaded workspace, not just the active
// one.
//
// propagateTheme pushed to r.current() alone, and the only other caller of
// SetTheme is loadWorkspace -- i.e. construction -- so a loaded but
// inactive workspace never heard about a change, and switching to it does
// not re-theme something already loaded. Root's header and status bar read
// r.theme live and were always right, which is what made it look as though
// the workspace were caching resolved styles. Nothing caches a style: the
// copy was simply never refreshed.

// themedWorkspace records the theme it was last handed and renders its own
// chrome from it, the way a real workspace does -- a border style and a
// content style resolved per render from its OWN copy, never from Root's.
type themedWorkspace struct {
	*Placeholder
	id    WorkspaceID
	sets  int
	theme theme.Theme
}

func newThemedWorkspace(id WorkspaceID) *themedWorkspace {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	return &themedWorkspace{
		Placeholder: NewPlaceholder(id, th),
		id:          id,
		theme:       th,
	}
}

func (w *themedWorkspace) ID() WorkspaceID { return w.id }

func (w *themedWorkspace) SetTheme(th theme.Theme) {
	w.sets++
	w.theme = th
	w.Placeholder.SetTheme(th)
}

func (w *themedWorkspace) Update(msg tea.Msg) (Workspace, tea.Cmd) {
	_, cmd := w.Placeholder.Update(msg)
	return w, cmd
}

// ownChrome is what this workspace draws from its own theme copy: the
// equivalent of the Editor's box border and its syntax-highlighted text.
func (w *themedWorkspace) ownChrome() string {
	return w.theme.Style(theme.BorderIdle).Render("│") +
		w.theme.Style(theme.AccentPrimary).Render("def")
}

func chromeFor(name string) string {
	th := theme.New(name, theme.BackgroundTransparent)
	return th.Style(theme.BorderIdle).Render("│") +
		th.Style(theme.AccentPrimary).Render("def")
}

// themeRoot builds a Root with two themable workspaces, both loaded, the
// chooser dismissed.
func themeRoot(t *testing.T) (*Root, *themedWorkspace, *themedWorkspace) {
	t.Helper()
	a := newThemedWorkspace(WorkspaceEditor)
	b := newThemedWorkspace(WorkspaceChat)
	r, err := New(Options{
		Theme: theme.New("turquoise", theme.BackgroundTransparent),
		State: config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		Workspaces: []WorkspaceDef{
			{ID: WorkspaceEditor, Name: "Editor", New: func() Workspace { return a }},
			{ID: WorkspaceChat, Name: "Chat", New: func() Workspace { return b }},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if _, ok := r.overlay.(*Chooser); ok {
		r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	}
	return r, a, b
}

func switchTo(t *testing.T, r *Root, id WorkspaceID) {
	t.Helper()
	if cmd := r.switchWorkspace(id); cmd != nil {
		cmd()
	}
}

// The repro, exactly as reported: theme the Editor butterfly from the
// Editor, switch to Chat, theme green from Chat, switch back. The Editor's
// OWN chrome must be green.
func TestAThemeChangeReachesALoadedButInactiveWorkspace(t *testing.T) {
	r, editor, chat := themeRoot(t)

	switchTo(t, r, WorkspaceEditor)
	r.selectTheme("butterfly")
	if got := editor.theme.Name(); got != "butterfly" {
		t.Fatalf("the active workspace is on %q, want butterfly", got)
	}

	switchTo(t, r, WorkspaceChat)
	setsBefore := editor.sets
	r.selectTheme("green")

	if got := chat.theme.Name(); got != "green" {
		t.Errorf("the active workspace is on %q, want green", got)
	}
	// The whole bug, in one assertion: the INACTIVE workspace heard too.
	if got := editor.theme.Name(); got != "green" {
		t.Errorf("the inactive loaded workspace is still on %q, want green", got)
	}
	if editor.sets <= setsBefore {
		t.Error("the inactive workspace was never told about the change")
	}
	if _, loaded := r.workspaces[WorkspaceEditor]; !loaded {
		t.Error("the Editor was unloaded; the point is that a LOADED one is updated")
	}

	// Switching back needs no rebuild, and its own chrome is green.
	switchTo(t, r, WorkspaceEditor)
	if got, want := editor.ownChrome(), chromeFor("green"); got != want {
		t.Errorf("the workspace's own chrome is not the new palette:\n got %q\nwant %q", got, want)
	}
	if editor.ownChrome() == chromeFor("butterfly") {
		t.Error("the workspace's own chrome is still butterfly")
	}
}

// And in reverse, which is the same bug from the other side: theme from
// Chat while the Editor is backgrounded, and from the Editor while Chat
// is. Root's own chrome is checked at every step too, since it is the half
// that was always right and must stay so.
func TestAThemeChangeReachesEveryLoadedWorkspaceInBothDirections(t *testing.T) {
	r, editor, chat := themeRoot(t)
	for _, id := range []WorkspaceID{WorkspaceEditor, WorkspaceChat} {
		switchTo(t, r, id)
	}

	for _, step := range []struct {
		active WorkspaceID
		name   string
	}{
		{WorkspaceChat, "butterfly"},
		{WorkspaceEditor, "green"},
		{WorkspaceChat, "gold"},
		{WorkspaceEditor, "turquoise"},
	} {
		switchTo(t, r, step.active)
		r.selectTheme(step.name)

		for _, ws := range []*themedWorkspace{editor, chat} {
			if got := ws.theme.Name(); got != step.name {
				t.Errorf("after theming %q from %s: %s is on %q",
					step.name, step.active, ws.id, got)
			}
			if got, want := ws.ownChrome(), chromeFor(step.name); got != want {
				t.Errorf("after theming %q from %s: %s renders its own chrome as %q",
					step.name, step.active, ws.id, got)
			}
		}
		if got := r.theme.Name(); got != step.name {
			t.Errorf("Root is on %q, want %q", got, step.name)
		}
	}
}

// The background mode travels the same way -- it is the other half of
// theme.New and goes through the same propagateTheme.
func TestABackgroundChangeReachesALoadedButInactiveWorkspace(t *testing.T) {
	r, editor, chat := themeRoot(t)
	for _, id := range []WorkspaceID{WorkspaceEditor, WorkspaceChat} {
		switchTo(t, r, id)
	}
	r.selectBackground("dark")
	for _, ws := range []*themedWorkspace{editor, chat} {
		if got := ws.theme.Background(); got != theme.BackgroundDark {
			t.Errorf("%s is on background %q, want dark", ws.id, got)
		}
	}
}

// A workspace loaded AFTER the change gets the current theme at
// construction, which is loadWorkspace's own SetTheme and must keep
// working.
func TestAWorkspaceLoadedAfterAThemeChangeGetsTheCurrentTheme(t *testing.T) {
	r, editor, chat := themeRoot(t)
	switchTo(t, r, WorkspaceEditor)
	r.selectTheme("gold")

	if _, loaded := r.workspaces[WorkspaceChat]; loaded {
		t.Fatal("precondition: Chat is already loaded")
	}
	switchTo(t, r, WorkspaceChat)
	if got := chat.theme.Name(); got != "gold" {
		t.Errorf("a freshly loaded workspace is on %q, want gold", got)
	}
	if got := editor.theme.Name(); got != "gold" {
		t.Errorf("the first workspace is on %q, want gold", got)
	}
}

// With nothing loaded, the vacant diagnostics screen still gets it: it is
// not in r.workspaces but it is what is on screen.
func TestAThemeChangeReachesTheVacantScreen(t *testing.T) {
	r, _, _ := themeRoot(t)
	if len(r.workspaces) != 0 {
		t.Fatal("precondition: something is loaded")
	}

	r.selectTheme("gold")

	ta, ok := r.current().(ThemeAware)
	if !ok {
		t.Fatal("the vacant screen is not ThemeAware")
	}
	_ = ta
	// The vacant screen draws from its own copy, so the rendered frame is
	// the honest check: gold's accent must appear in it.
	gold := theme.New("gold", theme.BackgroundTransparent)
	marker := strings.TrimSuffix(strings.TrimPrefix(
		gold.Style(theme.AccentPrimary).Render("X"), ""), "")
	marker = marker[:strings.IndexByte(marker, 'X')]
	if !strings.Contains(r.View(), marker) {
		t.Errorf("the vacant screen does not render gold's accent (%q)", marker)
	}
}

// EVERY palette, and every ORDERED PAIR of them, in both directions.
//
// The repro named butterfly and green, and the bug was reported as
// affecting all themes -- which it did: propagateTheme reaching only the
// active workspace has nothing to do with which palette is chosen. This
// test says so exhaustively rather than sampling two, so a future change
// that fixes one palette's path and not another's cannot pass.
func TestEveryPaletteReachesEveryLoadedWorkspace(t *testing.T) {
	names := theme.Names()
	if len(names) < 2 {
		t.Fatalf("only %d palettes registered; this test needs at least two", len(names))
	}

	for _, from := range names {
		for _, to := range names {
			if from == to {
				continue
			}
			t.Run(from+"->"+to, func(t *testing.T) {
				r, editor, chat := themeRoot(t)
				for _, id := range []WorkspaceID{WorkspaceEditor, WorkspaceChat} {
					switchTo(t, r, id)
				}

				// Set the first palette from the Editor...
				switchTo(t, r, WorkspaceEditor)
				r.selectTheme(from)
				for _, ws := range []*themedWorkspace{editor, chat} {
					if got := ws.theme.Name(); got != from {
						t.Fatalf("%s is on %q, want %q", ws.id, got, from)
					}
				}

				// ...and the second from Chat, with the Editor loaded and
				// inactive. That is the reported sequence.
				switchTo(t, r, WorkspaceChat)
				r.selectTheme(to)

				for _, ws := range []*themedWorkspace{editor, chat} {
					if got := ws.theme.Name(); got != to {
						t.Errorf("%s is on %q, want %q", ws.id, got, to)
					}
					if got, want := ws.ownChrome(), chromeFor(to); got != want {
						t.Errorf("%s renders its own chrome as %q, want %q", ws.id, got, want)
					}
					if ws.ownChrome() == chromeFor(from) {
						t.Errorf("%s still renders the previous palette %q", ws.id, from)
					}
				}
				if got := r.theme.Name(); got != to {
					t.Errorf("Root is on %q, want %q", got, to)
				}
			})
		}
	}
}

// Every palette resolves to DIFFERENT escape codes for the roles this
// package's theme tests compare, so none of the assertions above can pass
// vacuously because two palettes happen to render the same.
func TestEveryPaletteRendersDistinctChrome(t *testing.T) {
	seen := map[string]string{}
	for _, name := range theme.Names() {
		chrome := chromeFor(name)
		if prev, dup := seen[chrome]; dup {
			t.Errorf("palettes %q and %q render identical chrome; a theme test "+
				"comparing them would prove nothing", prev, name)
		}
		seen[chrome] = name
	}
	if len(seen) != len(theme.Names()) {
		t.Errorf("%d distinct renderings for %d palettes", len(seen), len(theme.Names()))
	}
}
