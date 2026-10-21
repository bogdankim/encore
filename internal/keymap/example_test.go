package keymap_test

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
)

// model shows how a Bubble Tea Update drives the engine.
type model struct {
	keys     *keymap.Engine
	focus    keymap.Context
	cursor   int
	whichKey []keymap.Hint // shown while a sequence is pending
	typed    string        // stands in for a text input
}

func (m model) Update(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		r := m.keys.Feed(m.focus, keymap.Token(msg))
		m.whichKey = r.Hints
		switch {
		case r.Action != "":
			return m.do(r.Action, r.Count)
		case r.Ambiguous:
			return m, m.keys.TimeoutCmd() // the popup stays open otherwise
		case !r.Consumed && m.focus == keymap.Input:
			m.typed += msg.Text // hand the key to the text input
		}
	case keymap.TimeoutMsg:
		m.whichKey = nil
		if r := m.keys.HandleTimeout(msg); r.Action != "" {
			return m.do(r.Action, r.Count)
		}
	}
	return m, nil
}

func (m model) do(a keymap.Action, count int) (model, tea.Cmd) {
	switch a {
	case keymap.NavDown:
		m.cursor += count
	case keymap.NavTop:
		m.cursor = 0
	case keymap.Filter:
		m.focus = keymap.Input
	case keymap.InputCancel:
		m.focus = keymap.List
	case keymap.Quit:
		return m, tea.Quit
	}
	return m, nil
}

func press(text string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(text)[0], Text: text} }

func Example() {
	m := model{keys: keymap.New(), focus: keymap.List}
	if warns := m.keys.Apply(map[string][]string{"play-pause": {"<C-p>"}}, "space"); len(warns) > 0 {
		fmt.Println(warns)
	}

	for _, k := range []string{"5", "j"} {
		m, _ = m.Update(press(k))
	}
	fmt.Println("cursor:", m.cursor)

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	for _, h := range m.whichKey[:3] {
		if h.Group {
			h.Label += "…"
		}
		fmt.Printf("%s  %s\n", h.Key, h.Label)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	for _, k := range []string{"/", "q"} {
		m, _ = m.Update(press(k))
	}
	fmt.Printf("typed %q, still in %s\n", m.typed, m.focus)
	fmt.Println("help keys:", m.keys.KeysFor(keymap.Help))

	// Output:
	// cursor: 5
	// a  AirPlay
	// b  Sidebar
	// g  Go to…
	// typed "q", still in Input
	// help keys: [? space ?]
}
