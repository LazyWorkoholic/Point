package array

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// Menu is a scrollable, filterable list.
//
// It keeps two slices: `all` is every item it was given, `items` is what is
// currently visible after filtering. Filtering never destroys `all`, so a
// filter can be cleared, narrowed, or replaced.
type Menu struct {
	all      []*Item // every item — never filtered
	items    []*Item // currently visible
	query    string
	selected int
	cursor   int // index of the first visible row
	width    int
	height   int
}

func NewMenu() Menu {
	return Menu{
		all:      make([]*Item, 0),
		items:    make([]*Item, 0),
		selected: 0,
		cursor:   0,
		height:   7,
		width:    40,
	}
}

func (m Menu) Init() tea.Cmd { return nil }

func (m Menu) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width-4, msg.Height-3)

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			m.scrollUp()
		case "down", "j":
			m.scrollDown()
		}

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scrollUp()
		case tea.MouseButtonWheelDown:
			m.scrollDown()
		}
	}
	return m, nil
}

func (m Menu) View() string { return m.renderList() }

// ---- movement ----

func (m *Menu) scrollUp() {
	if m.selected > 0 {
		m.selected--
		if m.selected < m.cursor {
			m.cursor = m.selected
		}
	}
}

func (m *Menu) scrollDown() {
	if m.selected < len(m.items)-1 {
		m.selected++
		if m.selected >= m.cursor+m.height {
			m.cursor = m.selected - m.height + 1
		}
	}
}

// ---- data ----

// SetItems replaces the full item set and re-applies the current filter.
func (m *Menu) SetItems(items []*Item) {
	m.all = items
	m.applyFilter()
}

// AppendItems adds to the full set, not to the filtered view.
func (m *Menu) AppendItems(item *Item) {
	m.all = append(m.all, item)
	m.applyFilter()
}

// Filter sets the query. An empty query restores the full list.
func (m *Menu) Filter(query string) {
	m.query = query
	m.applyFilter()
}

func (m *Menu) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.query))

	if q == "" {
		m.items = m.all
	} else {
		filtered := make([]*Item, 0, len(m.all))
		for _, it := range m.all {
			if strings.Contains(strings.ToLower(it.GetTitle()), q) {
				filtered = append(filtered, it)
			}
		}
		m.items = filtered
	}

	m.selected = 0
	m.cursor = 0
}

func (m *Menu) SetSize(width, height int) {
	m.width = width

	switch {
	case height < 6:
		m.height = 3
	case height > 10:
		m.height = 7
	default:
		m.height = height
	}
}

func (m *Menu) Selected() (*Item, bool) {
	if len(m.items) == 0 {
		return nil, false
	}
	return m.items[m.selected], true
}

// Len reports the number of visible items (after filtering).
func (m *Menu) Len() int { return len(m.items) }

// TotalLen reports the number of items before filtering.
func (m *Menu) TotalLen() int { return len(m.all) }

// ---- render ----

func (m *Menu) renderList() string {
	if m.width < 15 || m.height < 2 {
		return "[ screen too small ]"
	}

	var b strings.Builder
	b.Grow(m.height * m.width)

	if len(m.items) == 0 {
		if m.query != "" {
			b.WriteString("  no matches")
		} else {
			b.WriteString("  no items")
		}
		for i := 1; i < m.height; i++ {
			b.WriteByte('\n')
		}
		return b.String()
	}

	end := m.cursor + m.height
	if end > len(m.items) {
		end = len(m.items)
	}

	// Leave room for the "> " prefix and one space of breathing room.
	maxTitle := m.width - 4
	if maxTitle < 5 {
		maxTitle = 5
	}

	rendered := 0
	for i := m.cursor; i < end; i++ {
		if rendered > 0 {
			b.WriteByte('\n')
		}

		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}

		// Truncate by DISPLAY width, not byte or rune count — CJK and emoji
		// occupy two cells and will break the surrounding border otherwise.
		// runewidth.Truncate is a no-op when the string already fits.
		title := runewidth.Truncate(m.items[i].GetTitle(), maxTitle, "…")

		b.WriteString(prefix)
		b.WriteString(title)
		rendered++
	}

	// Pad to a fixed height so the list never collapses the layout around it.
	for rendered < m.height {
		b.WriteByte('\n')
		b.WriteString("  ")
		rendered++
	}

	return b.String()
}