# Theme Reference

**Components reference roles, never colours.** Adding a fifth palette must be a
data change, not a code change.

```go
type Theme struct {
    BorderIdle, BorderFocus, BorderSubtle   lipgloss.Color
    TextPrimary, TextSecondary, TextDim     lipgloss.Color
    AccentPrimary, AccentSecondary          lipgloss.Color
    UserBorder, AIBar                       lipgloss.Color
    CodeBorder, CodeText, InlineCode        lipgloss.Color
    Bold, Link                              lipgloss.Color
    SelectBg, SelectText                    lipgloss.Color
    Running, Queued, Done                   lipgloss.Color
    Error, Warning                          lipgloss.Color   // outside the palette
    Watermark, Scrollbar                    lipgloss.Color
}
```

## Rules

1. **Three depths, always.** Dim (structure you ignore) → primary (what you read)
   → accent (what you look at). Most bad TUI themes fail by putting everything at
   one brightness.
2. **Borders sit one step below text.** A border as bright as its contents
   competes with them.
3. **One accent per screen.** Focused border, active tab, and the streaming
   indicator are the *same* accent — never three bright colours at once.
4. **Error and warning are fixed and outside the palette.** A gold "error" is not
   readable as an error.
5. **Background is transparent by default** (`background = transparent | dark |
   light`). The terminal's own background shows through, so the palette applies
   to text, borders and accents only. Transparent needs no background detection,
   which is unreliable over SSH and inside tmux.
6. Fills only for small emphasis surfaces — a selected list row, a focused input.
   Never whole panes in transparent mode.
7. Fall back to the nearest 256-colour when truecolor is not detected; honour
   `NO_COLOR` by dropping to plain text with borders intact.

**Windows Terminal supports truecolor**, so these render as written. The legacy
console host does not — if colours look wrong, check which host is running before
blaming the theme.

## Palettes

Default: **turquoise**. Luminance-picked to stay legible on transparent
backgrounds (most terminals are dark).

```
ROLE            TURQUOISE     BUTTERFLY     GREEN         GOLD
accent.primary  #2EC4C6       #B16DFF       #1FA97A       #E0A64B
accent.second   #7FD9D2       #FF77C8       #6FBF73       #C98F3C
text.primary    #DCEFEE       #E8DCF5       #DCE9DC       #EFE3CB
text.secondary  #A8C7C6       #B9A6CC       #A9BCA6       #C7B48F
text.dim        #5F7E80       #6E5A80       #63795F       #7E6B4C
border.idle     #3E5C5E       #4A3A5C       #435843       #5C4C34
border.subtle   #2C4244       #362A44       #2F3E2F       #42361F
border.focus    #2EC4C6       #B16DFF       #1FA97A       #E0A64B
user.border     #1F8F92       #8A2BE2       #2E7D5B       #A87B30
ai.bar          #6FC8C4       #FF9ECF       #7FC98A       #D9B871
code.text       #BFE8E6       #E0B3FF       #C6E3C0       #F0D9A8
code.border     #2A6E70       #6A1B9A       #34613F       #6E5A32
inline.code     #14B8A6       #FF3E9E       #3FA34D       #D89A2E
bold            #F0FFFE       #FFFFFF       #F2FFF2       #FFF6E5
link            #7FD9D2       #8A9BFF       #8FD9C0       #D9C089
select.bg       #16403F       #3A2450       #1E3A2A       #3A2E18
select.text     #F0FFFE       #FFFFFF       #F2FFF2       #FFF6E5
running         #2EC4C6       #B16DFF       #1FA97A       #E0A64B
queued          #5F7E80       #6E5A80       #63795F       #7E6B4C
done            #7FD9A8       #7FD97F       #8FD98F       #C2C46B
error           #E5484D       #E5484D       #E5484D       #E5544D
warning         #F5A524       #F5A524       #F5A524       #F5B024
watermark       #2C4244       #362A44       #2F3E2F       #42361F
scrollbar       #3E5C5E       #4A3A5C       #435843       #5C4C34
```

### Notes on the hard ones

- **Gold** is the riskiest: golds cluster in a narrow hue band, so the accent is
  pushed warmer and the dim shades browner to create separation, and `done` uses
  an olive rather than a green so it does not look foreign. Gold assumes a dark
  terminal.
- **Green** needs the same treatment; `running` and `done` are separated by
  brightness, not hue.
- **Butterfly** has the widest natural range (pink → violet → periwinkle) and
  needs the least intervention.
- **Turquoise** is the safest, hence the default.

## Adding a palette

Add a column here and a `Theme` literal in the theme package. No component
changes. If a component needs a colour that is not a role, the fix is a **new
role**, not a literal.
