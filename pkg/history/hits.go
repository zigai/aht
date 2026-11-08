package history

import "slices"

type conversationHits struct {
	count int
	hits  []bool
}

func (h *conversationHits) absorb(child conversationHits) {
	h.count += child.count
	if h.hits == nil && child.hits != nil {
		h.hits = make([]bool, len(child.hits))
	}

	for i, hit := range child.hits {
		h.hits[i] = h.hits[i] || hit
	}
}

func (h *conversationHits) qualifies(terms int) bool {
	return h.count > 0 && len(h.hits) >= terms && !slices.Contains(h.hits, false)
}
