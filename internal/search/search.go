// Package search finds songs, albums, artists and playlists in a
// library.Library as the user types. See docs/design.md, "Search".
//
// A query is folded like library fields and split on whitespace; every term
// must occur in one of an entity's fields. When no entity in scope matches
// that way, terms are matched as fuzzy subsequences instead.
package search

import (
	"math/bits"
	"slices"
	"strings"
	"sync"

	"github.com/bogdankim/encore/internal/library"
)

// Scope limits a search to one kind of entity.
type Scope int

const (
	All Scope = iota
	Songs
	Albums
	Artists
	Playlists
)

// Kind identifies the kind of a TopHit.
type Kind int

const (
	KindSong Kind = iota
	KindAlbum
	KindArtist
	KindPlaylist
)

// Match positions are rune indexes into the displayed string (Track.Title,
// Album.Artist, …), ascending, for highlighting.

// TrackHit is a matching song.
type TrackHit struct {
	Track                                     *library.Track
	Score                                     int
	TitleMatches, ArtistMatches, AlbumMatches []int
}

// AlbumHit is a matching album.
type AlbumHit struct {
	Album                       *library.Album
	Score                       int
	TitleMatches, ArtistMatches []int
}

// ArtistHit is a matching artist.
type ArtistHit struct {
	Artist      *library.Artist
	Score       int
	NameMatches []int
}

// PlaylistHit is a matching playlist (folders included).
type PlaylistHit struct {
	Playlist    *library.Playlist
	Score       int
	NameMatches []int
}

// TopHit points at one of the best hits: Results.Songs[Index] for KindSong,
// Results.Albums[Index] for KindAlbum, and so on.
type TopHit struct {
	Kind  Kind
	Index int
}

// Results holds hits per kind, each sorted best first.
type Results struct {
	Query string
	Scope Scope
	// Fuzzy is set when nothing matched strictly and the hits come from fuzzy
	// matching.
	Fuzzy bool
	// Top lists, for scope All, the strongest hit of each kind (at most one
	// per kind, best first) whose match is at least a word prefix.
	Top       []TopHit
	Songs     []TrackHit
	Albums    []AlbumHit
	Artists   []ArtistHit
	Playlists []PlaylistHit
}

// Empty reports whether there are no hits at all.
func (r *Results) Empty() bool {
	return len(r.Songs)+len(r.Albums)+len(r.Artists)+len(r.Playlists) == 0
}

// Index is a search index over one Library. It is immutable and safe for
// concurrent use; build a new one when the library is swapped.
type Index struct {
	songs, albums, artists, playlists corpus
	lib                               *library.Library
}

// Field weights: what a term matched in counts as much as how it matched.
const (
	wPrimary = 6 // song and album titles, artist and playlist names
	wArtist  = 4
	wAlbum   = 3
)

// NewIndex precomputes the search corpora for lib.
func NewIndex(lib *library.Library) *Index {
	x := &Index{lib: lib}
	x.songs = newCorpus(len(lib.Songs), []int{wPrimary, wArtist, wAlbum}, func(i int, f []string) int {
		t := lib.Songs[i]
		f[0], f[1], f[2] = t.FoldTitle, t.FoldArtist, t.FoldAlbum
		pop := bits.Len(uint(t.Plays))
		if t.Favorited {
			pop += 8
		}
		return pop
	})
	x.albums = newCorpus(len(lib.Albums), []int{wPrimary, wArtist}, func(i int, f []string) int {
		a := lib.Albums[i]
		f[0], f[1] = a.FoldTitle, a.FoldArtist
		plays, fav := 0, 0
		for _, t := range a.Tracks {
			plays += t.Plays
			if t.Favorited {
				fav = 4
			}
		}
		return bits.Len(uint(plays)) + fav
	})
	x.artists = newCorpus(len(lib.Artists), []int{wPrimary}, func(i int, f []string) int {
		a := lib.Artists[i]
		f[0] = a.FoldName
		plays := 0
		for _, al := range a.Albums {
			for _, t := range al.Tracks {
				plays += t.Plays
			}
		}
		return bits.Len(uint(plays)) + bits.Len(uint(a.Tracks))
	})
	x.playlists = newCorpus(len(lib.PlaylistsFlat), []int{wPrimary}, func(i int, f []string) int {
		f[0] = lib.PlaylistsFlat[i].FoldName
		return 0
	})
	return x
}

// Search runs query over scope and returns at most limit hits per kind
// (limit <= 0 means no limit). An empty query returns empty Results.
func (x *Index) Search(query string, scope Scope, limit int) Results {
	r := Results{Query: query, Scope: scope}
	q := parseQuery(query)
	if q == nil {
		return r
	}
	in := func(s Scope) bool { return scope == All || scope == s }

	var songs, albums, artists, playlists []cand
	run := func(fuzzy bool) {
		if in(Songs) {
			songs = x.songs.find(q, limit, fuzzy)
		}
		if in(Albums) {
			albums = x.albums.find(q, limit, fuzzy)
		}
		if in(Artists) {
			artists = x.artists.find(q, limit, fuzzy)
		}
		if in(Playlists) {
			playlists = x.playlists.find(q, limit, fuzzy)
		}
	}
	run(false)
	if len(songs)+len(albums)+len(artists)+len(playlists) == 0 {
		r.Fuzzy = true
		run(true)
	}

	lib := x.lib
	r.Songs = hits(songs, func(c cand) TrackHit {
		t := lib.Songs[c.idx]
		m := x.songs.positions(c.idx, q, r.Fuzzy, t.Title, t.Artist, t.AlbumName)
		return TrackHit{Track: t, Score: c.score, TitleMatches: m[0], ArtistMatches: m[1], AlbumMatches: m[2]}
	})
	r.Albums = hits(albums, func(c cand) AlbumHit {
		a := lib.Albums[c.idx]
		m := x.albums.positions(c.idx, q, r.Fuzzy, a.Title, a.Artist)
		return AlbumHit{Album: a, Score: c.score, TitleMatches: m[0], ArtistMatches: m[1]}
	})
	r.Artists = hits(artists, func(c cand) ArtistHit {
		a := lib.Artists[c.idx]
		m := x.artists.positions(c.idx, q, r.Fuzzy, a.Name)
		return ArtistHit{Artist: a, Score: c.score, NameMatches: m[0]}
	})
	r.Playlists = hits(playlists, func(c cand) PlaylistHit {
		p := lib.PlaylistsFlat[c.idx]
		m := x.playlists.positions(c.idx, q, r.Fuzzy, p.Name)
		return PlaylistHit{Playlist: p, Score: c.score, NameMatches: m[0]}
	})

	if scope == All && !r.Fuzzy {
		r.Top = topHits(songs, albums, artists, playlists)
	}
	return r
}

func hits[H any](cs []cand, mk func(cand) H) []H {
	if len(cs) == 0 {
		return nil
	}
	out := make([]H, len(cs))
	for i, c := range cs {
		out[i] = mk(c)
	}
	return out
}

// topHits picks the best hit of each kind that is at least a word-prefix
// match, ordered by match tier, then artist, album, song, playlist, then score.
func topHits(songs, albums, artists, playlists []cand) []TopHit {
	type pick struct {
		kind Kind
		c    cand
		prio int
	}
	var picks []pick
	add := func(k Kind, cs []cand, prio int) {
		if len(cs) > 0 && cs[0].tier >= tierWords {
			picks = append(picks, pick{k, cs[0], prio})
		}
	}
	add(KindArtist, artists, 0)
	add(KindAlbum, albums, 1)
	add(KindSong, songs, 2)
	add(KindPlaylist, playlists, 3)
	slices.SortStableFunc(picks, func(a, b pick) int {
		if a.c.tier != b.c.tier {
			return b.c.tier - a.c.tier
		}
		if a.prio != b.prio {
			return a.prio - b.prio
		}
		return b.c.score - a.c.score
	})
	out := make([]TopHit, len(picks))
	for i, p := range picks {
		out[i] = TopHit{Kind: p.kind}
	}
	return out
}

// query is a parsed, folded query.
type query struct {
	folded  string   // the whole folded query, single-spaced
	terms   []string // folded terms
	scan    int      // index of the longest term, used to scan corpora
	compact string   // terms concatenated, for coverage
	fuzzy   [][]rune // terms as runes, for fuzzy matching
}

func parseQuery(s string) *query {
	f := library.Fold(s)
	if f == "" {
		return nil
	}
	q := &query{folded: f, terms: strings.Split(f, " ")}
	for i, t := range q.terms {
		if len(t) > len(q.terms[q.scan]) {
			q.scan = i
		}
		q.compact += t
		q.fuzzy = append(q.fuzzy, []rune(t))
	}
	return q
}

// FilterTracks returns the tracks matching query in their original order.
// Matching is the same as Search, without ranking; an empty query returns ts.
//
// The folded fields of the last few inputs are kept in a compact buffer, so
// filtering the same list again on the next keystroke costs one sweep over
// that buffer.
func FilterTracks(ts []*library.Track, query string) []*library.Track {
	return trackFilters.filter(ts, query)
}

// FilterAlbums is FilterTracks for albums (title and artist).
func FilterAlbums(as []*library.Album, query string) []*library.Album {
	return albumFilters.filter(as, query)
}

// FilterArtists is FilterTracks for artists (name).
func FilterArtists(as []*library.Artist, query string) []*library.Artist {
	return artistFilters.filter(as, query)
}

// FilterPlaylists is FilterTracks for playlists (name).
func FilterPlaylists(ps []*library.Playlist, query string) []*library.Playlist {
	return playlistFilters.filter(ps, query)
}

var (
	trackFilters = &filterCache[*library.Track]{weights: []int{wPrimary, wArtist, wAlbum}, fields: func(t *library.Track, f []string) {
		f[0], f[1], f[2] = t.FoldTitle, t.FoldArtist, t.FoldAlbum
	}}
	albumFilters = &filterCache[*library.Album]{weights: []int{wPrimary, wArtist}, fields: func(a *library.Album, f []string) {
		f[0], f[1] = a.FoldTitle, a.FoldArtist
	}}
	artistFilters = &filterCache[*library.Artist]{weights: []int{wPrimary}, fields: func(a *library.Artist, f []string) {
		f[0] = a.FoldName
	}}
	playlistFilters = &filterCache[*library.Playlist]{weights: []int{wPrimary}, fields: func(p *library.Playlist, f []string) {
		f[0] = p.FoldName
	}}
)

// filterCache remembers corpora for recently filtered slices. An entry is
// reused only when the input holds exactly the same elements, so callers may
// reuse or reorder their slices freely.
type filterCache[T comparable] struct {
	weights []int
	fields  func(T, []string)

	mu      sync.Mutex
	entries []*filterEntry[T] // most recently used first
}

type filterEntry[T comparable] struct {
	items []T // a copy of the input
	c     corpus
}

const filterCacheSize = 4

// ResetFilters drops the corpora FilterTracks and the other Filter functions
// keep for recent inputs. Call it when the library is swapped: an entry holds
// pointers into the old library, which keeps all of it alive.
func ResetFilters() {
	trackFilters.reset()
	albumFilters.reset()
	artistFilters.reset()
	playlistFilters.reset()
}

func (fc *filterCache[T]) reset() {
	fc.mu.Lock()
	clear(fc.entries)
	fc.entries = nil
	fc.mu.Unlock()
}

func (fc *filterCache[T]) filter(xs []T, query string) []T {
	q := parseQuery(query)
	if q == nil || len(xs) == 0 {
		return xs
	}
	c := fc.corpus(xs)
	idx := c.matchAll(q, false)
	if len(idx) == 0 {
		idx = c.matchAll(q, true)
	}
	if len(idx) == 0 {
		return nil
	}
	out := make([]T, len(idx))
	for i, j := range idx {
		out[i] = xs[j]
	}
	return out
}

func (fc *filterCache[T]) corpus(xs []T) *corpus {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	for i, e := range fc.entries {
		if slices.Equal(e.items, xs) {
			copy(fc.entries[1:i+1], fc.entries[:i])
			fc.entries[0] = e
			return &e.c
		}
	}
	e := &filterEntry[T]{items: slices.Clone(xs)}
	e.c = newCorpus(len(xs), fc.weights, func(i int, f []string) int {
		fc.fields(xs[i], f)
		return 0
	})
	if len(fc.entries) < filterCacheSize {
		fc.entries = append(fc.entries, nil)
	}
	copy(fc.entries[1:], fc.entries)
	fc.entries[0] = e
	return &e.c
}
