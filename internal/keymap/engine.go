// Package keymap turns key presses into actions: vim-style sequences with a
// leader, count prefixes, per-context bindings and a timeout for sequences
// that are both an action and a prefix.
package keymap

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// Context is where focus is. Lookups walk from the focused context to its
// parents: Queue → List → Global, List/Sidebar/Lyrics → Global. Input stands
// alone and only knows its own bindings, so text fields keep every other key.
type Context uint8

const (
	Global Context = iota
	List
	Sidebar
	Lyrics
	Queue
	Input
	numContexts
)

var contextNames = [numContexts]string{"Global", "List", "Sidebar", "Lyrics", "Queue", "Input"}

func (c Context) String() string {
	if c < numContexts {
		return contextNames[c]
	}
	return "Context(" + strconv.Itoa(int(c)) + ")"
}

// chain returns c followed by its parents, highest priority first.
func (c Context) chain() []Context {
	switch c {
	case Global:
		return []Context{Global}
	case List, Sidebar, Lyrics:
		return []Context{c, Global}
	case Queue:
		return []Context{Queue, List, Global}
	case Input:
		return []Context{Input}
	}
	return nil
}

// Hint is one possible next key while a sequence is pending.
type Hint struct {
	Key    string // display form, e.g. "s", "space"
	Label  string // action label, or the group name when Group is set
	Action Action // the action this key fires, if any
	Group  bool   // more keys follow
}

// Result is what one key (or a timeout) did.
type Result struct {
	Action  Action // fired action, "" if none
	Count   int    // with Action: the count prefix for nav actions, otherwise 1
	Pending bool   // a sequence or count is waiting for more keys
	// Ambiguous is set while pending when the keys so far are already a
	// complete action: start [Engine.TimeoutCmd] so it fires if nothing
	// follows. Other pending states wait for a key or esc with no timer.
	Ambiguous bool
	Consumed  bool   // the engine used the key; false means pass it on or ignore it
	Keys      string // display form of the pending keys, e.g. "space s" or "5"
	Hints     []Hint // continuations while pending, sorted by key
}

// BindingInfo is one action's keys in one context, for the help screen.
type BindingInfo struct {
	ActionInfo
	Context Context
	Keys    []string // display form, e.g. "space s s", "g g", "↓"
}

// TimeoutMsg is sent by the command from [Engine.TimeoutCmd].
type TimeoutMsg struct{ gen uint64 }

type binding struct {
	ctx    Context
	action Action
	seq    string // config notation
	keys   []string
	user   bool
	dead   bool // lost a conflict in its own context
}

type node struct {
	children map[string]*node
	action   Action
	binding  *binding
}

func (n *node) child(tok string) *node {
	if n == nil {
		return nil
	}
	return n.children[tok]
}

// Engine matches key sequences. Create it with [New]. It is not safe for
// concurrent use; call it from Update.
type Engine struct {
	// TimeoutDuration is how long a pending sequence waits for the next key.
	TimeoutDuration time.Duration

	leader    string
	overrides map[Action][]string
	bindings  []*binding
	tries     [numContexts]*node
	prefixes  map[string]string // joined tokens → which-key group name

	ctx   Context
	buf   []string
	count int
	gen   uint64
}

// New returns an engine with the default keymap.
func New() *Engine {
	e := &Engine{TimeoutDuration: time.Second, leader: DefaultLeader}
	e.build()
	return e
}

// Leader returns the leader token.
func (e *Engine) Leader() string { return e.leader }

// Pending reports whether a sequence or count is waiting for more keys.
func (e *Engine) Pending() bool { return len(e.buf) > 0 || e.count > 0 }

// Reset drops any pending sequence and count.
func (e *Engine) Reset() {
	e.buf = e.buf[:0]
	e.count = 0
	e.gen++
}

const maxCount = 9999

// Feed processes one key token (see [Token]) with ctx focused.
//
// A pending sequence started in another context is dropped first. While
// pending, esc cancels. A key that continues nothing resets the engine and
// comes back unconsumed, except after a complete action that is also a
// prefix: that action fires and the key is consumed.
func (e *Engine) Feed(ctx Context, tok string) Result {
	if e.Pending() && ctx != e.ctx {
		e.Reset()
	}
	e.ctx = ctx
	e.gen++

	if e.Pending() && tok == "esc" {
		e.Reset()
		return Result{Consumed: true}
	}

	chain := ctx.chain()
	if d, ok := digit(tok); ok && len(e.buf) == 0 && ctx != Input &&
		(e.count > 0 || (d > 0 && !e.rootBinds(chain, tok))) {
		e.count = min(e.count*10+d, maxCount)
		return Result{Pending: true, Consumed: true, Keys: strconv.Itoa(e.count)}
	}

	seq := append(slices.Clip(e.buf), tok)
	nodes := e.find(chain, seq)
	if len(nodes) == 0 {
		prev := firstAction(e.find(chain, e.buf))
		count := e.count
		e.Reset()
		if prev != "" {
			return fire(prev, count)
		}
		return Result{}
	}

	action := firstAction(nodes)
	if !hasChildren(nodes) {
		count := e.count
		e.Reset()
		return fire(action, count)
	}
	e.buf = seq
	return e.pendingResult(nodes)
}

// Timeout ends a pending sequence: an action that is also a prefix fires,
// anything else is dropped. [Engine.HandleTimeout] calls it for the timer
// started on an Ambiguous result.
func (e *Engine) Timeout() Result {
	if len(e.buf) == 0 {
		e.Reset()
		return Result{}
	}
	action := firstAction(e.find(e.ctx.chain(), e.buf))
	count := e.count
	e.Reset()
	if action == "" {
		return Result{}
	}
	return fire(action, count)
}

// TimeoutCmd starts the pending timer for the current state. Schedule it after
// an Ambiguous result; the resulting TimeoutMsg goes to [Engine.HandleTimeout].
func (e *Engine) TimeoutCmd() tea.Cmd {
	gen := e.gen
	return tea.Tick(e.TimeoutDuration, func(time.Time) tea.Msg { return TimeoutMsg{gen} })
}

// HandleTimeout calls [Engine.Timeout] if no key arrived since the timer was
// started, and returns an empty Result otherwise.
func (e *Engine) HandleTimeout(msg TimeoutMsg) Result {
	if msg.gen != e.gen || !e.Pending() {
		return Result{}
	}
	return e.Timeout()
}

func fire(a Action, count int) Result {
	if count < 1 || !a.IsNav() {
		count = 1
	}
	return Result{Action: a, Count: count, Consumed: true}
}

func digit(tok string) (int, bool) {
	if len(tok) == 1 && tok[0] >= '0' && tok[0] <= '9' {
		return int(tok[0] - '0'), true
	}
	return 0, false
}

// rootBinds reports whether a sequence in chain starts with tok, which turns
// a digit from a count into a key.
func (e *Engine) rootBinds(chain []Context, tok string) bool {
	for _, c := range chain {
		if e.tries[c].child(tok) != nil {
			return true
		}
	}
	return false
}

// find returns the trie node for seq in each context of chain that has one,
// highest priority first.
func (e *Engine) find(chain []Context, seq []string) []*node {
	var out []*node
	for _, c := range chain {
		n := e.tries[c]
		for _, t := range seq {
			if n = n.child(t); n == nil {
				break
			}
		}
		if n != nil && n != e.tries[c] {
			out = append(out, n)
		}
	}
	return out
}

func firstAction(nodes []*node) Action {
	for _, n := range nodes {
		if n.action != "" {
			return n.action
		}
	}
	return ""
}

func hasChildren(nodes []*node) bool {
	for _, n := range nodes {
		if len(n.children) > 0 {
			return true
		}
	}
	return false
}

func (e *Engine) pendingResult(nodes []*node) Result {
	keys := map[string]bool{}
	for _, n := range nodes {
		for k := range n.children {
			keys[k] = true
		}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Slice(sorted, func(i, j int) bool { return hintLess(sorted[i], sorted[j]) })

	hints := make([]Hint, 0, len(sorted))
	for _, k := range sorted {
		var next []*node
		for _, n := range nodes {
			if c := n.children[k]; c != nil {
				next = append(next, c)
			}
		}
		h := Hint{Key: DisplayToken(k), Action: firstAction(next), Group: hasChildren(next)}
		if h.Action != "" {
			info, _ := Info(h.Action)
			h.Label = info.Label()
		} else {
			h.Label = e.groupName(append(slices.Clip(e.buf), k), next)
		}
		hints = append(hints, h)
	}
	r := Result{Pending: true, Ambiguous: firstAction(nodes) != "", Consumed: true, Keys: Display(e.buf), Hints: hints}
	if e.count > 0 {
		r.Keys = strconv.Itoa(e.count) + " " + r.Keys
	}
	return r
}

// hintLess orders keys: letters and digits case-insensitively with lowercase
// first, then other characters, then named keys like "space" or "enter".
func hintLess(a, b string) bool {
	if ra, rb := hintRank(a), hintRank(b); ra != rb {
		return ra < rb
	}
	if fa, fb := strings.ToLower(a), strings.ToLower(b); fa != fb {
		return fa < fb
	}
	return a > b
}

func hintRank(tok string) int {
	r, n := utf8.DecodeRuneInString(tok)
	switch {
	case n != len(tok):
		return 2
	case unicode.IsLetter(r) || unicode.IsDigit(r):
		return 0
	}
	return 1
}

// groupName labels a prefix: its configured name, or the group of the first
// action under it.
func (e *Engine) groupName(seq []string, nodes []*node) string {
	if name, ok := e.prefixes[strings.Join(seq, " ")]; ok {
		return name
	}
	for _, n := range nodes {
		if a := firstDescendant(n); a != "" {
			info, _ := Info(a)
			return strings.ToLower(info.Group)
		}
	}
	return ""
}

func firstDescendant(n *node) Action {
	if n.action != "" {
		return n.action
	}
	keys := make([]string, 0, len(n.children))
	for k := range n.children {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return hintLess(keys[i], keys[j]) })
	for _, k := range keys {
		if a := firstDescendant(n.children[k]); a != "" {
			return a
		}
	}
	return ""
}

// Bindings returns the bindings in effect when ctx is focused, including
// those inherited from its parents, minus keys a closer context takes over.
// Sorted by group, then by action.
func (e *Engine) Bindings(ctx Context) []BindingInfo {
	chain := ctx.chain()
	taken := map[string]bool{}
	type key struct {
		ctx    Context
		action Action
	}
	byKey := map[key]*BindingInfo{}
	var out []*BindingInfo
	for _, c := range chain {
		var level []string
		for _, b := range e.bindings {
			if b.dead || b.ctx != c {
				continue
			}
			seq := strings.Join(b.keys, " ")
			if taken[seq] {
				continue
			}
			level = append(level, seq)
			k := key{c, b.action}
			bi := byKey[k]
			if bi == nil {
				info, _ := Info(b.action)
				bi = &BindingInfo{ActionInfo: info, Context: c}
				byKey[k] = bi
				out = append(out, bi)
			}
			bi.Keys = appendUnique(bi.Keys, Display(b.keys))
		}
		for _, s := range level {
			taken[s] = true
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		gi, gj := slices.Index(Groups, out[i].Group), slices.Index(Groups, out[j].Group)
		if gi != gj {
			return gi < gj
		}
		return actionIndex[out[i].Action] < actionIndex[out[j].Action]
	})
	res := make([]BindingInfo, len(out))
	for i, b := range out {
		res[i] = *b
	}
	return res
}

// KeysFor returns the display form of every sequence bound to a, in any
// context, in binding order: e.g. ["j", "↓"] for nav.down.
func (e *Engine) KeysFor(a Action) []string {
	var out []string
	for _, b := range e.bindings {
		if b.action == a && !b.dead {
			out = appendUnique(out, Display(b.keys))
		}
	}
	return out
}

func appendUnique(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}
