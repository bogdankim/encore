package keymap

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestToken(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		{"letter", tea.KeyPressMsg{Code: 'j', Text: "j"}, "j"},
		{"shifted letter", tea.KeyPressMsg{Code: 'g', ShiftedCode: 'G', Text: "G", Mod: tea.ModShift}, "G"},
		{"shift letter without text", tea.KeyPressMsg{Code: 'g', Mod: tea.ModShift}, "G"},
		{"caps lock", tea.KeyPressMsg{Code: 'j', Text: "j", Mod: tea.ModCapsLock}, "j"},
		{"question mark", tea.KeyPressMsg{Code: '/', ShiftedCode: '?', Text: "?", Mod: tea.ModShift}, "?"},
		{"colon", tea.KeyPressMsg{Code: ':', Text: ":"}, ":"},
		{"less than", tea.KeyPressMsg{Code: '<', Text: "<"}, "<"},
		{"bracket", tea.KeyPressMsg{Code: ']', Text: "]"}, "]"},
		{"space", tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, "space"},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, "enter"},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}, "esc"},
		{"tab", tea.KeyPressMsg{Code: tea.KeyTab}, "tab"},
		{"shift+tab", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "shift+tab"},
		{"ctrl+d", tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, "ctrl+d"},
		{"alt+x", tea.KeyPressMsg{Code: 'x', Mod: tea.ModAlt}, "alt+x"},
		{"down", tea.KeyPressMsg{Code: tea.KeyDown}, "down"},
		{"pgdown", tea.KeyPressMsg{Code: tea.KeyPgDown}, "pgdown"},
		{"home", tea.KeyPressMsg{Code: tea.KeyHome}, "home"},
		{"digit", tea.KeyPressMsg{Code: '5', Text: "5"}, "5"},
	}
	for _, tt := range tests {
		if got := Token(tt.msg); got != tt.want {
			t.Errorf("%s: Token = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestParseSequence(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"j", []string{"j"}},
		{"gg", []string{"g", "g"}},
		{"g g", []string{"g", "g"}},
		{"G", []string{"G"}},
		{"<leader>ss", []string{"space", "s", "s"}},
		{"<leader><leader>", []string{"space", "space"}},
		{"<Leader>sal", []string{"space", "s", "a", "l"}},
		{"space s s", []string{"space", "s", "s"}},
		{"<C-d>", []string{"ctrl+d"}},
		{"<c-D>", []string{"ctrl+d"}},
		{"ctrl+d", []string{"ctrl+d"}},
		{"<space>", []string{"space"}},
		{"<esc>", []string{"esc"}},
		{"<CR>", []string{"enter"}},
		{"<S-Tab>", []string{"shift+tab"}},
		{"shift+tab", []string{"shift+tab"}},
		{"<S-g>", []string{"G"}},
		{"<A-J>", []string{"alt+shift+j"}},
		{"<lt>", []string{"<"}},
		{"<", []string{"<"}},
		{">", []string{">"}},
		{"<<", []string{"<", "<"}},
		{"+", []string{"+"}},
		{"-", []string{"-"}},
		{"ctrl++", []string{"ctrl++"}},
		{"<C-->", []string{"ctrl+-"}},
		{"pgdown", []string{"pgdown"}},
		{"<PageDown>", []string{"pgdown"}},
		{"End", []string{"end"}},
		{"bs", []string{"b", "s"}},
		{"<BS>", []string{"backspace"}},
		{"<leader>?", []string{"space", "?"}},
		{"<F5>", []string{"f5"}},
	}
	for _, tt := range tests {
		got, err := ParseSequence(tt.in, "space")
		if err != nil {
			t.Errorf("ParseSequence(%q): %v", tt.in, err)
			continue
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("ParseSequence(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseSequenceErrors(t *testing.T) {
	for _, in := range []string{"", "   ", "<foo>", "<X-a>", "<C-foo>"} {
		if got, err := ParseSequence(in, "space"); err == nil {
			t.Errorf("ParseSequence(%q) = %q, want error", in, got)
		}
	}
	if _, err := ParseSequence("<leader>s", ""); err == nil {
		t.Error("ParseSequence with <leader> and no leader: want error")
	}
}

func TestTokenMatchesParse(t *testing.T) {
	// What a terminal sends must equal what the config notation parses to.
	pairs := []struct {
		msg tea.KeyPressMsg
		cfg string
	}{
		{tea.KeyPressMsg{Code: 'g', Text: "G", Mod: tea.ModShift}, "G"},
		{tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, "<C-d>"},
		{tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "<S-Tab>"},
		{tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, "<space>"},
		{tea.KeyPressMsg{Code: tea.KeyEnter}, "<CR>"},
		{tea.KeyPressMsg{Code: tea.KeyPgUp}, "<PageUp>"},
		{tea.KeyPressMsg{Code: 'j', Mod: tea.ModAlt | tea.ModShift}, "<A-J>"},
	}
	for _, p := range pairs {
		toks, err := ParseSequence(p.cfg, "space")
		if err != nil {
			t.Fatal(err)
		}
		if got := Token(p.msg); len(toks) != 1 || toks[0] != got {
			t.Errorf("%q parses to %q, terminal sends %q", p.cfg, toks, got)
		}
	}
}

func TestDisplay(t *testing.T) {
	tests := []struct {
		toks []string
		want string
	}{
		{[]string{"space", "s", "s"}, "space s s"},
		{[]string{"g", "g"}, "g g"},
		{[]string{"ctrl+d"}, "ctrl+d"},
		{[]string{"down"}, "↓"},
		{[]string{"up"}, "↑"},
		{[]string{"pgdown"}, "PgDn"},
		{[]string{"home"}, "Home"},
		{[]string{"shift+tab"}, "shift+tab"},
	}
	for _, tt := range tests {
		if got := Display(tt.toks); got != tt.want {
			t.Errorf("Display(%q) = %q, want %q", tt.toks, got, tt.want)
		}
	}
}

func TestLookupAction(t *testing.T) {
	tests := []struct {
		name string
		want Action
		ok   bool
	}{
		{"search.songs", SearchSongs, true},
		{"player.playPause", PlayerPlayPause, true},
		{"Player.PlayPause", PlayerPlayPause, true},
		{"play-pause", PlayerPlayPause, true},
		{"quit", Quit, true},
		{"songs", "", false}, // search.songs and go.songs
		{"shuffle", PlayerShuffle, true},
		{"shuffle-all", PageShuffle, true},
		{"page.playAll", PagePlay, true},
		{"open.artistInAppleMusic", OpenArtistInCatalog, true},
		{"artist-in-library", OpenArtistInLibrary, true},
		{"nope", "", false},
	}
	for _, tt := range tests {
		got, ok := LookupAction(tt.name)
		if got != tt.want || ok != tt.ok {
			t.Errorf("LookupAction(%q) = %q, %v; want %q, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestEveryActionHasInfo(t *testing.T) {
	for _, a := range actions {
		if a.Name == "" || !slices.Contains(Groups, a.Group) {
			t.Errorf("%s: desc %q group %q", a.Action, a.Name, a.Group)
		}
	}
	for _, d := range defaults {
		if _, ok := Info(d.action); !ok {
			t.Errorf("default binding for unknown action %s", d.action)
		}
	}
}
