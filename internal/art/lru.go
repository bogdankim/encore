package art

import "container/list"

// lru is a small least-recently-used cache. Not safe for concurrent use.
type lru[K comparable, V any] struct {
	cap     int
	ll      *list.List
	items   map[K]*list.Element
	onEvict func(K, V)

	// With cost set, entries are also evicted (oldest first, never the one
	// just added) while their summed cost is over budget.
	cost   func(V) int
	budget int
	used   int
}

type lruEntry[K comparable, V any] struct {
	key  K
	val  V
	cost int
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
	n := 0
	if c.cost != nil {
		n = c.cost(v)
	}
	if e, ok := c.items[k]; ok {
		ent := e.Value.(*lruEntry[K, V])
		c.used += n - ent.cost
		ent.val, ent.cost = v, n
		c.ll.MoveToFront(e)
		c.trim()
		return
	}
	c.items[k] = c.ll.PushFront(&lruEntry[K, V]{k, v, n})
	c.used += n
	c.trim()
}

func (c *lru[K, V]) trim() {
	for c.ll.Len() > c.cap || (c.budget > 0 && c.used > c.budget && c.ll.Len() > 1) {
		last := c.ll.Back()
		ent := last.Value.(*lruEntry[K, V])
		c.ll.Remove(last)
		delete(c.items, ent.key)
		c.used -= ent.cost
		if c.onEvict != nil {
			c.onEvict(ent.key, ent.val)
		}
	}
}

// newCostLRU is newLRU with a byte budget: cost(v) is an entry's size.
func newCostLRU[K comparable, V any](capacity, budget int, cost func(V) int) *lru[K, V] {
	c := newLRU[K, V](capacity, nil)
	c.cost, c.budget = cost, budget
	return c
}

func (c *lru[K, V]) len() int { return c.ll.Len() }

// clear empties the cache, evicting every entry.
func (c *lru[K, V]) clear() {
	for c.ll.Len() > 0 {
		last := c.ll.Back()
		ent := last.Value.(*lruEntry[K, V])
		c.ll.Remove(last)
		delete(c.items, ent.key)
		c.used -= ent.cost
		if c.onEvict != nil {
			c.onEvict(ent.key, ent.val)
		}
	}
}

// remove drops k without calling onEvict.
func (c *lru[K, V]) remove(k K) {
	if e, ok := c.items[k]; ok {
		c.used -= e.Value.(*lruEntry[K, V]).cost
		c.ll.Remove(e)
		delete(c.items, k)
	}
}
