package theme

// Glyphs is the set of symbols the UI draws. Every glyph is exactly one cell
// wide unless noted, so layouts can count on it. Nerd Font glyphs tend to bleed
// into the next cell; always leave a space after them.
type Glyphs struct {
	Nerd bool

	Play, Pause, Prev, Next    string
	Shuffle, Repeat, RepeatOne string
	Heart, HeartOutline        string
	Volume, VolumeOff          string
	AirPlay, Lyrics, Queue     string
	Search                     string

	Playing  string // marks the playing row
	Marker   string // focus marker at the left edge of the selected row
	Explicit string // explicit tag (one cell; leave a space after it)
	Blocked  string // leads an unavailable track's title
	InLib    string // marks an Apple Music item the library already has (one cell)
	Dot      string // separator
	Ellipsis string

	BarFill, BarHead, BarRest  string   // progress bar
	Levels                     []string // volume meter, low to high
	RuleH, RuleV               string
	ScrollThumb, ScrollTrack   string
	Disclosure, DisclosureOpen string // playlist folders
}

// Unicode is the default set: plain Unicode text glyphs with text presentation,
// no emoji.
var Unicode = Glyphs{
	Play: "▶", Pause: "❙❙", Prev: "◂◂", Next: "▸▸", // Pause, Prev, Next are 2 cells
	Shuffle: "⇄", Repeat: "↻", RepeatOne: "↻",
	Heart: "♥", HeartOutline: "♡",
	Volume: "♪", VolumeOff: "♪",
	AirPlay: "◎", Lyrics: "❞", Queue: "≡",
	Search: "⌕",

	Playing: "▶", Marker: "▎", Explicit: "E", Blocked: "⊘", InLib: "✓", Dot: "·", Ellipsis: "…",
	BarFill: "━", BarHead: "╸", BarRest: "─",
	Levels: []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"},
	RuleH:  "─", RuleV: "│",
	ScrollThumb: "┃", ScrollTrack: "│",
	Disclosure: "▸", DisclosureOpen: "▾",
}

// NerdFont swaps media controls and pane toggles for Material Design icons from
// Nerd Fonts. WezTerm bundles these in its default font fallback.
var NerdFont = func() Glyphs {
	g := Unicode
	g.Nerd = true
	g.Play, g.Pause, g.Prev, g.Next = "\U000F040A", "\U000F03E4", "\U000F04AE", "\U000F04AD"
	g.Shuffle, g.Repeat, g.RepeatOne = "\U000F049F", "\U000F0456", "\U000F0458"
	g.Heart, g.HeartOutline = "\U000F02D1", "\U000F02D5"
	g.Volume, g.VolumeOff = "\U000F057E", "\U000F0581"
	g.AirPlay, g.Lyrics, g.Queue = "\U000F04C3", "\U000F0757", "\U000F0CB8"
	g.Search = "\U000F0349"
	g.Explicit = "\U000F0B0C" // md alpha-e-box
	return g
}()

// GlyphsFor resolves the "icons" config value. auto picks Nerd Font glyphs on
// WezTerm (which ships them) and Unicode elsewhere.
func GlyphsFor(setting, termProgram string) Glyphs {
	switch setting {
	case "nerd":
		return NerdFont
	case "unicode":
		return Unicode
	}
	if termProgram == "WezTerm" {
		return NerdFont
	}
	return Unicode
}
