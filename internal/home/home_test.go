package home

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdankim/encore/internal/musiccache/musiccachetest"
)

// The fixtures below are invented, shaped like amp-api's responses with
// format[resources]=map.

const (
	listenNowKey = "https://amp-api.music.apple.com/v1/me/recommendations?name=listen-now&format%5Bresources%5D=map&geoHashLocation=zzzzzz&platform=auto&types=albums,playlists,stations"
	groupingsKey = "https://amp-api-edge.music.apple.com/v1/editorial/ca/groupings?name=music&tabs=subscriber&format%5Bresources%5D=map&platform=auto"
)

type obj = map[string]any

func refs(rs ...string) obj {
	var data []any
	for _, r := range rs {
		typ, id, _ := strings.Cut(r, ":")
		data = append(data, obj{"id": id, "type": typ, "href": "/v1/x/" + id})
	}
	return obj{"href": "/v1/x", "data": data}
}

func art(tmpl string) obj {
	return obj{"url": "https://is1-ssl.mzstatic.com/image/thumb/Fake/v4/aa/" + tmpl, "width": 3000, "height": 3000, "bgColor": "112233", "textColor1": "ffeedd"}
}

func album(id, name, artist string, explicit bool) obj {
	a := obj{"name": name, "artistName": artist, "artwork": art("x.jpg/{w}x{h}bb.{f}"), "playParams": obj{"id": id, "kind": "album"},
		"releaseDate": "2026-10-01", "trackCount": 9, "recordLabel": "Nobody Records"}
	if explicit {
		a["contentRating"] = "explicit"
	}
	return obj{"id": id, "type": "albums", "href": "/v1/catalog/ca/albums/" + id, "attributes": a}
}

func playlist(id, name, curator, kind string) obj {
	return obj{"id": id, "type": "playlists", "attributes": obj{"name": name, "curatorName": curator, "playlistType": kind,
		"artwork": art("p.jpg/{w}x{h}SC.DN02.{f}?l=en-CA"), "playParams": obj{"id": id, "kind": "playlist"}, "isChart": false}}
}

func station(id, name, kind string, live bool, events ...string) obj {
	s := obj{"id": id, "type": "stations", "attributes": obj{"name": name, "kind": kind, "isLive": live, "mediaKind": "audio",
		"artwork": art("s.jpg/{w}x{h}{c}.{f}"), "playParams": obj{"id": id, "kind": "radioStation", "format": "tracks"}},
		"relationships": obj{"radio-show": obj{"data": []any{}}}}
	if live {
		var rs []string
		for _, e := range events {
			rs = append(rs, "station-events:"+e)
		}
		s["relationships"].(obj)["events"] = refs(rs...)
	}
	return s
}

func song(id, name, artist string) obj {
	return obj{"id": id, "type": "songs", "attributes": obj{"name": name, "artistName": artist, "albumName": "Whatever",
		"artwork": art("t.jpg/{w}x{h}{c}.{f}"), "playParams": obj{"id": id, "kind": "song"}, "durationInMillis": 200000}}
}

func shelf(id, kind, display, title, module string, contents ...string) obj {
	a := obj{"kind": kind, "display": obj{"kind": display, "decorations": []any{}}, "hasSeeAll": false, "nextUpdateDate": "2026-10-21T00:00:00Z"}
	if title != "" {
		a["title"] = obj{"stringForDisplay": title}
	}
	return obj{"id": id, "type": "personal-recommendation", "attributes": a,
		"relationships": obj{"contents": refs(contents...), "primary-content": refs()},
		"meta":          obj{"metrics": obj{"moduleType": module, "reco_id": "r"}}}
}

func index(objs ...obj) obj {
	out := obj{}
	for _, o := range objs {
		typ := o["type"].(string)
		if out[typ] == nil {
			out[typ] = obj{}
		}
		out[typ].(obj)[o["id"].(string)] = o
	}
	return out
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// listenNow is a listen-now response with every kind of shelf the parser
// has to keep or drop.
func listenNow() []byte {
	shelves := []obj{
		shelf("19-top", "music-recommendations", "MusicNotesHeroShelf", "Top Picks for You", "19",
			"albums:1001", "playlists:pl.pm-mix1", "stations:ra.u-me"),
		shelf("7-recent", "recently-played", "MusicCoverShelf", "Recently Played", "7",
			"library-playlists:p.lib1", "albums:1002", "library-albums:l.lib2", "stations:ra.u-me"),
		shelf("11-friends", "music-recommendations", "MusicCoverShelf", "Friends Are Listening To", "11",
			"albums:1003", "playlists:pl.u-friend"),
		shelf("6-mixes", "music-recommendations", "MusicNotesHeroShelf", "Made for You", "6",
			"playlists:pl.pm-mix1", "playlists:pl.pm-mix2"),
		shelf("15-hero", "music-recommendations", "MusicSuperHeroShelf", "", "15", "playlists:pl.ed1"),
		shelf("15-empty", "music-recommendations", "MusicCoverShelf", "Only Library Things", "15", "library-albums:l.lib3"),
		shelf("20-st", "music-recommendations", "MusicCoverShelf", "Stations for You", "20", "stations:ra.123", "stations:ra.q-mood"),
		shelf("25-concerts", "concerts-for-you", "MusicCoverShelf", "Concerts", "25", "concerts:ce.1"),
		shelf("18-fav", "your-favorites", "MusicCoverShelf", "Favourite Artists", "18", "stations:ra.a-55"),
		shelf("23-replay", "music-recommendations", "MusicNotesHeroShelf", "Replay: Your Top Music", "23",
			"editorial-items:999", "playlists:pl.rp-2026"),
		shelf("12-people", "recommended-friends", "MusicSocialCardShelf", "Your Friends Are Following", "12",
			"social-profiles:sp.pat", "social-upsells:InlineUpsellContacts"),
	}
	var data []any
	for _, s := range shelves {
		data = append(data, obj{"id": s["id"], "type": "personal-recommendation", "href": "/v1/me/x"})
	}
	res := index(append(shelves,
		album("1001", "Paper Lanterns", "The Nobodies", true),
		album("1002", "Quiet Machines", "Lena Example", false),
		album("1003", "Friend Picks", "Somebody", false),
		playlist("pl.pm-mix1", "Your Essentials", "Apple Music for You", "personal-mix"),
		playlist("pl.pm-mix2", "Chill", "Apple Music for You", "personal-mix"),
		playlist("pl.ed1", "Antidote", "Apple Music Alternative", "editorial"),
		playlist("pl.u-friend", "Pat's Playlist", "Pat Example", "user-shared"),
		playlist("pl.rp-2026", "Replay 2026", "Apple Music for You", "replay"),
		station("ra.u-me", "Your Station", "user", false),
		station("ra.123", "The Nobodies & Similar Artists", "artistSeeded", false),
		station("ra.q-mood", "Energy", "query", false),
		station("ra.a-55", "Lena Example", "artistOnly", false),
		obj{"id": "p.lib1", "type": "library-playlists"},
		obj{"id": "ce.1", "type": "concerts", "attributes": obj{"name": "The Nobodies @ Secret Venue"}},
		obj{"id": "sp.pat", "type": "social-profiles", "attributes": obj{"name": "Pat Example", "handle": "patexample"}},
		obj{"id": "999", "type": "editorial-items", "attributes": obj{}},
	)...)
	return mustJSON(obj{"data": data, "resources": res, "meta": obj{"metrics": obj{}}})
}

func element(id, kind, name string, rels obj) obj {
	a := obj{"editorialElementKind": kind, "doNotFilter": false}
	if name != "" {
		a["name"] = name
	}
	return obj{"id": id, "type": "editorial-elements", "attributes": a, "relationships": rels}
}

func event(id, title, start, end string) obj {
	return obj{"id": id, "type": "station-events", "attributes": obj{"title": title, "startTime": start, "endTime": end,
		"description": obj{"standard": "x"}, "artwork": art("e.jpg/{w}x{h}{c}.{f}")}, "relationships": obj{"curator": refs()}}
}

// groupings is a New tab response with a hero, two song lists, album and
// playlist shelves, radio episodes and live radio.
func groupings() []byte {
	children := []string{
		"editorial-elements:hero", "editorial-elements:songs1", "editorial-elements:albums1",
		"editorial-elements:albums2", "editorial-elements:pls1", "editorial-elements:songs2",
		"editorial-elements:pls2", "editorial-elements:episodes", "editorial-elements:live", "editorial-elements:links",
	}
	res := index(
		obj{"id": "700", "type": "groupings", "attributes": obj{"name": "Music"}, "relationships": obj{"tabs": refs("editorial-elements:tab")}},
		element("tab", "382", "", obj{"children": refs(children...)}),
		element("hero", "316", "", obj{"children": refs("editorial-elements:card")}),
		element("card", "317", "", obj{"contents": refs("albums:2001")}),
		element("songs1", "327", "Best New Songs", obj{"contents": refs("songs:3001", "songs:3002")}),
		element("albums1", "326", "New This Week", obj{"contents": refs("albums:2001", "albums:2002")}),
		element("albums2", "326", "Recent Releases", obj{"contents": refs("albums:2002")}),
		element("pls1", "326", "Updated Playlists", obj{"contents": refs("playlists:pl.ed2")}),
		element("songs2", "327", "Trending Songs", obj{"contents": refs("songs:3002")}),
		element("pls2", "326", "Spooky Season", obj{"contents": refs("playlists:pl.ed3")}),
		element("episodes", "326", "New Radio Episodes", obj{"contents": refs("stations:ra.ep1")}),
		element("live", "326", "Listen to Live Radio Now", obj{"contents": refs("stations:ra.live1", "stations:ra.live2")}),
		obj{"id": "links", "type": "editorial-elements", "attributes": obj{"editorialElementKind": "322", "links": []any{}}},
		album("2001", "Fresh Thing", "New Band", false),
		album("2002", "Other Thing", "Old Band", true),
		song("3001", "First Song", "New Band"),
		song("3002", "Second Song", "Old Band"),
		playlist("pl.ed2", "Peak Nothing", "Apple Music Indie", "editorial"),
		playlist("pl.ed3", "Halloween Nothing", "Apple Music", "editorial"),
		obj{"id": "ra.ep1", "type": "stations", "attributes": obj{"name": "Episode 1", "kind": "streaming", "isLive": false,
			"playParams": obj{"id": "ra.ep1", "kind": "radioStation", "format": "stream"}}},
		station("ra.live1", "Radio One", "streaming", true, "ra.live1-0", "ra.live1-1"),
		station("ra.live2", "Radio Two", "streaming", true, "ra.live2-0"),
		event("ra.live1-0", "Radio One", "2026-10-20T17:00:00Z", "2026-10-20T19:00:00Z"),
		event("ra.live1-1", "The Evening Show", "2026-10-20T19:00:00Z", "2026-10-20T21:00:00Z"),
		event("ra.live2-0", "Radio Two", "2026-10-20T18:00:00Z", "2026-10-20T20:00:00Z"),
	)
	return mustJSON(obj{"data": []any{obj{"id": "700", "type": "groupings"}}, "resources": res})
}

func newReader(t *testing.T, music, store string, now time.Time) *Reader {
	t.Helper()
	r := New(music, store)
	r.now = func() time.Time { return now }
	return r
}

func load(t *testing.T, r *Reader) *Page {
	t.Helper()
	p, err := r.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func shelfSummary(ss []Shelf) []string {
	var out []string
	for _, s := range ss {
		var ids []string
		for _, it := range s.Items {
			ids = append(ids, it.ID)
		}
		out = append(out, s.Kind+" "+s.Title+": "+strings.Join(ids, ","))
	}
	return out
}

func sameLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got %s\nwant %s", what, strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
}

var at1900 = time.Date(2026, 10, 20, 19, 30, 0, 0, time.UTC)

func TestLoadShelves(t *testing.T) {
	f := musiccachetest.New(t)
	f.Add(listenNowKey, "2026-10-20 19:00:00", listenNow(), true)
	f.Add(groupingsKey, "2026-10-20 19:00:01", groupings(), true)
	p := load(t, newReader(t, f.Dir, t.TempDir(), at1900))

	if want := time.Date(2026, 10, 20, 19, 0, 1, 0, time.UTC); !p.Updated.Equal(want) {
		t.Errorf("Updated = %v, want %v", p.Updated, want)
	}
	sameLines(t, "shelves", shelfSummary(p.Shelves), []string{
		"mixed Top Picks for You: 1001,pl.pm-mix1,ra.u-me",
		"recently-played Recently Played: 1002,ra.u-me",
		"mixes Made for You: pl.pm-mix1,pl.pm-mix2",
		"featured Featured: pl.ed1",
		"stations Stations for You: ra.123,ra.q-mood",
		"favorites Favourite Artists: ra.a-55",
		"replay Replay: Your Top Music: pl.rp-2026",
		"live Live Radio: ra.live1,ra.live2",
	})
	sameLines(t, "new", shelfSummary(p.New), []string{
		"songs Best New Songs: 3001,3002",
		"albums New This Week: 2001,2002",
		"playlists Updated Playlists: pl.ed2",
	})

	top := p.Shelves[0].Items
	a, pl, st := top[0], top[1], top[2]
	if a.Type != TypeAlbum || a.Title != "Paper Lanterns" || a.Subtitle != "The Nobodies" || !a.Explicit {
		t.Errorf("album: %+v", a)
	}
	if pl.Type != TypePlaylist || pl.Subtitle != "Apple Music for You" || pl.PlaylistType != "personal-mix" || pl.Explicit {
		t.Errorf("playlist: %+v", pl)
	}
	if st.Type != TypeStation || st.StationKind != "user" || st.IsLive || st.Subtitle != "" {
		t.Errorf("station: %+v", st)
	}
	if a.Art.BgColor != "112233" || a.Art.TextColor != "ffeedd" || a.Art.Width != 3000 {
		t.Errorf("artwork: %+v", a.Art)
	}
	if s := p.New[0].Items[1]; s.Type != TypeSong || s.Subtitle != "Old Band" {
		t.Errorf("song: %+v", s)
	}
}

func TestArtURL(t *testing.T) {
	for _, c := range []struct{ tmpl, want string }{
		{"https://x/a.jpg/{w}x{h}bb.{f}", "https://x/a.jpg/300x300bb.jpg"},
		{"https://x/a.jpg/{w}x{h}{c}.{f}", "https://x/a.jpg/300x300bb.jpg"},
		{"https://x/a.jpg/{w}x{h}SC.DN02.{f}?l=en-CA", "https://x/a.jpg/300x300SC.DN02.jpg?l=en-CA"},
		{"https://x/a.jpg/{w}x{h}AM.RSMA04.{f}?imgLeft=L%2Fa.png", "https://x/a.jpg/300x300AM.RSMA04.jpg?imgLeft=L%2Fa.png"},
		{"", ""},
	} {
		if got := (Item{Art: Artwork{Template: c.tmpl}}).ArtURL(300); got != c.want {
			t.Errorf("ArtURL(%q) = %q, want %q", c.tmpl, got, c.want)
		}
	}
	if got := (Artwork{Template: "u/{w}x{h}bb.{f}"}).URL(640, 360); got != "u/640x360bb.jpg" {
		t.Errorf("URL(640, 360) = %q", got)
	}
}

func TestLiveEvents(t *testing.T) {
	f := musiccachetest.New(t)
	f.Add(groupingsKey, "2026-10-20 18:00:00", groupings(), false)
	r := newReader(t, f.Dir, "", at1900)

	live := func() []Item {
		p := load(t, r)
		s := p.Shelves[len(p.Shelves)-1]
		if s.Kind != KindLive {
			t.Fatalf("last shelf %q is %s", s.Title, s.Kind)
		}
		return s.Items
	}
	check := func(when string, it Item, sub, now string) {
		t.Helper()
		if it.Subtitle != sub || it.NowPlaying != now {
			t.Errorf("%s %s: subtitle %q now %q, want %q %q", when, it.Title, it.Subtitle, it.NowPlaying, sub, now)
		}
	}
	items := live()
	if len(items[0].Events) != 2 || !items[0].IsLive || items[0].StationKind != "streaming" {
		t.Fatalf("Radio One: %+v", items[0])
	}
	// 19:30: Radio One's evening show, Radio Two's own programme.
	check("19:30", items[0], "The Evening Show", "The Evening Show")
	check("19:30", items[1], "Live", "Radio Two")

	// Later, with nothing new in Music's cache: the schedule is re-read.
	r.now = func() time.Time { return time.Date(2026, 10, 20, 18, 0, 0, 0, time.UTC) }
	items = live()
	check("18:00", items[0], "Live", "Radio One")
	r.now = func() time.Time { return time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC) }
	items = live()
	check("22:00", items[0], "Live", "")
	check("22:00", items[1], "Live", "")
	// An end time is exclusive.
	r.now = func() time.Time { return time.Date(2026, 10, 20, 21, 0, 0, 0, time.UTC) }
	check("21:00", live()[0], "Live", "")
}

func TestSavedCopy(t *testing.T) {
	f := musiccachetest.New(t)
	store := t.TempDir()
	ln := f.Add(listenNowKey, "2026-10-20 19:00:00", listenNow(), true)
	gr := f.Add(groupingsKey, "2026-10-20 19:00:01", groupings(), false)
	r := newReader(t, f.Dir, store, at1900)
	want := shelfSummary(load(t, r).Shelves)

	// Only what the page shows reaches the copy.
	for _, name := range []string{"listen-now.json", "new.json"} {
		b, err := os.ReadFile(filepath.Join(store, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"Pat Example", "patexample", "Friend", "Secret Venue", "geoHash", "zzzzzz", "Nobody Records", "11-friends", "social", "concert"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s holds %q", name, bad)
			}
		}
		if fi, _ := os.Stat(filepath.Join(store, name)); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", name, fi.Mode().Perm())
		}
	}

	// NSURLCache evicts both responses: the same reader and a new one
	// both serve the copy.
	f.Remove(ln)
	f.Remove(gr)
	sameLines(t, "after eviction", shelfSummary(load(t, r).Shelves), want)
	r2 := newReader(t, f.Dir, store, at1900)
	p := load(t, r2)
	sameLines(t, "new reader", shelfSummary(p.Shelves), want)
	if len(p.New) != 3 || !p.Updated.Equal(time.Date(2026, 10, 20, 19, 0, 1, 0, time.UTC)) {
		t.Errorf("new reader: %d New shelves, updated %v", len(p.New), p.Updated)
	}
	if it := p.Shelves[len(p.Shelves)-1].Items[0]; it.NowPlaying != "The Evening Show" {
		t.Errorf("saved live station: %+v", it)
	}

	// Music shows Home again: the newer response replaces the copy.
	newer := mustJSON(obj{
		"data":      []any{obj{"id": "9-new", "type": "personal-recommendation"}},
		"resources": index(shelf("9-new", "music-recommendations", "MusicCoverShelf", "New Releases for You", "9", "albums:1009"), album("1009", "Brand New", "Someone", false)),
	})
	f.Add(listenNowKey+"&v=2", "2026-10-21 08:00:00", newer, false)
	sameLines(t, "newer", shelfSummary(load(t, r2).Shelves), []string{
		"albums New Releases for You: 1009",
		"live Live Radio: ra.live1,ra.live2",
	})
	r3 := newReader(t, t.TempDir(), store, at1900) // no Music cache at all
	if got := shelfSummary(load(t, r3).Shelves); len(got) != 2 || got[0] != "albums New Releases for You: 1009" {
		t.Errorf("copy after update: %v", got)
	}

	// An older response in Music's cache doesn't replace a newer copy.
	f2 := musiccachetest.New(t)
	f2.Add(listenNowKey, "2026-10-19 12:00:00", listenNow(), false)
	if got := shelfSummary(load(t, newReader(t, f2.Dir, store, at1900)).Shelves); got[0] != "albums New Releases for You: 1009" {
		t.Errorf("older live response won: %v", got)
	}
}

func TestNewestAndNames(t *testing.T) {
	f := musiccachetest.New(t)
	one := mustJSON(obj{
		"data":      []any{obj{"id": "9-a", "type": "personal-recommendation"}},
		"resources": index(shelf("9-a", "music-recommendations", "MusicCoverShelf", "Old", "9", "albums:1"), album("1", "One", "A", false)),
	})
	two := mustJSON(obj{
		"data":      []any{obj{"id": "9-b", "type": "personal-recommendation"}},
		"resources": index(shelf("9-b", "music-recommendations", "MusicCoverShelf", "New", "9", "albums:2"), album("2", "Two", "B", false)),
	})
	f.Add(listenNowKey+"&a=1", "2026-10-20 10:00:00", one, false)
	f.Add(listenNowKey+"&a=2", "2026-10-20 11:00:00", two, false)
	// Same path, other names: not the Home page.
	f.Add(strings.Replace(listenNowKey, "name=listen-now", "name=browse", 1), "2026-10-20 12:00:00", one, false)
	f.Add(strings.Replace(groupingsKey, "name=music", "name=radio", 1), "2026-10-20 12:00:00", groupings(), false)
	p := load(t, newReader(t, f.Dir, "", at1900))
	sameLines(t, "shelves", shelfSummary(p.Shelves), []string{"albums New: 2"})
	if len(p.New) != 0 {
		t.Errorf("New from another grouping: %v", shelfSummary(p.New))
	}
}

func TestPlaylistTracks(t *testing.T) {
	f := musiccachetest.New(t)
	store := t.TempDir()
	f.Add(listenNowKey, "2026-10-20 19:00:00", listenNow(), false)

	// Inline: /playlists/<id>?include=tracks.
	f.Add("https://amp-api.music.apple.com/v1/catalog/ca/playlists/pl.pm-mix1?include=tracks&l=en-CA", "2026-10-20 19:01:00",
		mustJSON(obj{"data": []any{obj{"id": "pl.pm-mix1", "type": "playlists", "attributes": obj{"name": "Your Essentials"},
			"relationships": obj{"tracks": obj{"data": []any{
				obj{"id": "501", "type": "songs", "attributes": obj{"name": "a", "playParams": obj{"id": "501", "kind": "song"}}},
				obj{"id": "601", "type": "music-videos", "attributes": obj{"name": "v"}},
				obj{"id": "502", "type": "songs", "attributes": obj{"name": "b"}},
			}}}}}}), false)
	// Map format: /playlists?ids=….
	f.Add("https://amp-api.music.apple.com/v1/catalog/ca/playlists?ids=pl.ed1,pl.nothome&include=tracks&format%5Bresources%5D=map", "2026-10-20 19:02:00",
		mustJSON(obj{"data": []any{obj{"id": "pl.ed1", "type": "playlists"}, obj{"id": "pl.nothome", "type": "playlists"}},
			"resources": index(
				obj{"id": "pl.ed1", "type": "playlists", "attributes": obj{"name": "Antidote"}, "relationships": obj{"tracks": refs("songs:701", "songs:702")}},
				obj{"id": "pl.nothome", "type": "playlists", "attributes": obj{"name": "x"}, "relationships": obj{"tracks": refs("songs:801")}},
				song("701", "c", "C"), song("702", "d", "D"),
			)}), true)
	// A later page of /tracks is not the whole playlist's start.
	f.Add("https://amp-api.music.apple.com/v1/catalog/ca/playlists/pl.pm-mix2/tracks?offset=100", "2026-10-20 19:03:00",
		mustJSON(obj{"data": []any{song("901", "e", "E")}}), false)
	// Library playlists are not catalog playlists.
	f.Add("https://amp-api.music.apple.com/v1/me/library/playlists/p.lib1?include=tracks", "2026-10-20 19:03:00",
		mustJSON(obj{"data": []any{obj{"id": "pl.rp-2026", "type": "playlists", "relationships": obj{"tracks": refs("songs:1")}}}}), false)

	tracks := func(p *Page) map[string]string {
		out := map[string]string{}
		for _, s := range p.Shelves {
			for _, it := range s.Items {
				if it.Tracks != nil {
					out[it.ID] = strings.Join(it.Tracks, ",")
				}
			}
		}
		return out
	}
	want := map[string]string{"pl.pm-mix1": "501,502", "pl.ed1": "701,702"}
	got := tracks(load(t, newReader(t, f.Dir, store, at1900)))
	if len(got) != len(want) || got["pl.pm-mix1"] != want["pl.pm-mix1"] || got["pl.ed1"] != want["pl.ed1"] {
		t.Errorf("tracks = %v, want %v", got, want)
	}
	b, _ := os.ReadFile(filepath.Join(store, "playlists.json"))
	if strings.Contains(string(b), "pl.nothome") {
		t.Errorf("saved tracks of a playlist not on the page: %s", b)
	}
	// From the copy once Music dropped them.
	got = tracks(load(t, newReader(t, t.TempDir(), store, at1900)))
	if got["pl.ed1"] != "701,702" || got["pl.pm-mix1"] != "501,502" {
		t.Errorf("saved tracks = %v", got)
	}
}

func TestChangedAndEmpty(t *testing.T) {
	f := musiccachetest.New(t)
	r := newReader(t, f.Dir, "", at1900)
	if !r.Changed() {
		t.Error("Changed before the first Load")
	}
	if _, err := r.Load(context.Background()); !errors.Is(err, ErrNotCached) {
		t.Errorf("empty cache: %v", err)
	}
	if r.Changed() {
		t.Error("Changed right after Load")
	}
	f.Add(groupingsKey, "2026-10-20 19:00:00", groupings(), false)
	if !r.Changed() {
		t.Error("not Changed after a write")
	}
	if p := load(t, r); len(p.Shelves) != 1 || len(p.New) != 3 {
		t.Errorf("groupings only: %v %v", shelfSummary(p.Shelves), shelfSummary(p.New))
	}

	if _, err := newReader(t, t.TempDir(), "", at1900).Load(context.Background()); !errors.Is(err, ErrNotCached) {
		t.Errorf("no Cache.db: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
}

func TestParseBadBodies(t *testing.T) {
	for _, b := range []string{"", "{", `{"data":[]}`, `{"data":[{"id":"x","type":"personal-recommendation"}],"resources":{}}`} {
		if s, _ := parseListenNow([]byte(b)); len(s) != 0 {
			t.Errorf("parseListenNow(%q) = %v", b, s)
		}
		if s, _ := parseGroupings([]byte(b)); len(s) != 0 {
			t.Errorf("parseGroupings(%q) = %v", b, s)
		}
	}
	// A body that isn't JSON doesn't stop the rest of the page.
	f := musiccachetest.New(t)
	f.Add(listenNowKey, "2026-10-20 19:00:00", []byte("<html>"), false)
	f.Add(groupingsKey, "2026-10-20 19:00:00", groupings(), false)
	if p := load(t, newReader(t, f.Dir, "", at1900)); len(p.Shelves) != 1 {
		t.Errorf("shelves: %v", shelfSummary(p.Shelves))
	}
}
