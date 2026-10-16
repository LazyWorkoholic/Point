package app

import (
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// Round 6: the clamped countdown dropped its minutes field under a minute.
//
// Reported as `idle 1s of 1m00s` and `idle 58s of 1m00s` where both sides
// should read `0m01s` / `0m58s`, matching the budget side that already
// rendered correctly.
//
// The cause was NOT a second formatter for the elapsed value. Both sides
// of the pair always called shortDuration; that function had a single
// special case -- `if total < 60 { return fmt.Sprintf("%ds", total) }` --
// so its OUTPUT changed shape at sixty seconds. A sixty-second budget took
// the minutes branch and anything under it did not, which is why it looked
// like two paths. Removing the branch fixes the pair without giving either
// side a special case.

// shortDuration is the only duration formatter in the package, and it
// always emits minutes and seconds.
func TestShortDurationAlwaysEmitsMinutesAndSeconds(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0m00s"},
		{time.Second, "0m01s"},
		{23 * time.Second, "0m23s"},
		{58 * time.Second, "0m58s"},
		{59 * time.Second, "0m59s"},
		{time.Minute, "1m00s"},
		{61 * time.Second, "1m01s"},
		{90 * time.Second, "1m30s"},
		{5 * time.Minute, "5m00s"},
		{59*time.Minute + 59*time.Second, "59m59s"},
		{time.Hour, "60m00s"},
		// Truncated, never rounded: 1.9s is 1s, so a readout can never
		// read one unit higher than what it counts down to.
		{1900 * time.Millisecond, "0m01s"},
		{59900 * time.Millisecond, "0m59s"},
		// Negative is floored at zero rather than printing "-1m-1s".
		{-time.Second, "0m00s"},
	}
	for _, c := range cases {
		if got := shortDuration(c.d); got != c.want {
			t.Errorf("shortDuration(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

// Both sides of the pair always have the same SHAPE, at every elapsed
// value against every budget. This is the property the special case broke,
// and asserting it directly is what stops a future "just show seconds when
// it is short" from coming back.
func TestBothSidesOfTheIdleReadoutHaveTheSameShape(t *testing.T) {
	budgets := []time.Duration{30 * time.Second, time.Minute, 5 * time.Minute, time.Hour}
	for _, budget := range budgets {
		for elapsed := time.Duration(0); elapsed <= budget+time.Minute; elapsed += 7 * time.Second {
			st := WorkspaceStatus{
				Def: WorkspaceDef{ID: "x", Name: "X"}, Loaded: true,
				Idle: elapsed, Timeout: budget,
			}
			label := st.Label()
			parts := strings.Split(strings.TrimPrefix(label, "idle "), " of ")
			if len(parts) != 2 {
				t.Fatalf("budget %s elapsed %s: %q does not split into a pair",
					budget, elapsed, label)
			}
			if !strings.Contains(parts[0], "m") || !strings.Contains(parts[1], "m") {
				t.Errorf("budget %s elapsed %s: %q -- one side has no minutes field",
					budget, elapsed, label)
			}
		}
	}
}

// The exact rendered strings the report named, under a minute, beside the
// one that was already right.
func TestTheIdleReadoutRendersMinutesUnderOneMinute(t *testing.T) {
	cases := []struct {
		elapsed time.Duration
		budget  time.Duration
		pinned  bool
		want    string
	}{
		// Reported as wrong.
		{time.Second, time.Minute, false, "idle 0m01s of 1m00s"},
		{58 * time.Second, time.Minute, false, "idle 0m58s of 1m00s"},
		// Reported as wrong, on the pinned row.
		{time.Second, time.Minute, true, "idle 0m01s of 1m00s · pinned"},
		// Reported as already correct -- must stay so.
		{time.Minute, time.Minute, true, "idle 1m00s of 1m00s · pinned"},
		// Clamped from well past the budget, which is the pinned case.
		{time.Hour, time.Minute, true, "idle 1m00s of 1m00s · pinned"},
		// A sub-minute budget, where BOTH sides are under a minute --
		// the case that would have printed "idle 5s of 30s".
		{5 * time.Second, 30 * time.Second, false, "idle 0m05s of 0m30s"},
		{0, 30 * time.Second, false, "idle 0m00s of 0m30s"},
	}
	for _, c := range cases {
		st := WorkspaceStatus{
			Def: WorkspaceDef{ID: "x", Name: "X"}, Loaded: true,
			Idle: c.elapsed, Timeout: c.budget, Pinned: c.pinned,
		}
		if got := st.Label(); got != c.want {
			t.Errorf("elapsed %s budget %s pinned %v: %q, want %q",
				c.elapsed, c.budget, c.pinned, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// The grace warning keeps BARE SECONDS, and has its own formatter
// ---------------------------------------------------------------------------
//
// Round 6 routed this sentence through shortDuration as a side effect of
// fixing the switcher's readout, which spelled it `unloads in 0m25s` and
// contradicted gate_f13a.ps1's own already-verified Step 10b text. Round 7
// reversed it with `graceSeconds`, a formatter used at that one call site
// -- NOT by putting the under-a-minute branch back inside shortDuration,
// which is what caused round 6's bug in the first place.
//
// This replaces TestTheGraceWarningUsesTheSameDurationFormat, whose
// premise was that reversal.

func TestGraceSecondsRendersBareSeconds(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{time.Second, "1s"},
		{25 * time.Second, "25s"},
		{29 * time.Second, "29s"},
		{idleGrace, "30s"},
		// Truncated, never rounded, so the countdown never reads one
		// higher than the thing it counts down to.
		{29900 * time.Millisecond, "29s"},
		{-time.Second, "0s"},
	}
	for _, c := range cases {
		if got := graceSeconds(c.d); got != c.want {
			t.Errorf("graceSeconds(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

// The warning's exact rendered text, at several points inside the grace
// window, so it cannot drift back to m+s -- or anywhere else -- silently.
func TestTheGraceWarningTextIsBareSeconds(t *testing.T) {
	cases := []struct {
		elapsed time.Duration
		want    string
	}{
		{31 * time.Second, "Chat unloads in 29s"},
		{35 * time.Second, "Chat unloads in 25s"},
		{45 * time.Second, "Chat unloads in 15s"},
		{59 * time.Second, "Chat unloads in 1s"},
	}
	for _, c := range cases {
		f := lifecycleRootWithState(t,
			config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
			idleCfg(1, nil), "chat", "editor")
		f.start(t)
		f.root.switchWorkspace("chat")
		f.root.switchWorkspace("editor")

		f.advance(c.elapsed) // inside the 30s grace window of a 60s timeout
		f.root.walkIdle(f.clock)

		if !strings.Contains(f.root.status, c.want) {
			t.Errorf("elapsed %s: status = %q, want it to contain %q",
				c.elapsed, f.root.status, c.want)
		}
		// Not the paired formatter's shape, in either direction.
		if strings.Contains(f.root.status, "m0") || strings.Contains(f.root.status, "0m") {
			t.Errorf("elapsed %s: status = %q carries a minutes field",
				c.elapsed, f.root.status)
		}
		// ...and the rest of the sentence is intact.
		if !strings.Contains(f.root.status, "to keep it") {
			t.Errorf("elapsed %s: status = %q lost its instruction",
				c.elapsed, f.root.status)
		}
	}
}

// graceSeconds prints no minutes field because `remaining` can never
// reach one: it is bounded by idleGrace. That is an assumption, so it is
// asserted -- raising idleGrace past a minute fails HERE, naming the
// function to fix, rather than quietly printing "unloads in 90s".
func TestGraceSecondsAssumesASubMinuteWindow(t *testing.T) {
	if idleGrace >= time.Minute {
		t.Fatalf("idleGrace is %s: graceSeconds prints seconds only and would "+
			"now read \"unloads in %s\". Give it a minutes field, or keep the "+
			"grace window under a minute.", idleGrace, graceSeconds(idleGrace))
	}
}

// The two formatters stay separate: the paired one is unconditional, the
// grace one is seconds-only, and neither has the other's shape. Asserted
// together so a future "simplify these into one" has to read why.
func TestTheTwoDurationFormattersAreDeliberatelyDifferent(t *testing.T) {
	for _, d := range []time.Duration{0, time.Second, 29 * time.Second} {
		paired, grace := shortDuration(d), graceSeconds(d)
		if paired == grace {
			t.Errorf("%s: both formatters render %q; the paired one must carry "+
				"a minutes field", d, paired)
		}
		if !strings.Contains(paired, "m") {
			t.Errorf("%s: shortDuration = %q, want a minutes field", d, paired)
		}
		if strings.Contains(grace, "m") {
			t.Errorf("%s: graceSeconds = %q, want seconds only", d, grace)
		}
	}
	// At a minute and above they agree on the seconds count but still not
	// on the shape, which is the point.
	if got := shortDuration(90 * time.Second); got != "1m30s" {
		t.Errorf("shortDuration(90s) = %q, want 1m30s", got)
	}
}

// ---------------------------------------------------------------------------
// The second bug: the pinned label was genuinely truncated, not wrapped
// ---------------------------------------------------------------------------

// `idle 1m00s of 1m00s · pinned` is 28 cells and rowWidths' default
// ceiling is 24, so Popup rendered `idle 1m00s of 1m00s …` and the label
// was gone. Confirmed as real truncation by rendering the row, not
// inferred from the report's own `· p...`.
//
// Measured while fixing it: rowWidths(86) still answered 24, because the
// 24-cell cap binds before the third-of-the-row rule does -- so no amount
// of extra popup width could have fixed it, and the switcher needed a
// column of its own.
func TestThePinnedReadoutIsNotTruncatedInTheSwitcher(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(1, nil), "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.switchWorkspace("editor")
	f.ws["chat"].dirty = 2
	f.advance(5 * time.Minute)
	f.root.walkIdle(f.clock)

	var want string
	for _, st := range f.root.WorkspaceStatuses(f.clock) {
		if st.Def.ID == "chat" {
			want = st.Label()
		}
	}
	if want != "idle 1m00s of 1m00s · pinned" {
		t.Fatalf("precondition: the label is %q", want)
	}
	if runewidth.StringWidth(want) <= 24 {
		t.Fatalf("precondition: the label is %d cells, so it would not have "+
			"exceeded the default ceiling and this test proves nothing",
			runewidth.StringWidth(want))
	}

	p := newSwitcherOverlay(f.root)
	w, h := p.PreferredSize(f.root.layout.Main.W-2, f.root.layout.Main.H-2)
	view := stripANSI(p.View(theme.New("turquoise", theme.BackgroundTransparent), w-2, h-2))

	if !strings.Contains(view, want) {
		t.Errorf("the switcher elided the pinned readout at %dx%d:\n%s", w, h, view)
	}
	if strings.Contains(view, "pinn…") || strings.Contains(view, "of 1m00s …") {
		t.Errorf("the readout is still being cut:\n%s", view)
	}

	// Through the real frame too, so the width has to survive overlayRect
	// and not only PreferredSize.
	f.root.OpenOverlay(newSwitcherOverlay(f.root))
	if frame := stripANSI(f.root.View()); !strings.Contains(frame, want) {
		t.Errorf("the rendered frame elided the pinned readout:\n%s", frame)
	}
}

// rowWidths: a declared NoteW is honoured, clamped against the NAME
// column rather than against a third of the row, and the default path is
// untouched for every popup that does not ask.
func TestRowWidthsHonoursADeclaredNoteWidth(t *testing.T) {
	// The default: a third of the row, capped at 24.
	for _, c := range []struct{ avail, wantNote int }{
		{30, 10}, {60, 20}, {72, 24}, {120, 24}, {200, 24},
	} {
		if _, note := rowWidths(c.avail, 0); note != c.wantNote {
			t.Errorf("rowWidths(%d, 0) note = %d, want %d", c.avail, note, c.wantNote)
		}
	}

	// A declared width is given in full while the name column keeps its
	// minimum.
	if _, note := rowWidths(68, 28); note != 28 {
		t.Errorf("rowWidths(68, 28) note = %d, want 28", note)
	}
	if name, note := rowWidths(40, 28); note != 23 || name != 16 {
		t.Errorf("rowWidths(40, 28) = name %d note %d, want 16/23", name, note)
	}
	// Even an absurd request leaves the name column alive.
	for _, avail := range []int{18, 20, 30, 50} {
		name, note := rowWidths(avail, 999)
		if name < 1 || note < 1 {
			t.Errorf("rowWidths(%d, 999) = name %d note %d; both must stay positive",
				avail, name, note)
		}
		if name+note+1 > avail && avail > 18 {
			t.Errorf("rowWidths(%d, 999) = name %d note %d, which overflows the row",
				avail, name, note)
		}
	}
}

// The palette and Help must be unaffected: they declare no NoteW, so they
// take the default path and their name column is unchanged.
func TestTheDefaultNoteWidthPathIsUnchanged(t *testing.T) {
	for _, avail := range []int{30, 50, 72, 100, 200} {
		name, note := rowWidths(avail, 0)
		third := avail / 3
		wantNote := 24
		if third < wantNote {
			wantNote = third
		}
		if note != wantNote {
			t.Errorf("rowWidths(%d, 0) note = %d, want %d", avail, note, wantNote)
		}
		wantName := avail - wantNote - 1
		if wantName > 40 {
			wantName = 40
		}
		if wantName < 1 {
			wantName = 1
		}
		if name != wantName {
			t.Errorf("rowWidths(%d, 0) name = %d, want %d", avail, name, wantName)
		}
	}
}
