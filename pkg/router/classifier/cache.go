package classifier

import (
	"container/list"
	"sync"
)

// lruCache is a small, fixed-capacity, concurrency-safe map of request
// fingerprint to chosen candidate index.
type lruCache struct {
	mu sync.Mutex

	capacity int

	ll    *list.List
	items map[uint64]*list.Element
}

type lruEntry struct {
	key   uint64
	value int
}

func newLRU(capacity int) *lruCache {
	if capacity < 1 {
		capacity = 1
	}

	return &lruCache{
		capacity: capacity,

		ll:    list.New(),
		items: make(map[uint64]*list.Element),
	}
}

func (c *lruCache) get(key uint64) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*lruEntry).value, true
	}

	return 0, false
}

func (c *lruCache) put(key uint64, value int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*lruEntry).value = value

		return
	}

	el := c.ll.PushFront(&lruEntry{key: key, value: value})
	c.items[key] = el

	if c.ll.Len() > c.capacity {
		if oldest := c.ll.Back(); oldest != nil {
			c.ll.Remove(oldest)
			delete(c.items, oldest.Value.(*lruEntry).key)
		}
	}
}
