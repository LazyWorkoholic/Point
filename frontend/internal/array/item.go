package array

type Item struct {
	id    string
	title string
	// meta  map[string]string
}

func (i *Item) GetID() string { return i.id }

func (i *Item) GetTitle() string { return i.title }

// func (i *Item) GetMeta(key string) string { return i.meta[key] }

func (i *Item) SetTitle(newTitle string) { i.title = newTitle }

// func (i *Item) SetMeta(key, value string) { i.meta[key] = value }

func NewItem(id, title string) *Item {
	return &Item{
		id:    id,
		title: title,
		// meta:  make(map[string]string),
	}
}
