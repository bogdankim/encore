package ui

import (
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/theme"
)

// barZone is a clickable part of the player bar.
type barZone uint8

const (
	zoneNone barZone = iota
	zonePrev
	zonePlay
	zoneNext
	zoneShuffle
	zoneRepeat
	zoneTitle  // the title and the album name: opens the album
	zoneArtist // opens the artist
	zoneArt    // the album art: opens the album, like the title
	zoneHeart
	zoneProgress
	zoneVolume
	zoneAirPlay
	zoneLyrics
	zoneQueue
	zoneLastfm
)

// zone is a clickable span: columns [x0, x1) on row y, or every row when
// y < 0.
type zone struct {
	z      barZone
	x0, x1 int
	y      int
}

const (
	artW, artH    = 6, 3
	meterCells    = 8
	lcdMaxW       = 90
	lcdMinW       = 40 // the LCD's floor before groups are dropped
	lcdMinCompact = 40 // the same for the one-line bar
	airNameMax    = 18
	airNameMin    = 6 // below this the name hides and the glyph stays
	minCut        = 8 // a name cut with "…" keeps this many cells, or drops
	btnGap        = 2
	clusterGap    = 4
	groupGap      = 2 // the least space between the bar's groups
	barEdge       = 2
	miniBarUnder  = compactBarUnder // cols: play, title · artist, elapsed
	tinyBarUnder  = 40              // cols: play and title only
	spinnerAfter  = 300 * time.Millisecond
)

// Bar levels: each one drops a group, in this order, until the LCD has
// lcdMinW columns. The title is never dropped.
const (
	levelAll       = iota
	levelNoAirName // the AirPlay device name
	levelNoVolNum  // the volume number
	levelNoShuffle // shuffle and repeat
	levelVolGlyph  // the volume meter, for one volume glyph
	levelNoToggles // the lyrics and Up Next toggles
	levelNoRemain  // the remaining time
	numBarLevels
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// barState is what the player bar needs besides Ctx.
type barState struct {
	hover        barZone
	scrubbing    bool
	scrub        float64 // 0..1 while scrubbing
	volDragging  bool
	air          airLabel // the selected AirPlay outputs
	airRemote    bool     // something other than this computer plays
	lastfmIssue  bool     // last.fm needs attention: show the dot
	loadingSince time.Time
}

// airLabel names the selected AirPlay outputs: the first one, and how many
// more there are.
type airLabel struct {
	name string
	more int
}

// fit returns the label in at most w cells, "Living Room + 1", shortening
// the name before it gives up the count.
func (l airLabel) fit(w int) string {
	if l.name == "" || w <= 0 {
		return ""
	}
	suffix := ""
	if l.more > 0 {
		suffix = " + " + itoa(l.more)
	}
	if ansi.StringWidth(l.name)+ansi.StringWidth(suffix) <= w {
		return l.name + suffix
	}
	if nw := w - ansi.StringWidth(suffix); nw >= airNameMin-1 {
		return truncWord(l.name, nw) + suffix
	}
	return truncWord(l.name+suffix, w)
}

// truncWord truncates s to w cells with "…", dropping a space the cut
// leaves before it ("MacBook…", not "MacBook …").
func truncWord(s string, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}
	return strings.TrimRight(ansi.Truncate(s, w-1, ""), " ") + "…"
}

// barLayout positions everything in the player bar for a width. Every
// control owns a zone one cell wider than its glyph on each side; zones and
// groups never overlap.
type barLayout struct {
	w, rows      int
	zones        []zone
	mini         bool   // under 60 columns: play, title · artist, elapsed
	level        int    // groups dropped, see levelAll
	gap          int    // space between the groups
	btn          [5]int // x of prev, play, next, shuffle, repeat; -1 hidden
	leftEnd      int    // first column after the left group
	rightX       int    // first column of the right group
	lcdX, lcdW   int
	art          bool
	textX, textW int
	timeW        int
	timeX        int // elapsed time (mini and one-line bars)
	progX, progW int
	remain       bool // the remaining time shows
	volX         int  // -1 hidden
	meter        int  // meter cells; 0 shows one volume glyph
	volNum       bool
	airX         int // -1 hidden
	airName      string
	dotX         int // last.fm dot, -1 when hidden
	lyricsX      int // -1 hidden
	queueX       int // -1 hidden
}

func slotW(g string) int { return max(2, ansi.StringWidth(g)) }

// barPlan is one candidate set of groups for layoutBar.
type barPlan struct {
	level   int
	gap     int
	airName string
}

func (p barPlan) leftW(g *theme.Glyphs) int {
	n := barEdge + slotW(g.Prev) + btnGap + slotW(g.Pause) + btnGap + slotW(g.Next)
	if p.level < levelNoShuffle {
		n += clusterGap + slotW(g.Shuffle) + btnGap + slotW(g.Repeat)
	}
	return n
}

func (p barPlan) volW(g *theme.Glyphs) int {
	n := meterCells
	if p.level >= levelVolGlyph {
		n = slotW(g.Volume)
	}
	if p.level < levelNoVolNum {
		n += 4
	}
	return n
}

func (p barPlan) airW(g *theme.Glyphs) int {
	n := slotW(g.AirPlay)
	if p.airName != "" {
		n += 1 + ansi.StringWidth(p.airName)
	}
	return n
}

func (p barPlan) rightW(g *theme.Glyphs, dot bool) int {
	n := p.volW(g) + clusterGap + p.airW(g) + barEdge
	if p.level < levelNoToggles {
		n += clusterGap + slotW(g.Lyrics) + btnGap + slotW(g.Queue)
	}
	if dot {
		n += 1 + btnGap
	}
	return n
}

func (p barPlan) avail(w int, g *theme.Glyphs, dot bool) int {
	return w - p.leftW(g) - p.rightW(g, dot) - 2*p.gap
}

func layoutBar(w, rows int, g *theme.Glyphs, air airLabel, dot bool, dur time.Duration) barLayout {
	b := barLayout{w: w, rows: rows, timeW: clockWidth(dur), btn: [5]int{-1, -1, -1, -1, -1},
		volX: -1, airX: -1, dotX: -1, lyricsX: -1, queueX: -1}
	if w < miniBarUnder {
		b.layoutMini(g)
		return b
	}
	need := lcdMinW
	if rows < artH {
		need = lcdMinCompact
	}
	full := air.fit(airNameMax)
	plan, found := barPlan{level: numBarLevels - 1, gap: groupGap}, false
	for level := levelAll; level < numBarLevels && !found; level++ {
		n := need
		if level >= levelNoRemain {
			n -= b.timeW + 2
		}
		for _, gap := range []int{clusterGap, groupGap} {
			p := barPlan{level: level, gap: gap}
			if level < levelNoAirName {
				p.airName = full
			}
			av := p.avail(w, g, dot)
			if av < n && p.airName != "" {
				// shorten the name before giving it up
				if nw := ansi.StringWidth(full) - (n - av); nw >= airNameMin {
					p.airName = air.fit(nw)
					av = p.avail(w, g, dot)
				}
			}
			if av >= n {
				plan, found = p, true
				break
			}
		}
	}
	b.place(plan, g, dot)
	return b
}

// place positions the groups of plan.
func (b *barLayout) place(p barPlan, g *theme.Glyphs, dot bool) {
	b.level, b.gap, b.airName = p.level, p.gap, p.airName
	b.volNum = p.level < levelNoVolNum
	b.meter = meterCells
	if p.level >= levelVolGlyph {
		b.meter = 0
	}
	b.remain = p.level < levelNoRemain

	// left: transport, then shuffle and repeat
	x := barEdge
	glyphs := [5]string{g.Prev, g.Pause, g.Next, g.Shuffle, g.Repeat}
	zs := [5]barZone{zonePrev, zonePlay, zoneNext, zoneShuffle, zoneRepeat}
	n := 3
	if p.level < levelNoShuffle {
		n = 5
	}
	for i := 0; i < n; i++ {
		if i == 3 {
			x += clusterGap - btnGap
		}
		b.btn[i] = x
		sw := slotW(glyphs[i])
		b.zones = append(b.zones, zone{zs[i], x - 1, x + sw + 1, -1})
		x += sw + btnGap
	}
	b.leftEnd = x - btnGap

	// right: last.fm dot, volume, AirPlay, pane toggles
	rx := b.w - p.rightW(g, dot)
	b.rightX = rx
	if dot {
		b.dotX = rx
		b.zones = append(b.zones, zone{zoneLastfm, rx - 1, rx + 2, -1})
		rx += 1 + btnGap
	}
	b.volX = rx
	vw := p.volW(g)
	b.zones = append(b.zones, zone{zoneVolume, rx - 1, rx + vw + 1, -1})
	rx += vw + clusterGap
	b.airX = rx
	aw := p.airW(g)
	b.zones = append(b.zones, zone{zoneAirPlay, rx - 1, rx + aw + 1, -1})
	rx += aw
	if p.level < levelNoToggles {
		rx += clusterGap
		b.lyricsX = rx
		b.zones = append(b.zones, zone{zoneLyrics, rx - 1, rx + slotW(g.Lyrics) + 1, -1})
		rx += slotW(g.Lyrics) + btnGap
		b.queueX = rx
		b.zones = append(b.zones, zone{zoneQueue, rx - 1, rx + slotW(g.Queue) + 1, -1})
	}

	// the LCD, centred between the groups
	avail := max(b.rightX-b.leftEnd-2*b.gap, 0)
	b.lcdW = min(avail, lcdMaxW)
	b.lcdX = b.leftEnd + b.gap + (avail-b.lcdW)/2
	b.art = b.rows >= artH && b.lcdW >= 30
	b.textX, b.textW = b.lcdX, b.lcdW
	if b.art {
		b.zones = append(b.zones, zone{zoneArt, b.lcdX, b.lcdX + artW, -1})
		b.textX += artW + 2
		b.textW -= artW + 2
	}
	remainW := 0
	if b.remain {
		remainW = 1 + b.timeW + 1
	}
	if b.rows >= artH {
		b.timeX = b.textX
		b.progX = b.textX + b.timeW + 1
		b.progW = max(b.textW-b.timeW-1-remainW, 0)
		b.zones = append(b.zones, zone{zoneProgress, b.progX - 1, b.progX + b.progW + 1, 2})
		return
	}
	// one line: title · artist, elapsed, progress, remaining
	// the title and artist keep 22 columns before the progress bar shrinks
	barW := min(24, max(b.lcdW-2-b.timeW-1-remainW-22, 6))
	timePart := b.timeW + 1 + barW + remainW
	b.textW = max(b.lcdW-timePart-2, 0)
	b.timeX = b.lcdX + b.textW + 2
	b.progX, b.progW = b.timeX+b.timeW+1, barW
	b.zones = append(b.zones, zone{zoneProgress, b.progX - 1, b.progX + b.progW + 1, 0})
}

// layoutMini is the bar under 60 columns: "⏯  Title · Artist  1:08", and
// under 40 the play button and the title.
func (b *barLayout) layoutMini(g *theme.Glyphs) {
	b.mini = true
	b.level = numBarLevels
	b.btn[1] = 1
	b.zones = append(b.zones, zone{zonePlay, 0, 1 + slotW(g.Pause) + 1, -1})
	b.textX = 1 + slotW(g.Pause) + 2
	end := b.w - 1
	if b.w >= tinyBarUnder {
		b.timeX = end - b.timeW
		end = b.timeX - 2
	} else {
		b.timeX = -1
	}
	b.textW = max(end-b.textX, 0)
	b.lcdX, b.lcdW = b.textX, b.textW
	b.leftEnd, b.rightX = b.textX, b.w
}

func (b barLayout) hit(x, y int) barZone {
	for _, z := range b.zones {
		if (z.y < 0 || z.y == y) && x >= z.x0 && x < z.x1 {
			return z.z
		}
	}
	return zoneNone
}

func (b barLayout) zone(z barZone) (zone, bool) {
	for _, zn := range b.zones {
		if zn.z == z && zn.y < 0 {
			return zn, true
		}
	}
	return zone{}, false
}

// addZone records a zone found while rendering (title and heart depend on
// the text).
func (b *barLayout) addZone(z barZone, x0, x1, y int) {
	if x1 > x0 {
		b.zones = append(b.zones, zone{z, x0, x1, y})
	}
}

// progressFrac maps a column to a position in the track.
func (b barLayout) progressFrac(x int) float64 {
	if b.progW <= 0 {
		return 0
	}
	return math.Min(math.Max(float64(x-b.progX)/float64(max(b.progW-1, 1)), 0), 1)
}

// volumeAt maps a column on the meter to a volume; the cell left of the
// meter is 0.
func (b barLayout) volumeAt(x int) int {
	if b.meter <= 0 {
		return -1
	}
	if x < b.volX {
		return 0
	}
	cell := min(x-b.volX, b.meter-1)
	return (cell + 1) * 100 / b.meter
}

// renderBar draws the player bar: rows lines of w cells.
func (a *App) renderBar(w, rows int, c *Ctx) []string {
	np := c.Player.NowPlaying
	g := &c.Theme.G
	p := paintsFor(c.Theme)
	air := a.bar.air
	if !a.bar.airRemote {
		// only this computer plays: the glyph is enough, the hint names it
		air = airLabel{}
	}
	b := layoutBar(w, rows, g, air, a.bar.lastfmIssue, c.Player.Duration)
	lines := make([]*line, rows)
	for i := range lines {
		lines[i] = newLine(p, surfNone, w)
	}
	mid := lines[rows/2]

	playGlyph := g.Play
	if np.State == bridge.Playing || c.Player.Loading {
		playGlyph = g.Pause
	}
	if b.mini {
		a.ctl(mid, &b, zonePlay, b.btn[1], toneText, playGlyph)
		a.renderMini(&b, mid, c)
		return a.finishBar(b, lines)
	}

	// transport
	repeat := g.Repeat
	if np.Repeat == bridge.RepeatOne {
		repeat = g.RepeatOne
		if g.RepeatOne == g.Repeat {
			repeat += "¹"
		}
	}
	btns := [5]struct {
		z     barZone
		glyph string
		tn    tone
	}{
		{zonePrev, g.Prev, toneText}, {zonePlay, playGlyph, toneText}, {zoneNext, g.Next, toneText},
		{zoneShuffle, g.Shuffle, a.bar.toggleTone(zoneShuffle, np.Shuffle)},
		{zoneRepeat, repeat, a.bar.toggleTone(zoneRepeat, np.Repeat != bridge.RepeatOff && np.Repeat != "")},
	}
	for i, bt := range btns {
		if b.btn[i] >= 0 {
			a.ctl(mid, &b, bt.z, b.btn[i], bt.tn, bt.glyph)
		}
	}

	if b.art && np.Track != nil {
		art := strings.Split(c.NowPlayingArt(b.lcdX, 0, artW, artH), "\n")
		for i := 0; i < artH && i < len(art) && i < rows; i++ {
			lines[i].to(b.lcdX).raw(art[i], artW)
		}
	}
	a.renderLCD(&b, lines, c)

	// last.fm dot, volume, AirPlay, pane toggles
	if b.dotX >= 0 {
		a.ctl(mid, &b, zoneLastfm, b.dotX, a.bar.toggleTone(zoneLastfm, false), "•")
	}
	a.renderVolume(&b, mid, c)
	airTone := a.bar.toggleTone(zoneAirPlay, a.bar.airRemote)
	label := g.AirPlay
	if b.airName != "" {
		label += strings.Repeat(" ", slotW(g.AirPlay)-ansi.StringWidth(g.AirPlay)+1) + b.airName
	}
	a.ctlSplit(mid, &b, zoneAirPlay, b.airX, airTone, g.AirPlay, label)
	if b.lyricsX >= 0 {
		// lit only while the pane is on screen: under 100 columns a pane
		// that was open hides, and the buttons open full-screen lyrics and
		// the Up Next drawer instead
		a.ctl(mid, &b, zoneLyrics, b.lyricsX, a.bar.toggleTone(zoneLyrics, a.paneShown(rightLyrics)), g.Lyrics)
		a.ctl(mid, &b, zoneQueue, b.queueX, a.bar.toggleTone(zoneQueue, a.paneShown(rightQueue)), g.Queue)
	}
	return a.finishBar(b, lines)
}

func (a *App) finishBar(b barLayout, lines []*line) []string {
	a.barLayout = b
	if a.bar.hover != zoneNone && a.hint != "" {
		// the pointer rests on a control whose state a click (or a key)
		// just changed: "Play" turns into "Pause" without moving it
		a.hint = a.barHint(a.bar.hover)
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.String()
	}
	return out
}

// ctl draws a control's glyph at x; while hovered, its zone (the glyph and
// one cell each side) sits on the hover surface.
func (a *App) ctl(l *line, b *barLayout, z barZone, x int, tn tone, glyph string) {
	a.ctlSplit(l, b, z, x, tn, glyph, glyph)
}

// ctlSplit is ctl for a control with a label after its glyph (the AirPlay
// name), drawn dim unless hovered.
func (a *App) ctlSplit(l *line, b *barLayout, z barZone, x int, tn tone, glyph, label string) {
	zn, ok := b.zone(z)
	hovered := ok && a.bar.hover == z && zn.x0 >= l.col()
	rest := strings.TrimPrefix(label, glyph)
	if !hovered {
		l.to(x).add(tn, glyph)
		if rest != "" {
			l.add(toneDim, rest)
		}
		return
	}
	if tn == toneDim {
		tn = toneText
	}
	p := l.p
	lead := x - zn.x0
	tail := zn.x1 - x - ansi.StringWidth(label)
	l.to(zn.x0)
	s := p.paint(tn, surfHover, strings.Repeat(" ", lead)+glyph)
	if rest != "" {
		s += p.paint(toneText, surfHover, rest)
	}
	s += p.spaces(surfHover, tail)
	l.raw(s, zn.x1-zn.x0)
}

// renderVolume draws the meter and the number, or one volume glyph.
func (a *App) renderVolume(b *barLayout, l *line, c *Ctx) {
	g := &c.Theme.G
	np := c.Player.NowPlaying
	zn, _ := b.zone(zoneVolume)
	active := a.bar.hover == zoneVolume || a.bar.volDragging
	sf := surfNone
	if active && zn.x0 >= l.col() {
		sf = surfHover
		l.to(zn.x0)
		l.raw(l.p.spaces(sf, b.volX-zn.x0), b.volX-zn.x0)
	}
	l.to(b.volX)
	var s strings.Builder
	n := 0
	if b.meter == 0 {
		tn := toneDim
		if active {
			tn = toneText
		}
		glyph := g.Volume
		if np.Volume == 0 {
			glyph = g.VolumeOff
		}
		s.WriteString(l.p.paint(tn, sf, glyph))
		n += ansi.StringWidth(glyph)
		pad := slotW(g.Volume) - ansi.StringWidth(glyph)
		s.WriteString(l.p.spaces(sf, pad))
		n += pad
	} else {
		lit := int(math.Round(float64(np.Volume) * float64(b.meter) / 100))
		for i := 0; i < b.meter; i++ {
			lvl := g.Levels[min(i*len(g.Levels)/b.meter, len(g.Levels)-1)]
			if b.meter < len(g.Levels) {
				lvl = g.Levels[min((i+1)*len(g.Levels)/b.meter-1, len(g.Levels)-1)]
			}
			tn := toneFaint
			if i < lit {
				tn = toneDim
				if active {
					tn = toneText
				}
			}
			s.WriteString(l.p.paint(tn, sf, lvl))
			n++
		}
	}
	if b.volNum {
		num := itoa(np.Volume)
		s.WriteString(l.p.spaces(sf, 4-len(num)))
		s.WriteString(l.p.paint(toneDim, sf, num))
		n += 4
	}
	if sf == surfHover {
		s.WriteString(l.p.spaces(sf, zn.x1-b.volX-n))
		n = zn.x1 - b.volX
	}
	l.raw(s.String(), n)
}

func (s *barState) toggleTone(z barZone, on bool) tone {
	switch {
	case on:
		return toneAccent
	case s.hover == z:
		return toneText
	}
	return toneDim
}

// hoverTone is fg while z is hovered, else tn.
func (s *barState) hoverTone(tn tone, zs ...barZone) tone {
	for _, z := range zs {
		if s.hover == z {
			return toneText
		}
	}
	return tn
}

// renderLCD draws the centre: art slot, title, artist · album, progress.
func (a *App) renderLCD(b *barLayout, lines []*line, c *Ctx) {
	np := c.Player.NowPlaying
	ct := np.Track
	if ct == nil {
		mid := lines[len(lines)/2]
		msg := notPlaying(c)
		mid.to(b.lcdX+max(b.lcdW-ansi.StringWidth(msg), 0)/2).fit(toneDim, msg, b.lcdW)
		return
	}
	g := &c.Theme.G
	if b.rows < artH {
		a.renderLCDCompact(b, lines[0], c)
		return
	}

	// title ♥
	fav := c.IsFavoritePID(ct.PID, ct.Favorited)
	heart := ""
	switch {
	case c.Player.Catalog != nil:
		// an Apple Music song outside the library can't be a favorite
	case fav:
		heart = g.Heart
	case a.bar.hover == zoneTitle || a.bar.hover == zoneHeart:
		heart = g.HeartOutline
	}
	hw := 1 + slotW(g.Heart)
	title := ct.Name
	tw := min(ansi.StringWidth(title), b.textW-hw)
	l0 := lines[0].to(b.textX)
	l0.fit(toneBold, title, tw)
	b.addZone(zoneTitle, b.textX, b.textX+tw, 0)
	if heart != "" {
		tn := toneAccent
		if !fav {
			tn = a.bar.hoverTone(toneFaint, zoneHeart)
		}
		l0.pad(1).add(tn, heart)
	}
	b.addZone(zoneHeart, b.textX+tw, b.textX+tw+hw, 0)

	// artist · album: each opens its page. A station's own songs show the
	// station in the album's place: it is where the music comes from, and
	// the album has no page here.
	if st := stationPlaying(c); st != "" {
		a.renderStationSub(b, lines[1], b.textX, b.textW, 1, ct.Artist, st)
	} else {
		a.renderSub(b, lines[1], b.textX, b.textW, 1, ct.Artist, ct.Album, " · ")
	}

	// progress
	l2 := lines[2].to(b.timeX)
	pos, dur := a.barPosition(c)
	l2.right(toneDim, a.elapsed(c, pos), b.timeW).pad(1)
	l2.raw(progressBar(paintsFor(c.Theme), g, frac(pos, dur), b.progW), b.progW)
	if dur > 0 && b.remain {
		l2.pad(1).fit(toneDim, "-"+clock(dur-pos.Truncate(time.Second)), b.timeW+1)
	}
}

// renderSub draws "artist · album" in w cells from x on row y, with the
// artist's zone and the album's.
func (a *App) renderSub(b *barLayout, l *line, x, w, y int, artist, album, sep string) {
	l.to(x)
	aw := min(ansi.StringWidth(artist), w)
	if artist != "" {
		l.fit(a.bar.hoverTone(toneDim, zoneArtist), artist, aw)
		b.addZone(zoneArtist, x, x+aw, y)
	}
	rest := w - aw - ansi.StringWidth(sep)
	if album == "" || !fitsCut(album, rest) {
		return
	}
	if artist != "" {
		l.add(toneDim, sep)
	}
	ax := l.col()
	alw := min(ansi.StringWidth(album), rest)
	l.fit(a.bar.hoverTone(toneDim, zoneTitle), album, alw)
	b.addZone(zoneTitle, ax, ax+alw, y)
}

// stationPlaying is the name of the station whose own song plays, or "".
func stationPlaying(c *Ctx) string {
	st := c.Player.Station
	if st == nil || c.Player.Track != nil || c.Player.Catalog != nil {
		return ""
	}
	return st.Name
}

// renderStationSub draws "artist · Station" in w cells from x on row y: the
// artist as renderSub does, the station's name dim and not a link.
func (a *App) renderStationSub(b *barLayout, l *line, x, w, y int, artist, station string) {
	l.to(x)
	if artist == station {
		artist = "" // live radio reports the station as the artist
	}
	sw := ansi.StringWidth(station)
	aw := min(ansi.StringWidth(artist), max(w-sw-3, minCut))
	if artist != "" && aw > 0 {
		aw = min(aw, w)
		l.fit(a.bar.hoverTone(toneDim, zoneArtist), artist, aw)
		b.addZone(zoneArtist, x, x+aw, y)
		l.add(toneDim, " · ")
	}
	if rest := x + w - l.col(); fitsCut(station, rest) {
		l.fit(toneDim, station, min(sw, rest))
	}
}

// fitsCut reports whether s shows in w cells: whole, or cut to at least
// minCut cells. A shorter stub ("Joa…") says less than leaving it out.
func fitsCut(s string, w int) bool {
	return w >= minCut || (w > 0 && ansi.StringWidth(s) <= w)
}

func (a *App) barPosition(c *Ctx) (pos, dur time.Duration) {
	pos, dur = c.Player.Position, c.Player.Duration
	if a.bar.scrubbing && dur > 0 {
		pos = time.Duration(a.bar.scrub * float64(dur))
	}
	if c.Player.Loading {
		pos = 0
	}
	return pos, dur
}

// notPlaying is what the bar says when Music reports no track.
func notPlaying(c *Ctx) string {
	if st := c.Player.Station; st != nil && c.Player.Loading && st.Name != "" {
		return "Starting " + st.Name + "…"
	}
	return "Not Playing"
}

// elapsed is the elapsed time, "Live" for live radio, or a spinner while a
// track loads.
func (a *App) elapsed(c *Ctx, pos time.Duration) string {
	if !c.Player.Loading {
		if c.Player.Live {
			return "Live"
		}
		return clock(pos)
	}
	if !a.bar.loadingSince.IsZero() && c.Now.Sub(a.bar.loadingSince) > spinnerAfter {
		return spinnerFrames[int(c.Now.Sub(a.bar.loadingSince)/(100*time.Millisecond))%len(spinnerFrames)]
	}
	return "…"
}

func frac(pos, dur time.Duration) float64 {
	if dur <= 0 {
		return 0
	}
	return float64(pos) / float64(dur)
}

// renderLCDCompact is the one-line LCD: title · artist, elapsed, progress,
// remaining.
func (a *App) renderLCDCompact(b *barLayout, l *line, c *Ctx) {
	ct := c.Player.NowPlaying.Track
	g := &c.Theme.G
	l.to(b.lcdX)
	tw := min(ansi.StringWidth(ct.Name), b.textW)
	l.fit(toneBold, ct.Name, tw)
	b.addZone(zoneTitle, b.lcdX, b.lcdX+tw, 0)
	if rest := b.textW - tw - 3; ct.Artist != "" && fitsCut(ct.Artist, rest) {
		l.add(toneDim, " · ")
		a.renderSub(b, l, l.col(), rest, 0, ct.Artist, "", "")
	}
	pos, dur := a.barPosition(c)
	l.to(b.timeX).right(toneDim, a.elapsed(c, pos), b.timeW).pad(1)
	l.raw(progressBar(paintsFor(c.Theme), g, frac(pos, dur), b.progW), b.progW)
	if dur > 0 && b.remain {
		l.pad(1).fit(toneDim, "-"+clock(dur-pos.Truncate(time.Second)), b.timeW+1)
	}
}

// renderMini is the bar under 60 columns after the play button:
// "Title · Artist  1:08".
func (a *App) renderMini(b *barLayout, l *line, c *Ctx) {
	ct := c.Player.NowPlaying.Track
	l.to(b.textX)
	if ct == nil {
		l.fit(toneDim, notPlaying(c), b.textW)
		return
	}
	tw := min(ansi.StringWidth(ct.Name), b.textW)
	l.fit(a.bar.hoverTone(toneBold, zoneTitle), ct.Name, tw)
	b.addZone(zoneTitle, b.textX, b.textX+tw, -1)
	if b.timeX >= 0 {
		if rest := b.textW - tw - 3; ct.Artist != "" && fitsCut(ct.Artist, rest) {
			l.add(toneDim, " · ")
			x := l.col()
			aw := min(ansi.StringWidth(ct.Artist), rest)
			l.fit(a.bar.hoverTone(toneDim, zoneArtist), ct.Artist, aw)
			b.addZone(zoneArtist, x, x+aw, -1)
		}
		pos, _ := a.barPosition(c)
		l.to(b.timeX).right(toneDim, a.elapsed(c, pos), b.timeW)
	}
}

// progressBar is "━━━━╸────" w cells wide: the fill in accent at half-cell
// resolution, the rest in faint.
func progressBar(p *paints, g *theme.Glyphs, frac float64, w int) string {
	if w <= 0 {
		return ""
	}
	frac = math.Min(math.Max(frac, 0), 1)
	halves := int(math.Round(frac * float64(w) * 2))
	full, half := halves/2, halves%2
	rest := w - full - half
	var s strings.Builder
	s.WriteString(p.paint(toneAccent, surfNone, strings.Repeat(g.BarFill, full)))
	if half > 0 {
		s.WriteString(p.paint(toneAccent, surfNone, g.BarHead))
	}
	s.WriteString(p.paint(toneFaint, surfNone, strings.Repeat(g.BarRest, rest)))
	return s.String()
}

// airPlayLabel summarises the selected outputs: "MacBook Pro", "Living
// Room", or "Living Room + 1" (a speaker named before this computer).
// remote is false when only this computer plays.
func airPlayLabel(devs []bridge.AirPlayDevice) (l airLabel, remote bool) {
	var sel []bridge.AirPlayDevice
	for _, d := range devs {
		if d.Selected {
			sel = append(sel, d)
		}
	}
	if len(sel) == 0 {
		return airLabel{}, false
	}
	first := sel[0]
	for _, d := range sel {
		if d.Kind != "computer" {
			first = d
			remote = true
			break
		}
	}
	return airLabel{name: first.Name, more: len(sel) - 1}, remote
}
