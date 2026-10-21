package keymap

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// feed sends a space-separated list of tokens and returns the last result.
func feed(e *Engine, ctx Context, toks string) Result {
	var r Result
	for _, t := range strings.Fields(toks) {
		r = e.Feed(ctx, t)
	}
	return r
}

func TestDefaultsBuildCleanly(t *testing.T) {
	e := &Engine{TimeoutDuration: time.Second, leader: DefaultLeader}
	if warns := e.build(); len(warns) > 0 {
		t.Fatalf("default keymap warnings:\n%s", strings.Join(warns, "\n"))
	}
}

func TestDefaultSequences(t *testing.T) {
	tests := []struct {
		ctx  Context
		keys string
		want Action
	}{
		{List, "j", NavDown},
		{List, "down", NavDown},
		{List, "g g", NavTop},
		{List, "G", NavBottom},
		{List, "ctrl+d", NavHalfDown},
		{List, "pgdown", NavPageDown},
		{List, "enter", Open},
		{List, "/", Filter},
		{List, "a", QueueAdd},
		{List, "A", QueuePlayNext},
		{Global, "p", PlayerPlayPause},
		{Global, "space space", PlayerPlayPause},
		{Global, "space s s", SearchSongs},
		{Global, "space s a l", SearchAlbums},
		{Global, "space s a r", SearchArtists},
		{Global, "space s p", SearchPlaylists},
		{Global, "space s m", SearchCatalog},
		{Global, "space /", SearchAll},
		{Global, "space l", PaneLyrics},
		{Global, "space L", LyricsFullscreen},
		{Global, "space g a r", GoArtists},
		{Global, "space g a l", GoAlbums},
		{Global, "space g n", GoNowPlaying},
		{Global, "space ,", SettingsLastfm},
		{Global, "?", Help},
		{Global, "space ?", Help},
		{Global, ":", Palette},
		{Global, "ctrl+k", Palette},
		{Global, "<", PlayerSeekBackLong},
		{Global, ">", PlayerSeekFwdLong},
		{Global, "q", Quit},
		{Queue, "J", QueueMoveDown},
		{Queue, "K", QueueMoveUp},
		{Queue, "x", QueueRemove},
		{Lyrics, "}", LyricsOffsetUp},
		{Lyrics, "{", LyricsOffsetDown},
		{Lyrics, "-", PlayerVolDown},
		{Lyrics, "=", PlayerVolUp},
		{Global, "-", PlayerVolDown},
		{Sidebar, "m", Menu},
		{List, "P", PagePlay},
		{List, "S", PageShuffle},
		{List, "p", PlayerPlayPause},
		{List, "s", PlayerShuffle},
		{Global, "space o a l", OpenAlbumInCatalog},
		{Global, "space o a r", OpenArtistInCatalog},
		{Global, "space o l l", OpenAlbumInLibrary},
		{Global, "space o l r", OpenArtistInLibrary},
	}
	for _, tt := range tests {
		e := New()
		r := feed(e, tt.ctx, tt.keys)
		if r.Action != tt.want || !r.Consumed || r.Pending {
			t.Errorf("%s %q = %+v, want %s", tt.ctx, tt.keys, r, tt.want)
		}
		if e.Pending() {
			t.Errorf("%s %q left the engine pending", tt.ctx, tt.keys)
		}
	}
}

func TestContextFallback(t *testing.T) {
	e := New()
	// Queue inherits List (f) and Global (j).
	if r := e.Feed(Queue, "f"); r.Action != TrackFavorite {
		t.Errorf("Queue f = %q, want %s", r.Action, TrackFavorite)
	}
	if r := e.Feed(Queue, "j"); r.Action != NavDown {
		t.Errorf("Queue j = %q, want %s", r.Action, NavDown)
	}
	// Sidebar and Lyrics don't get List bindings.
	if r := e.Feed(Sidebar, "f"); r.Consumed {
		t.Errorf("Sidebar f = %+v, want unconsumed", r)
	}
	// Queue-only keys don't leak into List.
	if r := e.Feed(List, "x"); r.Consumed {
		t.Errorf("List x = %+v, want unconsumed", r)
	}
}

func TestLeaderHints(t *testing.T) {
	e := New()
	r := e.Feed(Global, "space")
	if !r.Pending || !r.Consumed || r.Action != "" || r.Keys != "space" {
		t.Fatalf("space = %+v", r)
	}
	got := map[string]Hint{}
	var order []string
	for _, h := range r.Hints {
		got[h.Key] = h
		order = append(order, h.Key)
	}
	want := map[string]Hint{
		"s":     {Key: "s", Label: "Search", Group: true},
		"g":     {Key: "g", Label: "Go to", Group: true},
		"l":     {Key: "l", Label: "Lyrics", Action: PaneLyrics},
		"L":     {Key: "L", Label: "Full-Screen Lyrics", Action: LyricsFullscreen},
		"space": {Key: "space", Label: "Play / Pause", Action: PlayerPlayPause},
		"?":     {Key: "?", Label: "Help", Action: Help},
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("hint %q = %+v, want %+v", k, got[k], w)
		}
	}
	if i, j := slices.Index(order, "l"), slices.Index(order, "L"); i < 0 || j != i+1 {
		t.Errorf("hint order %q: want l right before L", order)
	}
	if order[len(order)-1] != "space" {
		t.Errorf("hint order %q: want named keys last", order)
	}

	r = e.Feed(Global, "s")
	if !r.Pending || r.Keys != "space s" {
		t.Fatalf("space s = %+v", r)
	}
	want2 := []Hint{
		{Key: "a", Label: "Albums / Artists", Group: true},
		{Key: "m", Label: "Apple Music", Action: SearchCatalog},
		{Key: "p", Label: "Playlists", Action: SearchPlaylists},
		{Key: "s", Label: "Songs", Action: SearchSongs},
	}
	if !slices.Equal(r.Hints, want2) {
		t.Errorf("space s hints = %+v, want %+v", r.Hints, want2)
	}

	r = e.Feed(Global, "a")
	want3 := []Hint{
		{Key: "l", Label: "Albums", Action: SearchAlbums},
		{Key: "r", Label: "Artists", Action: SearchArtists},
	}
	if !slices.Equal(r.Hints, want3) {
		t.Errorf("space s a hints = %+v, want %+v", r.Hints, want3)
	}
	if r = e.Feed(Global, "r"); r.Action != SearchArtists {
		t.Errorf("space s a r = %+v", r)
	}
}

func TestOpenInHints(t *testing.T) {
	e := New()
	r := e.Feed(Global, "space")
	var group Hint
	for _, h := range r.Hints {
		if h.Key == "o" {
			group = h
		}
	}
	if group != (Hint{Key: "o", Label: "Open in", Group: true}) {
		t.Fatalf("space o hint = %+v", group)
	}
	r = e.Feed(Global, "o")
	want := []Hint{
		{Key: "a", Label: "Apple Music", Group: true},
		{Key: "l", Label: "Library", Group: true},
		{Key: "m", Label: "Music.app", Action: MusicOpen},
	}
	if !slices.Equal(r.Hints, want) {
		t.Errorf("space o hints = %+v, want %+v", r.Hints, want)
	}
	// a pure prefix: nothing runs while it waits for the next key
	if r.Ambiguous || r.Action != "" || !r.Pending {
		t.Errorf("space o = %+v, want pending", r)
	}
	r = e.Feed(Global, "a")
	if r.Ambiguous || !r.Pending {
		t.Errorf("space o a = %+v, want a pure prefix", r)
	}
	want = []Hint{
		{Key: "l", Label: "Album", Action: OpenAlbumInCatalog},
		{Key: "r", Label: "Artist", Action: OpenArtistInCatalog},
	}
	if !slices.Equal(r.Hints, want) {
		t.Errorf("space o a hints = %+v, want %+v", r.Hints, want)
	}
	if r = e.Feed(Global, "r"); r.Action != OpenArtistInCatalog {
		t.Errorf("space o a r = %+v", r)
	}
	feed(e, Global, "space o")
	r = e.Feed(Global, "l")
	want = []Hint{
		{Key: "l", Label: "Album", Action: OpenAlbumInLibrary},
		{Key: "r", Label: "Artist", Action: OpenArtistInLibrary},
	}
	if !slices.Equal(r.Hints, want) {
		t.Errorf("space o l hints = %+v, want %+v", r.Hints, want)
	}
	if r = e.Feed(Global, "l"); r.Action != OpenAlbumInLibrary {
		t.Errorf("space o l l = %+v", r)
	}
}

// P and S play and shuffle the page in lists; p and s stay play / pause
// and the shuffle toggle everywhere else.
func TestPageKeysOnlyInLists(t *testing.T) {
	e := New()
	for _, ctx := range []Context{Sidebar, Lyrics} {
		if r := e.Feed(ctx, "S"); r.Consumed {
			t.Errorf("%s S = %+v, want unbound", ctx, r)
		}
	}
	if r := e.Feed(Queue, "P"); r.Action != PagePlay {
		t.Errorf("Queue P = %+v (Queue inherits List)", r)
	}
	if got := e.KeysFor(PageShuffle); !slices.Equal(got, []string{"S"}) {
		t.Errorf("KeysFor(PageShuffle) = %q", got)
	}
}

func TestCounts(t *testing.T) {
	e := New()
	r := e.Feed(List, "5")
	if !r.Pending || !r.Consumed || r.Keys != "5" {
		t.Fatalf("5 = %+v", r)
	}
	if r = e.Feed(List, "j"); r.Action != NavDown || r.Count != 5 {
		t.Errorf("5j = %+v", r)
	}
	if r = feed(e, List, "1 2 k"); r.Action != NavUp || r.Count != 12 {
		t.Errorf("12k = %+v", r)
	}
	if r = feed(e, List, "1 0 j"); r.Count != 10 {
		t.Errorf("10j = %+v", r)
	}
	// Counts carry through a multi-key motion.
	r = feed(e, List, "3 g")
	if !r.Pending || r.Keys != "3 g" {
		t.Errorf("3g = %+v", r)
	}
	if r = e.Feed(List, "g"); r.Action != NavTop || r.Count != 3 {
		t.Errorf("3gg = %+v", r)
	}
	// Counts don't apply to other actions.
	if r = feed(e, List, "4 p"); r.Action != PlayerPlayPause || r.Count != 1 {
		t.Errorf("4p = %+v", r)
	}
	if r = e.Feed(List, "j"); r.Count != 1 {
		t.Errorf("plain j count = %d", r.Count)
	}
	// "0" alone isn't a count and is unbound.
	if r = e.Feed(List, "0"); r.Consumed || e.Pending() {
		t.Errorf("0 = %+v", r)
	}
	// No counts in text input.
	if r = e.Feed(Input, "5"); r.Consumed {
		t.Errorf("Input 5 = %+v", r)
	}
	// A count followed by an unbound key is dropped.
	if r = feed(e, List, "5 z"); r.Consumed || e.Pending() {
		t.Errorf("5z = %+v", r)
	}
	if r = feed(e, List, "5 esc"); !r.Consumed || r.Action != "" || e.Pending() {
		t.Errorf("5 esc = %+v", r)
	}
}

func TestBoundDigitIsNotACount(t *testing.T) {
	e := New()
	if w := e.Apply(map[string][]string{"go.songs": {"1"}}, ""); len(w) > 0 {
		t.Fatal(w)
	}
	if r := e.Feed(List, "1"); r.Action != GoSongs {
		t.Errorf("1 = %+v, want %s", r, GoSongs)
	}
	// Other digits still count, and a started count takes any digit.
	if r := feed(e, List, "2 1 j"); r.Action != NavDown || r.Count != 21 {
		t.Errorf("21j = %+v", r)
	}
}

func TestEscCancels(t *testing.T) {
	e := New()
	feed(e, Global, "space s")
	r := e.Feed(Global, "esc")
	if !r.Consumed || r.Action != "" || r.Pending || e.Pending() {
		t.Errorf("space s esc = %+v", r)
	}
	// esc with nothing pending is a normal key.
	if r = e.Feed(List, "esc"); r.Action != Back {
		t.Errorf("esc = %+v, want %s", r, Back)
	}
}

func TestUnknownContinuation(t *testing.T) {
	e := New()
	feed(e, Global, "space")
	r := e.Feed(Global, "z")
	if r.Consumed || r.Action != "" || e.Pending() {
		t.Errorf("space z = %+v", r)
	}
	// The engine starts fresh afterwards.
	if r = e.Feed(Global, "j"); r.Action != NavDown {
		t.Errorf("j after reset = %+v", r)
	}
	if r = feed(e, List, "g x"); r.Consumed || e.Pending() {
		t.Errorf("g x = %+v", r)
	}
}

func TestContextChangeDropsPending(t *testing.T) {
	e := New()
	feed(e, List, "g")
	if r := e.Feed(Queue, "g"); !r.Pending {
		t.Errorf("g in a new context should start a new sequence, got %+v", r)
	}
}

func TestTimeoutOnAmbiguity(t *testing.T) {
	e := New()
	warns := e.Apply(map[string][]string{"go.songs": {"g"}}, "")
	if len(warns) != 1 || !strings.Contains(warns[0], "is the start of g g (nav.top)") {
		t.Fatalf("warnings = %q", warns)
	}
	r := e.Feed(List, "g")
	if !r.Pending || !r.Ambiguous || r.Action != "" {
		t.Fatalf("g = %+v", r)
	}
	if r = e.Timeout(); r.Action != GoSongs || e.Pending() {
		t.Errorf("timeout after g = %+v", r)
	}
	// Completing the longer sequence before the timeout wins.
	feed(e, List, "g")
	if r = e.Feed(List, "g"); r.Action != NavTop {
		t.Errorf("g g = %+v", r)
	}
	// A key that continues nothing fires the complete prefix.
	feed(e, List, "g")
	if r = e.Feed(List, "z"); r.Action != GoSongs || !r.Consumed {
		t.Errorf("g z = %+v", r)
	}
}

func TestTimeoutOnPlainPrefixDrops(t *testing.T) {
	e := New()
	if r := feed(e, Global, "space s"); !r.Pending || r.Ambiguous {
		t.Errorf("space s = %+v, want pending without a timer", r)
	}
	if r := e.Feed(List, "5"); !r.Pending || r.Ambiguous {
		t.Errorf("5 = %+v, want pending without a timer", r)
	}
	e.Reset()
	feed(e, Global, "space s")
	if r := e.Timeout(); r.Action != "" || e.Pending() {
		t.Errorf("timeout = %+v", r)
	}
	if r := e.Timeout(); r.Consumed {
		t.Errorf("timeout with nothing pending = %+v", r)
	}
}

func TestHandleTimeoutIgnoresStaleTimers(t *testing.T) {
	e := New()
	e.Apply(map[string][]string{"go.songs": {"g"}}, "")
	e.Feed(List, "g")
	stale := TimeoutMsg{gen: e.gen}
	e.Feed(List, "esc")
	e.Feed(List, "g")
	if r := e.HandleTimeout(stale); r.Action != "" || !e.Pending() {
		t.Errorf("stale timeout = %+v", r)
	}
	fresh := TimeoutMsg{gen: e.gen}
	if r := e.HandleTimeout(fresh); r.Action != GoSongs {
		t.Errorf("fresh timeout = %+v", r)
	}
	if e.TimeoutCmd() == nil {
		t.Error("TimeoutCmd = nil")
	}
}

func TestInputPassThrough(t *testing.T) {
	e := New()
	tests := []struct {
		key  string
		want Action
	}{
		{"esc", InputCancel},
		{"enter", InputSubmit},
		{"tab", InputNextScope},
		{"shift+tab", InputPrevScope},
		{"down", InputNext},
		{"ctrl+j", InputNext},
		{"ctrl+n", InputNext},
		{"up", InputPrev},
		{"ctrl+k", InputPrev},
		{"ctrl+p", InputPrev},
	}
	for _, tt := range tests {
		if r := e.Feed(Input, tt.key); r.Action != tt.want {
			t.Errorf("Input %s = %+v, want %s", tt.key, r, tt.want)
		}
	}
	for _, k := range []string{"j", "q", "space", "g", "?", ":", "left", "backspace", "ctrl+w", "ctrl+u", "1"} {
		if r := e.Feed(Input, k); r.Consumed || e.Pending() {
			t.Errorf("Input %s = %+v, want pass-through", k, r)
		}
	}
}

func TestOverrides(t *testing.T) {
	e := New()
	warns := e.Apply(map[string][]string{
		"play-pause":   {"<C-p>", "<leader><leader>"},
		"search.songs": {"<leader>f"},
	}, "")
	if len(warns) > 0 {
		t.Fatal(warns)
	}
	if r := e.Feed(Global, "p"); r.Consumed {
		t.Errorf("p after override = %+v, want unbound", r)
	}
	if r := e.Feed(Global, "ctrl+p"); r.Action != PlayerPlayPause {
		t.Errorf("ctrl+p = %+v", r)
	}
	if r := feed(e, Global, "space f"); r.Action != SearchSongs {
		t.Errorf("space f = %+v", r)
	}
	// The old sequence is gone and the s group keeps its other members.
	r := feed(e, Global, "space s")
	for _, h := range r.Hints {
		if h.Key == "s" {
			t.Errorf("space s still offers s: %+v", r.Hints)
		}
	}
	e.Reset()
	if got := e.KeysFor(PlayerPlayPause); !slices.Equal(got, []string{"ctrl+p", "space space"}) {
		t.Errorf("KeysFor(playPause) = %q", got)
	}
}

func TestOverrideKeepsContext(t *testing.T) {
	e := New()
	e.Apply(map[string][]string{"queue.remove": {"d d"}}, "")
	if r := feed(e, Queue, "d d"); r.Action != QueueRemove {
		t.Errorf("Queue d d = %+v", r)
	}
	if r := e.Feed(List, "d"); r.Consumed {
		t.Errorf("List d = %+v, want unbound", r)
	}
}

func TestUnbind(t *testing.T) {
	e := New()
	e.Apply(map[string][]string{"quit": {}}, "")
	if r := e.Feed(Global, "q"); r.Consumed {
		t.Errorf("q after unbinding = %+v", r)
	}
	if got := e.KeysFor(Quit); len(got) != 0 {
		t.Errorf("KeysFor(quit) = %q", got)
	}
}

func TestLeaderChange(t *testing.T) {
	e := New()
	if w := e.Apply(nil, `\`); len(w) > 0 {
		t.Fatal(w)
	}
	if r := feed(e, Global, `\ s s`); r.Action != SearchSongs {
		t.Errorf(`\ s s = %+v`, r)
	}
	if r := feed(e, Global, `\ \`); r.Action != PlayerPlayPause {
		t.Errorf(`\ \ = %+v`, r)
	}
	if r := e.Feed(Global, "space"); r.Consumed {
		t.Errorf("space after the leader moved = %+v", r)
	}
	if got := e.KeysFor(SearchSongs); !slices.Equal(got, []string{`\ s s`}) {
		t.Errorf("KeysFor(search.songs) = %q", got)
	}

	// A leader that collides with defaults is reported.
	if w := New().Apply(nil, ","); len(w) == 0 {
		t.Error("leader , : want warnings (, is seek back)")
	}

	e2 := New()
	if w := e2.Apply(nil, "<leader>x"); len(w) != 1 || e2.Leader() != DefaultLeader {
		t.Errorf("bad leader: warnings %q, leader %q", w, e2.Leader())
	}
}

func TestConflictWarnings(t *testing.T) {
	e := New()
	warns := e.Apply(map[string][]string{
		"search.songs":    {"p"},
		"go.albums":       {"Z"},
		"go.artists":      {"Z"},
		"not.an.action":   {"x"},
		"go.favorites":    {"<bogus>"},
		"queue.remove":    {"j"},
		"lyrics.offsetUp": {"=", "+"},
	}, "")
	wantParts := []string{
		`p in Global is bound to search.songs and player.playPause; keeping search.songs`,
		`Z in Global is bound to go.albums and go.artists; keeping go.albums`,
		`unknown action "not.an.action"`,
		`go.favorites: "<bogus>": unknown key <bogus>`,
		`j for queue.remove in Queue hides nav.down from Global`,
		`= for lyrics.offsetUp in Lyrics hides player.volUp from Global`,
	}
	for _, w := range wantParts {
		if !slices.Contains(warns, w) {
			t.Errorf("missing warning %q in\n%s", w, strings.Join(warns, "\n"))
		}
	}
	if len(warns) != len(wantParts) {
		t.Errorf("got %d warnings, want %d:\n%s", len(warns), len(wantParts), strings.Join(warns, "\n"))
	}
	if r := e.Feed(Global, "p"); r.Action != SearchSongs {
		t.Errorf("p = %+v", r)
	}
	if r := e.Feed(Global, "Z"); r.Action != GoAlbums {
		t.Errorf("Z = %+v", r)
	}
	if got := e.KeysFor(PlayerPlayPause); !slices.Equal(got, []string{"space space"}) {
		t.Errorf("KeysFor(playPause) = %q", got)
	}
}

func TestPrefixShadowWarning(t *testing.T) {
	e := New()
	warns := e.Apply(map[string][]string{"sidebar.toggle": {"<leader>"}}, "")
	want := "space (sidebar.toggle) is the start of space space (player.playPause) and 25 more; sidebar.toggle fires after the 1s timeout"
	if len(warns) != 1 || warns[0] != want {
		t.Errorf("warnings = %q, want [%q]", warns, want)
	}
}

func TestBindings(t *testing.T) {
	e := New()
	find := func(bs []BindingInfo, a Action) *BindingInfo {
		for i := range bs {
			if bs[i].Action == a {
				return &bs[i]
			}
		}
		return nil
	}

	g := e.Bindings(Global)
	if b := find(g, NavDown); b == nil || !slices.Equal(b.Keys, []string{"j", "↓"}) || b.Group != GroupNavigation || b.Name != "Down" {
		t.Errorf("Global nav.down = %+v", b)
	}
	if b := find(g, SearchAlbums); b == nil || !slices.Equal(b.Keys, []string{"space s a l"}) {
		t.Errorf("Global search.albums = %+v", b)
	}
	if b := find(g, NavTop); b == nil || !slices.Equal(b.Keys, []string{"g g", "Home"}) {
		t.Errorf("Global nav.top = %+v", b)
	}
	if b := find(g, NavHalfDown); b == nil || !slices.Equal(b.Keys, []string{"ctrl+d"}) {
		t.Errorf("Global nav.halfDown = %+v", b)
	}
	if find(g, QueueRemove) != nil || find(g, InputSubmit) != nil {
		t.Error("Global bindings include Queue or Input actions")
	}
	// Sorted by group.
	last := -1
	for _, b := range g {
		i := slices.Index(Groups, b.Group)
		if i < last {
			t.Errorf("%s (%s) out of group order", b.Action, b.Group)
		}
		last = i
	}

	q := e.Bindings(Queue)
	if b := find(q, QueueRemove); b == nil || b.Context != Queue {
		t.Errorf("Queue queue.remove = %+v", b)
	}
	if b := find(q, TrackFavorite); b == nil || b.Context != List {
		t.Errorf("Queue track.favorite = %+v", b)
	}

	// The lyrics offset keys leave volume alone.
	l := e.Bindings(Lyrics)
	if b := find(l, PlayerVolDown); b == nil || !slices.Equal(b.Keys, []string{"-"}) {
		t.Errorf("Lyrics volume down = %+v", b)
	}
	if b := find(l, LyricsOffsetDown); b == nil || !slices.Equal(b.Keys, []string{"{"}) {
		t.Errorf("Lyrics offsetDown = %+v", b)
	}
}

func TestKeysFor(t *testing.T) {
	e := New()
	tests := map[Action][]string{
		NavDown:         {"j", "↓"},
		NavPageDown:     {"PgDn"},
		PlayerPlayPause: {"p", "space space"},
		Help:            {"?", "space ?"},
		Palette:         {":", "ctrl+k"},
		Menu:            {"m"},
		InputNext:       {"↓", "ctrl+j", "ctrl+n"},
	}
	for a, want := range tests {
		if got := e.KeysFor(a); !slices.Equal(got, want) {
			t.Errorf("KeysFor(%s) = %q, want %q", a, got, want)
		}
	}
}

func TestEveryActionIsBound(t *testing.T) {
	e := New()
	for _, a := range Actions() {
		if len(e.KeysFor(a.Action)) == 0 {
			t.Errorf("%s has no default keys", a.Action)
		}
	}
}

func TestMusicOpenKeys(t *testing.T) {
	e := New()
	for _, ctx := range []Context{Global, List, Queue, Sidebar} {
		if r := e.Feed(ctx, "M"); r.Action != MusicOpen {
			t.Errorf("M in %v = %+v", ctx, r)
		}
	}
	e.Feed(Global, "space")
	e.Feed(Global, "o")
	if r := e.Feed(Global, "m"); r.Action != MusicOpen {
		t.Errorf("space o m = %+v", r)
	}
	if got := e.KeysFor(MusicOpen); !slices.Equal(got, []string{"M", "space o m"}) {
		t.Errorf("KeysFor(MusicOpen) = %q", got)
	}
}
