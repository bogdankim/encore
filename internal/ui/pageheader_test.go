package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
)

// playingCtx is a test Ctx with tr playing.
func playingCtx(tr *library.Track) *Ctx {
	c, _ := albumTestCtx(albumTestLib)
	c.Player.NowPlaying.Track = &bridge.CurrentTrack{PID: tr.PID}
	return c
}

// bigAlbum is an album with at least n tracks.
func bigAlbum(t *testing.T, n int) *library.Album {
	t.Helper()
	for _, a := range albumTestLib.Albums {
		if len(a.Tracks) >= n {
			return a
		}
	}
	t.Skipf("no album with %d tracks", n)
	return nil
}

func TestAlbumOpensOnPlayingTrack(t *testing.T) {
	a := bigAlbum(t, 12)
	want := len(a.Tracks) - 2
	c := playingCtx(a.Tracks[want])
	v := NewAlbumView(a)
	v.Render(100, 24, c)
	if v.cursor != want {
		t.Fatalf("cursor on %d, want the playing track %d", v.cursor, want)
	}
	if r := v.rowOf[want] - v.top; r < 0 || r >= v.listH {
		t.Fatalf("playing row %d not in view (top %d, %d rows)", v.rowOf[want], v.top, v.listH)
	}
	// Later renders leave the cursor where the user puts it.
	v.moveTo(0)
	v.Render(100, 24, c)
	if v.cursor != 0 {
		t.Fatalf("cursor jumped back to %d", v.cursor)
	}
}

func TestPlaylistOpensOnPlayingTrack(t *testing.T) {
	var pl *library.Playlist
	for _, p := range albumTestLib.PlaylistsFlat {
		if p.Kind != "folder" && len(p.Tracks) > 60 {
			pl = p
			break
		}
	}
	if pl == nil {
		t.Skip("no long playlist")
	}
	want := 50
	c := playingCtx(pl.Tracks[want])
	v := newPlaylistView(pl).(*tracksView)
	v.Render(120, 40, c)
	tb := v.table
	if tb.cursor != want {
		t.Fatalf("cursor on %d, want %d", tb.cursor, want)
	}
	if mid := tb.cursor - tb.top; mid < tb.bodyH/2-1 || mid > tb.bodyH/2+1 {
		t.Fatalf("playing row at %d of %d rows, want the middle", mid, tb.bodyH)
	}
}

// One header for every detail page: at the same size the art, the text
// column and the button row sit in the same place.
func TestDetailHeadersAgree(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	a := bigAlbum(t, 2)
	var pl *library.Playlist
	for _, p := range albumTestLib.PlaylistsFlat {
		if p.Kind != "folder" && len(p.Tracks) > 0 {
			pl = p
			break
		}
	}
	type shape struct{ textX, buttonsY, rows int }
	shapeOf := func(lines []string, zones []albumZone) shape {
		s := shape{rows: len(lines)}
		for _, z := range zones {
			if strings.Contains(ansi.Strip(lines[z.y]), "Play") {
				s.buttonsY = z.y
				s.textX = z.x0 + 1
				break
			}
		}
		return s
	}
	for _, sz := range [][2]int{{80, 24}, {114, 36}, {174, 49}} {
		w, h := sz[0], sz[1]
		av := NewAlbumView(a)
		al := av.header(w, h, c)
		pv := newPlaylistView(pl).(*tracksView)
		pll := pv.header(w, h, c)
		ar := newArtistPage(albumTestLib.Artists[0])
		arl := ar.header(w, h, c)
		sa, sp, sr := shapeOf(al, av.zones), shapeOf(pll, pv.zones), shapeOf(arl, ar.zones)
		if sa != sp || sa != sr {
			t.Errorf("%dx%d: album %+v, playlist %+v, artist %+v", w, h, sa, sp, sr)
		}
		for _, l := range append(append(al, pll...), arl...) {
			if got := ansi.StringWidth(l); got != w {
				t.Fatalf("%dx%d: header line %d wide", w, h, got)
			}
		}
	}
}

func TestTitleLineDropsMetaThatDoesNotFit(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	for _, tc := range []struct {
		w          int
		meta, want string
	}{
		{40, "3,158 songs · 9 days", "3,158 songs · 9 days"},
		{28, "3,158 songs · 9 days", "3,158 songs"},
		{20, "523", "523"},
		{17, "3,158 songs", ""},
	} {
		s := ansi.Strip(titleLine(c, tc.w, "Albums", tc.meta))
		got := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "Albums"))
		if got != tc.want {
			t.Errorf("w=%d %q: meta %q, want %q", tc.w, tc.meta, got, tc.want)
		}
	}
}

// The Artists list marks its selection like every list: the marker and the
// sel background, the name in the text colour.
func TestArtistsSelectionLooksLikeLists(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	v := NewArtistsView(albumTestLib)
	v.Render(120, 30, c)
	row := strings.Split(v.renderList(c, 5), "\n")[0]
	if strings.Contains(row, ansi.Strip(row)) {
		t.Fatal("selected row has no styling")
	}
	p := paintsFor(c.Theme)
	name := v.items[0].Name
	if !strings.Contains(row, p.pre[toneText][surfSel]) || strings.Contains(row, p.pre[toneAccent][surfSel]+name) {
		t.Fatalf("selected artist is not fg on sel: %q", row)
	}
}

// Every view's content ends at the same column, and the title row's
// controls stay put when the grid becomes a list.
func TestOneRightEdge(t *testing.T) {
	c, _ := albumTestCtx(albumTestLib)
	for _, w := range []int{73, 114, 174} {
		v := NewAlbumsView(albumTestLib)
		v.Render(w, 30, c)
		grid := v.zones[len(v.zones)-1].x1
		v.grid.SetList(true)
		v.Render(w, 30, c)
		if list := v.zones[len(v.zones)-1].x1; list != grid || list != contentRight(w) {
			t.Errorf("w=%d: controls end at %d (grid) and %d (list), want %d", w, grid, list, contentRight(w))
		}
		if g := layoutGrid(w, 0); g.right() != contentRight(w) {
			t.Errorf("w=%d: grid ends at %d", w, g.right())
		}
	}
}
