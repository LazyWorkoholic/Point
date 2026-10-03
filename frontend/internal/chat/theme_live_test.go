package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// turquoise and gold disagree on essentially every role (theme\palettes.go),
// so a rendered line's escape sequences differ between them whenever the
// theme that produced it actually changed — the same detection
// TestCacheInvalidatesOnWidthChange already uses for a width change.
func turquoiseAndGold() (theme.Theme, theme.Theme) {
	return theme.New("turquoise", theme.BackgroundTransparent),
		theme.New("gold", theme.BackgroundTransparent)
}

// TestSetThemeInvalidatesTheRenderCache is the real bug a verification round
// found: changing the theme (Settings' Appearance tab, F10) repainted every
// OTHER surface immediately, but a PERSISTED message already in cache.go's
// by-id cache kept its old colours until some unrelated event (a resize, a
// message scrolling into view for the first time) happened to invalidate
// the right entry. SetTheme must clear the cache itself, the same way a
// width change already does.
func TestSetThemeInvalidatesTheRenderCache(t *testing.T) {
	turquoise, gold := turquoiseAndGold()
	w := newTestWorkspace(t)
	w.SetTheme(turquoise)
	msg := wireMessage{ID: "m1", Role: roleAssistant, Content: wireContent{Text: "hello there"}}

	a := w.renderCached(msg, 40)
	if _, ok := w.cache["m1"]; !ok {
		t.Fatal("expected the render to be cached")
	}

	w.SetTheme(gold)
	b := w.renderCached(msg, 40)

	if len(w.cache) != 1 {
		t.Fatalf("SetTheme should have reset and re-populated the cache, has %d entries", len(w.cache))
	}
	if strings.Join(a, "\n") == strings.Join(b, "\n") {
		t.Fatal("the cached render did not change after SetTheme — stale colours would survive on screen")
	}
}

// TestSetThemeInvalidatesTheLiveEntryMemo is the SAME bug in the other render
// path: a live/streaming entry's per-entry memo (stream_render.go's
// renderLive) is "fresh" whenever version/width/focused match what it was
// last rendered with, none of which a theme change touches — a verification
// round saw exactly this on the "Point:" label and the streaming cursor bar,
// which kept their old colour until a token or a resize happened to bump one
// of those three unrelated fields.
func TestSetThemeInvalidatesTheLiveEntryMemo(t *testing.T) {
	turquoise, gold := turquoiseAndGold()
	w := newTestWorkspace(t)
	w.SetTheme(turquoise)
	w.frame = 1

	e := &liveEntry{
		kind: liveReply, lane: laneTurn,
		text: "hello there", status: statusDone, startedAt: time.Now(),
	}

	a := w.renderLive(e, 40, false)

	w.SetTheme(gold)
	w.frame = 2 // a real frame boundary — sameFrame must not short-circuit this
	b := w.renderLive(e, 40, false)

	if strings.Join(a, "\n") == strings.Join(b, "\n") {
		t.Fatal("the live entry's render did not change after SetTheme — stale colours would survive on screen")
	}
}
