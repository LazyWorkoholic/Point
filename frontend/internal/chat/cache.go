package chat

// Render caching: one message's rendered block never changes at a fixed
// width (F3 does not stream — nothing mutates a message's content after it
// arrives), so the only invalidation this needs is a width change, and the
// only bound it needs is on total size, for a session scrolled through in
// full. cacheEvictFactor keeps that bound proportional to Scrollback rather
// than hardcoding a number LAYOUT.md never gave.
const cacheEvictFactor = 2

// renderCached returns msg's rendered block at width, computing and
// memoizing it on a miss. Never includes focus styling — see render.go's
// renderMessage doc: the focused message is re-rendered directly, bypassing
// the cache, so a moving focus never has to invalidate two entries.
func (w *Workspace) renderCached(msg wireMessage, width int) []string {
	if width != w.cacheWidth {
		w.invalidateCache()
		w.cacheWidth = width
	}
	if lines, ok := w.cache[msg.ID]; ok {
		return lines
	}
	lines := renderMessage(w.theme, msg, width, false)
	w.cache[msg.ID] = lines
	w.cacheOrder = append(w.cacheOrder, msg.ID)
	w.evictIfNeeded()
	return lines
}

func (w *Workspace) invalidateCache() {
	w.cache = make(map[string][]string)
	w.cacheOrder = w.cacheOrder[:0]
}

func (w *Workspace) evictIfNeeded() {
	limit := w.scrollback * cacheEvictFactor
	if limit <= 0 || len(w.cacheOrder) <= limit {
		return
	}
	drop := len(w.cacheOrder) - w.scrollback
	for _, id := range w.cacheOrder[:drop] {
		delete(w.cache, id)
	}
	w.cacheOrder = w.cacheOrder[drop:]
}
