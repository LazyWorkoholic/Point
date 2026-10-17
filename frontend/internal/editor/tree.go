package editor

import (
	"sort"
	"strings"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// The file tree, built from ListProjectFiles' FLAT list of POSIX-relative
// paths (B7). The backend has no directory walk on the wire and does not
// need one: a flat index is what the `@` picker wants, and a tree is one
// grouping pass over it.
//
// # What honours .gitignore, and where the toggle is
//
// Neither is here. `_build_index` applies `.gitignore` itself, keyed on
// `index.respect_gitignore`, and that key is a BEHAVIOUR OVERRIDE B26
// already owns and F10b's Behaviour tab already exposes. Building a second
// control over one backend key would be two places that can disagree about
// it, which is the thing this project refuses on principle. What this file
// does instead is re-request the index with refresh:true when that override
// changes (see protocol.go's onBehaviourOverrides), so the tree reflects the
// toggle rather than owning it.
//
// `.venv`, `node_modules`, `__pycache__` and `.git` are skipped by
// `_ALWAYS_SKIP_DIRS` on the backend, which is F13's "always skips .venv
// and __pycache__" already satisfied one layer down.
//
// # The size band
//
// The index drops anything over `index.max_file_kb` (100), so a file
// between that and `editor.max_file_kb` (512) is openable by path and
// INVISIBLE here. That is B32's recorded accepted limitation, inherited
// rather than introduced: fixing it means threading a per-call max_kb
// through list_files whose cache keys on project.id alone, which B32's
// handoff calls a real episode. Nothing in this session's UI opens a file
// the tree does not show, so the band has no route in from the Editor at
// all this session.

// treeNode is one entry. Directories are synthesised from the path
// segments of the files under them -- the wire never names a directory.
type treeNode struct {
	name     string
	path     string // POSIX, project-relative; "" for the synthetic root
	dir      bool
	children []*treeNode
	expanded bool
}

// fileTree is the right pane's File Tree card.
type fileTree struct {
	root *treeNode

	// rows is the flattened visible list, rebuilt whenever anything
	// expands or collapses. Selection is an index into it, so it is
	// recomputed rather than cached across a change -- an index into a
	// stale flattening is how a tree ends up opening the wrong file.
	rows []treeRow
	sel  int
	top  int

	// expanded survives a rebuild: ListProjectFiles can return a
	// different set of files (a refresh, a gitignore toggle) and the
	// directories the user opened are still the directories the user
	// opened.
	expanded map[string]bool

	loaded bool
	note   string

	// reveal is the tree's half of doc.reveal -- see that field. Without
	// it, render's ensureVisible undid every wheel scroll of this card
	// on the same frame it happened.
	reveal bool
}

type treeRow struct {
	node  *treeNode
	depth int
	last  bool
}

func newFileTree() *fileTree {
	return &fileTree{expanded: map[string]bool{}, note: "no project"}
}

// build groups a flat path list into a tree, keeping whatever was expanded.
func (t *fileTree) build(paths []string) {
	root := &treeNode{dir: true, expanded: true}
	index := map[string]*treeNode{"": root}

	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)

	for _, p := range sorted {
		segs := strings.Split(p, "/")
		cur := root
		for i, seg := range segs {
			full := strings.Join(segs[:i+1], "/")
			child := index[full]
			if child == nil {
				child = &treeNode{
					name:     seg,
					path:     full,
					dir:      i < len(segs)-1,
					expanded: t.expanded[full],
				}
				index[full] = child
				cur.children = append(cur.children, child)
			}
			cur = child
		}
	}

	sortChildren(root)
	t.root = root
	t.loaded = true
	t.note = ""
	t.flatten()
}

// sortChildren puts directories first, then files, each alphabetically --
// LAYOUT.md's own sketch (`runtime\` above its three files).
func sortChildren(n *treeNode) {
	sort.SliceStable(n.children, func(i, j int) bool {
		a, b := n.children[i], n.children[j]
		if a.dir != b.dir {
			return a.dir
		}
		return strings.ToLower(a.name) < strings.ToLower(b.name)
	})
	for _, c := range n.children {
		sortChildren(c)
	}
}

func (t *fileTree) flatten() {
	t.rows = t.rows[:0]
	if t.root == nil {
		return
	}
	t.walk(t.root, -1, false)
	if t.sel >= len(t.rows) {
		t.sel = len(t.rows) - 1
	}
	if t.sel < 0 {
		t.sel = 0
	}
}

// walk appends n and its visible descendants.
//
// `last` -- whether this node is the final child of its parent, which is
// what makes the glyph `└` rather than `├` -- is passed DOWN rather than
// looked up afterwards. Lastness is a property of the parent's child list,
// so the parent is the only place that knows it for free; deriving it later
// means re-finding every node's parent, which is a second traversal per row.
func (t *fileTree) walk(n *treeNode, depth int, last bool) {
	if depth >= 0 {
		t.rows = append(t.rows, treeRow{node: n, depth: depth, last: last})
		if n.dir && !n.expanded {
			return
		}
	}
	for i, c := range n.children {
		t.walk(c, depth+1, i == len(n.children)-1)
	}
}

func parentPath(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

func (t *fileTree) find(p string) *treeNode {
	if t.root == nil {
		return nil
	}
	if p == "" {
		return t.root
	}
	cur := t.root
	for _, seg := range strings.Split(p, "/") {
		var next *treeNode
		for _, c := range cur.children {
			if c.name == seg {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

func (t *fileTree) selected() *treeNode {
	if t.sel < 0 || t.sel >= len(t.rows) {
		return nil
	}
	return t.rows[t.sel].node
}

func (t *fileTree) move(delta int) {
	if len(t.rows) == 0 {
		return
	}
	t.reveal = true
	t.sel += delta
	if t.sel < 0 {
		t.sel = 0
	}
	if t.sel >= len(t.rows) {
		t.sel = len(t.rows) - 1
	}
}

// toggle expands or collapses the selected directory. Reports whether it
// did anything, so the caller can fall through to "open the file" when the
// selection is not a directory.
func (t *fileTree) toggle() bool {
	n := t.selected()
	if n == nil || !n.dir {
		return false
	}
	n.expanded = !n.expanded
	t.expanded[n.path] = n.expanded
	t.reveal = true
	t.flatten()
	return true
}

func (t *fileTree) collapse() bool {
	n := t.selected()
	if n == nil {
		return false
	}
	if n.dir && n.expanded {
		n.expanded = false
		t.expanded[n.path] = false
		t.reveal = true
		t.flatten()
		return true
	}
	// Already collapsed, or a file: jump to the parent, which is what
	// Left means in every tree widget and saves a scroll back up a long
	// directory.
	parent := parentPath(n.path)
	if parent == "" {
		return false
	}
	for i, r := range t.rows {
		if r.node.path == parent {
			t.sel = i
			t.reveal = true
			return true
		}
	}
	return false
}

// expandedPaths is the Suspend blob's share of this tree: which directories
// are open, and nothing else. The file list itself is re-fetched on Resume,
// which is the same "rehydrate from the source" rule F18's anchor follows.
func (t *fileTree) expandedPaths() []string {
	out := make([]string, 0, len(t.expanded))
	for p, open := range t.expanded {
		if open {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (t *fileTree) restoreExpanded(paths []string) {
	for _, p := range paths {
		t.expanded[p] = true
	}
}

// ensureVisible scrolls the card so the selection is inside it, but ONLY
// when something asked for it -- see fileTree.reveal.
func (t *fileTree) ensureVisible(rows int) {
	if rows <= 0 {
		return
	}
	if max := len(t.rows) - 1; t.top > max {
		t.top = max
	}
	if t.top < 0 {
		t.top = 0
	}
	if !t.reveal {
		return
	}
	t.reveal = false
	if t.sel < t.top {
		t.top = t.sel
	}
	if t.sel >= t.top+rows {
		t.top = t.sel - rows + 1
	}
	if t.top < 0 {
		t.top = 0
	}
}

// render draws the card's content lines.
func (t *fileTree) render(th theme.Theme, width, rows int, focused bool) []string {
	if rows <= 0 {
		return nil
	}
	if !t.loaded {
		note := t.note
		if note == "" {
			note = "loading…"
		}
		return []string{th.Style(theme.TextDim).Render(note)}
	}
	if len(t.rows) == 0 {
		return []string{th.Style(theme.TextDim).Render("no files indexed")}
	}

	t.ensureVisible(rows)
	out := make([]string, 0, rows)
	for i := t.top; i < len(t.rows) && len(out) < rows; i++ {
		r := t.rows[i]
		glyph := "├ "
		if r.last {
			glyph = "└ "
		}
		if r.depth == 0 {
			glyph = ""
		}
		label := r.node.name
		if r.node.dir {
			label += "\\"
			if !r.node.expanded {
				label = "▸ " + label
			} else {
				label = "▾ " + label
			}
		}
		line := strings.Repeat(" ", r.depth) + glyph + label

		switch {
		case i == t.sel && focused:
			// Fill, not a bare foreground: THEME.md rule 6 allows a fill
			// for a selected list row, and Fill is also what honours
			// NO_COLOR (it reverses instead of colouring).
			line = th.Fill(theme.SelectText, theme.SelectBg).Render(padCells(line, width))
		case i == t.sel:
			line = th.Style(theme.AccentPrimary).Render(line)
		case r.node.dir:
			line = th.Style(theme.TextSecondary).Render(line)
		default:
			line = th.Style(theme.TextPrimary).Render(line)
		}
		out = append(out, line)
	}
	return out
}
