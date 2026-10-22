package art

import "testing"

func TestCostLRUEvictsOverBudget(t *testing.T) {
	c := newCostLRU[string, int](10, 100, func(v int) int { return v })
	c.put("a", 40)
	c.put("b", 40)
	c.put("c", 40) // 120 > 100: the oldest goes
	if _, ok := c.get("a"); ok || c.used != 80 || c.len() != 2 {
		t.Fatalf("after c: used %d len %d", c.used, c.len())
	}
	c.get("b")
	c.put("d", 30) // b is fresher than c: c goes
	if _, ok := c.get("c"); ok || c.used != 70 {
		t.Fatalf("after d: used %d", c.used)
	}
	c.put("b", 90) // update in place: 90+30 > 100, d goes
	if _, ok := c.get("d"); ok || c.used != 90 {
		t.Fatalf("after b: used %d", c.used)
	}
	c.put("huge", 500) // never evicts itself
	if _, ok := c.get("huge"); !ok || c.len() != 1 || c.used != 500 {
		t.Fatalf("huge: used %d len %d", c.used, c.len())
	}
	c.remove("huge")
	if c.used != 0 {
		t.Fatalf("used %d after remove", c.used)
	}
	c.put("x", 10)
	c.clear()
	if c.used != 0 || c.len() != 0 {
		t.Fatalf("used %d after clear", c.used)
	}
}
