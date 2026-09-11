package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// F07's own rule: "No is focused by default."
func TestConfirmDefaultsToNo(t *testing.T) {
	fired := false
	c := NewConfirm("Title", "Body", "Yes", "No", func() tea.Msg { fired = true; return nil })
	if c.focusYes {
		t.Fatal("a fresh Confirm must default to No focused")
	}

	o, _ := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if o != nil {
		t.Error("Enter on the default (No) should still close the dialog")
	}
	if fired {
		t.Error("Enter on the default (No) must not run OnConfirm")
	}
}

func TestConfirmArrowsToggleFocusAndEnterConfirms(t *testing.T) {
	fired := false
	c := NewConfirm("Title", "Body", "Yes", "No", func() tea.Msg { fired = true; return nil })

	o, _ := c.Update(tea.KeyMsg{Type: tea.KeyLeft})
	c = o.(*Confirm)
	if !c.focusYes {
		t.Fatal("left/right did not move focus to Yes")
	}

	o, cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if o != nil {
		t.Error("confirming should close the dialog")
	}
	if cmd == nil {
		t.Fatal("confirming returned no command")
	}
	cmd()
	if !fired {
		t.Error("OnConfirm did not run")
	}
}

func TestConfirmEscCancelsRegardlessOfFocus(t *testing.T) {
	fired := false
	c := NewConfirm("Title", "Body", "Yes", "No", func() tea.Msg { fired = true; return nil })
	o, _ := c.Update(tea.KeyMsg{Type: tea.KeyLeft}) // focus Yes
	c = o.(*Confirm)

	o, cmd := c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if o != nil {
		t.Error("esc should close the dialog")
	}
	if cmd != nil {
		cmd()
	}
	if fired {
		t.Error("esc must never run OnConfirm")
	}
}

// F7's one wired consumer: quitting with a backend task running routes
// through Confirm instead of quitting immediately.
func TestQuitWithTasksRunningOpensConfirm(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30) // OpenOverlay refuses below its fit check otherwise
	r.conn.inFlight = []string{"task-1", "task-2"}

	a := mustAction(t, r, "app.quit")
	cmd := a.Run(r)

	if r.overlay == nil {
		t.Fatal("quitting with tasks running should open a Confirm overlay")
	}
	if _, ok := r.overlay.(*Confirm); !ok {
		t.Fatalf("overlay = %T, want *Confirm", r.overlay)
	}
	if r.quitting {
		t.Error("quitting must not be set until the user actually confirms")
	}
	if cmd != nil {
		t.Error("app.quit should return no command while Confirm is pending")
	}
}

// Confirming the dialog actually quits — the SAME effect ctrl+q has when
// nothing is running, not a second, drifting copy of it.
func TestConfirmingQuitActuallyQuits(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)
	r.conn.inFlight = []string{"task-1"}
	ws := &fakeDraftWorkspace{id: WorkspaceChat}
	r.SetWorkspace(WorkspaceChat, ws)

	a := mustAction(t, r, "app.quit")
	a.Run(r)

	c, ok := r.overlay.(*Confirm)
	if !ok {
		t.Fatal("expected a Confirm overlay")
	}
	o, cmd := c.Update(tea.KeyMsg{Type: tea.KeyLeft}) // focus Yes ("Quit")
	c = o.(*Confirm)
	o, cmd = c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if o != nil {
		t.Fatal("confirming should close the dialog")
	}
	if cmd == nil {
		t.Fatal("confirming returned no command")
	}

	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("confirmed quit produced %T, want tea.QuitMsg", msg)
	}
	if !r.quitting {
		t.Error("quitting was not set")
	}
	if ws.flushed != 1 {
		t.Errorf("FlushDraft called %d times, want 1", ws.flushed)
	}
}

// Cancelling the dialog leaves the app exactly as it was.
func TestCancellingQuitDoesNotQuit(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)
	r.conn.inFlight = []string{"task-1"}

	a := mustAction(t, r, "app.quit")
	a.Run(r)
	c := r.overlay.(*Confirm)

	o, _ := c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if o != nil {
		t.Fatal("esc should close the Confirm")
	}
	if r.quitting {
		t.Error("cancelling must not set quitting")
	}
}

// Polish (F7 re-verification): Quit and Cancel are padded to the same
// width, so the pair reads as one evenly-weighted control. Proxy: since
// each block's own width would change with focus if the two labels were
// NOT padded to a shared width, the total row width staying constant as
// focus moves is exactly the property "evenly padded" means.
func TestConfirmButtonsAreEvenlyPadded(t *testing.T) {
	c := NewConfirm("T", "body", "Quit", "Cancel", nil)
	th := theme.New("turquoise", theme.BackgroundTransparent)

	c.focusYes = true
	yesFocused := visibleWidth(c.renderButtons(th))
	c.focusYes = false
	noFocused := visibleWidth(c.renderButtons(th))

	if yesFocused != noFocused {
		t.Errorf("button row width changes with focus (%d vs %d) — Quit and Cancel are not evenly padded",
			yesFocused, noFocused)
	}
}

// The whole button row is centred within the dialog's width — rendered
// through the SAME centre() helper the header uses, so this is really an
// assertion that View() actually calls it on the button row.
func TestConfirmButtonsAreCentred(t *testing.T) {
	c := NewConfirm("T", "body", "Quit", "Cancel", nil)
	th := theme.New("turquoise", theme.BackgroundTransparent)
	textW := 38 // w=40 minus the 2-cell border pad — see View's own doc

	out := c.View(th, 40, 7)
	lines := strings.Split(out, "\n")
	var buttonLine string
	for _, l := range lines {
		if strings.Contains(stripANSI(l), "Quit") {
			buttonLine = l
			break
		}
	}
	if buttonLine == "" {
		t.Fatal("no line contains the buttons")
	}
	want := centre(c.renderButtons(th), textW)
	if buttonLine != want {
		t.Errorf("button line = %q, want centre()'s own output %q", buttonLine, want)
	}
}

// Polish: the focused button uses turquoise's own accent colour, the same
// role box.go's border uses for focus (BorderFocus == AccentPrimary in
// every palette) — never a hardcoded colour.
func TestConfirmFocusUsesThemeAccent(t *testing.T) {
	c := NewConfirm("T", "body", "Quit", "Cancel", nil)
	turquoise := theme.New("turquoise", theme.BackgroundTransparent)
	gold := theme.New("gold", theme.BackgroundTransparent)

	a := c.renderButtons(turquoise)
	b := c.renderButtons(gold)
	if a == b {
		t.Fatal("the focused button's colour did not change between palettes — it is not theme-driven")
	}
	if stripANSI(a) != stripANSI(b) {
		t.Fatal("the palette changed the buttons' TEXT, not just their colour")
	}
}

// Polish: a footer hint names the navigation keys.
func TestConfirmFooterNamesNavigationKeys(t *testing.T) {
	c := NewConfirm("T", "body", "Quit", "Cancel", nil)
	out := stripANSI(c.View(theme.New("turquoise", theme.BackgroundTransparent), 40, 7))
	if !strings.Contains(out, "←/→") {
		t.Errorf("footer does not name the navigation keys:\n%s", out)
	}
}

// The body word-wraps rather than being silently cut — the same "no
// ellipsis" truncation bug found in the popup's row rendering, reproduced
// here with the actual production quit-with-tasks message.
func TestConfirmBodyWrapsRatherThanTruncates(t *testing.T) {
	body := "2 task(s) still running. Quitting now abandons their progress."
	c := NewConfirm("T", body, "Quit", "Cancel", nil)
	out := stripANSI(c.View(theme.New("turquoise", theme.BackgroundTransparent), 40, 7))

	for _, word := range strings.Fields(body) {
		if !strings.Contains(out, word) {
			t.Errorf("word %q from the body is missing — the message was cut:\n%s", word, out)
		}
	}
}
