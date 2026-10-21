package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/search"
	"github.com/bogdankim/encore/internal/theme"
)

var (
	ovLibOnce sync.Once
	ovLib     *library.Library
	ovIdx     *search.Index
)

// ovTestCtx returns a Ctx over the demo library with a recording host.
func ovTestCtx(t *testing.T) (*Ctx, *ovRecorder) {
	t.Helper()
	ovLibOnce.Do(func() {
		f := bridge.NewFakeWithClock(1, func() time.Time { return time.Unix(1_760_000_000, 0) })
		snap, err := f.Snapshot(context.Background())
		if err != nil {
			panic(err)
		}
		ovLib = library.Build(snap)
		ovIdx = search.NewIndex(ovLib)
	})
	h := &ovRecorder{}
	c := &Ctx{
		Theme: theme.New(theme.Lookup("encore", true), theme.Unicode, false),
		Lib:   ovLib, Index: ovIdx, Keys: keymap.New(), Host: h, Now: time.Now(),
	}
	return c, h
}

// ovRecorder is a Host that records calls in order.
type ovRecorder struct {
	calls []string
	play  struct {
		src    player.Source
		tracks []*library.Track
		i      int
	}
	pushed []Overlay
	opened []View
	openIn []Subject
}

func (h *ovRecorder) rec(format string, a ...any) tea.Cmd {
	h.calls = append(h.calls, fmt.Sprintf(format, a...))
	return nil
}

func (h *ovRecorder) Play(src player.Source, ts []*library.Track, i int) tea.Cmd {
	h.play.src, h.play.tracks, h.play.i = src, ts, i
	return h.rec("Play")
}
func (h *ovRecorder) PlayNext(ts []*library.Track) tea.Cmd   { return h.rec("PlayNext %d", len(ts)) }
func (h *ovRecorder) AddToQueue(ts []*library.Track) tea.Cmd { return h.rec("AddToQueue %d", len(ts)) }
func (h *ovRecorder) OpenInMusic(kind, id, name string) tea.Cmd {
	return h.rec("OpenInMusic %s %s %s", kind, id, name)
}
func (h *ovRecorder) ToggleFavorite(ts []*library.Track) tea.Cmd {
	return h.rec("ToggleFavorite %d", len(ts))
}
func (h *ovRecorder) Open(v View) tea.Cmd {
	h.opened = append(h.opened, v)
	return h.rec("Open %s", v.Key())
}
func (h *ovRecorder) OpenAlbum(a *library.Album) tea.Cmd   { return h.rec("OpenAlbum %s", a.Title) }
func (h *ovRecorder) OpenArtist(a *library.Artist) tea.Cmd { return h.rec("OpenArtist %s", a.Name) }
func (h *ovRecorder) OpenPlaylist(p *library.Playlist) tea.Cmd {
	return h.rec("OpenPlaylist %s", p.Name)
}
func (h *ovRecorder) OpenIn(act keymap.Action, s Subject) tea.Cmd {
	h.openIn = append(h.openIn, s)
	return h.rec("OpenIn %s", act)
}
func (h *ovRecorder) PushOverlay(o Overlay) tea.Cmd {
	h.pushed = append(h.pushed, o)
	return h.rec("PushOverlay")
}
func (h *ovRecorder) CloseOverlay() tea.Cmd { return h.rec("CloseOverlay") }
func (h *ovRecorder) Menu(x, y int, ts []*library.Track) tea.Cmd {
	return h.rec("Menu %d,%d %d", x, y, len(ts))
}
func (h *ovRecorder) Status(msg string) tea.Cmd                           { return h.rec("Status %s", msg) }
func (h *ovRecorder) Do(a keymap.Action) tea.Cmd                          { return h.rec("Do %s", a) }
func (h *ovRecorder) Art(key, pid string, w, ht int, mode ArtMode) string { return Blank(w, ht) }

func (h *ovRecorder) last() string {
	if len(h.calls) == 0 {
		return ""
	}
	return h.calls[len(h.calls)-1]
}

// ovType feeds text to an overlay one key at a time and returns the last
// command.
func ovType(o Overlay, c *Ctx, text string) tea.Cmd {
	var cmd tea.Cmd
	for _, r := range text {
		cmd = o.Key(tea.KeyPressMsg{Code: r, Text: string(r)}, c)
	}
	return cmd
}

// ovSettle runs cmd and hands its message back to the overlay.
func ovSettle(o Overlay, c *Ctx, cmd tea.Cmd) {
	if cmd != nil {
		o.Update(cmd(), c)
	}
}

// ovCheckSize renders o on a screen and checks it fills its Rect exactly.
func ovCheckSize(t *testing.T, name string, o Overlay, c *Ctx, sw, sh int) {
	t.Helper()
	r := o.Rect(sw, sh)
	if r.Min.X < 0 || r.Min.Y < 0 || r.Max.X > sw || r.Max.Y > sh {
		t.Fatalf("%s at %dx%d: rect %v leaves the screen", name, sw, sh, r)
	}
	out := o.Render(c)
	lines := strings.Split(out, "\n")
	if r.Dy() == 0 {
		return
	}
	if len(lines) != r.Dy() {
		t.Fatalf("%s at %dx%d: %d lines, want %d", name, sw, sh, len(lines), r.Dy())
	}
	for i, l := range lines {
		if w := Width(l); w != r.Dx() {
			t.Fatalf("%s at %dx%d: line %d is %d wide, want %d: %q", name, sw, sh, i, w, r.Dx(), l)
		}
	}
}
