package theme

// The four palettes, transcribed from THEME.md's table. This file and this
// file alone contains colour literals; a grep for hex outside theme\ must come
// back empty.
//
// Turquoise is the default because it is the safest on a transparent
// background. Gold is the riskiest — golds cluster in a narrow hue band, so its
// accent is pushed warmer, its dim shades browner, and `done` is an olive
// rather than a green so it does not look foreign.

// DefaultPalette is used when config names nothing, or names something that
// does not exist.
const DefaultPalette = "turquoise"

var palettes = []Palette{
	{
		Name: "turquoise",
		Colors: map[Role]string{
			AccentPrimary:   "#2EC4C6",
			AccentSecondary: "#7FD9D2",
			TextPrimary:     "#DCEFEE",
			TextSecondary:   "#A8C7C6",
			TextDim:         "#5F7E80",
			BorderIdle:      "#3E5C5E",
			BorderSubtle:    "#2C4244",
			BorderFocus:     "#2EC4C6",
			UserBorder:      "#1F8F92",
			AIBar:           "#6FC8C4",
			CodeText:        "#BFE8E6",
			CodeBorder:      "#2A6E70",
			InlineCode:      "#14B8A6",
			Bold:            "#F0FFFE",
			Link:            "#7FD9D2",
			SelectBg:        "#16403F",
			SelectText:      "#F0FFFE",
			Running:         "#2EC4C6",
			Queued:          "#5F7E80",
			Done:            "#7FD9A8",
			Error:           "#E5484D",
			Warning:         "#F5A524",
			Watermark:       "#2C4244",
			Scrollbar:       "#3E5C5E",
		},
	},
	{
		Name: "butterfly",
		Colors: map[Role]string{
			AccentPrimary:   "#B16DFF",
			AccentSecondary: "#FF77C8",
			TextPrimary:     "#E8DCF5",
			TextSecondary:   "#B9A6CC",
			TextDim:         "#6E5A80",
			BorderIdle:      "#4A3A5C",
			BorderSubtle:    "#362A44",
			BorderFocus:     "#B16DFF",
			UserBorder:      "#8A2BE2",
			AIBar:           "#FF9ECF",
			CodeText:        "#E0B3FF",
			CodeBorder:      "#6A1B9A",
			InlineCode:      "#FF3E9E",
			Bold:            "#FFFFFF",
			Link:            "#8A9BFF",
			SelectBg:        "#3A2450",
			SelectText:      "#FFFFFF",
			Running:         "#B16DFF",
			Queued:          "#6E5A80",
			Done:            "#7FD97F",
			Error:           "#E5484D",
			Warning:         "#F5A524",
			Watermark:       "#362A44",
			Scrollbar:       "#4A3A5C",
		},
	},
	{
		Name: "green",
		Colors: map[Role]string{
			AccentPrimary:   "#1FA97A",
			AccentSecondary: "#6FBF73",
			TextPrimary:     "#DCE9DC",
			TextSecondary:   "#A9BCA6",
			TextDim:         "#63795F",
			BorderIdle:      "#435843",
			BorderSubtle:    "#2F3E2F",
			BorderFocus:     "#1FA97A",
			UserBorder:      "#2E7D5B",
			AIBar:           "#7FC98A",
			CodeText:        "#C6E3C0",
			CodeBorder:      "#34613F",
			InlineCode:      "#3FA34D",
			Bold:            "#F2FFF2",
			Link:            "#8FD9C0",
			SelectBg:        "#1E3A2A",
			SelectText:      "#F2FFF2",
			Running:         "#1FA97A",
			Queued:          "#63795F",
			Done:            "#8FD98F",
			Error:           "#E5484D",
			Warning:         "#F5A524",
			Watermark:       "#2F3E2F",
			Scrollbar:       "#435843",
		},
	},
	{
		Name: "gold",
		Colors: map[Role]string{
			AccentPrimary:   "#E0A64B",
			AccentSecondary: "#C98F3C",
			TextPrimary:     "#EFE3CB",
			TextSecondary:   "#C7B48F",
			TextDim:         "#7E6B4C",
			BorderIdle:      "#5C4C34",
			BorderSubtle:    "#42361F",
			BorderFocus:     "#E0A64B",
			UserBorder:      "#A87B30",
			AIBar:           "#D9B871",
			CodeText:        "#F0D9A8",
			CodeBorder:      "#6E5A32",
			InlineCode:      "#D89A2E",
			Bold:            "#FFF6E5",
			Link:            "#D9C089",
			SelectBg:        "#3A2E18",
			SelectText:      "#FFF6E5",
			Running:         "#E0A64B",
			Queued:          "#7E6B4C",
			Done:            "#C2C46B",
			Error:           "#E5544D",
			Warning:         "#F5B024",
			Watermark:       "#42361F",
			Scrollbar:       "#5C4C34",
		},
	},
}

// Lookup finds a palette by name. The bool is false for an unknown name, which
// New turns into the default.
func Lookup(name string) (Palette, bool) {
	for _, p := range palettes {
		if p.Name == name {
			return p, true
		}
	}
	return Palette{}, false
}

// Names lists every palette, in the order THEME.md lists them. Settings (F10)
// and any "unknown theme" message read this rather than hardcoding four
// strings.
func Names() []string {
	out := make([]string, 0, len(palettes))
	for _, p := range palettes {
		out = append(out, p.Name)
	}
	return out
}
