// Package libtest generates deterministic snapshots for library and search
// tests and benchmarks.
package libtest

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/bogdankim/encore/internal/bridge"
)

// Fixture is a small hand-written library covering the cases tests check by
// name: "The" artists, diacritics, compilations, albums without a pid,
// numbered titles, casing variants of one artist, names that start with
// digits or punctuation, playlist-only tracks and a playlist folder.
func Fixture() *bridge.Snapshot {
	g := newGen(1)
	g.fixture()
	return g.s
}

func (g *gen) fixture() {
	g.album(albumSpec{artist: "The Beatles", sortArtist: "Beatles", title: "Abbey Road", year: 1969,
		songs: []string{"Come Together", "Something", "Here Comes the Sun"}})
	g.album(albumSpec{artist: "The Beatles", sortArtist: "Beatles", title: "Help!", year: 1965,
		songs: []string{"Help!", "Yesterday", "Ticket to Ride"}})
	g.album(albumSpec{artist: "Daft Punk", title: "Random Access Memories", year: 2013,
		songs:    []string{"Give Life Back to Music", "Instant Crush", "Lose Yourself to Dance", "Get Lucky"},
		favorite: map[int]bool{3: true}, plays: 40})
	g.album(albumSpec{artist: "Daft Punk", title: "Discovery", year: 2001,
		songs: []string{"One More Time", "Digital Love"}})
	g.album(albumSpec{artist: "Björk", title: "Homogenic", year: 1997,
		songs: []string{"Jóga", "Bachelorette"}})
	g.album(albumSpec{artist: "Beyoncé", title: "Lemonade", year: 2016, songs: []string{"Formation"}})
	g.album(albumSpec{artist: "Beyoncé", title: "Renaissance", year: 2022, songs: []string{"Alien Superstar", "Cuff It"}})
	g.album(albumSpec{artist: "BEYONCÉ", title: "4", year: 2011, songs: []string{"Love On Top"}})
	g.album(albumSpec{artist: "Sigur Rós", title: "Ágætis byrjun", year: 1999, noPID: true,
		songs: []string{"Svefn-g-englar", "Starálfur"}})
	g.album(albumSpec{artist: "2Pac", title: "All Eyez on Me", year: 1996, songs: []string{"California Love"}})
	g.album(albumSpec{artist: "!!!", title: "Myth Takes", year: 2007, songs: []string{"Must Be the Moon"}})
	g.album(albumSpec{artist: "Numbers", title: "Counting", year: 2020,
		songs: []string{"Track 1", "Track 2", "Track 10", "Track 3", "Track 20", "Track 11"}})
	g.album(albumSpec{title: "Summer Hits", year: 2010, compilation: true,
		artists: []string{"Daft Punk", "Björk", "Robyn"},
		songs:   []string{"Around the World", "Army of Me", "Dancing On My Own"}})
	g.album(albumSpec{artist: "Radiohead", title: "Kid A", year: 2000, notInLibrary: true,
		songs: []string{"Everything in Its Right Place", "Idioteque"}})

	g.playlist("Moods", "", "folder", nil)
	moods := g.lastPlaylist()
	g.playlist("Chill", moods, "user", g.pids("Jóga", "Idioteque", "Digital Love"))
	g.playlist("Gym", moods, "user", g.pids("Get Lucky", "Formation"))
	g.playlist("Road Trip", "", "subscription", g.pids("Everything in Its Right Place", "Come Together"))
	g.playlist("Library", "", "user", nil)
	g.s.Playlists[len(g.s.Playlists)-1].Special = "library"
}

// Snapshot returns the Fixture plus generated filler, n tracks in all (about a
// fifth of them only in playlists), with playlists, folders and a "library"
// master playlist holding every in-library track.
func Snapshot(n int) *bridge.Snapshot {
	g := newGen(42)
	g.fixture()

	nArtists := max(n/25, 10)
	artists := make([]string, nArtists)
	for i := range artists {
		artists[i] = g.artistName()
	}
	for len(g.s.Tracks) < n {
		spec := albumSpec{
			artist:       artists[g.r.IntN(len(artists))],
			title:        g.phrase(1, 4),
			year:         1960 + g.r.IntN(66),
			noPID:        g.r.IntN(20) == 0,
			notInLibrary: g.r.IntN(5) == 0,
			plays:        g.r.IntN(60),
		}
		if g.r.IntN(30) == 0 {
			spec.artist = ""
			spec.compilation = true
			for range 4 {
				spec.artists = append(spec.artists, artists[g.r.IntN(len(artists))])
			}
		}
		count := min(4+g.r.IntN(13), n-len(g.s.Tracks))
		for range count {
			spec.songs = append(spec.songs, g.phrase(1, 5))
		}
		spec.favorite = map[int]bool{}
		for i := range count {
			if g.r.IntN(20) == 0 {
				spec.favorite[i] = true
			}
		}
		g.album(spec)
	}

	for i := range max(n/200, 4) {
		parent := ""
		if i%10 == 0 {
			g.playlist(g.phrase(1, 2)+" Folder", "", "folder", nil)
			parent = g.lastPlaylist()
		}
		items := make([]string, 10+g.r.IntN(150))
		for j := range items {
			items[j] = g.s.Tracks[g.r.IntN(len(g.s.Tracks))].PID
		}
		g.playlist(g.phrase(1, 3), parent, []string{"user", "smart", "subscription"}[i%3], items)
	}
	var all []string
	for _, t := range g.s.Tracks {
		if t.InLibrary {
			all = append(all, t.PID)
		}
	}
	for i := range g.s.Playlists {
		if g.s.Playlists[i].Special == "library" {
			g.s.Playlists[i].Items = all
		}
	}
	return g.s
}

type gen struct {
	r    *rand.Rand
	s    *bridge.Snapshot
	next uint64
}

func newGen(seed uint64) *gen {
	return &gen{r: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), s: &bridge.Snapshot{GeneratedAt: 1760908800}, next: 1}
}

func (g *gen) pid() string {
	g.next++
	return fmt.Sprintf("%016X", 0xA000000000000000|g.next*2654435761)
}

type albumSpec struct {
	artist, sortArtist, title string
	artists                   []string // per-track artists for compilations
	year                      int
	songs                     []string
	compilation, noPID        bool
	notInLibrary              bool
	favorite                  map[int]bool
	plays                     int
}

func (g *gen) album(a albumSpec) {
	albumPID := ""
	if !a.noPID {
		albumPID = g.pid()
	}
	added := int64(1500000000 + g.r.IntN(260000000))
	for i, song := range a.songs {
		artist := a.artist
		if len(a.artists) > 0 {
			artist = a.artists[i%len(a.artists)]
		}
		g.s.Tracks = append(g.s.Tracks, bridge.SnapshotTrack{
			PID:         g.pid(),
			Title:       song,
			Artist:      artist,
			AlbumArtist: a.artist,
			Album:       a.title,
			AlbumPID:    albumPID,
			Genre:       []string{"Pop", "Rock", "Electronic", "Jazz", "Hip-Hop"}[len(a.title)%5],
			Year:        a.year,
			Track:       i + 1,
			TrackCount:  len(a.songs),
			Disc:        1,
			DiscCount:   1,
			Duration:    int64(120000 + g.r.IntN(300000)),
			Added:       added + int64(i),
			Plays:       a.plays + g.r.IntN(5),
			Favorited:   a.favorite[i],
			Compilation: a.compilation,
			InLibrary:   !a.notInLibrary,
			Available:   true,
			Kind:        "subscription",
			Art:         true,
			SortArtist:  a.sortArtist,
			// Music fills the album artist sort field only when it differs.
			SortAlbumArtist: a.sortArtist,
		})
	}
}

func (g *gen) playlist(name, parent, kind string, items []string) {
	g.s.Playlists = append(g.s.Playlists, bridge.SnapshotPlaylist{
		PID: g.pid(), ParentPID: parent, Name: name, Kind: kind, Items: items,
	})
}

func (g *gen) lastPlaylist() string { return g.s.Playlists[len(g.s.Playlists)-1].PID }

// pids returns the pids of the tracks with these titles.
func (g *gen) pids(titles ...string) []string {
	var out []string
	for _, title := range titles {
		for _, t := range g.s.Tracks {
			if t.Title == title {
				out = append(out, t.PID)
				break
			}
		}
	}
	return out
}

// Filler words avoid the fixture's search terms (beat, lucky, bjork) so tests
// over a generated library stay predictable.
var words = strings.Fields(`
	love night heart fire dream light rain summer blue city river road wild
	golden silver black white ocean star moon sun shadow ghost glass paper
	stone garden winter spring autumn morning evening midnight electric
	velvet neon crystal broken falling rising running burning dancing
	lonely happy secret silent sweet bitter honey sugar candy cherry
	echo signal motion rhythm pulse wave tide storm thunder lightning
	home away back forward under over inside outside between beyond
	little big young old new last first only every never always forever
	song story letter message voice whisper scream song tears smile
	kiss touch hold run fly fall rise shine glow fade burn freeze melt
	angel devil saint sinner king queen prince princess lover stranger
	mirror window door wall floor ceiling street avenue highway bridge
	café über señor naïve déjà vu fiancée crème brûlée façade jalapeño
	part chapter volume side one two three seven
`)

func (g *gen) word() string {
	w := words[g.r.IntN(len(words))]
	return strings.ToUpper(w[:1]) + w[1:]
}

func (g *gen) phrase(lo, hi int) string {
	n := lo + g.r.IntN(hi-lo+1)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = g.word()
	}
	if g.r.IntN(12) == 0 {
		parts = append(parts, fmt.Sprint(1+g.r.IntN(20)))
	}
	if g.r.IntN(15) == 0 {
		parts = append([]string{"The"}, parts...)
	}
	return strings.Join(parts, " ")
}

func (g *gen) artistName() string {
	switch g.r.IntN(6) {
	case 0:
		return "The " + g.word() + "s"
	case 1:
		return g.word() + " & The " + g.word() + "s"
	default:
		return g.phrase(1, 2)
	}
}
