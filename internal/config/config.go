package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the user's config.toml. Zero-valued fields never reach callers:
// Load starts from Default() and only overrides what the file sets.
type Config struct {
	Theme        string            `toml:"theme"`  // palette name: encore, mono, light, auto, ...
	Accent       string            `toml:"accent"` // "#RRGGBB" accent override, "" = palette's own
	Icons        string            `toml:"icons"`  // auto, nerd, unicode
	Art          string            `toml:"art"`    // auto, iip, kitty, sixel, blocks, off
	SidebarWidth int               `toml:"sidebar_width"`
	Start        string            `toml:"start"` // the page encore opens on: home, recent, artists, albums, songs, favorites
	Mouse        bool              `toml:"mouse"`
	Lyrics       LyricsConfig      `toml:"lyrics"`
	LastFM       LastFMConfig      `toml:"lastfm"`
	Keys         Keys              `toml:"keys"`
	Colors       map[string]string `toml:"colors"` // token -> "#RRGGBB" overrides
}

type LyricsConfig struct {
	Enabled bool `toml:"enabled"`
	Apple   bool `toml:"apple"`   // read Apple Music's lyrics from Music's own cache
	LRCLIB  bool `toml:"lrclib"`  // query lrclib.net (otherwise Music's own lyrics only)
	LeadMS  int  `toml:"lead_ms"` // highlight a line this many ms before its timestamp
	// WordLeadMS starts filling each timed word this many ms before its
	// timestamp, for the time the frame takes to reach the screen.
	WordLeadMS int `toml:"word_lead_ms"`
}

type LastFMConfig struct {
	Enabled bool `toml:"enabled"`
}

// Keys is the [keys] table: `leader = "space"` plus action = ["seq", ...].
// A single string is accepted in place of a one-element list.
type Keys struct {
	Leader   string
	Bindings map[string][]string // action -> key sequences; only user overrides

	warnings []string
}

// The sidebar_width range, the same the layout keeps the sidebar in.
const (
	MinSidebarWidth = 14
	MaxSidebarWidth = 40
)

var (
	IconSets  = []string{"auto", "nerd", "unicode"}
	ArtModes  = []string{"auto", "iip", "kitty", "sixel", "blocks", "off"}
	Starts    = []string{"home", "recent", "artists", "albums", "songs", "favorites"}
	hexColour = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
)

// Default is the configuration used when the file is absent.
func Default() Config {
	return Config{
		Theme:        "encore",
		Icons:        "auto",
		Art:          "auto",
		SidebarWidth: 22,
		Start:        "recent",
		Mouse:        true,
		Lyrics:       LyricsConfig{Enabled: true, Apple: true, LRCLIB: true, LeadMS: 200, WordLeadMS: 100},
		LastFM:       LastFMConfig{Enabled: true},
		Keys:         Keys{Leader: "space", Bindings: map[string][]string{}},
		Colors:       map[string]string{},
	}
}

// Load reads ConfigPath(). A missing file is not an error. Unknown keys and
// invalid values come back as warnings and fall back to defaults; err is set
// only when the file can't be read or isn't valid TOML, in which case the
// returned Config is Default().
func Load() (Config, []string, error) { return LoadFile(ConfigPath()) }

// LoadFile is Load for an explicit path.
func LoadFile(path string) (Config, []string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil, nil
	}
	if err != nil {
		return Default(), nil, err
	}
	return Parse(string(data))
}

// Parse decodes config TOML text on top of Default().
func Parse(src string) (Config, []string, error) {
	c := Default()
	md, err := toml.Decode(src, &c)
	if err != nil {
		return Default(), nil, fmt.Errorf("config: %w", err)
	}
	var warns []string
	for _, k := range md.Undecoded() {
		warns = append(warns, fmt.Sprintf("unknown key %q", k.String()))
	}
	warns = append(warns, c.Keys.warnings...)
	c.Keys.warnings = nil
	warns = append(warns, c.validate()...)
	return c, warns, nil
}

func (c *Config) validate() []string {
	var w []string
	d := Default()
	if c.Theme = strings.TrimSpace(c.Theme); c.Theme == "" {
		c.Theme = d.Theme
	}
	if c.Accent != "" && !hexColour.MatchString(c.Accent) {
		w = append(w, fmt.Sprintf("accent %q is not a #RRGGBB colour; ignored", c.Accent))
		c.Accent = ""
	}
	if !slices.Contains(IconSets, c.Icons) {
		w = append(w, fmt.Sprintf("icons %q must be one of %s; using %q", c.Icons, strings.Join(IconSets, ", "), d.Icons))
		c.Icons = d.Icons
	}
	if !slices.Contains(ArtModes, c.Art) {
		w = append(w, fmt.Sprintf("art %q must be one of %s; using %q", c.Art, strings.Join(ArtModes, ", "), d.Art))
		c.Art = d.Art
	}
	if !slices.Contains(Starts, c.Start) {
		w = append(w, fmt.Sprintf("start %q must be one of %s; using %q", c.Start, strings.Join(Starts, ", "), d.Start))
		c.Start = d.Start
	}
	if c.SidebarWidth < MinSidebarWidth || c.SidebarWidth > MaxSidebarWidth {
		w = append(w, fmt.Sprintf("sidebar_width %d is outside %d..%d; using %d", c.SidebarWidth, MinSidebarWidth, MaxSidebarWidth, d.SidebarWidth))
		c.SidebarWidth = d.SidebarWidth
	}
	if c.Lyrics.LeadMS < -5000 || c.Lyrics.LeadMS > 5000 {
		w = append(w, fmt.Sprintf("lyrics.lead_ms %d is outside -5000..5000; using %d", c.Lyrics.LeadMS, d.Lyrics.LeadMS))
		c.Lyrics.LeadMS = d.Lyrics.LeadMS
	}
	if c.Lyrics.WordLeadMS < -5000 || c.Lyrics.WordLeadMS > 5000 {
		w = append(w, fmt.Sprintf("lyrics.word_lead_ms %d is outside -5000..5000; using %d", c.Lyrics.WordLeadMS, d.Lyrics.WordLeadMS))
		c.Lyrics.WordLeadMS = d.Lyrics.WordLeadMS
	}
	if c.Colors == nil {
		c.Colors = map[string]string{}
	}
	for _, tok := range sortedKeys(c.Colors) {
		if !hexColour.MatchString(c.Colors[tok]) {
			w = append(w, fmt.Sprintf("colors.%s %q is not a #RRGGBB colour; ignored", tok, c.Colors[tok]))
			delete(c.Colors, tok)
		}
	}
	if c.Keys.Leader = strings.TrimSpace(c.Keys.Leader); c.Keys.Leader == "" {
		c.Keys.Leader = d.Keys.Leader
	}
	if c.Keys.Bindings == nil {
		c.Keys.Bindings = map[string][]string{}
	}
	return w
}

// UnmarshalTOML decodes [keys]. Bad entries become warnings rather than errors
// so one typo doesn't discard the whole file.
func (k *Keys) UnmarshalTOML(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		k.warnings = append(k.warnings, "keys must be a table; ignored")
		return nil
	}
	if k.Bindings == nil {
		k.Bindings = map[string][]string{}
	}
	for _, action := range sortedKeys(m) {
		switch val := m[action].(type) {
		case string:
			if action == "leader" {
				k.Leader = val
			} else {
				k.Bindings[action] = []string{val}
			}
		case []any:
			seqs := make([]string, 0, len(val))
			bad := false
			for _, s := range val {
				str, ok := s.(string)
				if !ok {
					bad = true
					break
				}
				seqs = append(seqs, str)
			}
			if bad || action == "leader" {
				k.warnings = append(k.warnings, fmt.Sprintf("keys.%s must be a string or a list of strings; ignored", action))
				continue
			}
			k.Bindings[action] = seqs
		default:
			k.warnings = append(k.warnings, fmt.Sprintf("keys.%s must be a string or a list of strings; ignored", action))
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// WriteDefault writes a commented example config to path unless a file is
// already there. It reports whether it wrote one.
func WriteDefault(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := EnsureDir(filepath.Dir(path)); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := f.WriteString(DefaultFile); err != nil {
		f.Close()
		return false, err
	}
	return true, f.Close()
}

// DefaultFile is the example config written by WriteDefault. Every value in it
// matches Default().
const DefaultFile = `# encore config. Delete a line to get the built-in default back.

# Palette: "encore" (follows the terminal's light/dark background), "light",
# "mono", "auto", or another built-in name.
theme = "encore"

# Accent override as "#RRGGBB". Empty keeps the palette's accent.
accent = ""

# Media and UI glyphs: "auto", "nerd" (Nerd Font symbols) or "unicode".
icons = "auto"

# Album art: "auto", "iip" (iTerm2/WezTerm), "kitty", "sixel", "blocks" (half
# blocks) or "off". Inside tmux, auto asks tmux which terminal it runs in.
art = "auto"

sidebar_width = 22
start = "recent"   # home, recent, artists, albums, songs, favorites
mouse = true

[lyrics]
enabled = true
apple = true    # Apple Music's synced lyrics, from Music's own cache
lrclib = true   # look lyrics up on lrclib.net; off = Music's own lyrics only
lead_ms = 200   # highlight each line slightly before its timestamp
word_lead_ms = 100  # start filling each timed word this much early

[lastfm]
enabled = true  # run ":lastfm" in encore to connect an account

# Override palette tokens: fg, dim, faint, accent, sel, hover, warn.
[colors]
# accent = "#FA586A"
# sel = "#26262B"

# Key bindings. Each action takes a list of key sequences; <leader> expands
# to the leader key. Press ? in encore to see every action name.
[keys]
leader = "space"
# "play-pause" = ["p", "<leader><leader>"]
# "search.songs" = ["<leader>ss"]
`
