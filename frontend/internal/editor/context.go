package editor

import (
	"regexp"
	"strings"
)

// EditorContext is what the Filler-Explainer needs beyond the session's own
// history: which file, which language, where the caret is, what is selected,
// and the enclosing scope around it.
//
// It mirrors `backend\managers\editor_context.py`'s dataclass by name and by
// field, which is deliberate and is the same crossing `Tool.to_schema()`
// makes into `providers\` -- one concept, two sides of a process boundary.
// B11's module says so in its own header: "Mirrors F11/F15's Go
// EditorContext by name and by field, on purpose."
//
// # The frontend's version wins, and this is the reason it exists
//
// `resolve_scope()` (B11) computes an enclosing range from the text dump it
// is handed, because `cli.py` has no editor. This package has one: it knows
// the real caret, the real selection, and the buffer's own line list
// including edits that are not on disk. So `Enclosing` is supplied here and
// `resolve_scope` returns it untouched -- its first branch is "`enclosing`,
// if given, wins outright".
//
// # Imports is carried, and the backend currently ignores it
//
// Said plainly rather than implied, because the field looks load-bearing and
// is not: `_render_editor_context` (`managers\context.py`) renders the
// header, the snippet and the mode instructions, and never reads
// `imports` -- `EditorContext.from_dict` decodes it and nothing downstream
// consumes it. It is sent because F15's Build lists it and because the wire
// field already exists, not because it changes what the model is shown
// today. Recorded in this episode's handoff; no claim is made for it here.
type EditorContext struct {
	// File is the ABSOLUTE path the backend echoed back on the read.
	//
	// Absolute rather than project-relative, and it is safe here where it
	// would not be for an attachment: `_render_editor_context` resolves it
	// with `allow_outside_project=True`, on the stated grounds that the
	// editor already has the file open in a real buffer and the path was
	// set by the invocation site, never typed by a model. The backend then
	// reduces it to `path.name` for the prompt header, which is also what
	// B24 matches a proposal's own `file` against (bare basename accepted).
	File string `json:"file"`

	// Mode is MODE_EXPLAIN or MODE_FILL, decided by the invocation site.
	//
	// Sent EXPLICITLY, always. `cli.py`'s `_resolve_mode` infers it from
	// which of --select/--cursor was passed, and B24's own completed doc
	// records that getting that backwards made every one of its fill-mode
	// checks silently run the explain path. Nothing is inferred here.
	Mode string `json:"mode"`

	Language string `json:"language,omitempty"`

	// Cursor is the 1-based caret line.
	Cursor int `json:"cursor,omitempty"`

	// Selection is the 1-based inclusive selected line span, or nil.
	Selection []int `json:"selection,omitempty"`

	// Enclosing is the 1-based inclusive scope this package computed.
	Enclosing []int `json:"enclosing,omitempty"`

	Imports []string `json:"imports,omitempty"`
}

// Mode values, transcribed from backend\managers\editor_context.py.
const (
	modeExplain = "explain"
	modeFill    = "fill"
)

// enclosingTokenCap and fallbackMargin are B11's own numbers, and they are
// duplicated rather than negotiated because there is nowhere to negotiate
// them: the two sides compute the same heuristic independently and only one
// answer is ever used (ours, when supplied). The same 4-chars-per-token
// approximation B8/B10 use everywhere else an estimate is made.
const (
	enclosingTokenCap = 2000
	fallbackMargin    = 20
)

var (
	defRe         = regexp.MustCompile(`^[ \t]*(async[ \t]+)?(def|class)[ \t]+\w`)
	braceHeaderRe = regexp.MustCompile(`^[ \t]*(func|class|type)\b`)
	funcClassRe   = regexp.MustCompile(`\b(function|class)\b`)
	importRe      = regexp.MustCompile(`^[ \t]*(import|from|#include|using|require|package)\b`)
)

// editorContextFor builds the context for one invocation out of a document.
//
// mode is the caller's -- explain or fill -- and is the one field that
// cannot be derived from the buffer.
func editorContextFor(d *doc, mode string) EditorContext {
	lines := docLines(d)

	ec := EditorContext{
		File:     d.path,
		Mode:     mode,
		Language: d.lang,
		Cursor:   d.cursor.Line + 1,
		Imports:  importBlock(lines),
	}

	// A selection is only a selection when it is real. `hasSelection`
	// already distinguishes "dragging with nothing selected yet" from
	// "nothing selected", which is a distinction doc.go keeps for the
	// renderer and which matters identically here: sending a collapsed
	// range as a selection would make `resolve_scope`'s fallback centre on
	// a span instead of the caret.
	if d.hasSelection() {
		from, to := d.selectionRange()
		ec.Selection = []int{from.Line + 1, to.Line + 1}
	}

	start, end := resolveScope(lines, ec.Selection, ec.Cursor)
	ec.Enclosing = []int{start, end}
	return ec
}

// docLines is the buffer's lines as a plain slice.
//
// From the BUFFER, not from disk, and that is the whole point of computing
// the scope here: the user may have been typing, and the region the question
// is about is the one on screen. The backend reads the file instead -- which
// is why the two can disagree, and why only one of them is used.
func docLines(d *doc) []string {
	out := make([]string, d.buf.Lines())
	for i := range out {
		out[i] = d.buf.Line(i)
	}
	return out
}

// resolveScope is B11's `resolve_scope` minus the branch that does not apply
// here: there is no "enclosing was supplied" case, because this function IS
// the supplier.
//
// 1-based inclusive, both ends. The order of the three outcomes is B11's,
// and the cap is checked BEFORE the enclosing range is accepted rather than
// after -- "if the enclosing scope is larger, fall back to selection ± 20
// lines" (F15's Build), not "send it anyway and let the budget evict it".
func resolveScope(lines []string, selection []int, cursor int) (int, int) {
	target := cursor
	if len(selection) == 2 {
		target = selection[0]
	}
	if target < 1 {
		target = 1
	}
	if start, end, ok := findEnclosing(lines, target); ok {
		chars := 0
		for _, l := range lines[start-1 : end] {
			chars += len(l)
		}
		if chars/4 <= enclosingTokenCap {
			return start, end
		}
	}
	return fallbackRange(len(lines), selection, cursor)
}

// findEnclosing is the Tier 1 heuristic: nearest enclosing def/class by
// indentation, or func/class...{ by brace matching. NO PARSER -- F15's Build
// says so twice, and B11's module header records the failure cases the
// heuristic actually hits.
//
// Transcribed from `_find_enclosing` deliberately step for step, including
// the parts that look incidental:
//
//   - a blank target line scans FORWARD for its reference indent, because
//     the caret resting on the empty line between two functions belongs to
//     the one below it;
//   - a target line that itself opens a scope IS the header, rather than
//     looking upward for an outer one, so invoking on a `def` line explains
//     that function and not its class;
//   - trailing blank lines are trimmed off the end, which is what makes a
//     Python function's range stop at its last statement instead of running
//     to the next header.
func findEnclosing(lines []string, target int) (int, int, bool) {
	n := len(lines)
	if n == 0 {
		return 0, 0, false
	}
	targetIdx := target - 1
	if targetIdx < 0 {
		targetIdx = 0
	}
	if targetIdx > n-1 {
		targetIdx = n - 1
	}

	refIdx := targetIdx
	for refIdx < n && strings.TrimSpace(lines[refIdx]) == "" {
		refIdx++
	}
	if refIdx >= n {
		refIdx = targetIdx
	}
	refIndent := indentWidth(lines[refIdx])

	headerIdx, headerIndent := -1, 0
	if opensScope(lines[refIdx]) {
		headerIdx, headerIndent = refIdx, refIndent
	} else {
		for i := refIdx - 1; i >= 0; i-- {
			line := lines[i]
			if strings.TrimSpace(line) != "" && opensScope(line) && indentWidth(line) < refIndent {
				headerIdx, headerIndent = i, indentWidth(line)
				break
			}
		}
	}
	if headerIdx < 0 {
		return 0, 0, false
	}

	var endIdx int
	if braceStyle(lines[headerIdx]) {
		endIdx = matchBraceEnd(lines, headerIdx)
	} else {
		endIdx = indentStyleEnd(lines, headerIdx, headerIndent)
	}
	return headerIdx + 1, endIdx + 1, true
}

func opensScope(line string) bool {
	stripped := strings.TrimSpace(line)
	if stripped == "" {
		return false
	}
	if defRe.MatchString(line) {
		return true
	}
	if strings.HasSuffix(stripped, "{") &&
		(braceHeaderRe.MatchString(line) || funcClassRe.MatchString(stripped)) {
		return true
	}
	return false
}

func braceStyle(line string) bool {
	return strings.HasSuffix(strings.TrimRight(line, " \t"), "{")
}

func matchBraceEnd(lines []string, headerIdx int) int {
	depth := 0
	started := false
	for i := headerIdx; i < len(lines); i++ {
		for _, ch := range lines[i] {
			switch ch {
			case '{':
				depth++
				started = true
			case '}':
				depth--
			}
		}
		if started && depth <= 0 {
			return i
		}
	}
	return len(lines) - 1
}

func indentStyleEnd(lines []string, headerIdx, headerIndent int) int {
	endIdx := headerIdx
	for i := headerIdx + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			endIdx = i
			continue
		}
		if indentWidth(line) <= headerIndent {
			break
		}
		endIdx = i
	}
	for endIdx > headerIdx && strings.TrimSpace(lines[endIdx]) == "" {
		endIdx--
	}
	return endIdx
}

func indentWidth(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// fallbackRange is "selection ± 20 lines", clamped. Reached when nothing
// encloses the target at all (top-level code) or when the enclosing scope is
// over the cap.
func fallbackRange(n int, selection []int, cursor int) (int, int) {
	start, end := cursor, cursor
	if len(selection) == 2 {
		start, end = selection[0], selection[1]
	}
	if start == 0 {
		start, end = 1, 1
	}
	return clampRangeTo(start-fallbackMargin, end+fallbackMargin, n)
}

func clampRangeTo(start, end, n int) (int, int) {
	if start < 1 {
		start = 1
	}
	if n < 1 {
		n = 1
	}
	if end > n {
		end = n
	}
	if end < start {
		end = start
	}
	return start, end
}

// importBlock is the file's import lines, "cheap to grab" (F15's Build).
//
// A scan of the first lines for an import-ish keyword, stopping at the first
// scope header -- not a parser, and not the whole file: imports that appear
// after the first `def` are a deliberate local import and are not part of
// the file's declared surface. Capped so a generated file with a thousand
// `using` lines cannot become the request.
func importBlock(lines []string) []string {
	const (
		scanLimit = 200
		maxKept   = 40
	)
	var out []string
	for i, line := range lines {
		if i >= scanLimit || len(out) >= maxKept {
			break
		}
		if opensScope(line) {
			break
		}
		if importRe.MatchString(line) {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Naming the scope, for the explain panel's header
// ---------------------------------------------------------------------------

// scopeName describes the enclosing block a region sits in, by kind and
// name: "function add(a, b)", "class Foo", or "selection" when nothing
// encloses it.
//
// Derived from the HEADER LINE the enclosing scan already found -- the one
// line findEnclosing identified as opening the scope -- and from nothing
// else. No parser, which is F15's own rule for this heuristic, and no
// second scan: the range it is handed is the range that was sent, so the
// label cannot describe a different region from the one the model saw.
//
// It reads the kind from the keyword and the name from what follows it,
// stopping at the first delimiter. A header this does not recognise falls
// back to the trimmed header line itself, truncated -- showing the real
// line is more honest than inventing a name for it.
func scopeName(lines []string, start, end int) string {
	if start < 1 || start > len(lines) {
		return "selection"
	}
	header := strings.TrimSpace(lines[start-1])
	if header == "" || !opensScope(lines[start-1]) {
		// The fallback range (selection +/- 20) has no header: nothing
		// encloses the target at all, which is a real and common answer
		// for top-level code.
		return "selection"
	}

	fields := strings.Fields(header)
	if len(fields) == 0 {
		return "selection"
	}
	kind := strings.TrimSuffix(fields[0], ":")
	if kind == "async" && len(fields) > 1 {
		// `async def foo(...)` -- the kind a reader means is the second
		// word, and "async" alone is not one.
		fields = fields[1:]
		kind = strings.TrimSuffix(fields[0], ":")
	}
	switch kind {
	case "def", "func", "function":
		kind = "function"
	case "class", "type", "struct", "interface":
		// Left as written: "class Foo" and "type Foo" are both what the
		// language itself calls them.
	default:
		return truncateCells(header, 40)
	}
	if len(fields) < 2 {
		return kind
	}

	// The name, plus its parameter list when it is on this line. Cut at
	// the body opener (`:` or `{`) so a Python `def` does not carry a
	// trailing colon and a Go `func` does not carry its brace.
	rest := strings.TrimSpace(strings.Join(fields[1:], " "))
	if i := strings.IndexAny(rest, "{"); i >= 0 {
		rest = strings.TrimSpace(rest[:i])
	}
	rest = strings.TrimSuffix(rest, ":")
	if rest == "" {
		return kind
	}
	return kind + " " + rest
}
