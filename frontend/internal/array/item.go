package array

// Item is one row a Menu can hold.
//
// Title is the only field fuzzy matching runs against. Description, Binding
// and Section are optional display data a caller may leave empty — Menu
// itself never reads them; only the renderer (app.Popup) does. They exist
// here, on the shared item, rather than on a second per-consumer type,
// because F07 folds the palette and the help screen into ONE component: both
// need a name, a description and a binding on the same row, and a second
// type would be the "second list" the episode explicitly forbids.
type Item struct {
	id          string
	title       string
	description string
	binding     string
	section     string
	disabled    bool
}

func NewItem(id, title string) *Item {
	return &Item{id: id, title: title}
}

func (i *Item) GetID() string          { return i.id }
func (i *Item) GetTitle() string       { return i.title }
func (i *Item) GetDescription() string { return i.description }
func (i *Item) GetBinding() string     { return i.binding }
func (i *Item) GetSection() string     { return i.section }
func (i *Item) IsDisabled() bool       { return i.disabled }

func (i *Item) SetTitle(newTitle string) { i.title = newTitle }

// The Set* fields below return the item itself so a caller can build one in
// a single expression: array.NewItem(id, name).SetDescription(d).SetBinding(b).

func (i *Item) SetDescription(d string) *Item { i.description = d; return i }
func (i *Item) SetBinding(b string) *Item     { i.binding = b; return i }

// SetSection groups items under a dim header when the Menu is unfiltered.
// Items are expected to arrive already grouped by section — Menu does not
// sort; see menu.go's sectionedRows.
func (i *Item) SetSection(s string) *Item { i.section = s; return i }

// SetDisabled marks an item unselectable-but-visible — the palette's "grey
// out an action whose When predicate is false" requirement. A disabled item
// stays in the list and stays reachable by the arrow keys; only the meaning
// of choosing it changes.
func (i *Item) SetDisabled(d bool) *Item { i.disabled = d; return i }
