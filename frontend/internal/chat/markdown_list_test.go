package chat

import (
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// A list item's children are BLOCK nodes, and every one of them has to
// render.
//
// renderList used to handle exactly two: a nested list, and "assume it is
// inline-bearing". A fenced code block has no inline children -- its content
// is in Lines() segments -- so it produced zero words and vanished, with the
// surrounding prose left looking intact. Models format almost every
// multi-step answer as a numbered list with code inside the steps, so that
// was most of the code in a code-heavy reply.
//
// It presented as a streaming/render race and is neither: until the closing
// fence arrives, splitOpenFence routes the block into the raw tail, which
// renders as plain visible text. The block disappeared at the exact moment
// the fence closed and the text first reached this walker -- deterministic,
// and entirely a function of the markdown shape.
func TestListItemsRenderEveryBlockChild(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)

	cases := []struct {
		name string
		text string
		// want, in the order they must appear.
		want []string
		box  bool
	}{
		{
			name: "fenced code in an ordered list",
			text: "1. do this:\n\n   ```go\n   KEEPME\n   ```\n\n2. then that\n",
			want: []string{"do this:", "KEEPME", "then that"},
			box:  true,
		},
		{
			name: "fenced code in a bullet list",
			text: "- do this:\n\n  ```go\n  KEEPME\n  ```\n\n- then that\n",
			want: []string{"do this:", "KEEPME", "then that"},
			box:  true,
		},
		{
			name: "indented code in a list item",
			text: "1. do this:\n\n       KEEPME\n\n2. then that\n",
			want: []string{"do this:", "KEEPME", "then that"},
			box:  true,
		},
		{
			name: "paragraph, code, paragraph in ONE item",
			text: "1. before:\n\n   ```go\n   KEEPME\n   ```\n\n   after the code\n\n2. next\n",
			want: []string{"before:", "KEEPME", "after the code", "next"},
			box:  true,
		},
		{
			name: "two paragraphs in a list item",
			text: "1. first para\n\n   KEEPME second para\n\n2. next\n",
			want: []string{"first para", "KEEPME", "next"},
		},
		{
			name: "blockquote in a list item",
			text: "1. see:\n\n   > KEEPME\n\n2. next\n",
			want: []string{"see:", "KEEPME", "next"},
		},
		{
			name: "nested list still nests",
			text: "1. outer\n\n   - KEEPME inner\n\n2. next\n",
			want: []string{"outer", "KEEPME", "next"},
		},
		{
			name: "code at top level (control)",
			text: "see:\n\n```go\nKEEPME\n```\n\ndone\n",
			want: []string{"see:", "KEEPME", "done"},
			box:  true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := stripSGR(strings.Join(renderStreamBody(th, c.text, 66), "\n"))

			at := -1
			for _, needle := range c.want {
				i := strings.Index(out, needle)
				if i < 0 {
					t.Fatalf("%q is missing entirely:\n%s", needle, out)
				}
				if i < at {
					t.Fatalf("%q rendered out of document order:\n%s", needle, out)
				}
				at = i
			}
			if got := strings.Contains(out, "╭"); got != c.box {
				t.Errorf("code box present = %v, want %v:\n%s", got, c.box, out)
			}
		})
	}
}

// The code box inside a list item is drawn at the ITEM's content width and
// indented under the bullet, not at the full transcript width — content
// aligns to its container, the same way every other nested block does.
func TestCodeBoxInAListItemIsIndentedUnderTheBullet(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	width := 66

	out := stripSGR(strings.Join(
		renderStreamBody(th, "1. do this:\n\n   ```go\n   KEEPME\n   ```\n", width), "\n"))

	var top string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "╭") {
			top = l
			break
		}
	}
	if top == "" {
		t.Fatalf("no code box drawn:\n%s", out)
	}

	lead := len(top) - len(strings.TrimLeft(top, " "))
	if lead == 0 {
		t.Errorf("the box starts at column 0 — it should be indented under the bullet:\n%s", out)
	}
	// The BOX, not the whole prefixed row: the row spans the transcript
	// width (correctly), while the box inside it is the item's content
	// width — narrower by exactly the indent it sits behind.
	box := visibleWidth(strings.TrimLeft(top, " "))
	if box >= width {
		t.Errorf("the box is %d cells wide at transcript width %d — it should fit the item's "+
			"content width, not the transcript's:\n%s", box, width, out)
	}
	if box != width-lead {
		t.Errorf("the box is %d cells at indent %d; it should fill exactly the %d left to "+
			"the item:\n%s", box, lead, width-lead, out)
	}
}
