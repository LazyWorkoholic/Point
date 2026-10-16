package editor

import (
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// testDoc builds an editable document from content, as if ReadProjectFile
// had returned it inside a project. Metadata defaults to the LF / no-BOM /
// trailing-newline / writable case; the tests that care set their own.
func testDoc(t *testing.T, content string) *doc {
	t.Helper()
	return newDoc(wireFile{
		Path:            "C:\\p\\sample.py",
		Content:         content,
		LineEnding:      lineEndingLF,
		TrailingNewline: true,
		Writable:        true,
	}, "C:\\p", true)
}

func testTheme() theme.Theme { return theme.New("turquoise", theme.BackgroundTransparent) }

// B32's metadata is CARRIED, not re-derived. This is the test that fails if
// anyone ever adds a "detect the line ending from the content" shortcut --
// the content here is LF-only (it is always LF on the wire) while the
// metadata says CRLF with a BOM, which is exactly the shape B32 puts on the
// wire for a CRLF+BOM file.
func TestMetadataIsCarriedNotDerived(t *testing.T) {
	d := newDoc(wireFile{
		Path:            "C:\\p\\crlf.py",
		Content:         "def a():\n    return 1",
		Mtime:           1791623519.07509,
		Size:            27,
		LineEnding:      lineEndingCRLF,
		CRLFCount:       2,
		LFCount:         0,
		Bom:             true,
		TrailingNewline: true,
		Writable:        true,
	}, "C:\\p", true)

	if d.lineEnding != lineEndingCRLF {
		t.Errorf("lineEnding = %q, want crlf", d.lineEnding)
	}
	if !d.bom {
		t.Error("bom was not carried")
	}
	if !d.trailingNewline {
		t.Error("trailingNewline was not carried")
	}
	if d.crlfCount != 2 || d.lfCount != 0 {
		t.Errorf("counts = %d/%d, want 2/0", d.crlfCount, d.lfCount)
	}
	if d.mtime != 1791623519.07509 || d.size != 27 {
		t.Errorf("mtime/size = %v/%d", d.mtime, d.size)
	}
	// And the content has no U+FEFF in it: B32 strips the BOM and reports
	// it separately, so nothing here may look for one.
	if strings.ContainsRune(d.buf.Text(), '\ufeff') {
		t.Error("a BOM reached the buffer")
	}
	if d.readOnly {
		t.Error("a writable CRLF+BOM file must be editable")
	}
}

// B4's display half: a mixed file opens EDITABLE and the status bar says
// MIXED with both counts. It must not claim CRLF or LF, and it must not be
// read-only -- a mixed file in a real repo is an ordinary file to edit.
func TestMixedLineEndingsOpenEditableAndSayMixed(t *testing.T) {
	d := newDoc(wireFile{
		Path: "C:\\p\\mixed.py", Content: "a\nb\nc",
		LineEnding: lineEndingMixed, CRLFCount: 2, LFCount: 1,
		TrailingNewline: true, Writable: true,
	}, "C:\\p", true)

	if d.readOnly {
		t.Fatal("a mixed file must open editable")
	}
	if got := lineEndingLabel(d); got != "MIXED 2/1" {
		t.Errorf("label = %q, want MIXED 2/1", got)
	}
}

// B5: a file that could not be decoded losslessly opens STRICTLY
// read-only, and the status bar says why. B32's handoff asks for exactly
// this: errors="replace" has already turned bytes into U+FFFD, so saving
// would write the replacements over the original.
func TestLossyDecodeOpensReadOnly(t *testing.T) {
	d := newDoc(wireFile{
		Path: "C:\\p\\bad.py", Content: "\ufffd\ufffdbad utf8",
		LineEnding: lineEndingLF, DecodeReplacements: 2, Writable: true,
	}, "C:\\p", true)

	if !d.readOnly || d.roReason != roLossyDecode {
		t.Fatalf("readOnly=%v reason=%q, want true/%q", d.readOnly, d.roReason, roLossyDecode)
	}
	// And no edit gets through.
	before := d.buf.Text()
	d.insertRune('x')
	d.newline()
	d.backspace()
	d.indentSelection()
	if d.buf.Text() != before {
		t.Error("a read-only document was modified")
	}
	if d.dirty {
		t.Error("a read-only document was marked dirty")
	}
}

func TestNotWritableOpensReadOnly(t *testing.T) {
	d := newDoc(wireFile{
		Path: "C:\\p\\ro.py", Content: "x", LineEnding: lineEndingLF, Writable: false,
	}, "C:\\p", true)
	if !d.readOnly || d.roReason != roNotWritable {
		t.Fatalf("readOnly=%v reason=%q", d.readOnly, d.roReason)
	}
}

// The task doc's note 4, and _handle_write_project_file's own choice: no
// project means read-only, because a file with no .point\ above it cannot
// take a lock.
func TestNoProjectOpensReadOnly(t *testing.T) {
	d := newDoc(wireFile{
		Path: "C:\\elsewhere\\x.py", Content: "x",
		LineEnding: lineEndingLF, Writable: true,
	}, "", false)
	if !d.readOnly || d.roReason != roNoProject {
		t.Fatalf("readOnly=%v reason=%q", d.readOnly, d.roReason)
	}
}

func TestDetectIndent(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		tabs  bool
		width int
	}{
		{"four spaces", "def a():\n    x = 1\n    if x:\n        y = 2\n", false, 4},
		{"two spaces", "a:\n  b:\n    c: 1\n  d: 2\n", false, 2},
		{"hard tabs", "func a() {\n\tx := 1\n\tif x {\n\t\ty := 2\n", true, tabStop},
		{"no indentation at all", "one\ntwo\nthree\n", false, defaultIndentWidth},
		{"empty", "", false, defaultIndentWidth},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectIndent(newLineBuffer(c.body))
			if got.tabs != c.tabs || got.width != c.width {
				t.Errorf("detectIndent = %+v, want tabs=%v width=%d", got, c.tabs, c.width)
			}
		})
	}
}

// One indented line is not evidence. Eight spaces is indistinguishable
// from a four-space file that skipped a level, and trusting it would make
// every Tab press in such a file insert two levels -- so a width needs two
// demonstrations or the default stands.
//
// Written before the rule was: the first implementation picked the most
// common step with no minimum and answered 8 here.
func TestDetectIndentIgnoresASingleSample(t *testing.T) {
	if got := detectIndent(newLineBuffer("a:\n        b\n")); got.width != defaultIndentWidth {
		t.Errorf("width = %d, want the default %d", got.width, defaultIndentWidth)
	}
}

// ...but a file that demonstrates a wide step repeatedly is taken at its
// word, which is what stops the minimum from becoming "always 4".
func TestDetectIndentTrustsARepeatedWideStep(t *testing.T) {
	body := "a:\n        b:\n                c:\n        d:\n                e:\n"
	if got := detectIndent(newLineBuffer(body)); got.width != 8 {
		t.Errorf("width = %d, want 8", got.width)
	}
}

// A four-space file with two hand-aligned continuation lines must not be
// read as a two-space file on the strength of its exceptions.
func TestDetectIndentPrefersTheCommonStepOverTheSmallest(t *testing.T) {
	var b strings.Builder
	b.WriteString("def f():\n")
	for i := 0; i < 6; i++ {
		b.WriteString("    x = 1\n")
		b.WriteString("y = 2\n")
	}
	b.WriteString("z = (\n")
	b.WriteString("  1,\n")
	b.WriteString("w = (\n")
	b.WriteString("  2,\n")
	if got := detectIndent(newLineBuffer(b.String())); got.width != 4 {
		t.Errorf("width = %d, want 4", got.width)
	}
}

// The real repo's own layout: a Python file in this repo indents with four
// spaces, and the detector has to say so rather than defaulting to it by
// accident. Built from a shape that would detect differently under a naive
// "count the first indent" rule.
func TestDetectIndentUsesTheStepNotTheDepth(t *testing.T) {
	body := "class A:\n" +
		"    def b(self):\n" +
		"        if True:\n" +
		"            return 1\n" +
		"    def c(self):\n" +
		"        pass\n"
	if got := detectIndent(newLineBuffer(body)); got.width != 4 {
		t.Errorf("width = %d, want 4", got.width)
	}
}

func TestRelativeTo(t *testing.T) {
	cases := []struct{ root, abs, want string }{
		{"C:\\p", "C:\\p\\a\\b.py", "a/b.py"},
		{"C:\\p\\", "C:\\p\\a.py", "a.py"},
		{"c:\\p", "C:\\P\\a.py", "a.py"}, // Windows paths are case-insensitive
		{"C:\\p", "D:\\other\\a.py", ""}, // a different volume has no relative form
		{"", "C:\\p\\a.py", ""},
		{"C:\\p", "C:\\pp\\a.py", ""}, // a prefix that is not a parent
	}
	for _, c := range cases {
		if got := relativeTo(c.root, c.abs); got != c.want {
			t.Errorf("relativeTo(%q, %q) = %q, want %q", c.root, c.abs, got, c.want)
		}
	}
}
