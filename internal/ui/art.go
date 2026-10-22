package ui

import (
	"bytes"
	"container/list"
	"context"
	"image"
	_ "image/jpeg" // artwork may arrive as JPEG
	_ "image/png"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/art"
	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/theme"
)

const (
	// tileArtPx is the artwork size fetched for grid tiles. Half blocks show
	// cols×2·rows pixels, so even a wide tile needs well under this.
	tileArtPx = 128
	// tileImagePx is the size fetched for tiles drawn as images (iTerm2's
	// protocol or sixel): enough to look crisp in a tile, small enough to
	// send again on every scroll.
	tileImagePx = 256
	// tileImageMaxPx caps the longest side of a tile image as encoded for
	// the terminal. Without it a tile is encoded at its cells' pixel size
	// (380 px on a Retina screen), upscaled from tileImagePx, and every
	// wheel step re-sends each one; the terminal scales it up just as well.
	// Sixel images can't be scaled by the terminal and ignore the cap.
	tileImageMaxPx = 240
	// heroArtPx is the size fetched for the player bar and page headers.
	heroArtPx = 600

	// heroImages is how many images, besides grid tiles, a frame can hold:
	// the player bar's, a page header's, and two more for the pieces the
	// play badge cuts a row of tiles into. Under an image limit (sixel in
	// tmux) the grid gets what is left.
	heroImages = 4

	// maxTileImages caps the tiles drawn as images in one frame; the rest
	// are half blocks. The art renderer keeps 96 decoded images, shared with
	// the player bar and page headers, and more tiles than that would evict
	// each other on every frame.
	maxTileImages = 64

	artWorkers    = 4
	artQueueMax   = 96 // requests from older frames are dropped; art still on screen asks again
	tileCacheMax  = 512
	heroSizesMax  = 256 // hero and tile-image sizes
	artRetryAfter = 30 * time.Second
	// heroRetryAfter is artRetryAfter for hero art. Music can be slow to
	// answer while tracks are skipped quickly, and the player bar should
	// not stay empty for half a minute after one timeout.
	heroRetryAfter = 3 * time.Second
	// heroNoneRetry is how long after Music reported no artwork for a pid
	// hero art asks again, doubling each time up to heroNoneRetryMax, for as
	// long as it is on screen: a station or catalog song that just started
	// reports none until Music has fetched its artwork, which can take a
	// while. After heroNoneTries answers the track counts as having none.
	// Tiles take the first answer. bridge.NoArtworkTTL must not be longer
	// than heroNoneRetry, or the retry gets the bridge client's remembered
	// answer.
	heroNoneRetry    = time.Second
	heroNoneRetryMax = 30 * time.Second
	heroNoneTries    = 4

	// nowArtHold is how long after the track changed the now-playing art
	// may keep showing the last track's art while the new one loads or
	// Music is still fetching it. After that it is the new track's or the
	// placeholder, never another song's.
	nowArtHold = 4 * time.Second
)

// TileKeyPrefix starts the renderer key of every grid tile's image. Inside
// tmux, give it to the renderer as art.Options.TilePrefix: tiles then go as
// iTerm2 images through passthrough, the rest as sixel.
const TileKeyPrefix = "tile:"

// tileImageKey is the renderer key for a tile's image. Tiles are fetched
// smaller than hero art, so they must not share the album's key.
func tileImageKey(key string) string { return TileKeyPrefix + key }

// artMsg means artwork finished loading; the frame is redrawn.
type artMsg struct{}

// artReq is one artwork load: a half-block tile of w×h cells (plus, when
// tiles are images, the tile image prepared at w×h), or a hero block
// prepared at w×h by the art renderer.
type artReq struct {
	key, pid string
	w, h     int
	hero     bool
}

// artJob is a queued request. Workers take the one asked for in the latest
// frame first and, within a frame, in the order it was asked for: the player
// bar, then the page header, then grid tiles from the top left.
type artJob struct {
	req      artReq
	gen, seq uint64
}

type artSize struct {
	key  string
	w, h int
}

// noArt is when Music last reported no artwork for a pid, and how often.
type noArt struct {
	at time.Time
	n  int
}

// retry is how long after the last answer hero art asks again.
func (e noArt) retry() time.Duration {
	d := heroNoneRetry
	for i := 1; i < e.n && d < heroNoneRetryMax; i++ {
		d *= 2
	}
	return min(d, heroNoneRetryMax)
}

// artState is where a load stands, for art that is not ready.
type artState int

const (
	artLoading artState = iota // queued or under way
	artWaiting                 // Music had none, or failed, a moment ago; asked again soon
	artMissing                 // Music has none, after the quick retries (or a tile's first answer)
)

func (st artState) String() string {
	return [...]string{"loading", "waiting", "missing"}[st]
}

// artStore loads artwork off the UI goroutine. View asks for a block; on a
// miss the store queues a load (deduped per key and size, latest frame first,
// artWorkers at a time) and the app redraws when the load is done. Tiles are
// half blocks cached here; hero art lives in the renderer, which speaks the
// terminal's protocol.
type artStore struct {
	client bridge.Client
	r      *art.Renderer

	mu       sync.Mutex
	cond     *sync.Cond
	started  bool
	queue    []artJob
	gen, seq uint64 // frame and request counters for queue order
	busy     map[artReq]bool
	tiles    *lruCache[artSize, string]
	prepared *lruCache[artSize, bool] // hero sizes Prepare has run for
	none     map[string]noArt         // pids Music reported no artwork for
	failed   map[artReq]time.Time
	ready    chan struct{}
	now      func() time.Time
	// after runs f once d has passed (time.AfterFunc); wakeAt is the
	// earliest wake-up it has been asked for.
	after  func(d time.Duration, f func())
	wakeAt time.Time
	// tileImages: grid tiles are drawn as images too (iTerm2's protocol or
	// sixel).
	tileImages bool
	// rowImages: the terminal keeps only a few images (art = "sixel" in
	// tmux), so grid tiles are drawn a row at a time, each row one image
	// (see Ctx.TileRow), at most rowBudget rows per frame.
	rowImages bool
	rowBudget int
	composing map[string]bool
	// playlists holds how to draw each playlist's art, by key (see
	// Ctx.PlaylistArt).
	playlists map[string]plArtSpec
	// urls loads artwork given by URL (Apple Music catalog items) instead
	// of a pid.
	urls urlArt
}

func newArtStore(client bridge.Client, r *art.Renderer) *artStore {
	s := &artStore{
		client:    client,
		r:         r,
		busy:      map[artReq]bool{},
		tiles:     newLRUCache[artSize, string](tileCacheMax),
		prepared:  newLRUCache[artSize, bool](heroSizesMax),
		none:      map[string]noArt{},
		failed:    map[artReq]time.Time{},
		composing: map[string]bool{},
		playlists: map[string]plArtSpec{},
		ready:     make(chan struct{}, 1),
		now:       time.Now,
		after:     func(d time.Duration, f func()) { time.AfterFunc(d, f) },

		tileImages: r != nil && r.Protocol().Placed(),
	}
	s.cond = sync.NewCond(&s.mu)
	if s.tileImages {
		r.Limit(tileImageKey(""), tileImageMaxPx)
		if n := r.ImageLimit(); n > 0 {
			s.rowBudget = max(n-heroImages, 0)
			s.rowImages = true
		}
	}
	return s
}

// wait returns a command that delivers artMsg after the next load finishes.
func (s *artStore) wait() tea.Cmd {
	ready := s.ready
	return func() tea.Msg {
		<-ready
		return artMsg{}
	}
}

// block returns the artwork block for key at w×h cells, or ok=false while it
// loads, when there is none, and when art is off.
func (s *artStore) block(key, pid string, w, h int, mode ArtMode) (string, bool) {
	if s == nil || s.r == nil || s.r.Protocol() == art.ProtoOff || key == "" || pid == "" {
		return "", false
	}
	sz := artSize{key, w, h}
	req := artReq{key: key, pid: pid, w: w, h: h, hero: mode == ArtHero}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !req.hero {
		if b, ok := s.tiles.get(sz); ok {
			return b, true
		}
		s.wantLocked(req)
		return "", false
	}
	if _, ok := s.prepared.get(sz); ok && s.r.Has(key) {
		return s.r.Block(key, nil, w, h), true
	}
	s.wantLocked(req)
	return "", false
}

// tileImage reports whether the image for tile key is ready to draw at w×h,
// and queues its load when it is not. For iTerm2 images that means the
// encoded payload is kept: the decoded image may be long gone, and the tile
// still draws, so scrolling back over covers loads nothing. Pass decoded for
// a tile that is cut (at the grid's edge, beside an overlay): the pieces are
// cut from the decoded image.
func (s *artStore) tileImage(key, pid string, w, h int, decoded bool) bool {
	if s == nil || !s.tileImages || key == "" || pid == "" {
		return false
	}
	ik := tileImageKey(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.r.Ready(ik, w, h) && (!decoded || s.r.Has(ik)) {
		return true
	}
	s.wantLocked(artReq{key: key, pid: pid, w: w, h: h})
	return false
}

// heroReady reports whether hero art for key is ready to draw at w×h.
func (s *artStore) heroReady(key string, w, h int) bool {
	if s == nil || s.r == nil {
		return false
	}
	if strings.HasPrefix(key, TileKeyPrefix) {
		return s.r.Ready(key, w, h) // tiles are not tracked in prepared
	}
	s.mu.Lock()
	_, ok := s.prepared.get(artSize{key, w, h})
	s.mu.Unlock()
	return ok && s.r.Has(key)
}

// hero asks for hero art for key at w×h, like block, and reports whether
// it is ready to draw and, when it is not, where its load stands.
func (s *artStore) hero(key, pid string, w, h int) (bool, artState) {
	if s == nil || s.r == nil || key == "" || pid == "" {
		return false, artMissing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.prepared.get(artSize{key, w, h}); ok && s.r.Has(key) {
		return true, artLoading
	}
	return false, s.wantLocked(artReq{key: key, pid: pid, w: w, h: h, hero: true})
}

// answeredNone reports whether the last answer for pid was that it has no
// artwork.
func (s *artStore) answeredNone(pid string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.none[pid]
	return ok
}

// poke wakes the UI for a redraw.
func (s *artStore) poke() {
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

// wakeLocked makes sure the UI redraws d from now, when hero art is due to
// be asked for again: nothing else may redraw (paused, or the terminal in
// the background).
func (s *artStore) wakeLocked(d time.Duration) {
	now := s.now()
	at := now.Add(d)
	if s.wakeAt.After(now) && !s.wakeAt.After(at) {
		return // an earlier wake-up is coming; that frame asks for the next
	}
	s.wakeAt = at
	s.after(d, s.poke)
}

// frame starts a new frame: what it asks for goes ahead of what earlier
// frames asked for and no longer show.
func (s *artStore) frame() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.gen++
	s.mu.Unlock()
}

// wantLocked asks for req: it queues a load unless one is queued or under
// way, Music said a moment ago there is none, or the last load failed a
// moment ago. It reports which.
func (s *artStore) wantLocked(req artReq) artState {
	if e, ok := s.none[req.pid]; ok {
		if !req.hero {
			return artMissing
		}
		if wait := e.retry() - s.now().Sub(e.at); wait > 0 {
			s.wakeLocked(wait)
			if e.n >= heroNoneTries {
				return artMissing
			}
			return artWaiting
		}
	}
	s.seq++
	if s.busy[req] {
		// asked for again: move it up to this frame's place
		for i := range s.queue {
			if s.queue[i].req == req {
				s.queue[i].gen, s.queue[i].seq = s.gen, s.seq
				break
			}
		}
		return artLoading
	}
	retry := artRetryAfter
	if req.hero {
		retry = heroRetryAfter
	}
	if at, ok := s.failed[req]; ok {
		if wait := retry - s.now().Sub(at); wait > 0 {
			if req.hero {
				s.wakeLocked(wait)
			}
			return artWaiting
		}
	}
	if !s.started {
		s.started = true
		for range artWorkers {
			go s.work()
		}
	}
	s.busy[req] = true
	s.queue = append(s.queue, artJob{req, s.gen, s.seq})
	artf("queue %v frame %d", req, s.gen)
	if len(s.queue) > artQueueMax {
		i := s.lastLocked()
		artf("drop %v (queue full)", s.queue[i].req)
		delete(s.busy, s.queue[i].req)
		s.queue = slices.Delete(s.queue, i, i+1)
	}
	s.cond.Signal()
	return artLoading
}

// before reports whether job a is served before job b: the latest frame's
// first, hero art (the player bar, a page header) ahead of grid tiles, then
// in the order the frame asked.
func (a artJob) before(b artJob) bool {
	if a.gen != b.gen {
		return a.gen > b.gen
	}
	if a.req.hero != b.req.hero {
		return a.req.hero
	}
	return a.seq < b.seq
}

// pruneLocked drops the queued jobs no frame has asked for since the one
// before the latest: their art is off screen (a skipped track, a page left
// behind, tiles scrolled away). Art still on screen asks again every frame
// until it is ready, so it is never dropped, and the workers go to what is
// on screen now.
func (s *artStore) pruneLocked() {
	keep := s.queue[:0]
	for _, j := range s.queue {
		if j.gen+1 < s.gen {
			artf("prune %v asked in frame %d, now %d", j.req, j.gen, s.gen)
			delete(s.busy, j.req)
			continue
		}
		keep = append(keep, j)
	}
	clear(s.queue[len(keep):])
	s.queue = keep
}

// firstLocked and lastLocked are the indexes of the queued jobs served first
// and last. The queue is not empty.
func (s *artStore) firstLocked() int {
	best := 0
	for i := range s.queue {
		if s.queue[i].before(s.queue[best]) {
			best = i
		}
	}
	return best
}

func (s *artStore) lastLocked() int {
	worst := 0
	for i := range s.queue {
		if s.queue[worst].before(s.queue[i]) {
			worst = i
		}
	}
	return worst
}

// forget drops what is known about missing artwork, after the library
// changed.
func (s *artStore) forget() {
	if s == nil {
		return
	}
	s.mu.Lock()
	clear(s.none)
	clear(s.failed)
	s.mu.Unlock()
}

// cellSizeChanged drops the prepared hero sizes; the renderer re-encodes
// for the new cell size.
func (s *artStore) cellSizeChanged() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.prepared = newLRUCache[artSize, bool](heroSizesMax)
	s.mu.Unlock()
}

func (s *artStore) work() {
	for {
		s.mu.Lock()
		s.pruneLocked()
		for len(s.queue) == 0 {
			s.cond.Wait()
			s.pruneLocked()
		}
		i := s.firstLocked()
		req := s.queue[i].req
		s.queue = slices.Delete(s.queue, i, i+1)
		artf("start %v", req)
		s.mu.Unlock()

		t0 := time.Now()
		tile, none, err := s.load(req)
		took := time.Since(t0).Round(time.Millisecond)

		s.mu.Lock()
		delete(s.busy, req)
		sz := artSize{req.key, req.w, req.h}
		switch {
		case err != nil:
			s.failed[req] = s.now()
			artf("failed %v in %v: %v", req, took, err)
			if req.hero {
				s.wakeLocked(heroRetryAfter)
			}
		case none:
			e := noArt{at: s.now(), n: s.none[req.pid].n + 1}
			s.none[req.pid] = e
			artf("none %v in %v (answer %d, next in %v)", req, took, e.n, e.retry())
			if req.hero {
				s.wakeLocked(e.retry())
			}
		case req.hero:
			s.prepared.put(sz, true)
			delete(s.none, req.pid) // Music had it after all
			artf("done %v in %v", req, took)
		default:
			artf("done %v in %v", req, took)
			if tile != "" {
				s.tiles.put(sz, tile)
			}
		}
		s.mu.Unlock()
		if err == nil && !none {
			s.poke()
		}
	}
}

// load fetches and prepares one request. Art the renderer has already
// decoded only needs preparing at the new size. A tile whose image is
// decoded but not cached as half blocks is fetched again.
func (s *artStore) load(req artReq) (tile string, none bool, err error) {
	if req.hero && s.r.Has(req.key) {
		return "", false, s.r.Prepare(req.key, nil, req.w, req.h)
	}
	ik := tileImageKey(req.key)
	if !req.hero && s.tileImages && s.r.Has(ik) && s.hasTile(req) {
		return "", false, s.r.Prepare(ik, nil, req.w, req.h) // encodes unless the payload is kept
	}
	px := tileArtPx
	switch {
	case req.hero:
		px = heroArtPx
	case s.tileImages:
		px = tileImagePx
	}
	var data []byte
	switch {
	case isArtURL(req.pid):
		data, none, err = s.urls.load(req.key, req.pid, px)
	case isPlaylistArt(req.pid):
		data, none, err = s.loadPlaylist(req.pid, px)
	default:
		data, none, err = s.loadBridge(req.pid, px)
	}
	if err != nil || none {
		return "", none, err
	}
	if req.hero {
		return "", false, s.r.Prepare(req.key, data, req.w, req.h)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", false, err
	}
	if s.tileImages {
		if err := s.r.PrepareDecoded(ik, img, req.w, req.h); err != nil {
			return "", false, err
		}
	}
	return art.Blocks(img, req.w, req.h), false, nil
}

// loadBridge fetches pid's artwork at px through the bridge.
func (s *artStore) loadBridge(pid string, px int) (data []byte, none bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), bridge.ArtworkTimeout)
	a, err := s.client.Artwork(ctx, pid, px)
	cancel()
	if err != nil {
		return nil, false, err
	}
	if a.Path == "" {
		return nil, true, nil
	}
	data, err = os.ReadFile(a.Path)
	return data, false, err
}

// compose builds a row composite (see Ctx.TileRow) in the background and
// redraws when it is ready.
func (s *artStore) compose(key string, cols, rows int, parts []art.ComposePart) {
	s.mu.Lock()
	if s.composing[key] {
		s.mu.Unlock()
		return
	}
	s.composing[key] = true
	s.mu.Unlock()
	go func() {
		ok := s.r.Compose(key, cols, rows, parts)
		s.mu.Lock()
		delete(s.composing, key)
		s.mu.Unlock()
		if ok {
			s.poke()
		}
	}()
}

func (s *artStore) hasTile(req artReq) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.tiles.get(artSize{req.key, req.w, req.h})
	return ok
}

// Art returns a w×h block of artwork for key, fetched with pid. It is the
// single place artwork enters the UI. Grid tiles are half blocks; hero art
// uses the terminal's best protocol (draw it with Ctx.HeroArt so it can be
// placed). Until the art arrives, and for albums without any, it is a calm
// square in the selection colour.
func (a *App) Art(key, pid string, w, h int, mode ArtMode) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if s, ok := a.art.block(key, pid, w, h, mode); ok {
		return s
	}
	return artPlaceholder(a.theme, w, h)
}

// HeroArt is Host.Art in ArtHero mode for a block the caller draws at (x, y)
// of its own area (relative to Origin). It reports where the block is, so
// that images drawn after the frame (iTerm2's protocol, sixel) land on it.
//
// Like a grid tile, a block that overlays leave less than artMinShown of is
// blank.
func (c *Ctx) HeroArt(key, pid string, x, y, w, h int) string {
	if c.art.place == nil || w <= 0 || h <= 0 {
		return c.Host.Art(key, pid, w, h, ArtHero)
	}
	x0, y0 := c.Origin.X+x, c.Origin.Y+y
	p := artPlace{
		key:   key,
		box:   image.Rect(x0, y0, x0+w, y0+h),
		layer: c.art.layer,
		clip:  c.art.clip,
	}
	if a, ok := c.Host.(*App); ok && a.artShown(p) < artMinShown {
		return Blank(w, h)
	}
	s := c.Host.Art(key, pid, w, h, ArtHero)
	c.art.place(p)
	return s
}

// artRef names a piece of art: its key and the pid to fetch it with.
type artRef struct{ key, pid string }

// nowArt is what the now-playing art at one block size shows: the playing
// track (since when) and its art, and the last art drawn ready.
type nowArt struct {
	track string
	since time.Time
	shown artRef
	note  string // the last decision logged
}

// NowPlayingArt is HeroArt for the playing track's art, w×h cells at (x, y)
// of the caller's area. When the track changes, the block keeps the last
// track's art while the new art loads, for up to nowArtHold, rather than
// going blank for the load; every track skipped past meanwhile is
// forgotten. After that it is the placeholder until the art comes. A song
// from outside the library may get its art elsewhere (App.playingArt).
func (c *Ctx) NowPlayingArt(x, y, w, h int) string {
	key, pid := nowPlayingArt(c)
	if a, ok := c.Host.(*App); ok && a.art != nil && key != "" {
		track := key
		if ct := c.Player.NowPlaying.Track; ct != nil {
			track = ct.PID
		}
		key, pid = a.nowPlayingArt(track, a.playingArt(c, artRef{key, pid}), w, h)
	}
	return c.HeroArt(key, pid, x, y, w, h)
}

// nowPlayingArt asks for cur, the art of the playing track, at w×h and
// returns the art to draw.
func (a *App) nowPlayingArt(track string, cur artRef, w, h int) (key, pid string) {
	sz := image.Pt(w, h)
	if a.nowArt == nil || len(a.nowArt) > 8 {
		a.nowArt = map[image.Point]*nowArt{}
	}
	n := a.nowArt[sz]
	if n == nil {
		n = &nowArt{}
		a.nowArt[sz] = n
	}
	now := a.art.now()
	if n.track != track {
		n.track, n.since = track, now
	}
	show, note := cur, "ready"
	ready, st := a.art.hero(cur.key, cur.pid, w, h)
	switch {
	case ready:
		n.shown = cur
	case n.shown.key != "" && n.shown.key != cur.key && st != artMissing &&
		now.Sub(n.since) < nowArtHold && a.art.heroReady(n.shown.key, w, h):
		show, note = n.shown, st.String()+", showing the last track's "+n.shown.key
		a.art.wake(n.since.Add(nowArtHold).Sub(now))
	default:
		note = st.String() + ", placeholder"
	}
	if artLog != nil && note != n.note {
		n.note = note
		artf("now playing %s pid=%s %dx%d: %s", cur.key, cur.pid, w, h, note)
	}
	return show.key, show.pid
}

// wake is wakeLocked for the UI goroutine.
func (s *artStore) wake(d time.Duration) {
	s.mu.Lock()
	s.wakeLocked(d)
	s.mu.Unlock()
}

// TileArt is a grid tile's artwork, w×h cells drawn at (x, y) of the
// caller's area, of which the top vis rows are on screen. On terminals that
// take iTerm2 or sixel images it is a real image once loaded, when crisp is set (the
// grid passes false while it scrolls); otherwise, and until then, it is half
// blocks.
func (c *Ctx) TileArt(key, pid string, x, y, w, h, vis int, crisp bool) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if a, ok := c.Host.(*App); ok && c.art.place != nil {
		x0, y0 := c.Origin.X+x, c.Origin.Y+y
		box := image.Rect(x0, y0, x0+w, y0+h)
		p := artPlace{
			key:   tileImageKey(key),
			box:   box,
			crop:  image.Rect(x0, y0, x0+w, y0+min(max(vis, 0), h)),
			layer: c.art.layer,
			clip:  c.art.clip,
		}
		if a.art.tileImages && a.artShown(p) < artMinShown {
			// a sliver beside an overlay: no art reads better than a
			// strip of it
			return Blank(w, h)
		}
		if a.tileArt(key, pid, p, crisp) {
			return Blank(w, h)
		}
	}
	return c.Host.Art(key, pid, w, h, ArtTile)
}

// RowTile is a tile in a row passed to Ctx.TileRow.
type RowTile struct {
	Key, PID string
	X        int // column in the caller's area
}

// TileRow draws the art of a row of grid tiles, each w×h cells at line y of
// the caller's area with the top vis lines on screen, as a single image, and
// reports whether it did; the caller then leaves the tiles' art blank. It
// only does so where the terminal keeps few images (sixel inside tmux keeps
// nine, see art.TmuxImageLimit), every tile in the row has loaded, and crisp
// is set. Otherwise the caller draws each tile with TileArt, as half blocks.
func (c *Ctx) TileRow(tiles []RowTile, y, w, h, vis int, crisp bool) bool {
	a, ok := c.Host.(*App)
	if !ok || c.art.place == nil || a.art == nil || !a.art.rowImages || len(tiles) == 0 || w <= 0 || h <= 0 {
		return false
	}
	ready := true
	for _, t := range tiles {
		ready = a.art.tileImage(t.Key, t.PID, w, h, false) && ready // queue every load
	}
	if !ready || !crisp {
		return false
	}
	n := 0
	for _, q := range a.frameArt {
		if strings.HasPrefix(q.key, "row:") {
			n++
		}
	}
	if n >= a.art.rowBudget {
		return false
	}
	// Like single tiles, a tile that overlays leave less than artMinShown
	// of is left out (blank): a sliver of art against an overlay reads as
	// torn.
	oy := c.Origin.Y + y
	crop := func(x0, x1 int) image.Rectangle {
		return image.Rect(c.Origin.X+x0, oy, c.Origin.X+x1, oy+min(max(vis, 0), h))
	}
	var keep []RowTile
	for _, t := range tiles {
		box := image.Rect(c.Origin.X+t.X, oy, c.Origin.X+t.X+w, oy+h)
		if a.artShown(artPlace{box: box, crop: crop(t.X, t.X+w), layer: c.art.layer}) >= artMinShown {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		return true
	}
	x0, x1 := keep[0].X, keep[len(keep)-1].X+w
	var key strings.Builder
	key.WriteString("row:" + strconv.Itoa(w) + "x" + strconv.Itoa(h))
	parts := make([]art.ComposePart, len(keep))
	for i, t := range keep {
		key.WriteString("|" + strconv.Itoa(t.X-x0) + ":" + t.Key)
		parts[i] = art.ComposePart{Key: tileImageKey(t.Key), X: t.X - x0, Cols: w, Rows: h}
	}
	if !a.artR.Composed(key.String(), x1-x0, h) {
		a.art.compose(key.String(), x1-x0, h, parts) // half blocks until it's done
		return false
	}
	p := artPlace{
		key:       key.String(),
		box:       image.Rect(c.Origin.X+x0, oy, c.Origin.X+x1, oy+h),
		crop:      crop(x0, x1),
		layer:     c.art.layer,
		clip:      c.art.clip,
		composite: true,
	}
	before := len(a.frameArt)
	a.placeArt(p)
	return len(a.frameArt) > before
}

// CoverArt reports that the caller draws over w×h cells at (x, y) of its
// area on top of its own art (a badge on a tile), so images are cut around
// them.
func (c *Ctx) CoverArt(x, y, w, h int) {
	if c.art.place == nil || w <= 0 || h <= 0 {
		return
	}
	x0, y0 := c.Origin.X+x, c.Origin.Y+y
	c.art.place(artPlace{box: image.Rect(x0, y0, x0+w, y0+h), layer: c.art.layer, cover: true})
}

// tileArt places tile p as an image and reports true when it can be one:
// the terminal takes images, it is loaded, crisp is set, and the frame has
// room for another tile image. It queues the load either way.
func (a *App) tileArt(key, pid string, p artPlace, crisp bool) bool {
	cut := !p.crop.Empty() && p.crop != p.box || a.artShown(p) < 1
	if !a.art.tileImage(key, pid, p.box.Dx(), p.box.Dy(), cut) || !crisp || a.art.rowImages {
		return false
	}
	n := 0
	for _, q := range a.frameArt {
		if strings.HasPrefix(q.key, "tile:") {
			n++
		}
	}
	if n >= maxTileImages {
		return false
	}
	before := len(a.frameArt)
	a.placeArt(p)
	return len(a.frameArt) > before
}

// artMinShown is how much of a grid tile or hero block must stay in view
// beside an overlay for it to keep its art, cropped cleanly around the
// overlay. With less, it is left blank: a sliver of art against an overlay's
// border reads as torn.
const artMinShown = 0.5

// artShown is the fraction of p's visible cells that no overlay above its
// layer covers.
func (a *App) artShown(p artPlace) float64 {
	vis := p.box
	if !p.crop.Empty() {
		vis = p.crop
	}
	if vis.Empty() {
		return 0
	}
	parts := []image.Rectangle{vis}
	for _, c := range a.frameCovers {
		if !c.overlay || c.layer <= p.layer {
			continue
		}
		var next []image.Rectangle
		for _, r := range parts {
			next = append(next, subtractRect(r, c.rect)...)
		}
		parts = next
	}
	n := 0
	for _, r := range parts {
		n += r.Dx() * r.Dy()
	}
	return float64(n) / float64(vis.Dx()*vis.Dy())
}

// artSink is where a Ctx reports hero art: the layer it draws on (0 for the
// base frame, i+1 for overlay i) and the rectangle its output is cut to.
type artSink struct {
	place func(artPlace)
	layer int
	clip  image.Rectangle
}

// artPlace is a hero block drawn this frame, in screen cells. crop, when
// set, is the part of box on screen (a tile cut off at the bottom of its
// grid). A cover is not art but something drawn over the art of its layer.
type artPlace struct {
	key   string
	box   image.Rectangle
	crop  image.Rectangle
	layer int
	clip  image.Rectangle
	cover bool
	// composite: built with Renderer.Compose, so ready once placed
	composite bool
}

// artCover is something drawn over the frame at a layer: an overlay, the
// which-key popup, or a badge or crop over a block (overlay false).
type artCover struct {
	rect    image.Rectangle
	layer   int
	overlay bool
}

// artOcclusion turns layered placements and covers into what the renderer
// understands: placements, and covers that hide art wherever they are. A
// cover is cut around art drawn on its own layer or above; art below that
// overlaps such higher art is left out instead.
func artOcclusion(places []artPlace, covers []artCover) ([]artPlace, []image.Rectangle) {
	var keep []artPlace
	for _, p := range places {
		hidden := false
		for _, q := range places {
			if q.layer > p.layer && q.box.Overlaps(p.box) {
				hidden = true
				break
			}
		}
		if !hidden {
			keep = append(keep, p)
		}
	}
	var rects []image.Rectangle
	for _, c := range covers {
		parts := []image.Rectangle{c.rect}
		for _, p := range keep {
			if p.layer < c.layer {
				continue
			}
			var next []image.Rectangle
			for _, r := range parts {
				next = append(next, subtractRect(r, p.box)...)
			}
			parts = next
		}
		rects = append(rects, parts...)
	}
	return keep, rects
}

// subtractRect returns r minus o as up to four rectangles.
func subtractRect(r, o image.Rectangle) []image.Rectangle {
	in := r.Intersect(o)
	if in.Empty() {
		return []image.Rectangle{r}
	}
	var out []image.Rectangle
	for _, c := range []image.Rectangle{
		image.Rect(r.Min.X, r.Min.Y, r.Max.X, in.Min.Y),
		image.Rect(r.Min.X, in.Max.Y, r.Max.X, r.Max.Y),
		image.Rect(r.Min.X, in.Min.Y, in.Min.X, in.Max.Y),
		image.Rect(in.Max.X, in.Min.Y, r.Max.X, in.Max.Y),
	} {
		if !c.Empty() {
			out = append(out, c)
		}
	}
	return out
}

// placeArt records a hero block drawn this frame. Blocks cut by their pane
// and art that hasn't loaded are left out. A cropped block is cut by covers
// over the part that is off its grid; those covers sit just above its layer,
// so art on higher layers (an overlay) keeps those cells.
func (a *App) placeArt(p artPlace) {
	if a.artR == nil || !a.artR.Protocol().Placed() {
		return
	}
	if p.cover {
		a.frameCovers = append(a.frameCovers, artCover{rect: p.box, layer: p.layer + 1})
		return
	}
	vis := p.box
	if !p.crop.Empty() {
		vis = p.crop
	}
	if !vis.In(p.clip) || (!p.composite && !a.art.heroReady(p.key, p.box.Dx(), p.box.Dy())) {
		return
	}
	a.frameArt = append(a.frameArt, p)
	if vis != p.box {
		for _, r := range subtractRect(p.box, vis) {
			a.frameCovers = append(a.frameCovers, artCover{rect: r, layer: p.layer + 1})
		}
	}
}

// commitArt hands the frame's placements and covers to the renderer. s is
// exactly the string given to tea.NewView.
func (a *App) commitArt(s string) {
	r := a.artR
	if r == nil {
		return
	}
	places, covers := artOcclusion(a.frameArt, a.frameCovers)
	for _, p := range places {
		r.Place(p.key, p.box.Min.X, p.box.Min.Y, p.box.Dx(), p.box.Dy())
	}
	if artLog != nil {
		a.logPlaced(places)
	}
	for _, c := range covers {
		r.Occlude(c.Min.X, c.Min.Y, c.Dx(), c.Dy())
	}
	r.Frame(s)
}

// logPlaced logs the hero art placed this frame when it differs from the
// last frame's (tiles and tile rows are left out).
func (a *App) logPlaced(places []artPlace) {
	var b strings.Builder
	for _, p := range places {
		if strings.HasPrefix(p.key, TileKeyPrefix) || strings.HasPrefix(p.key, "row:") {
			continue
		}
		b.WriteString(" " + p.key + "@" + strconv.Itoa(p.box.Min.X) + "," + strconv.Itoa(p.box.Min.Y) +
			" " + strconv.Itoa(p.box.Dx()) + "x" + strconv.Itoa(p.box.Dy()))
	}
	if s := b.String(); s != a.lastPlaced {
		a.lastPlaced = s
		if s == "" {
			s = " nothing"
		}
		artf("placed%s, frame %d", s, a.art.frameNo())
	}
}

// frameNo is the number of the frame being drawn.
func (s *artStore) frameNo() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

// artRedrawMsg draws every image again.
type artRedrawMsg struct{}

// artRedrawDelay is how long after focus returns the images are drawn
// again. tmux reports focus to the pane before it redraws the window, and
// that redraw would wipe images drawn straight away.
const artRedrawDelay = 150 * time.Millisecond

// redrawFragileArt draws the images again once tmux is done redrawing, when
// they went through passthrough (back from another tmux window or pane,
// which wiped them). Sixel images need nothing: tmux redraws those itself,
// and neither do images art.WatchTmux looks after.
func (a *App) redrawFragileArt() tea.Cmd {
	if a.artR == nil || !a.artR.Fragile() || a.artR.Watched() {
		return nil
	}
	return tea.Tick(artRedrawDelay, func(time.Time) tea.Msg { return artRedrawMsg{} })
}

// artResize tells the renderer the window size and asks the terminal for its
// cell size again.
func (a *App) artResize(w, h int) tea.Cmd {
	if a.artR == nil {
		return nil
	}
	a.artR.Resize(w, h)
	return tea.Raw(art.CellSizeQuery)
}

type artBlank struct {
	t    *theme.Theme
	w, h int
}

var artBlanks = map[artBlank]string{}

func artPlaceholder(t *theme.Theme, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	k := artBlank{t, w, h}
	if s, ok := artBlanks[k]; ok {
		return s
	}
	row := paintsFor(t).spaces(surfSel, w)
	s := strings.TrimSuffix(strings.Repeat(row+"\n", h), "\n")
	if len(artBlanks) > 64 {
		clear(artBlanks)
	}
	artBlanks[k] = s
	return s
}

// lruCache is a small least-recently-used map. Not safe for concurrent use.
type lruCache[K comparable, V any] struct {
	cap   int
	ll    *list.List
	items map[K]*list.Element
}

type lruItem[K comparable, V any] struct {
	k K
	v V
}

func newLRUCache[K comparable, V any](capacity int) *lruCache[K, V] {
	return &lruCache[K, V]{cap: capacity, ll: list.New(), items: map[K]*list.Element{}}
}

func (c *lruCache[K, V]) get(k K) (V, bool) {
	if e, ok := c.items[k]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*lruItem[K, V]).v, true
	}
	var zero V
	return zero, false
}

func (c *lruCache[K, V]) put(k K, v V) {
	if e, ok := c.items[k]; ok {
		e.Value.(*lruItem[K, V]).v = v
		c.ll.MoveToFront(e)
		return
	}
	c.items[k] = c.ll.PushFront(&lruItem[K, V]{k, v})
	for c.ll.Len() > c.cap {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.items, last.Value.(*lruItem[K, V]).k)
	}
}
