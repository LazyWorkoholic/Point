// Package theme is the only package in the frontend allowed to contain a
// colour literal.
//
// Components reference ROLES, never colours. Adding a fifth palette must be a
// data change (one column in THEME.md, one literal in palettes.go), not a code
// change. If a component needs a colour that is not a role, the fix is a new
// role — not a hex string in the component.
package theme

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Role names a semantic slot. The string values match THEME.md's table so a
// palette can be read straight off the document.
type Role string

const (
	BorderIdle   Role = "border.idle"
	BorderFocus  Role = "border.focus"
	BorderSubtle Role = "border.subtle"

	TextPrimary   Role = "text.primary"
	TextSecondary Role = "text.secondary"
	TextDim       Role = "text.dim"

	AccentPrimary   Role = "accent.primary"
	AccentSecondary Role = "accent.second"

	UserBorder Role = "user.border"
	AIBar      Role = "ai.bar"

	CodeBorder Role = "code.border"
	CodeText   Role = "code.text"
	InlineCode Role = "inline.code"

	Bold Role = "bold"
	Link Role = "link"

	SelectBg   Role = "select.bg"
	SelectText Role = "select.text"

	Running Role = "running"
	Queued  Role = "queued"
	Done    Role = "done"

	// Error and Warning are fixed and outside the palette. A gold "error" is
	// not readable as an error.
	Error   Role = "error"
	Warning Role = "warning"

	Watermark Role = "watermark"
	Scrollbar Role = "scrollbar"
)

// Palette is the data half: every role mapped to one colour. Four of these
// exist, transcribed from THEME.md.
type Palette struct {
	Name   string
	Colors map[Role]string
}

// Background decides whether the terminal's own background shows through.
//
// Transparent is the default because it needs no background detection, which
// is unreliable over SSH and inside tmux.
type Background string

const (
	BackgroundTransparent Background = "transparent"
	BackgroundDark        Background = "dark"
	BackgroundLight       Background = "light"
)

// The two background fills. They are not roles: in transparent mode — the
// default — neither is ever used, and a palette must not have to name a
// background it will normally never paint.
const (
	darkFill  = "#0F1214"
	lightFill = "#F7F7F5"
)

// Theme is the resolved thing components hold: one palette, one background
// mode, and whether colour is allowed at all.
type Theme struct {
	palette Palette
	bg      Background
	noColor bool
}

// New resolves a theme by palette name and background mode. An unknown name
// falls back to the default palette rather than failing — a typo in a config
// file must not stop the app from starting, and Names() is what tells the user
// what was available.
func New(paletteName string, bg Background) Theme {
	p, ok := Lookup(paletteName)
	if !ok {
		p, _ = Lookup(DefaultPalette)
	}
	switch bg {
	case BackgroundDark, BackgroundLight, BackgroundTransparent:
	default:
		bg = BackgroundTransparent
	}
	return Theme{palette: p, bg: bg, noColor: noColorRequested()}
}

// noColorRequested honours the NO_COLOR convention: any non-empty value means
// drop to plain text. Borders stay — they are structure, not decoration.
func noColorRequested() bool {
	return strings.TrimSpace(os.Getenv("NO_COLOR")) != ""
}

func (t Theme) Name() string           { return t.palette.Name }
func (t Theme) Background() Background { return t.bg }
func (t Theme) NoColor() bool          { return t.noColor }

// Color resolves a role. An unregistered role returns text.primary rather than
// an empty colour, so a missing entry shows up as flat text instead of an
// invisible one.
func (t Theme) Color(r Role) lipgloss.Color {
	if v, ok := t.palette.Colors[r]; ok {
		return lipgloss.Color(v)
	}
	return lipgloss.Color(t.palette.Colors[TextPrimary])
}

// Style is the only way a component gets a foreground colour.
func (t Theme) Style(r Role) lipgloss.Style {
	s := lipgloss.NewStyle()
	if t.noColor {
		return s
	}
	return s.Foreground(t.Color(r))
}

// Fill is for the small emphasis surfaces THEME.md allows: a selected list
// row, a focused input. Never a whole pane in transparent mode.
func (t Theme) Fill(fg, bg Role) lipgloss.Style {
	s := lipgloss.NewStyle()
	if t.noColor {
		return s.Reverse(true)
	}
	return s.Foreground(t.Color(fg)).Background(t.Color(bg))
}

// Box is a rounded border with its title on the top edge, which is how every
// pane, popup, card and diff in Point is drawn.
func (t Theme) Box(border Role) lipgloss.Style {
	s := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	if t.noColor {
		return s
	}
	return s.BorderForeground(t.Color(border))
}

// BackgroundColor is the fill for dark/light mode. The second return is false
// in transparent mode, which is the default and the common case.
func (t Theme) BackgroundColor() (lipgloss.Color, bool) {
	if t.noColor {
		return "", false
	}
	switch t.bg {
	case BackgroundDark:
		return lipgloss.Color(darkFill), true
	case BackgroundLight:
		return lipgloss.Color(lightFill), true
	default:
		return "", false
	}
}

// Roles is every role a palette must define. Used by the palette test, which
// is what makes "adding a palette is a data change" true rather than hopeful.
func Roles() []Role {
	return []Role{
		BorderIdle, BorderFocus, BorderSubtle,
		TextPrimary, TextSecondary, TextDim,
		AccentPrimary, AccentSecondary,
		UserBorder, AIBar,
		CodeBorder, CodeText, InlineCode,
		Bold, Link,
		SelectBg, SelectText,
		Running, Queued, Done,
		Error, Warning,
		Watermark, Scrollbar,
	}
}
