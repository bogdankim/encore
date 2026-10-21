package bridge

import (
	"cmp"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
)

// FakeEpoch is "now" for the demo library: every added and last-played date
// falls before it, so the library is identical on every run.
var FakeEpoch = time.Date(2026, 10, 12, 18, 30, 0, 0, time.UTC)

// fakeLibrary is the generated demo library.
type fakeLibrary struct {
	tracks    []SnapshotTrack
	playlists []SnapshotPlaylist
	albums    map[string]bool // album pids
	start     []string        // the queue the player starts paused in
	favorites int             // index of the "Favorite Songs" playlist
}

type gen struct {
	r     *rand.Rand
	pids  map[string]bool
	names map[string]bool
}

func (g *gen) pick(list []string) string { return list[g.r.IntN(len(list))] }
func (g *gen) chance(p float64) bool     { return g.r.Float64() < p }
func (g *gen) between(lo, hi int) int    { return lo + g.r.IntN(hi-lo+1) }

func (g *gen) pid() string {
	for {
		p := fmt.Sprintf("%016X", g.r.Uint64())
		if !g.pids[p] {
			g.pids[p] = true
			return p
		}
	}
}

// weighted returns an index into weights.
func (g *gen) weighted(weights ...float64) int {
	total := 0.0
	for _, w := range weights {
		total += w
	}
	x := g.r.Float64() * total
	for i, w := range weights {
		if x < w {
			return i
		}
		x -= w
	}
	return len(weights) - 1
}

type genArtist struct {
	name, sort, genre, script string
	lower                     bool // stylized all-lowercase
	debut                     int
}

func (g *gen) genre() string {
	w := make([]float64, len(wGenreWeight))
	for i, v := range wGenreWeight {
		w[i] = float64(v)
	}
	return wGenre[g.weighted(w...)]
}

func (g *gen) person() string {
	first := g.pick(wFirst)
	if g.chance(0.15) {
		first = g.pick(wFirstAccented)
	}
	return first + " " + g.pick(wLast)
}

func (g *gen) artist() genArtist {
	a := genArtist{genre: g.genre(), script: "latin"}
	for {
		switch {
		case a.genre == "Classical":
			if g.chance(0.5) {
				a.name = g.pick(wEnsembleA) + " " + g.pick(wEnsembleB)
			} else {
				a.name = g.person()
			}
		case a.genre == "Hip-Hop/Rap" && g.chance(0.6):
			switch g.r.IntN(3) {
			case 0:
				a.name = "Young " + g.pick(wNoun)
			case 1:
				a.name = g.pick(wFirst) + " " + g.pick(wNoun)
			default:
				a.name = strings.ToUpper(g.pick(wSylA)) + g.pick([]string{"", "Z", " DA KID", " 3000", "$"})
			}
		default:
			switch g.weighted(30, 12, 10, 8, 5, 5, 10, 6, 4, 5) {
			case 0:
				a.name = g.person()
			case 1:
				a.name = "The " + g.pick(wAdj) + " " + g.pick(wPlural)
			case 2:
				a.name = g.pick(wAdj) + " " + g.pick(wNoun)
			case 3:
				a.name = g.pick(wNoun) + " " + g.pick(wNoun)
			case 4:
				a.name = g.pick(wPlural) + " of " + g.pick(wPlace)
			case 5:
				a.name = g.pick(wFirst) + " & the " + g.pick(wPlural)
			case 6:
				a.name = g.pick(wSylA) + g.pick(wSylB)
			case 7:
				a.name = strings.ToLower(g.pick(wAdj) + " " + g.pick(wNoun))
				a.lower = true
			case 8:
				a.name = g.pick(wFirst) + " & " + g.pick(wFirst)
			default:
				a.name = g.pick(wAdj) + " " + g.pick(wPlural)
			}
		}
		if !g.names[a.name] {
			g.names[a.name] = true
			break
		}
	}
	decades := []int{1965, 1980, 1990, 2000, 2010, 2020}
	d := g.weighted(10, 10, 15, 20, 25, 20)
	hi := 2026
	if d < len(decades)-1 {
		hi = decades[d+1] - 1
	}
	a.debut = g.between(decades[d], hi)
	return a
}

func (g *gen) trackTitle(a genArtist) string {
	if a.script != "latin" && g.chance(0.7) {
		return g.pick(nlTitles[a.script])
	}
	if a.genre == "Ambient" && g.chance(0.5) {
		return fmt.Sprintf("%s %02d", g.pick(wNoun), g.between(1, 12))
	}
	var t string
	switch g.weighted(12, 16, 8, 4, 4, 5, 4, 3, 4, 3, 2, 0.7) {
	case 0:
		t = g.pick(wNoun)
	case 1:
		t = g.pick(wAdj) + " " + g.pick(wNoun)
	case 2:
		t = g.pick([]string{"Hold Me", "Hold Me Close", "Call You", "Call Me Home", "Wake Me Up", "Wake You Up",
			"Follow Me Home", "Carry Me Home", "Forget Me", "Remember Me", "Leave Me Here", "Find Me", "Find You",
			"Chase You", "Keep Me", "Keep You Close"})
	case 3:
		t = "Don't " + g.pick(wVerb)
	case 4:
		t = g.pick(wNoun) + " in " + g.pick(wPlace)
	case 5:
		t = g.pick(wAdj) + " " + g.pick(wPlural)
	case 6:
		t = "When the " + g.pick(wNoun) + " " + g.pick(wTail)
	case 7:
		t = g.pick(wPlace)
	case 8:
		t = g.pick(wNoun) + " of the " + g.pick(wNoun)
	case 9:
		t = g.pick([]string{"4 AM", "1999", "22", "Room 104", "Exit 9", "Seventeen", "11:11", "Route 66 Revisited"})
	case 10:
		t = g.pick(wVerb) + " Until " + g.pick([]string{"Morning", "Dawn", "It Hurts", "We're Gone"})
	default:
		t = fmt.Sprintf(g.pick(wLongTitles), g.pick(wPlace))
	}
	if a.lower {
		t = strings.ToLower(t)
	}
	return t
}

func (g *gen) albumTitle(a genArtist, n int) string {
	if a.script != "latin" && g.chance(0.65) {
		return g.pick(nlAlbums[a.script])
	}
	var t string
	switch g.weighted(18, 6, 8, 5, 8, 4, 2, 3, 5, 3, 4, 1.5, 3) {
	case 0:
		t = g.pick(wAdj) + " " + g.pick(wNoun)
	case 1:
		t = g.pick(wPlural)
	case 2:
		t = g.pick(wNoun)
	case 3:
		t = "The " + g.pick(wNoun) + " " + g.pick([]string{"Years", "Sessions", "Tapes", "Letters"})
	case 4:
		t = g.pick(wNoun) + " of " + g.pick(wPlural)
	case 5:
		t = g.pick(wPlace)
	case 6:
		t = fmt.Sprintf("%s %d", g.pick(wPlace), g.between(1975, 2024))
	case 7:
		t = "Songs from the " + g.pick(wNoun)
	case 8:
		t = g.pick(wAdj)
	case 9:
		if n > 0 && n < len(wRoman) {
			t = wRoman[n]
		} else {
			t = g.pick(wAdj) + " " + g.pick(wNoun)
		}
	case 10:
		if n == 0 {
			return a.name
		}
		t = g.pick(wVerb)
	case 11:
		t = g.pick(wLongAlbums)
	default:
		t = g.pick(wVerb)
	}
	if a.lower {
		t = strings.ToLower(t)
	}
	return t
}

// classicalPiece returns the movements of one invented work.
func (g *gen) classicalPiece(form string) (titles []string) {
	key := g.pick(wKey)
	no, op := g.between(1, 9), g.between(2, 120)
	moves := g.between(3, 4)
	for i := range moves {
		titles = append(titles, fmt.Sprintf("%s No. %d in %s, Op. %d: %s. %s", form, no, key, op, wRoman[i], g.pick(wTempo)))
	}
	return titles
}

// duration in ms, by genre.
func (g *gen) duration(genre, title string) int64 {
	mean, sd := 222.0, 48.0
	switch genre {
	case "Ambient", "Classical", "Jazz":
		mean, sd = 300, 75
	case "Hip-Hop/Rap", "Pop", "Dance":
		mean, sd = 200, 35
	case "Metal":
		mean, sd = 290, 70
	}
	if strings.Contains(title, "Interlude") {
		mean, sd = 95, 10
	}
	s := mean + g.r.NormFloat64()*sd
	if s > 540 {
		s = 480 + g.r.Float64()*60
	}
	s = math.Max(90, s)
	return int64(s*1000) + int64(g.r.IntN(1000))
}

type albumKind int

const (
	kindAlbum albumKind = iota
	kindSingle
	kindEP
	kindDeluxe
	kindLive
)

// album spec handed to emitAlbum.
type genAlbum struct {
	title       string
	artist      genArtist
	albumArtist string
	year        int
	kind        albumKind
	remaster    bool
	compilation bool
	genre       string
	trackArtist func() genArtist // compilations: a different artist per track
}

func generateLibrary(seed int64) *fakeLibrary {
	g := &gen{
		r:     rand.New(rand.NewPCG(uint64(seed), 0x656e636f7265)), // "encore"
		pids:  map[string]bool{},
		names: map[string]bool{"Various Artists": true},
	}
	lib := &fakeLibrary{albums: map[string]bool{}}

	var artists []genArtist
	for _, nl := range nlArtists {
		a := genArtist{name: nl.name, sort: nl.sort, genre: nl.genre, script: nl.script, debut: g.between(1998, 2022)}
		g.names[a.name] = true
		artists = append(artists, a)
	}
	for len(artists) < 260 {
		artists = append(artists, g.artist())
	}
	g.r.Shuffle(len(artists), func(i, j int) { artists[i], artists[j] = artists[j], artists[i] })

	for _, a := range artists {
		n := []int{1, 2, 3, 4, 5, 6, 8}[g.weighted(45, 28, 14, 7, 3, 2, 1)]
		year := a.debut
		for i := range n {
			if i > 0 {
				year += g.between(1, 4)
			}
			if year > 2026 {
				break
			}
			spec := genAlbum{artist: a, albumArtist: a.name, year: year, genre: a.genre}
			spec.kind = []albumKind{kindAlbum, kindSingle, kindEP, kindDeluxe, kindLive}[g.weighted(70, 14, 9, 4, 3)]
			if a.genre == "Classical" && (spec.kind == kindSingle || spec.kind == kindEP) {
				spec.kind = kindAlbum
			}
			spec.remaster = year < 1985 && g.chance(0.4)
			spec.title = g.albumTitle(a, i)
			lib.emitAlbum(g, spec, artists)
		}
	}

	compilations := []struct{ title, genre string }{
		{"Coastal Drive, Vol. 2", "Pop"},
		{"Late Bloomers: Indie Gems 2014", "Alternative"},
		{"Northern Lights: A Collection", "Electronic"},
		{"Sunday Soul Kitchen", "R&B/Soul"},
		{"Electronic Afternoons", "Electronic"},
		{"Sounds of the Underground '99", "Alternative"},
		{"Paper Moon Motel (Original Motion Picture Soundtrack)", "Soundtrack"},
		{"Summer Sessions 2019", "Pop"},
		{"Jazz After Dark", "Jazz"},
		{"Hôtel Lumière: Music from the Series", "Soundtrack"},
		{"Kitchen Disco Classics", "Dance"},
		{"Rainy Day Folk", "Folk"},
	}
	for _, c := range compilations {
		lib.emitAlbum(g, genAlbum{
			title: c.title, albumArtist: "Various Artists", year: g.between(1995, 2025),
			kind: kindAlbum, compilation: true, genre: c.genre,
			trackArtist: func() genArtist { return artists[g.r.IntN(len(artists))] },
		}, artists)
	}

	// Tracks that exist only inside the subscription playlist.
	var outside []string
	for range 12 {
		a := g.artist()
		for range g.between(2, 4) {
			title := g.trackTitle(a)
			t := lib.trackFor(g, a, a.name, title, title+" - Single", g.pid(), g.between(2018, 2026), a.genre)
			t.Track, t.TrackCount = 1, 1
			t.InLibrary = false
			t.Kind = "subscription"
			t.Plays, t.LastPlayed, t.Favorited, t.Added = 0, 0, false, FakeEpoch.Unix()-int64(g.between(3600, 86400*30))
			lib.albums[t.AlbumPID] = true
			lib.tracks = append(lib.tracks, t)
			outside = append(outside, t.PID)
		}
	}

	slices.SortStableFunc(lib.tracks, func(a, b SnapshotTrack) int { return cmp.Compare(a.Added, b.Added) })
	lib.buildPlaylists(g, outside)
	lib.pickStart()
	return lib
}

// emitAlbum appends one album's tracks.
func (lib *fakeLibrary) emitAlbum(g *gen, spec genAlbum, artists []genArtist) {
	a := spec.artist
	var n int
	switch spec.kind {
	case kindSingle:
		n = 1
	case kindEP:
		n = g.between(4, 6)
	case kindDeluxe:
		n = g.between(18, 22)
	default:
		n = g.between(10, 18)
	}
	if spec.compilation {
		n = g.between(14, 20)
	}

	var titles, composers []string
	if spec.genre == "Classical" {
		composer, form := g.person(), g.pick(wForm)
		spec.title = composer[strings.LastIndexByte(composer, ' ')+1:] + ": " + plural(form)
		for len(titles) < n {
			for _, t := range g.classicalPiece(form) {
				titles = append(titles, t)
				composers = append(composers, composer)
			}
		}
		titles, composers = titles[:n], composers[:n]
	}

	var trackArtists []genArtist
	seen := map[string]bool{}
	for len(titles) < n {
		ta := a
		if spec.trackArtist != nil {
			ta = spec.trackArtist()
		}
		t := g.trackTitle(ta)
		if seen[t] {
			continue
		}
		seen[t] = true
		titles = append(titles, t)
		trackArtists = append(trackArtists, ta)
	}
	if n >= 10 && spec.genre != "Classical" && g.chance(0.25) {
		i := g.between(n/3, 2*n/3)
		titles[i] = g.pick([]string{"Interlude", g.pick(wNoun) + " (Interlude)", "Intermission"})
	}

	album := spec.title
	switch spec.kind {
	case kindSingle:
		album = titles[0] + " - Single"
	case kindEP:
		album += " - EP"
	case kindDeluxe:
		album += " (Deluxe Edition)"
	case kindLive:
		album = "Live at the " + g.pick(wPlace) + " " + g.pick(wHall)
	}
	if spec.remaster {
		album += " (Remastered)"
	}

	albumPID := g.pid()
	lib.albums[albumPID] = true
	kind := []string{"subscription", "purchased", "uploaded", "local"}[g.weighted(72, 14, 9, 5)]
	art := kind == "subscription" || kind == "purchased" || g.chance(0.85)

	// Added: skewed towards recent, never before the release year.
	epoch := FakeEpoch.Unix()
	span := float64(6 * 365 * 86400)
	if rel := time.Date(spec.year, 1, 1, 0, 0, 0, 0, time.UTC).Unix(); epoch-rel < int64(span) {
		span = float64(epoch - rel)
	}
	added := epoch - int64(span*math.Pow(g.r.Float64(), 1.6)) - 3600
	popularity := math.Exp(g.r.NormFloat64()*0.9) * 6

	discs := 1
	if spec.kind == kindDeluxe {
		discs = 2
	}
	perDisc := (n + discs - 1) / discs
	partial := n >= 8 && g.chance(0.1)

	for i, title := range titles {
		ta := a
		if i < len(trackArtists) {
			ta = trackArtists[i]
		}
		if partial && g.chance(0.5) && i > 0 {
			continue
		}
		if spec.genre != "Classical" && !strings.Contains(title, "Interlude") {
			if g.chance(0.06) {
				other := artists[g.r.IntN(len(artists))].name
				if other != ta.name {
					title += " (feat. " + other + ")"
				}
			} else if g.chance(0.02) {
				title += " (Acoustic)"
			}
		}
		switch {
		case spec.kind == kindLive:
			title += " (Live)"
		case spec.remaster:
			title += fmt.Sprintf(" (Remastered %d)", g.between(2005, 2022))
		}

		artistName := ta.name
		collab := !spec.compilation && g.chance(0.02)
		if collab {
			artistName += " & " + artists[g.r.IntN(len(artists))].name
		}
		genre := spec.genre
		if spec.compilation && genre != "Soundtrack" {
			genre = ta.genre
		}
		t := lib.trackFor(g, ta, artistName, title, album, albumPID, spec.year, genre)
		t.AlbumArtist = spec.albumArtist
		if !spec.compilation && !collab && spec.kind != kindSingle && g.chance(0.4) {
			t.AlbumArtist = "" // often untagged; display falls back to the artist
		}
		t.Compilation = spec.compilation
		if spec.compilation {
			t.SortAlbumArtist = ""
		}
		t.Disc, t.DiscCount = i/perDisc+1, discs
		t.Track = i%perDisc + 1
		t.TrackCount = min(perDisc, n-(t.Disc-1)*perDisc)
		if composers != nil {
			t.Composer = composers[i]
		}
		t.Kind, t.Art = kind, art
		t.Added = added + int64(i)
		t.Plays = int(popularity * g.r.ExpFloat64())
		if g.chance(0.2) {
			t.Plays = 0
		}
		if t.Plays > 0 {
			t.LastPlayed = added + int64(float64(epoch-added)*math.Pow(g.r.Float64(), 0.4))
		}
		t.Favorited = g.chance(0.1 + 0.3*math.Min(float64(t.Plays), 30)/30)
		t.Disliked = !t.Favorited && g.chance(0.005)
		t.Available = kind != "subscription" || !g.chance(0.006)
		lib.tracks = append(lib.tracks, t)
	}
}

func (lib *fakeLibrary) trackFor(g *gen, a genArtist, artist, title, album, albumPID string, year int, genre string) SnapshotTrack {
	t := SnapshotTrack{
		PID: g.pid(), Title: title, Artist: artist, AlbumArtist: a.name, Album: album, AlbumPID: albumPID,
		Genre: genre, Year: year, Track: 1, TrackCount: 1, Disc: 1, DiscCount: 1,
		Duration: g.duration(genre, title), InLibrary: true, Available: true, Kind: "subscription", Art: true,
		SortArtist: a.sort, SortAlbumArtist: a.sort,
	}
	if a.genre == "Hip-Hop/Rap" {
		t.Explicit = g.chance(0.5)
	} else {
		t.Explicit = g.chance(0.008)
	}
	if !strings.HasPrefix(a.name, "The ") && genre != "Classical" && g.chance(0.6) {
		t.Composer = a.name
	}
	if g.chance(0.05) {
		t.Rating = 20 * g.between(1, 5)
	}
	return t
}

func (lib *fakeLibrary) buildPlaylists(g *gen, outside []string) {
	var inLib []int
	for i, t := range lib.tracks {
		if t.InLibrary {
			inLib = append(inLib, i)
		}
	}
	pids := func(idx []int) []string {
		out := make([]string, len(idx))
		for i, j := range idx {
			out[i] = lib.tracks[j].PID
		}
		return out
	}
	filter := func(keep func(*SnapshotTrack) bool) []int {
		var out []int
		for _, i := range inLib {
			if keep(&lib.tracks[i]) {
				out = append(out, i)
			}
		}
		return out
	}
	sample := func(pool []int, n int) []string {
		pool = slices.Clone(pool)
		g.r.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
		return pids(pool[:min(n, len(pool))])
	}
	genres := func(gs ...string) func(*SnapshotTrack) bool {
		return func(t *SnapshotTrack) bool { return slices.Contains(gs, t.Genre) }
	}
	add := func(name, kind, special, parent string, items []string) string {
		if items == nil {
			items = []string{}
		}
		pid := g.pid()
		lib.playlists = append(lib.playlists, SnapshotPlaylist{PID: pid, ParentPID: parent, Name: name, Kind: kind, Special: special, Items: items})
		return pid
	}
	sorted := func(by func(a, b *SnapshotTrack) int, n int, keep func(*SnapshotTrack) bool) []string {
		pool := filter(keep)
		slices.SortStableFunc(pool, func(a, b int) int { return by(&lib.tracks[a], &lib.tracks[b]) })
		return pids(pool[:min(n, len(pool))])
	}
	all := func(*SnapshotTrack) bool { return true }

	add("Library", "user", "library", "", pids(inLib))
	add("Music", "user", "music", "", pids(inLib))
	lib.favorites = len(lib.playlists)
	add("Favorite Songs", "smart", "favorites", "", nil)

	moods := add("Moods", "folder", "", "", nil)
	add("Deep Focus", "user", "", moods, sample(filter(genres("Ambient", "Classical", "Electronic", "Jazz")), 60))
	add("Night Drive", "user", "", moods, sample(filter(genres("Electronic", "Alternative", "R&B/Soul", "Indie Pop")), 48))
	add("Rainy Sunday", "user", "", moods, sample(filter(genres("Folk", "Singer/Songwriter", "Jazz", "Ambient")), 34))
	workouts := add("Workouts", "folder", "", "", nil)
	add("Cooldown", "user", "", workouts, sample(filter(genres("Ambient", "Indie Pop", "R&B/Soul")), 15))
	add("Lifting Heavy", "user", "", workouts, sample(filter(genres("Metal", "Hip-Hop/Rap", "Rock")), 40))
	add("Run Club 5K", "user", "", workouts, sample(filter(genres("Dance", "Electronic", "Pop", "Hip-Hop/Rap")), 28))

	add("90's Music", "smart", "", "", sorted(func(a, b *SnapshotTrack) int { return cmp.Compare(a.Artist, b.Artist) }, 500,
		func(t *SnapshotTrack) bool { return t.Year >= 1990 && t.Year <= 1999 }))
	add("Recently Added", "smart", "", "", sorted(func(a, b *SnapshotTrack) int { return cmp.Compare(b.Added, a.Added) }, 100, all))
	add("Recently Played", "smart", "", "", sorted(func(a, b *SnapshotTrack) int { return cmp.Compare(b.LastPlayed, a.LastPlayed) }, 50,
		func(t *SnapshotTrack) bool { return t.LastPlayed > 0 }))
	add("Top 25 Most Played", "smart", "", "", sorted(func(a, b *SnapshotTrack) int { return cmp.Compare(b.Plays, a.Plays) }, 25, all))

	sub := append(slices.Clone(outside), sample(inLib, 14)...)
	g.r.Shuffle(len(sub), func(i, j int) { sub[i], sub[j] = sub[j], sub[i] })

	users := []struct {
		name  string
		items []string
		kind  string
	}{
		{"All the Jazz", sample(filter(genres("Jazz")), 300), "user"},
		{"Best of 2025", sample(filter(func(t *SnapshotTrack) bool { return t.Year == 2025 }), 45), "user"},
		{"Chill", sample(filter(genres("Ambient", "Indie Pop", "R&B/Soul", "Jazz", "Folk")), 52), "user"},
		{"Gym", sample(filter(genres("Hip-Hop/Rap", "Dance", "Electronic", "Rock", "Metal")), 35), "user"},
		{"Northbound: Indie Discovery", sub, "subscription"},
		{"Road Trip 2024", sample(inLib, 80), "user"},
		{"Songs for the Long Drive Home After Midnight", sample(filter(genres("Alternative", "Rock", "Singer/Songwriter")), 25), "user"},
		{"Sunday Morning Coffee", sample(filter(genres("Folk", "Jazz", "Singer/Songwriter", "R&B/Soul")), 30), "user"},
		{"Throwbacks", sample(filter(func(t *SnapshotTrack) bool { return t.Year < 2005 }), 120), "user"},
		{"Wedding Ideas", nil, "user"},
		{"夏のプレイリスト", sample(inLib, 20), "user"},
	}
	for _, u := range users {
		add(u.name, u.kind, "", "", u.items)
	}

	// Apple Music playlists the demo library follows: their cloud ids are
	// those of the demo Home page's playlists (cmd/encore/demohome.go), so
	// Home marks them as in the library.
	for _, f := range []struct {
		name, global string
		genres       []string
	}{
		{"Indie Rising", "pl.demo-indie-rising", []string{"Indie Pop", "Alternative"}},
		{"Chill Vibes", "pl.demo-chill-vibes", []string{"Ambient", "Folk", "Jazz"}},
	} {
		add(f.name, "subscription", "", "", sample(filter(genres(f.genres...)), 24))
		lib.playlists[len(lib.playlists)-1].GlobalID = f.global
	}
}

// pickStart chooses the album the player starts on: the most recently added
// full-length album with artwork and a Latin title.
func (lib *fakeLibrary) pickStart() {
	byAlbum := map[string][]int{}
	var order []string
	for i, t := range lib.tracks {
		if !t.InLibrary {
			continue
		}
		if _, ok := byAlbum[t.AlbumPID]; !ok {
			order = append(order, t.AlbumPID)
		}
		byAlbum[t.AlbumPID] = append(byAlbum[t.AlbumPID], i)
	}
	for i := len(order) - 1; i >= 0; i-- {
		idx := byAlbum[order[i]]
		t := lib.tracks[idx[0]]
		if len(idx) < 8 || !t.Art || t.Compilation || t.Album[0] >= 0x80 || t.Genre == "Classical" {
			continue
		}
		ok := true
		for _, j := range idx {
			ok = ok && lib.tracks[j].Available
		}
		if !ok {
			continue
		}
		slices.SortFunc(idx, func(a, b int) int {
			ta, tb := &lib.tracks[a], &lib.tracks[b]
			return cmp.Or(cmp.Compare(ta.Disc, tb.Disc), cmp.Compare(ta.Track, tb.Track))
		})
		for _, j := range idx {
			lib.start = append(lib.start, lib.tracks[j].PID)
		}
		return
	}
}

func plural(form string) string {
	switch {
	case strings.HasSuffix(form, "y"):
		return form[:len(form)-1] + "ies"
	default:
		return form + "s"
	}
}
