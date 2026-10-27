package art

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/jpeg" // decode JPEG artwork
	"image/png"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Options configure a Renderer.
type Options struct {
	Protocol Protocol
	// Passthrough wraps image sequences for tmux (see Resolve).
	Passthrough bool
	// Tmux: the program runs inside tmux. tmux drops every sixel image on a
	// line when anything erases, inserts or deletes characters on it, so the
	// Output treats such a change as touching the whole line.
	Tmux bool
	// TilePrefix, with Protocol sixel inside tmux and Passthrough set:
	// images whose key starts with it are iTerm2 images sent through tmux
	// passthrough instead (WezTerm and iTerm2). tmux keeps only nine sixel
	// images and redraws them itself; passthrough images have no limit but
	// vanish whenever tmux redraws the pane, see WatchTmux.
	TilePrefix string
	// Cell is the cell size in pixels; zero means DefaultCellSize until
	// SetCellSize is called.
	Cell CellSize
	// NoScroll turns off scrolling images in the terminal: a grid that
	// scrolls then sends every tile again at its new place.
	NoScroll bool
}

// Renderer turns artwork into terminal output for one program. Views call
// Block (and, for IIP, Place) while building a frame and Frame with the
// finished content; an Output wrapped around the terminal then draws IIP
// images after each frame and sends kitty uploads. See the package docs.
//
// All methods are safe for concurrent use: Prepare is meant to run in a
// tea.Cmd, the rest in View, and the Output calls in from the renderer's
// goroutine.
type Renderer struct {
	proto       Protocol
	passthrough bool
	tmux        bool
	tilePrefix  string
	now         func() time.Time

	mu        sync.Mutex
	cell      CellSize
	cellKnown bool // sixel images wait for it: their pixel size is their cell size
	width     int
	height    int

	images    *lru[string, image.Image]
	blocks    *lru[boxKey, string]
	boxes     *lru[boxKey, *image.NRGBA] // IIP: the image scaled to its box
	sixels    *lru[boxKey, *sixelImage]  // sixel: the box reduced to a palette
	payloads  *lru[pieceKey, []byte]     // IIP: encoded pieces
	kittyIDs  *lru[boxKey, uint32]
	limits    []limit             // per-key-prefix pixel caps, see Limit
	qualities []limit             // per-key-prefix JPEG qualities, see Quality
	composed  map[string]CellSize // composites in images, and the cell size they were built for
	encoding  map[pieceKey]bool   // sixel images being encoded in the background
	nextID    uint32
	oob       strings.Builder // pending out-of-band output (kitty)

	// The frame being built, and the last committed one.
	places []placement
	occl   []rect
	lines  []string
	pieces []piece
	// screen is the text the terminal shows: the lines of the frame Bubble
	// Tea last wrote (see Output.Write), nil when that is not known.
	screen []string

	shown    map[piece]bool // IIP pieces believed to be on screen
	plan     *scrollPlan    // the scroll the output being written carries out, see takePlan
	noScroll bool           // never scroll images in the terminal, see Options.NoScroll
	dirtyAll bool
	notify   func()
	// notifyAfter asks for an output pass after d (set by the Output).
	notifyAfter func(d time.Duration)

	// Passthrough images inside tmux (see WatchTmux): none are drawn while
	// the pane is hidden or before holdUntil, when tmux is about to redraw
	// the pane and would drop or wipe them.
	pane      PaneState
	holdUntil time.Time
	watched   bool // WatchTmux reports tmux redraws
}

// PaneState is what tmux says about the pane the program runs in.
type PaneState struct {
	// Hidden: no client shows the pane (another window is current, the
	// session is detached, another pane is zoomed, or the pane is in a mode
	// such as copy mode). Passthrough images would land on whatever the
	// terminal shows instead.
	Hidden bool
	// Split: the pane is narrower than the window, or the window is larger
	// than the client. tmux then redraws a scrolled region itself, the whole
	// pane when the region is large.
	Split bool
	// Origin is the terminal cell of the pane's top-left corner, when
	// Located. Passthrough images carry their own cursor move to it: tmux
	// does not always put the terminal's cursor where the pane's is before
	// passing a sequence through (after resetting its scroll region, for
	// one), and the image would land at the top of the screen.
	Origin  Cell
	Located bool
}

// Cell is a terminal cell, zero-based.
type Cell struct{ X, Y int }

type boxKey struct {
	key        string
	cols, rows int
}

// limit caps the longest side, in pixels, of images whose key starts with
// prefix.
type limit struct {
	prefix string
	px     int
}

// Cache sizes. Decoded images and scaled boxes hold the album grid's tile
// images plus the player bar and page headers.
const (
	imagesMax = 96
	// imagesBudget caps the decoded images' pixels: a 600 px hero image is
	// 1.4 MB, so 96 of them would be 138 MB.
	imagesBudget = 48 << 20
	boxesBudget  = 32 << 20
	boxesMax     = 96
	// Encoded payloads (the bytes sent to the terminal) are small, about
	// 11 KB for a grid tile, and drawing from one needs no decoded image.
	// They are kept for a whole library of tiles within a byte budget, so
	// scrolling back over covers decodes and encodes nothing. They are
	// the whole boxes and the pieces overlays cut them into.
	payloadsMax    = 4096
	payloadsBudget = 8 << 20
)

// imageBytes is the memory an image's pixels take (4 bytes each at most).
func imageBytes(m image.Image) int {
	b := m.Bounds()
	return b.Dx() * b.Dy() * 4
}

func nrgbaBytes(m *image.NRGBA) int { return len(m.Pix) }

func newPayloadLRU() *lru[pieceKey, []byte] {
	return newCostLRU[pieceKey, []byte](payloadsMax, payloadsBudget, func(b []byte) int { return len(b) })
}

type rect struct{ x, y, w, h int }

func (r rect) empty() bool { return r.w <= 0 || r.h <= 0 }

func (r rect) intersect(o rect) rect {
	x0, y0 := max(r.x, o.x), max(r.y, o.y)
	x1, y1 := min(r.x+r.w, o.x+o.w), min(r.y+r.h, o.y+o.h)
	return rect{x0, y0, x1 - x0, y1 - y0}
}

// subtract returns r minus o as up to four disjoint rectangles.
func (r rect) subtract(o rect) []rect {
	in := r.intersect(o)
	if in.empty() {
		return []rect{r}
	}
	var out []rect
	add := func(c rect) {
		if !c.empty() {
			out = append(out, c)
		}
	}
	add(rect{r.x, r.y, r.w, in.y - r.y})                          // above
	add(rect{r.x, in.y + in.h, r.w, r.y + r.h - (in.y + in.h)})   // below
	add(rect{r.x, in.y, in.x - r.x, in.h})                        // left
	add(rect{in.x + in.w, in.y, r.x + r.w - (in.x + in.w), in.h}) // right
	return out
}

// subtractCols is subtract with full-height strips left and right of o and
// the column above and below it. tmux sixel pieces lose up to five pixels at
// the bottom (see sixelPayload); cut this way, the loss sits next to o, not
// in the middle of the art.
func (r rect) subtractCols(o rect) []rect {
	in := r.intersect(o)
	if in.empty() {
		return []rect{r}
	}
	var out []rect
	for _, c := range []rect{
		{r.x, r.y, in.x - r.x, r.h},                          // left
		{in.x + in.w, r.y, r.x + r.w - (in.x + in.w), r.h},   // right
		{in.x, r.y, in.w, in.y - r.y},                        // above
		{in.x, in.y + in.h, in.w, r.y + r.h - (in.y + in.h)}, // below
	} {
		if !c.empty() {
			out = append(out, c)
		}
	}
	return out
}

type placement struct {
	key string
	box rect
}

// piece is the visible part of a placement: the whole box, or what an
// overlay leaves of it.
type piece struct {
	key string
	box rect
	vis rect
}

type pieceKey struct {
	box  boxKey
	crop rect // relative to the box, in cells
	cell CellSize
}

// NewRenderer returns a Renderer for o.Protocol.
func NewRenderer(o Options) *Renderer {
	r := &Renderer{
		proto:       o.Protocol,
		passthrough: o.Passthrough,
		tmux:        o.Tmux,
		now:         time.Now,
		noScroll:    o.NoScroll,
		cell:        o.Cell,
		cellKnown:   o.Cell.W > 0 && o.Cell.H > 0,
		images:      newCostLRU[string, image.Image](imagesMax, imagesBudget, imageBytes),
		blocks:      newLRU[boxKey, string](256, nil),
		boxes:       newCostLRU[boxKey, *image.NRGBA](boxesMax, boxesBudget, nrgbaBytes),
		sixels:      newLRU[boxKey, *sixelImage](boxesMax, nil),
		payloads:    newPayloadLRU(),
		shown:       map[piece]bool{},
		composed:    map[string]CellSize{},
		encoding:    map[pieceKey]bool{},
	}
	if r.cell.W <= 0 || r.cell.H <= 0 {
		r.cell = DefaultCellSize
	}
	if o.Protocol == ProtoSixel && o.Tmux && o.Passthrough {
		r.tilePrefix = o.TilePrefix
	}
	r.kittyIDs = newLRU(128, func(_ boxKey, id uint32) {
		r.oob.WriteString(r.wrap(KittyDelete(id)))
	})
	return r
}

// Protocol returns the protocol in use.
func (r *Renderer) Protocol() Protocol { return r.proto }

// Resize records the terminal size in cells (from tea.WindowSizeMsg). It
// invalidates everything on screen.
func (r *Renderer) Resize(width, height int) {
	r.mu.Lock()
	if r.width > 0 && (width != r.width || height != r.height) {
		r.holdLocked() // tmux redraws the pane after a resize
	}
	r.width, r.height = width, height
	r.invalidateLocked()
	r.mu.Unlock()
}

// SetCellSize records the cell size in pixels (see CellSizeQuery). Images are
// re-encoded for it.
func (r *Renderer) SetCellSize(c CellSize) {
	if c.W <= 0 || c.H <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	known := r.cellKnown
	r.cellKnown = true
	if c == r.cell {
		if !known {
			r.invalidateLocked()
		}
		return
	}
	r.cell = c
	r.resetScaledLocked()
}

// Limit caps the longest side of images whose key starts with prefix at px
// pixels, below the cell-size-derived size and the package-wide cap; px <= 0
// removes the cap. The longest matching prefix wins. Use it for art drawn
// many times at once (grid tiles), where each image is re-sent whenever the
// cells under it change: the terminal scales the image up to its cells.
// Images are re-encoded for it.
func (r *Renderer) Limit(prefix string, px int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := slices.IndexFunc(r.limits, func(l limit) bool { return l.prefix == prefix })
	switch {
	case i >= 0 && r.limits[i].px == px:
		return
	case i >= 0 && px <= 0:
		r.limits = slices.Delete(r.limits, i, i+1)
	case i >= 0:
		r.limits[i].px = px
	case px <= 0:
		return
	default:
		r.limits = append(r.limits, limit{prefix, px})
	}
	r.resetScaledLocked()
}

// Quality sets the JPEG quality (1 to 100) of the images whose key starts with
// prefix, in place of DefaultQuality; q <= 0 removes it. The longest matching
// prefix wins. Grid tiles take a lower one: each is re-sent whenever the grid
// scrolls a row, so their bytes are what a scroll costs the terminal, and a
// small image shown over many pixels hides the difference. Images are
// re-encoded for it.
func (r *Renderer) Quality(prefix string, q int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := slices.IndexFunc(r.qualities, func(l limit) bool { return l.prefix == prefix })
	switch {
	case i >= 0 && r.qualities[i].px == q:
		return
	case i >= 0 && q <= 0:
		r.qualities = slices.Delete(r.qualities, i, i+1)
	case i >= 0:
		r.qualities[i].px = q
	case q <= 0:
		return
	default:
		r.qualities = append(r.qualities, limit{prefix, q})
	}
	r.resetScaledLocked()
}

// qualityLocked returns the JPEG quality for key.
func (r *Renderer) qualityLocked(key string) int {
	q, best := DefaultQuality, -1
	for _, l := range r.qualities {
		if len(l.prefix) > best && strings.HasPrefix(key, l.prefix) {
			q, best = l.px, len(l.prefix)
		}
	}
	return q
}

// limitLocked returns the pixel cap for key, or 0 for none.
func (r *Renderer) limitLocked(key string) int {
	px, best := 0, -1
	for _, l := range r.limits {
		if len(l.prefix) > best && strings.HasPrefix(key, l.prefix) {
			px, best = l.px, len(l.prefix)
		}
	}
	return px
}

// resetScaledLocked drops everything scaled for the old cell size or limits
// and redraws.
func (r *Renderer) resetScaledLocked() {
	r.boxes = newCostLRU[boxKey, *image.NRGBA](boxesMax, boxesBudget, nrgbaBytes)
	r.sixels = newLRU[boxKey, *sixelImage](boxesMax, nil)
	r.payloads = newPayloadLRU()
	r.invalidateLocked()
}

// Invalidate forgets what is on screen, so every image is drawn again after
// the next frame. The Output already notices clears and resizes in the
// output; call this when the terminal may have lost images some other way.
func (r *Renderer) Invalidate() {
	r.mu.Lock()
	r.invalidateLocked()
	r.mu.Unlock()
}

// Reload is Invalidate for when the terminal may have lost its images
// altogether (a redraw the user asked for, or tmux attached to another
// terminal): kitty images are also uploaded again, under new ids, the next
// time their blocks are drawn.
func (r *Renderer) Reload() {
	r.mu.Lock()
	r.kittyIDs.clear()
	r.invalidateLocked()
	r.mu.Unlock()
}

// TmuxImageLimit is how many sixel images tmux keeps, across the whole
// server: storing the tenth drops the oldest (MAX_IMAGE_COUNT in tmux's
// image.c, measured with tmux 3.6a). A dropped image stays on the terminal
// until tmux next redraws the pane, then it is gone.
const TmuxImageLimit = 9

// ImageLimit is how many images can be on screen at once and survive tmux
// redraws: TmuxImageLimit for sixel inside tmux, 0 for no limit. Images
// under the TilePrefix don't count; they are not sixel.
func (r *Renderer) ImageLimit() int {
	if r.proto == ProtoSixel && r.tmux && r.tilePrefix == "" {
		return TmuxImageLimit
	}
	return 0
}

// Fragile reports whether some images vanish whenever tmux redraws the pane
// (IIP through tmux passthrough): call Invalidate when that may have
// happened, such as on focus, or let WatchTmux tell the Renderer.
func (r *Renderer) Fragile() bool {
	return r.passthrough && (r.proto == ProtoIIP || r.tilePrefix != "")
}

// Watched reports whether WatchTmux tells the Renderer about tmux redraws,
// so the program need not guess them from focus changes.
func (r *Renderer) Watched() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.watched
}

// sixelKey reports whether key's image is drawn as sixel.
func (r *Renderer) sixelKey(key string) bool {
	return r.proto == ProtoSixel && (r.tilePrefix == "" || !strings.HasPrefix(key, r.tilePrefix))
}

// passthroughKey reports whether key's image is an iTerm2 image sent
// through tmux passthrough.
func (r *Renderer) passthroughKey(key string) bool {
	return r.passthrough && r.proto.Placed() && !r.sixelKey(key)
}

// redrawHold is how long passthrough images wait after tmux starts to
// redraw the pane: tmux drops passthrough output for a pane with a redraw
// pending, and the redraw itself wipes whatever passthrough images are on
// screen.
const redrawHold = 100 * time.Millisecond

// SetPane records what tmux says about the pane (see WatchTmux). When the
// pane comes into view, passthrough images are drawn again once tmux has
// redrawn it.
func (r *Renderer) SetPane(st PaneState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	was := r.pane
	r.pane = st
	switch {
	case was.Hidden && !st.Hidden:
		r.holdLocked()
	case st.Hidden:
		r.forgetPassthroughLocked()
	}
}

// TmuxRedrawn tells the Renderer that tmux redrew (or is about to redraw)
// the pane, wiping passthrough images: they are drawn again after
// redrawHold.
func (r *Renderer) TmuxRedrawn() {
	r.mu.Lock()
	r.holdLocked()
	r.mu.Unlock()
}

// holdLocked holds passthrough images back for redrawHold and forgets
// that they are on screen, so the next output pass after that draws them.
func (r *Renderer) holdLocked() {
	if !r.tmux || !r.Fragile() {
		return
	}
	if until := r.now().Add(redrawHold); until.After(r.holdUntil) {
		r.holdUntil = until
	}
	r.forgetPassthroughLocked()
	if r.notifyAfter != nil {
		r.notifyAfter(redrawHold)
	}
}

func (r *Renderer) forgetPassthroughLocked() {
	for p := range r.shown {
		if r.passthroughKey(p.key) {
			delete(r.shown, p)
		}
	}
}

func (r *Renderer) invalidateLocked() {
	r.screen = nil
	r.dirtyAll = true
	r.shown = map[piece]bool{}
	r.poke()
}

// PrepareDecoded is Prepare for art the caller has already decoded, so the
// bytes are not decoded a second time.
func (r *Renderer) PrepareDecoded(key string, m image.Image, cols, rows int) error {
	r.mu.Lock()
	if _, ok := r.images.get(key); !ok {
		r.images.put(key, m)
	}
	r.mu.Unlock()
	return r.Prepare(key, nil, cols, rows)
}

// Prepare decodes img and does the expensive work for showing it at
// cols×rows, so a later Block in View is a cache hit. Call it from a tea.Cmd.
func (r *Renderer) Prepare(key string, img []byte, cols, rows int) error {
	m, err := r.image(key, img)
	if err != nil || m == nil {
		return err
	}
	bk := boxKey{key, cols, rows}
	switch r.proto {
	case ProtoBlocks:
		r.blockFor(bk, m)
	case ProtoIIP, ProtoSixel:
		r.mu.Lock()
		cell, known := r.cell, r.cellKnown
		r.mu.Unlock()
		if r.sixelKey(key) && !known {
			return nil // encoded once the cell size is known
		}
		_, err = r.payload(pieceKey{bk, rect{0, 0, cols, rows}, cell}, m)
	case ProtoKitty:
		r.mu.Lock()
		_, ok := r.kittyIDs.get(bk)
		r.mu.Unlock()
		if !ok {
			r.kittyUpload(bk, m)
		}
	}
	return err
}

// Block returns the cols×rows block to put in the layout for the artwork
// identified by key. img (PNG or JPEG bytes) is only needed the first time a
// key is seen; pass nil afterwards. Unknown keys without data, and images
// that fail to decode, give blank cells.
//
// For IIP and sixel the block is blank and the image is drawn over it after
// the frame: call Place with the block's screen position.
func (r *Renderer) Block(key string, img []byte, cols, rows int) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}
	if r.proto == ProtoOff {
		return blank(cols, rows)
	}
	m, err := r.image(key, img)
	if err != nil || m == nil {
		return blank(cols, rows)
	}
	bk := boxKey{key, cols, rows}
	switch r.proto {
	case ProtoBlocks:
		return r.blockFor(bk, m)
	case ProtoKitty:
		r.mu.Lock()
		id, ok := r.kittyIDs.get(bk)
		r.mu.Unlock()
		if !ok {
			id = r.kittyUpload(bk, m)
		}
		if id == 0 {
			return blank(cols, rows)
		}
		return KittyPlaceholders(id, cols, rows)
	}
	return blank(cols, rows)
}

// ComposePart is one image in a composite: the artwork for Key, cropped to
// cover Cols×Rows cells at (X, Y), relative to the composite's top-left.
type ComposePart struct {
	Key              string
	X, Y, Cols, Rows int
}

// Compose makes key the artwork for a cols×rows block assembled from parts;
// cells no part covers are transparent. Place it like any other block once
// Composed reports it ready. It lets a row of grid tiles count as one image
// where the terminal keeps only a few (sixel in tmux, see TmuxImageLimit).
// It is built once per key and cell size, so the key must change with the
// parts, and it does the expensive work (for sixel, the encoding), so call
// it off the UI goroutine. It reports false, and builds nothing, while a
// part's artwork is not decoded.
func (r *Renderer) Compose(key string, cols, rows int, parts []ComposePart) bool {
	if cols <= 0 || rows <= 0 {
		return false
	}
	if r.Composed(key, cols, rows) {
		return true
	}
	r.mu.Lock()
	cell := r.cell
	srcs := make([]image.Image, len(parts))
	for i, p := range parts {
		srcs[i], _ = r.images.get(p.Key)
	}
	r.mu.Unlock()
	for _, m := range srcs {
		if m == nil {
			return false
		}
	}
	out := image.NewNRGBA(image.Rect(0, 0, cols*cell.W, rows*cell.H))
	for i, p := range parts {
		w, h := p.Cols*cell.W, p.Rows*cell.H
		m := resample(srcs[i], coverRect(srcs[i].Bounds(), w, h), w, h).nrgba()
		at := image.Pt(p.X*cell.W, p.Y*cell.H)
		draw.Draw(out, image.Rectangle{at, at.Add(image.Pt(w, h))}, m, image.Point{}, draw.Src)
	}
	r.mu.Lock()
	r.images.put(key, out)
	if len(r.composed) > imagesMax {
		clear(r.composed)
	}
	r.composed[key] = cell
	r.boxes.remove(boxKey{key, cols, rows}) // scaled from an older build
	r.sixels.remove(boxKey{key, cols, rows})
	r.mu.Unlock()
	if r.sixelKey(key) {
		r.sixelPayload(pieceKey{boxKey{key, cols, rows}, rect{0, 0, cols, rows}, cell}, out)
	}
	return true
}

// Composed reports whether Compose has built key for the current cell size
// (and, for sixel, encoded it whole).
func (r *Renderer) Composed(key string, cols, rows int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, have := r.images.get(key)
	if built, ok := r.composed[key]; !have || !ok || built != r.cell {
		return false
	}
	if !r.sixelKey(key) {
		return true
	}
	_, ok := r.payloads.get(pieceKey{boxKey{key, cols, rows}, rect{0, 0, cols, rows}, r.cell})
	return ok
}

// Ready reports whether the artwork for key can be drawn at cols×rows without
// decoding anything. For iTerm2 images (and sixel tiles that go as iTerm2
// images) that is the encoded payload of the whole box for the current cell
// size, which outlives the decoded image; for the other protocols it is the
// decoded image, as Has.
func (r *Renderer) Ready(key string, cols, rows int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proto.Placed() && !r.sixelKey(key) {
		_, ok := r.payloads.get(pieceKey{boxKey{key, cols, rows}, rect{0, 0, cols, rows}, r.cell})
		return ok
	}
	_, ok := r.images.get(key)
	return ok
}

// Has reports whether artwork for key is decoded and cached.
func (r *Renderer) Has(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.images.get(key)
	return ok
}

// Place records that the IIP or sixel block for key, cols×rows, sits at the
// zero-based screen cell (x, y) in the frame being built. It is a no-op for
// other protocols.
func (r *Renderer) Place(key string, x, y, cols, rows int) {
	if !r.proto.Placed() || cols <= 0 || rows <= 0 {
		return
	}
	r.mu.Lock()
	r.places = append(r.places, placement{key, rect{x, y, cols, rows}})
	r.mu.Unlock()
}

// Occlude records that something drawn on top (an overlay, a menu) covers
// the w×h rectangle at (x, y) in the frame being built. IIP and sixel images
// are cropped around it; without this they would be drawn over the overlay.
func (r *Renderer) Occlude(x, y, w, h int) {
	if !r.proto.Placed() || w <= 0 || h <= 0 {
		return
	}
	r.mu.Lock()
	r.occl = append(r.occl, rect{x, y, w, h})
	r.mu.Unlock()
}

// Frame commits the frame being built. content is exactly the string the
// view hands to Bubble Tea; it is used to repaint cells an image no longer
// covers. Call it last in View, every time.
func (r *Renderer) Frame(content string) {
	if !r.proto.Placed() {
		r.mu.Lock()
		if r.oob.Len() > 0 {
			r.poke()
		}
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = strings.Split(content, "\n")
	screen := rect{0, 0, r.width, r.height}
	if screen.empty() {
		screen = rect{0, 0, 1 << 20, len(r.lines)}
	}
	r.pieces = r.pieces[:0]
	for _, p := range r.places {
		vis := []rect{p.box.intersect(screen)}
		for _, o := range r.occl {
			var next []rect
			for _, v := range vis {
				if r.sixelKey(p.key) && r.tmux {
					next = append(next, v.subtractCols(o)...)
				} else {
					next = append(next, v.subtract(o)...)
				}
			}
			vis = next
		}
		for _, v := range vis {
			if !v.empty() {
				r.pieces = append(r.pieces, piece{p.key, p.box, v})
			}
		}
	}
	r.places, r.occl = r.places[:0], r.occl[:0]

	changed := r.dirtyAll || len(r.pieces) != len(r.shown)
	for _, p := range r.pieces {
		changed = changed || !r.shown[p]
	}
	if changed {
		r.poke()
	}
}

func (r *Renderer) poke() {
	if r.notify != nil {
		r.notify()
	}
}

// ops returns what to write after output that caused dmg: kitty uploads,
// repaints of cells images have left, and the IIP or sixel images that are
// new or were disturbed. It updates what the Renderer believes is on screen.
func (r *Renderer) ops(dmg *damage) string {
	r.mu.Lock()
	var b strings.Builder
	if r.oob.Len() > 0 {
		b.WriteString(r.oob.String())
		r.oob.Reset()
	}
	if !r.proto.Placed() || (r.proto == ProtoSixel && !r.cellKnown) {
		r.mu.Unlock()
		return b.String()
	}
	all := dmg.all || r.dirtyAll
	r.dirtyAll = false
	plan := r.plan
	r.plan = nil
	if all {
		plan = nil // the screen was cleared: everything is drawn again
	}
	pieces := slices.Clone(r.pieces)
	lines, width, cell, screen := r.lines, r.width, r.cell, r.screen
	prev := r.shown
	now := make(map[piece]bool, len(pieces))
	for _, p := range pieces {
		now[p] = true
	}
	r.shown = now
	images := make(map[string]image.Image)
	for _, p := range pieces {
		if m, ok := r.images.get(p.key); ok {
			images[p.key] = m
		}
	}
	pane := r.pane
	hidden, split := pane.Hidden, pane.Split
	holdFor := r.holdUntil.Sub(r.now())
	r.mu.Unlock()

	// Cells an image covered that no current piece covers keep their image
	// fragments until something writes them: pieces that went away, and
	// fragments a scroll carried out of their box. Repaint those rows from
	// the frame around the images on them, which keep their cells. A
	// cleared screen has no fragments left.
	var repaint damage
	var rows []int
	if plan != nil {
		dmg.shift(plan.top, plan.bot, plan.dy)
	}
	if !dmg.all {
		for p := range prev {
			if now[p] && len(dmg.scrolls) == 0 {
				continue
			}
			left := dmg.moved(p.vis)
			for _, c := range pieces {
				var next []rect
				for _, l := range left {
					next = append(next, l.subtract(c.vis)...)
				}
				left = next
			}
			for _, l := range left {
				for y := max(l.y, 0); y < l.y+l.h; y++ {
					if !slices.Contains(rows, y) {
						rows = append(rows, y)
					}
				}
			}
		}
	}
	// What stays where it is: cells the repaint must write around.
	keep := map[piece][]rect{}
	if plan != nil {
		// Bubble Tea does not know the scrolled rows moved, so every one
		// of them is written from the frame.
		for y := plan.top; y <= plan.bot; y++ {
			if !slices.Contains(rows, y) {
				rows = append(rows, y)
			}
		}
		for _, p := range pieces {
			if k := plan.kept(p, prev, dmg.hits); len(k) > 0 {
				keep[p] = k
			}
		}
	}
	if len(rows) > 0 {
		// around every image in this frame: the ones that stay keep their
		// cells, and new ones are drawn over theirs below
		skip := map[int][]ival{}
		for _, p := range pieces {
			spans := []rect{p.vis}
			if plan != nil {
				spans = keep[p]
			}
			for _, v := range spans {
				for y := v.y; y < v.y+v.h; y++ {
					skip[y] = append(skip[y], ival{v.x, v.x + v.w})
				}
			}
		}
		slices.Sort(rows)
		b.WriteString("\x1b7")
		for _, y := range rows {
			sp := mergeSpans(skip[y])
			var shows func(lo, hi int) string
			if plan != nil && y >= plan.top && y <= plan.bot && len(screen) == len(lines) {
				shows = plan.shows(screen, y, width)
			}
			b.WriteString(repaintAround(lines, y, width, sp, shows))
			x := 0
			for _, v := range sp {
				repaint.mark(y, x, v.lo)
				x = max(x, v.hi)
			}
			repaint.mark(y, x, 1<<30)
		}
		b.WriteString("\x1b8")
	}

	// The pieces to draw: new ones, and ones the output disturbed.
	var draw []piece
	for _, p := range pieces {
		if plan != nil {
			if _, planned := keep[p]; planned || plan.carried[p] != nil {
				// the scroll brought some of it: draw the rest
				for _, v := range without(p.vis, keep[p]) {
					draw = append(draw, piece{p.key, p.box, v})
				}
				continue
			}
		}
		hit := dmg.hits(p.vis)
		if r.sixelKey(p.key) && r.tmux {
			hit = dmg.hitsLine(p.vis)
		}
		if all || !prev[p] || hit || repaint.hits(p.vis) {
			draw = append(draw, p)
		}
	}

	// tmux redraws the whole pane when this output disturbs a sixel image
	// it keeps (it drops the image), or scrolls a pane narrower than the
	// window. Until that redraw is done tmux drops passthrough output for
	// the pane, and the redraw wipes passthrough images, so they wait.
	if r.tmux && r.Fragile() && !hidden && holdFor <= 0 && r.tmuxRedraws(dmg, &repaint, prev, draw, split) {
		r.mu.Lock()
		r.holdLocked()
		holdFor = r.holdUntil.Sub(r.now())
		r.mu.Unlock()
	}

	var late []piece
	held := false
	for _, p := range draw {
		sixel := r.sixelKey(p.key)
		if r.passthroughKey(p.key) && (hidden || holdFor > 0) {
			late = append(late, p)
			held = held || !hidden
			continue
		}
		crop := rect{p.vis.x - p.box.x, p.vis.y - p.box.y, p.vis.w, p.vis.h}
		pk := pieceKey{boxKey{p.key, p.box.w, p.box.h}, crop, cell}
		m, ok := images[p.key]
		if !ok {
			// the decoded image may be gone while its payload is kept
			if data, have := r.cachedPayload(pk); have {
				b.WriteString(r.drawPiece(p.key, p.vis, data, pane))
			} else {
				late = append(late, p) // a cut piece needs the image; drawn once it is back
			}
			continue
		}
		if sixel && !r.encodeSoon(pk, m) {
			late = append(late, p)
			continue
		}
		data, err := r.payload(pk, m)
		if err != nil {
			continue
		}
		b.WriteString(r.drawPiece(p.key, p.vis, data, pane))
	}
	if held && r.notifyAfter != nil {
		r.notifyAfter(holdFor)
	}
	if len(late) > 0 {
		r.mu.Lock()
		for _, p := range late {
			delete(r.shown, p) // drawn when its encoding is done
		}
		r.mu.Unlock()
	}
	return b.String()
}

// staying reports the image pieces that are on screen and stay where they
// are in the frame being drawn (see tracker.feed). Called from the Output
// while it holds its own lock.
func (r *Renderer) staying() *staying {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := &staying{spans: map[int][]ival{}, sixel: map[int]bool{}}
	for p := range r.shown {
		if r.sixelKey(p.key) {
			for y := p.vis.y; y < p.vis.y+p.vis.h; y++ {
				st.sixel[y] = true
			}
		}
	}
	for _, p := range r.pieces {
		stays := []rect{p.vis}
		if r.plan != nil && r.plan.carried[p] != nil {
			stays = r.plan.carried[p]
		} else if !r.shown[p] {
			continue
		}
		for _, v := range stays {
			for y := v.y; y < v.y+v.h; y++ {
				st.spans[y] = append(st.spans[y], ival{v.x, v.x + v.w})
			}
		}
	}
	for _, s := range st.spans {
		slices.SortFunc(s, func(a, b ival) int { return a.lo - b.lo })
	}
	return st
}

// tmuxRedraws reports whether output that caused dmg, plus the repaint of
// rows in repaint and the drawing of pieces in draw, makes tmux redraw the
// pane: it drops a sixel image it keeps, and redraws the pane, when the
// image's line is erased or its cells are written, and it redraws a
// scrolled region itself when the pane is split (the whole pane when the
// region is large).
func (r *Renderer) tmuxRedraws(dmg, repaint *damage, prev map[piece]bool, draw []piece, split bool) bool {
	if split && len(dmg.scrolls) > 0 {
		return true
	}
	for q := range prev {
		if !r.sixelKey(q.key) {
			continue
		}
		if dmg.hitsLine(q.vis) || repaint.hits(q.vis) {
			return true
		}
		// a sixel piece is drawn over spaces, which drop a kept image
		// under them
		for _, p := range draw {
			if r.sixelKey(p.key) && !q.vis.intersect(p.vis).empty() {
				return true
			}
		}
	}
	return false
}

// encodeSoon reports whether the sixel image for pk is encoded; if not, it
// encodes it in the background and pokes the Output when done. Quantising
// a row of grid tiles takes tens of milliseconds, too long to hold up the
// terminal's output, which ops runs in.
func (r *Renderer) encodeSoon(pk pieceKey, m image.Image) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.payloads.get(pk); ok {
		return true
	}
	if _, ok := r.sixels.get(pk.box); ok {
		return true // only a crop to encode, which is quick
	}
	if r.encoding[pk] {
		return false
	}
	r.encoding[pk] = true
	go func() {
		r.sixelPayload(pk, m)
		r.mu.Lock()
		delete(r.encoding, pk)
		r.poke()
		r.mu.Unlock()
	}()
	return false
}

// redo repaints rows from the frame around the cells in skip (see
// tracker.holdRows): text and blanks, never an erase, so the images there
// are not touched, inside tmux either.
func (r *Renderer) redo(rows []int, skip map[int][]ival) string {
	if len(rows) == 0 {
		return ""
	}
	r.mu.Lock()
	lines, width := r.lines, r.width
	r.mu.Unlock()
	var b strings.Builder
	b.WriteString("\x1b7")
	for _, y := range rows {
		b.WriteString(repaintAround(lines, y, width, skip[y], nil))
	}
	b.WriteString("\x1b8")
	return b.String()
}

// repaintAround rewrites screen row y from the frame except the cells in
// skip (sorted spans), which hold images: each stretch between them is the
// frame's text for those columns, padded with spaces.
func repaintAround(lines []string, y, width int, skip []ival, shows func(lo, hi int) string) string {
	line := ""
	if y < len(lines) {
		line = lines[y]
	}
	if width <= 0 {
		width = ansi.StringWidth(line)
	}
	var b strings.Builder
	x := 0
	emit := func(lo, hi int) {
		if hi <= lo {
			return
		}
		seg := ansi.Cut(line, lo, hi)
		if shows != nil && sameCells(seg, shows(lo, hi), hi-lo) {
			return
		}
		b.WriteString("\x1b[" + strconv.Itoa(y+1) + ";" + strconv.Itoa(lo+1) + "H\x1b[m" + seg + "\x1b[m")
		if pad := hi - lo - ansi.StringWidth(seg); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
	}
	put := func(lo, hi int) {
		if shows == nil || hi-lo < 16 {
			emit(lo, hi)
			return
		}
		// the text, then the blanks after it, which a repaint over the
		// same blanks need not write
		used := ansi.StringWidth(strings.TrimRight(ansi.Strip(ansi.Cut(line, lo, hi)), " "))
		emit(lo, lo+used)
		emit(lo+used, hi)
	}
	for _, v := range skip {
		put(x, min(v.lo, width))
		x = max(x, v.hi)
	}
	put(x, width)
	return b.String()
}

// mergeSpans sorts spans and joins the ones that overlap.
func mergeSpans(sp []ival) []ival {
	if len(sp) < 2 {
		return sp
	}
	sp = slices.Clone(sp)
	slices.SortFunc(sp, func(a, b ival) int { return a.lo - b.lo })
	out := sp[:1]
	for _, v := range sp[1:] {
		if last := &out[len(out)-1]; v.lo <= last.hi {
			last.hi = max(last.hi, v.hi)
		} else {
			out = append(out, v)
		}
	}
	return out
}

// drawPiece erases the cells first, so a redrawn image never stacks on its
// old fragments, then draws the image for key over them.
//
// Inside tmux cells are erased by printing spaces: tmux drops every sixel
// image on a line that ECH touches, but only the images under printed cells.
// tmux leaves the cursor below a sixel image; DECRC puts it back.
//
// A passthrough image whose pane is Located carries its erase and its
// cursor move inside the passthrough, addressed to the terminal: tmux holds
// printed cells back and writes them after a passthrough sequence that came
// later, which would print the spaces over the image.
func (r *Renderer) drawPiece(key string, vis rect, data []byte, pane PaneState) string {
	cup := func(x, y int) string { return "\x1b[" + strconv.Itoa(y+1) + ";" + strconv.Itoa(x+1) + "H" }
	sixel := r.sixelKey(key)
	var b strings.Builder
	b.WriteString("\x1b7\x1b[m")
	if !sixel && r.passthrough && r.tmux && pane.Located {
		var seq strings.Builder
		ox, oy := pane.Origin.X+vis.x, pane.Origin.Y+vis.y
		seq.WriteString("\x1b7\x1b[m")
		for y := range vis.h {
			seq.WriteString(cup(ox, oy+y) + "\x1b[" + strconv.Itoa(vis.w) + "X")
		}
		seq.WriteString(cup(ox, oy) + iip(data, vis.w, vis.h, false) + "\x1b8")
		b.WriteString(r.wrap(seq.String()))
		b.WriteString("\x1b8")
		return b.String()
	}
	for y := vis.y; y < vis.y+vis.h; y++ {
		b.WriteString(cup(vis.x, y))
		if sixel || r.tmux {
			b.WriteString(strings.Repeat(" ", vis.w))
		} else {
			b.WriteString("\x1b[" + strconv.Itoa(vis.w) + "X")
		}
	}
	b.WriteString(cup(vis.x, vis.y))
	if sixel {
		b.Write(data)
	} else {
		b.WriteString(r.wrap(iip(data, vis.w, vis.h, false)))
	}
	b.WriteString("\x1b8")
	return b.String()
}

// image returns the decoded image for key, decoding data on a miss.
func (r *Renderer) image(key string, data []byte) (image.Image, error) {
	r.mu.Lock()
	m, ok := r.images.get(key)
	r.mu.Unlock()
	if ok || len(data) == 0 {
		return m, nil
	}
	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.images.put(key, m)
	r.mu.Unlock()
	return m, nil
}

func (r *Renderer) blockFor(bk boxKey, m image.Image) string {
	r.mu.Lock()
	s, ok := r.blocks.get(bk)
	r.mu.Unlock()
	if ok {
		return s
	}
	s = Blocks(m, bk.cols, bk.rows)
	r.mu.Lock()
	r.blocks.put(bk, s)
	r.mu.Unlock()
	return s
}

// cachedPayload returns the encoded image for pk if it is kept.
func (r *Renderer) cachedPayload(pk pieceKey) ([]byte, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.payloads.get(pk)
}

// payload returns the encoded image for a piece, scaling and cropping on a
// miss. Must be called without r.mu held.
func (r *Renderer) payload(pk pieceKey, m image.Image) ([]byte, error) {
	if r.sixelKey(pk.box.key) {
		return r.sixelPayload(pk, m), nil
	}
	r.mu.Lock()
	data, ok := r.payloads.get(pk)
	box, hasBox := r.boxes.get(pk.box)
	maxPx := r.limitLocked(pk.box.key)
	quality := r.qualityLocked(pk.box.key)
	r.mu.Unlock()
	if ok {
		return data, nil
	}
	if !hasBox {
		w, h := pixelSize(pk.box.cols, pk.box.rows, pk.cell, maxPx)
		box = resample(m, coverRect(m.Bounds(), w, h), w, h).nrgba()
		r.mu.Lock()
		r.boxes.put(pk.box, box)
		r.mu.Unlock()
	}
	sub := image.Image(box)
	if pk.crop != (rect{0, 0, pk.box.cols, pk.box.rows}) {
		bw, bh := box.Bounds().Dx(), box.Bounds().Dy()
		px := image.Rect(
			pk.crop.x*bw/pk.box.cols, pk.crop.y*bh/pk.box.rows,
			(pk.crop.x+pk.crop.w)*bw/pk.box.cols, (pk.crop.y+pk.crop.h)*bh/pk.box.rows)
		c := image.NewNRGBA(image.Rect(0, 0, px.Dx(), px.Dy()))
		draw.Draw(c, c.Bounds(), box, px.Min, draw.Src)
		sub = c
	}
	data, err := encodeImage(sub, quality)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.payloads.put(pk, data)
	r.mu.Unlock()
	return data, nil
}

// sixelPayload is payload for sixel. A sixel image covers as many cells as
// its pixels fill, so it is always exactly the box's cell size in pixels: no
// caps, and no Limit.
func (r *Renderer) sixelPayload(pk pieceKey, m image.Image) []byte {
	r.mu.Lock()
	data, ok := r.payloads.get(pk)
	q, hasQ := r.sixels.get(pk.box)
	r.mu.Unlock()
	if ok {
		return data
	}
	if !hasQ {
		w, h := pk.box.cols*pk.cell.W, pk.box.rows*pk.cell.H
		px, exact := m.(*image.NRGBA)
		if !exact || px.Bounds() != image.Rect(0, 0, w, h) {
			px = resample(m, coverRect(m.Bounds(), w, h), w, h).nrgba()
		}
		q = quantize(px, sixelColors, sixelDither)
		r.mu.Lock()
		r.sixels.put(pk.box, q)
		r.mu.Unlock()
	}
	cw, ch := pk.cell.W, pk.cell.H
	h := pk.crop.h * ch
	if r.tmux {
		// tmux rounds a sixel image's height up to whole six-pixel bands
		// before working out the cells it covers; one pixel more than the
		// cells would cover the line below too, and tmux would drop the
		// image whenever that line changes.
		h -= h % 6
	}
	data = q.encode(image.Rect(pk.crop.x*cw, pk.crop.y*ch, (pk.crop.x+pk.crop.w)*cw, pk.crop.y*ch+h))
	r.mu.Lock()
	r.payloads.put(pk, data)
	r.mu.Unlock()
	return data
}

// kittyUpload assigns an image id to bk and queues its upload and virtual
// placement.
func (r *Renderer) kittyUpload(bk boxKey, m image.Image) uint32 {
	r.mu.Lock()
	cell, maxPx := r.cell, r.limitLocked(bk.key)
	r.mu.Unlock()
	w, h := pixelSize(bk.cols, bk.rows, cell, maxPx)
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, resample(m, coverRect(m.Bounds(), w, h), w, h).nrgba()); err != nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, ok := r.kittyIDs.get(bk); ok {
		return id // raced with another upload
	}
	r.nextID++
	if r.nextID >= 1<<24 {
		r.nextID = 1
	}
	id := r.nextID
	r.kittyIDs.put(bk, id)
	r.oob.WriteString(r.wrap(KittyTransmit(id, buf.Bytes())))
	r.oob.WriteString(r.wrap(KittyPlace(id, bk.cols, bk.rows)))
	r.poke()
	return id
}

func (r *Renderer) wrap(seq string) string {
	if r.passthrough {
		return ansi.TmuxPassthrough(seq)
	}
	return seq
}
