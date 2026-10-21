package library

import (
	"cmp"
	"slices"
)

// SortField is a column a view can sort by.
type SortField int

const (
	ByTitle SortField = iota
	ByArtist
	ByAlbum
	ByTime
	ByPlays
	ByAdded
	ByLastPlayed
	ByYear
)

var sortFieldNames = [...]string{"Title", "Artist", "Album", "Time", "Plays", "Added", "Last Played", "Year"}

func (f SortField) String() string {
	if f < 0 || int(f) >= len(sortFieldNames) {
		return "Title"
	}
	return sortFieldNames[f]
}

// SortTracks returns a sorted copy of ts. desc reverses the primary key only;
// ties fall through to secondary keys in their natural order (artist → album
// → disc and track, and so on), then to the input order.
func SortTracks(ts []*Track, by SortField, desc bool) []*Track {
	out := slices.Clone(ts)
	primary, rest := trackOrder(by)
	sortStable(out, func(a, b *Track) int {
		c := primary(a, b)
		if desc {
			c = -c
		}
		if c != 0 {
			return c
		}
		return rest(a, b)
	})
	return out
}

// SortAlbums returns a sorted copy of as by ByTitle, ByArtist, ByYear or
// ByAdded (other fields sort by title), with desc as in SortTracks.
func SortAlbums(as []*Album, by SortField, desc bool) []*Album {
	out := slices.Clone(as)
	var primary func(a, b *Album) int
	switch by {
	case ByArtist:
		primary = cmpAlbumArtist
	case ByYear:
		primary = func(a, b *Album) int { return cmp.Compare(a.Year, b.Year) }
	case ByAdded:
		primary = func(a, b *Album) int { return a.Added.Compare(b.Added) }
	default:
		primary = cmpAlbumTitle
	}
	sortStable(out, func(a, b *Album) int {
		c := primary(a, b)
		if desc {
			c = -c
		}
		if c != 0 {
			return c
		}
		return compareAlbumTitle(a, b)
	})
	return out
}

// sortStable is a stable sort that runs as pdqsort with the input position as
// the last key, which is several times faster than slices.SortStableFunc on
// inputs with long runs of equal keys.
func sortStable[T any](xs []T, cmp func(a, b T) int) {
	type item struct {
		v T
		i int
	}
	items := make([]item, len(xs))
	for i, x := range xs {
		items[i] = item{x, i}
	}
	slices.SortFunc(items, func(a, b item) int {
		if c := cmp(a.v, b.v); c != 0 {
			return c
		}
		return a.i - b.i
	})
	for i := range items {
		xs[i] = items[i].v
	}
}

func trackOrder(by SortField) (primary, rest func(a, b *Track) int) {
	switch by {
	case ByArtist:
		return cmpArtist, cmpAlbumThenOrder
	case ByAlbum:
		return cmpAlbumOfTrack, compareAlbumOrder
	case ByTime:
		return func(a, b *Track) int { return cmp.Compare(a.Duration, b.Duration) }, compareTrackTitle
	case ByPlays:
		return func(a, b *Track) int { return cmp.Compare(a.Plays, b.Plays) }, compareTrackTitle
	case ByAdded:
		return func(a, b *Track) int { return a.Added.Compare(b.Added) }, cmpAlbumThenOrder
	case ByLastPlayed:
		return func(a, b *Track) int { return a.LastPlayed.Compare(b.LastPlayed) }, compareTrackTitle
	case ByYear:
		return func(a, b *Track) int { return cmp.Compare(a.Year, b.Year) }, cmpAlbumThenOrder
	default:
		return cmpTitle, compareTrackTitle
	}
}

func cmpAlbumThenOrder(a, b *Track) int {
	if c := cmpAlbumOfTrack(a, b); c != 0 {
		return c
	}
	return compareAlbumOrder(a, b)
}

// rankCompare compares by Build-assigned ranks, falling back to the keys for
// values that did not come from Build.
func rankCompare(ra, rb int32, ka, kb string) int {
	if ra != 0 && rb != 0 {
		return cmp.Compare(ra, rb)
	}
	return CompareKeys(ka, kb)
}

func cmpTitle(a, b *Track) int {
	return rankCompare(a.titleRank, b.titleRank, a.SortTitle, b.SortTitle)
}

func cmpArtist(a, b *Track) int {
	return rankCompare(a.artistRank, b.artistRank, a.SortArtist, b.SortArtist)
}

// cmpAlbumOfTrack orders tracks by album name, then album artist, keeping
// same-named albums by different artists apart.
func cmpAlbumOfTrack(a, b *Track) int {
	if c := rankCompare(a.albumRank, b.albumRank, a.SortAlbum, b.SortAlbum); c != 0 {
		return c
	}
	if a.Album == b.Album || a.Album == nil || b.Album == nil {
		return 0
	}
	if c := cmpAlbumArtist(a.Album, b.Album); c != 0 {
		return c
	}
	return cmp.Compare(a.Album.Key, b.Album.Key)
}

func cmpDiscTrack(a, b *Track) int {
	if c := cmp.Compare(a.Disc, b.Disc); c != 0 {
		return c
	}
	// Untagged track numbers go after numbered tracks.
	ta, tb := a.TrackNo, b.TrackNo
	if ta <= 0 {
		ta = 1 << 30
	}
	if tb <= 0 {
		tb = 1 << 30
	}
	return cmp.Compare(ta, tb)
}

// compareAlbumOrder is the order of tracks within an album.
func compareAlbumOrder(a, b *Track) int {
	if c := cmpDiscTrack(a, b); c != 0 {
		return c
	}
	return cmpTitle(a, b)
}

// compareTrackTitle is the Songs view order.
func compareTrackTitle(a, b *Track) int {
	if c := cmpTitle(a, b); c != 0 {
		return c
	}
	if c := cmpArtist(a, b); c != 0 {
		return c
	}
	if c := cmpAlbumOfTrack(a, b); c != 0 {
		return c
	}
	return cmpDiscTrack(a, b)
}

func cmpAlbumTitle(a, b *Album) int {
	return rankCompare(a.titleRank, b.titleRank, a.SortTitle, b.SortTitle)
}

func cmpAlbumArtist(a, b *Album) int {
	return rankCompare(a.artistRank, b.artistRank, a.SortArtist, b.SortArtist)
}

// compareAlbumTitle is the Albums view order: title, artist, then year.
func compareAlbumTitle(a, b *Album) int {
	if c := cmpAlbumTitle(a, b); c != 0 {
		return c
	}
	if c := cmpAlbumArtist(a, b); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Year, b.Year); c != 0 {
		return c
	}
	return cmp.Compare(a.Key, b.Key)
}
