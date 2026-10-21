package art

import "container/list"

// lru is a small least-recently-used cache. Not safe for concurrent use.
type lru[K comparable, V any] struct {
	cap     int
	ll      *list.List
	items   map[K]*list.Element
	onEvict func(K, V)
}

type lruEntry[K comparable, V any] struct {
	key K
	val V
}

func newLRU[K comparable, V any](capacity int, onEvict func(K, V)) *lru[K, V] {
	return &lru[K, V]{cap: capacity, ll: list.New(), items: map[K]*list.Element{}, onEvict: onEvict}
}

func (c *lru[K, V]) get(k K) (V, bool) {
	if e, ok := c.items[k]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*lruEntry[K, V]).val, true
	}
	var zero V
	return zero, false
}

func (c *lru[K, V]) put(k K, v V) {
	if e, ok := c.items[k]; ok {
		e.Value.(*lruEntry[K, V]).val = v
		c.ll.MoveToFront(e)
		return
	}
	c.items[k] = c.ll.PushFront(&lruEntry[K, V]{k, v})
	for c.ll.Len() > c.cap {
		last := c.ll.Back()
		ent := last.Value.(*lruEntry[K, V])
		c.ll.Remove(last)
		delete(c.items, ent.key)
		if c.onEvict != nil {
			c.onEvict(ent.key, ent.val)
		}
	}
}

func (c *lru[K, V]) len() int { return c.ll.Len() }

// clear empties the cache, evicting every entry.
func (c *lru[K, V]) clear() {
	for c.ll.Len() > 0 {
		last := c.ll.Back()
		ent := last.Value.(*lruEntry[K, V])
		c.ll.Remove(last)
		delete(c.items, ent.key)
		if c.onEvict != nil {
			c.onEvict(ent.key, ent.val)
		}
	}
}

// remove drops k without calling onEvict.
func (c *lru[K, V]) remove(k K) {
	if e, ok := c.items[k]; ok {
		c.ll.Remove(e)
		delete(c.items, k)
	}
}
