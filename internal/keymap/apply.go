package keymap

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Apply sets the leader and replaces the default sequences of each action in
// overrides (config [keys] table: action name → sequences in config
// notation). An empty list unbinds the action. Overrides go to the contexts
// the action has by default. Calling Apply again adds to earlier overrides.
//
// It returns human-readable warnings: unknown actions, unparsable sequences,
// a sequence bound to two actions in one context (an override beats a
// default; between two overrides the first action by name wins), and
// sequences that are a prefix of another, which then fire only after the
// timeout.
func (e *Engine) Apply(overrides map[string][]string, leader string) []string {
	var warns []string
	if leader != "" {
		toks, err := ParseSequence(leader, "")
		switch {
		case err != nil:
			warns = append(warns, fmt.Sprintf("leader: %v", err))
		case len(toks) != 1:
			warns = append(warns, fmt.Sprintf("leader %q must be a single key", leader))
		default:
			e.leader = toks[0]
		}
	}

	names := make([]string, 0, len(overrides))
	for n := range overrides {
		names = append(names, n)
	}
	sort.Strings(names)
	if e.overrides == nil {
		e.overrides = map[Action][]string{}
	}
	seen := map[Action]string{}
	for _, name := range names {
		a, ok := LookupAction(name)
		if !ok {
			warns = append(warns, fmt.Sprintf("unknown action %q", name))
			continue
		}
		if prev, dup := seen[a]; dup {
			warns = append(warns, fmt.Sprintf("%q and %q both name %s; using %q", prev, name, a, prev))
			continue
		}
		seen[a] = name
		e.overrides[a] = slices.Clone(overrides[name])
	}

	e.Reset()
	return append(warns, e.build()...)
}

// contextsOf returns the contexts a has default bindings in.
func contextsOf(a Action) []Context {
	var out []Context
	for _, d := range defaults {
		if d.action == a && !slices.Contains(out, d.ctx) {
			out = append(out, d.ctx)
		}
	}
	if len(out) == 0 {
		out = []Context{Global}
	}
	return out
}

// build compiles defaults and overrides into tries and reports problems.
func (e *Engine) build() []string {
	var warns []string
	e.bindings = e.bindings[:0]

	// Overrides first, so they win conflicts against defaults.
	users := make([]Action, 0, len(e.overrides))
	for a := range e.overrides {
		users = append(users, a)
	}
	slices.Sort(users)
	for _, a := range users {
		for _, c := range contextsOf(a) {
			for _, s := range e.overrides[a] {
				e.bindings = append(e.bindings, &binding{ctx: c, action: a, seq: s, user: true})
			}
		}
	}
	for _, d := range defaults {
		if _, overridden := e.overrides[d.action]; overridden {
			continue
		}
		for _, s := range d.seqs {
			e.bindings = append(e.bindings, &binding{ctx: d.ctx, action: d.action, seq: s})
		}
	}

	live := e.bindings[:0]
	for _, b := range e.bindings {
		keys, err := ParseSequence(b.seq, e.leader)
		if err != nil {
			warns = append(warns, fmt.Sprintf("%s: %v", b.action, err))
			continue
		}
		b.keys = keys
		live = append(live, b)
	}
	e.bindings = live

	for c := range e.tries {
		e.tries[c] = &node{}
	}
	for _, b := range e.bindings {
		n := e.tries[b.ctx]
		for _, t := range b.keys {
			next := n.children[t]
			if next == nil {
				next = &node{}
				if n.children == nil {
					n.children = map[string]*node{}
				}
				n.children[t] = next
			}
			n = next
		}
		switch {
		case n.action == "":
			n.action, n.binding = b.action, b
		case n.action != b.action:
			b.dead = true
			warns = append(warns, fmt.Sprintf("%s in %s is bound to %s and %s; keeping %s",
				Display(b.keys), b.ctx, n.action, b.action, n.action))
		}
	}

	e.prefixes = map[string]string{}
	for seq, name := range prefixNames {
		if keys, err := ParseSequence(seq, e.leader); err == nil {
			e.prefixes[strings.Join(keys, " ")] = name
		}
	}

	return append(warns, e.shadowWarnings()...)
}

// shadowWarnings reports, for each focus context, sequences that are a
// prefix of others (they wait for the timeout) and overrides that hide or are
// hidden by the same keys in a parent context.
func (e *Engine) shadowWarnings() []string {
	var warns []string
	hidden := map[[2]*binding]bool{}
	var prefixes []*binding
	longer := map[*binding][]*binding{}
	for c := range numContexts {
		var active []*binding
		taken := map[string]*binding{}
		for _, level := range c.chain() {
			var added []*binding
			for _, b := range e.bindings {
				if b.dead || b.ctx != level {
					continue
				}
				seq := strings.Join(b.keys, " ")
				if top, ok := taken[seq]; ok {
					if pair := [2]*binding{top, b}; (top.user || b.user) && !hidden[pair] {
						hidden[pair] = true
						warns = append(warns, fmt.Sprintf("%s for %s in %s hides %s from %s",
							Display(b.keys), top.action, top.ctx, b.action, b.ctx))
					}
					continue
				}
				added = append(added, b)
				active = append(active, b)
			}
			for _, b := range added {
				taken[strings.Join(b.keys, " ")] = b
			}
		}
		for _, a := range active {
			for _, b := range active {
				if len(a.keys) >= len(b.keys) || !slices.Equal(a.keys, b.keys[:len(a.keys)]) || slices.Contains(longer[a], b) {
					continue
				}
				if longer[a] == nil {
					prefixes = append(prefixes, a)
				}
				longer[a] = append(longer[a], b)
			}
		}
	}
	for _, a := range prefixes {
		bs := longer[a]
		more := ""
		if len(bs) > 1 {
			more = fmt.Sprintf(" and %d more", len(bs)-1)
		}
		warns = append(warns, fmt.Sprintf("%s (%s) is the start of %s (%s)%s; %s fires after the %v timeout",
			Display(a.keys), a.action, Display(bs[0].keys), bs[0].action, more, a.action, e.TimeoutDuration))
	}
	return warns
}
