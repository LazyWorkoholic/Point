// Markdown rendering: goldmark's parser walked by hand into styled,
// width-wrapped terminal lines, instead of glamour.
//
// F3.md says "Glamour (or equivalent)". Glamour's own code-block rendering
// draws its own margin/background chrome, not a rounded code.border box with
// a title-less top edge matching every other box in Point — reconciling the
// two would mean fighting glamour's renderer more than using it. Walking
// goldmark's AST directly gives full control over exactly the shapes
// LAYOUT.md and THEME.md specify, at the cost of writing the walk once here.
package chat

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gmtext "github.com/yuin/goldmark/text"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// renderMarkdown parses text and returns lines fit for a width-cell box.
// Never errors — an unparseable fragment still walks the AST goldmark
// produced (it always produces one; CommonMark has no "invalid" document)
// and any node kind this switch does not recognise falls back to its plain
// text, so nothing here can panic a live render on real user input.
func renderMarkdown(th theme.Theme, text string, width int) []string {
	if width < 4 {
		width = 4
	}
	source := []byte(text)
	doc := goldmark.DefaultParser().Parse(gmtext.NewReader(source))
	lines := renderChildrenBlocks(th, doc, source, width)
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func renderChildrenBlocks(th theme.Theme, parent ast.Node, source []byte, width int) []string {
	var out []string
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		lines := renderBlock(th, n, source, width)
		if len(lines) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, lines...)
	}
	return out
}

func renderBlock(th theme.Theme, n ast.Node, source []byte, width int) []string {
	switch v := n.(type) {
	case *ast.Heading:
		marker := strings.Repeat("#", v.Level) + " "
		words := append(
			splitWords(strings.TrimSpace(marker), th.Style(theme.TextDim)),
			splitWords(plainText(v, source), th.Style(theme.AccentPrimary).Bold(true))...,
		)
		return wrapWords(words, width)

	case *ast.Paragraph:
		return wrapWords(collectInlineWords(th, n, source), width)

	case *ast.TextBlock:
		return wrapWords(collectInlineWords(th, n, source), width)

	case *ast.List:
		return renderList(th, v, source, width, 0)

	case *ast.FencedCodeBlock:
		lang := string(v.Language(source))
		return renderCodeBlock(th, lang, string(v.Text(source)), width)

	case *ast.CodeBlock:
		return renderCodeBlock(th, "", string(v.Lines().Value(source)), width)

	case *ast.Blockquote:
		inner := renderChildrenBlocks(th, n, source, width-2)
		dim := th.Style(theme.TextDim)
		out := make([]string, len(inner))
		for i, l := range inner {
			out[i] = dim.Render("▏ ") + l
		}
		return out

	case *ast.ThematicBreak:
		return []string{th.Style(theme.BorderIdle).Render(strings.Repeat("─", width))}

	case *ast.HTMLBlock:
		text := plainText(n, source)
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return wrapWords(splitWords(text, th.Style(theme.TextDim)), width)

	default:
		// Never error: anything unrecognised (a table, a footnote — neither
		// is in scope, see F3.md's list) renders as its own plain text
		// rather than being dropped or panicking.
		text := plainText(n, source)
		if text == "" {
			return nil
		}
		return wrapWords(splitWords(text, th.Style(theme.TextPrimary)), width)
	}
}

// renderList handles both marker kinds and simple one-level nesting.
// indent is display cells already consumed by an enclosing list.
func renderList(th theme.Theme, list *ast.List, source []byte, width, indent int) []string {
	var out []string
	idx := list.Start
	if idx <= 0 {
		idx = 1
	}
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		bullet := "• "
		if list.IsOrdered() {
			bullet = fmt.Sprintf("%d. ", idx)
		}
		idx++
		bw := runewidth.StringWidth(bullet)
		contentWidth := width - indent - bw
		if contentWidth < 4 {
			contentWidth = 4
		}

		var words []word
		var nested []string
		for c := item.FirstChild(); c != nil; c = c.NextSibling() {
			if list2, ok := c.(*ast.List); ok {
				nested = append(nested, renderList(th, list2, source, width, indent+bw+2)...)
				continue
			}
			words = append(words, collectInlineWords(th, c, source)...)
		}

		lines := wrapWords(words, contentWidth)
		if len(lines) == 0 {
			lines = []string{""}
		}
		lead := strings.Repeat(" ", indent)
		for j, l := range lines {
			prefix := lead + strings.Repeat(" ", bw)
			if j == 0 {
				prefix = lead + padCells(bullet, bw)
			}
			out = append(out, prefix+l)
		}
		out = append(out, nested...)
	}
	return out
}

// collectInlineWords walks a container's direct children as inline nodes —
// valid for Paragraph, TextBlock and a list item's own content, all of which
// hold inline nodes (Text, Emphasis, CodeSpan, Link, …) directly.
func collectInlineWords(th theme.Theme, container ast.Node, source []byte) []word {
	var words []word
	base := th.Style(theme.TextPrimary)
	for n := container.FirstChild(); n != nil; n = n.NextSibling() {
		words = append(words, inlineWords(th, n, source, base)...)
	}
	return words
}

func inlineWords(th theme.Theme, n ast.Node, source []byte, base lipgloss.Style) []word {
	switch v := n.(type) {
	case *ast.Text:
		return splitWords(string(v.Segment.Value(source)), base)

	case *ast.CodeSpan:
		return splitWords(plainText(v, source), th.Style(theme.InlineCode))

	case *ast.Emphasis:
		style := base.Bold(true)
		if v.Level >= 2 {
			style = th.Style(theme.Bold).Bold(true)
		}
		var words []word
		for c := v.FirstChild(); c != nil; c = c.NextSibling() {
			words = append(words, inlineWords(th, c, source, style)...)
		}
		return words

	case *ast.Link:
		style := th.Style(theme.Link).Underline(true)
		var words []word
		for c := v.FirstChild(); c != nil; c = c.NextSibling() {
			words = append(words, inlineWords(th, c, source, style)...)
		}
		if len(words) == 0 {
			words = splitWords(string(v.Destination), style)
		}
		return words

	case *ast.AutoLink:
		return splitWords(string(v.URL(source)), th.Style(theme.Link).Underline(true))

	case *ast.Image:
		// No terminal image support — the alt text, styled as a link, is a
		// readable degradation rather than nothing.
		return splitWords("["+plainText(v, source)+"]", th.Style(theme.Link))

	case *ast.RawHTML:
		return nil

	default:
		var words []word
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			words = append(words, inlineWords(th, c, source, base)...)
		}
		return words
	}
}

// plainText flattens a node to its literal text, ignoring every style — used
// for headings (styled uniformly regardless of inner emphasis) and as the
// fallback for any block kind this file does not otherwise handle.
func plainText(n ast.Node, source []byte) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		if t, ok := n.(*ast.Text); ok {
			b.Write(t.Segment.Value(source))
			if t.SoftLineBreak() {
				b.WriteByte(' ')
			}
			return
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
