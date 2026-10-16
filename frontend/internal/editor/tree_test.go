package editor

import (
	"strings"
	"testing"
)

func newTestTree(paths ...string) *fileTree {
	t := newFileTree()
	t.build(paths)
	return t
}

func visiblePaths(t *fileTree) []string {
	out := make([]string, 0, len(t.rows))
	for _, r := range t.rows {
		out = append(out, r.node.path)
	}
	return out
}

// Directories are synthesised from the path segments -- the wire never
// names one -- and start collapsed, so a project's top level is what you
// first see rather than every file in it.
func TestTreeGroupsAFlatListAndStartsCollapsed(t *testing.T) {
	tr := newTestTree("a.py", "pkg/b.py", "pkg/sub/c.py", "z.md")

	got := visiblePaths(tr)
	want := []string{"pkg", "a.py", "z.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("visible = %v, want %v", got, want)
	}
}

// LAYOUT.md's own sketch: directories above files, each alphabetically.
//
// Case-INSENSITIVELY, which is why `Gamma` sorts after `alpha` rather than
// before it: this is a Windows file tree over a case-insensitive
// filesystem, and ASCII order would put every capitalised directory in a
// block above every lowercase one for no reason a reader could see.
func TestTreeSortsDirectoriesFirstThenCaseInsensitively(t *testing.T) {
	tr := newTestTree("zeta.py", "alpha/x.py", "beta.py", "Gamma/y.py")
	got := visiblePaths(tr)
	want := []string{"alpha", "Gamma", "beta.py", "zeta.py"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("visible = %v, want %v", got, want)
	}
}

func TestTreeExpandAndCollapse(t *testing.T) {
	tr := newTestTree("pkg/b.py", "pkg/c.py", "a.py")

	// pkg is row 0.
	tr.sel = 0
	if !tr.toggle() {
		t.Fatal("toggle did not expand a directory")
	}
	if got := visiblePaths(tr); strings.Join(got, ",") != "pkg,pkg/b.py,pkg/c.py,a.py" {
		t.Fatalf("expanded = %v", got)
	}

	if !tr.toggle() {
		t.Fatal("toggle did not collapse")
	}
	if got := visiblePaths(tr); strings.Join(got, ",") != "pkg,a.py" {
		t.Errorf("collapsed = %v", got)
	}
}

// Toggle on a FILE reports false, so the caller falls through to opening
// it -- the one branch that makes Enter and Right do the right thing on
// both kinds of row.
func TestToggleOnAFileReportsFalse(t *testing.T) {
	tr := newTestTree("a.py")
	tr.sel = 0
	if tr.toggle() {
		t.Error("toggle claimed to have expanded a file")
	}
}

// Left on an already-collapsed row jumps to the parent, which saves a
// scroll back up a long directory.
func TestCollapseJumpsToTheParent(t *testing.T) {
	tr := newTestTree("pkg/b.py", "pkg/c.py")
	tr.sel = 0
	tr.toggle() // expand pkg
	tr.sel = 2  // pkg/c.py

	if !tr.collapse() {
		t.Fatal("collapse did nothing")
	}
	if got := tr.selected().path; got != "pkg" {
		t.Errorf("selection = %q, want the parent", got)
	}
}

// Expanded directories survive a rebuild: a refresh or a gitignore toggle
// returns a different file set, and the directories the user opened are
// still the ones the user opened.
func TestExpandedDirectoriesSurviveARebuild(t *testing.T) {
	tr := newTestTree("pkg/b.py")
	tr.sel = 0
	tr.toggle()

	tr.build([]string{"pkg/b.py", "pkg/new.py", "other/x.py"})
	for _, r := range tr.rows {
		if r.node.path == "pkg" && !r.node.expanded {
			t.Fatal("pkg collapsed across the rebuild")
		}
	}
	if got := visiblePaths(tr); !strings.Contains(strings.Join(got, ","), "pkg/new.py") {
		t.Errorf("visible = %v, want the new file inside the expanded pkg", got)
	}
}

func TestExpandedPathsRoundTripThroughTheBlob(t *testing.T) {
	tr := newTestTree("pkg/sub/b.py")
	tr.sel = 0
	tr.toggle() // pkg
	tr.sel = 1
	tr.toggle() // pkg/sub

	paths := tr.expandedPaths()
	if strings.Join(paths, ",") != "pkg,pkg/sub" {
		t.Fatalf("expandedPaths = %v", paths)
	}

	fresh := newFileTree()
	fresh.restoreExpanded(paths)
	fresh.build([]string{"pkg/sub/b.py"})
	if got := visiblePaths(fresh); strings.Join(got, ",") != "pkg,pkg/sub,pkg/sub/b.py" {
		t.Errorf("restored = %v", got)
	}
}

func TestTreeMoveIsClamped(t *testing.T) {
	tr := newTestTree("a.py", "b.py")
	tr.move(-5)
	if tr.sel != 0 {
		t.Errorf("sel = %d after moving up past the top", tr.sel)
	}
	tr.move(+50)
	if tr.sel != 1 {
		t.Errorf("sel = %d after moving down past the bottom", tr.sel)
	}
}

// The last child at each depth gets `└`, the rest `├`.
func TestTreeGlyphMarksTheLastChild(t *testing.T) {
	tr := newTestTree("pkg/a.py", "pkg/b.py", "pkg/c.py")
	tr.sel = 0
	tr.toggle()

	lasts := 0
	for _, r := range tr.rows {
		if r.last {
			lasts++
		}
	}
	// pkg itself is the last top-level row, and c.py is the last child.
	if lasts != 2 {
		t.Errorf("%d rows marked last, want 2", lasts)
	}
	if !tr.rows[len(tr.rows)-1].last {
		t.Error("the final row is not marked last")
	}
}

// An unloaded tree says it is loading, and a loaded empty one says there
// is nothing -- never a blank card, which reads as a rendering failure.
func TestTreeRendersItsEmptyStates(t *testing.T) {
	th := testTheme()

	fresh := newFileTree()
	if got := stripANSI(strings.Join(fresh.render(th, 20, 5, false), "")); !strings.Contains(got, "no project") {
		t.Errorf("unloaded tree rendered %q", got)
	}

	empty := newTestTree()
	if got := stripANSI(strings.Join(empty.render(th, 20, 5, false), "")); !strings.Contains(got, "no files") {
		t.Errorf("empty tree rendered %q", got)
	}
}

func TestTreeRenderNeverExceedsItsWidth(t *testing.T) {
	tr := newTestTree(
		"a/very/deeply/nested/path/with/a/long/file/name/inside/it.py",
		"short.py",
	)
	for i := range tr.rows {
		tr.rows[i].node.expanded = true
	}
	tr.flatten()
	for _, width := range []int{10, 18, 24, 32} {
		for _, focused := range []bool{false, true} {
			for _, line := range tr.render(testTheme(), width, len(tr.rows), focused) {
				if got := visibleWidth(line); got > width {
					t.Errorf("width %d focused %v: %d cells in %q",
						width, focused, got, stripANSI(line))
				}
			}
		}
	}
}

func TestTreeEnsureVisibleScrollsToTheSelection(t *testing.T) {
	paths := make([]string, 60)
	for i := range paths {
		paths[i] = "f" + itoa(i) + ".py"
	}
	tr := newTestTree(paths...)
	tr.move(50) // through move(), which is what requests a reveal
	tr.ensureVisible(10)
	if tr.sel != 50 {
		t.Fatalf("sel = %d", tr.sel)
	}
	if tr.sel < tr.top || tr.sel >= tr.top+10 {
		t.Errorf("sel %d not inside [%d,%d)", tr.sel, tr.top, tr.top+10)
	}
}

// ensureVisible only reveals when something ASKED for it. Without that
// gate it ran on every frame and undid the wheel on the frame it
// happened -- see fileTree.reveal.
func TestTreeEnsureVisibleLeavesAnUnrequestedScrollAlone(t *testing.T) {
	paths := make([]string, 60)
	for i := range paths {
		paths[i] = "f" + itoa(i) + ".py"
	}
	tr := newTestTree(paths...)
	tr.top = 30 // as the wheel would set it
	tr.ensureVisible(10)
	if tr.top != 30 {
		t.Errorf("top = %d, want the scroll left alone at 30", tr.top)
	}

	// ...but an out-of-range top is still pulled back, whether or not a
	// reveal was requested: a blank window is not a scroll position.
	//
	// Clamped to len-ROWS, not len-1. This test asserted len-1 until a
	// manual report of a rendering glitch at the end of both the tree
	// and the code pane, which was exactly that bound: it leaves ONE
	// row visible however tall the viewport is, and a wheel notch back
	// then shows four. len-rows leaves a full window showing, so the
	// one-row state is unreachable. See textutil.go's clampTop.
	tr.top = 9999
	tr.ensureVisible(10)
	if want := len(tr.rows) - 10; tr.top != want {
		t.Errorf("top = %d, want it clamped to %d (a FULL 10-row window)", tr.top, want)
	}
	if visible := len(tr.rows) - tr.top; visible != 10 {
		t.Errorf("%d rows visible in a 10-row viewport, want 10", visible)
	}
}
