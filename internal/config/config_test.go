package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPaths(t *testing.T) {
	t.Setenv("HOME", "/Users/x")
	t.Setenv("XDG_CONFIG_HOME", "")
	for got, want := range map[string]string{
		CacheDir():   "/Users/x/Library/Caches/encore",
		StateDir():   "/Users/x/Library/Application Support/encore",
		LogDir():     "/Users/x/Library/Logs/encore",
		ConfigPath(): "/Users/x/.config/encore/config.toml",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := ConfigPath(); got != "/xdg/encore/config.toml" {
		t.Errorf("XDG ConfigPath = %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative/ignored")
	if got := ConfigPath(); got != "/Users/x/.config/encore/config.toml" {
		t.Errorf("relative XDG ConfigPath = %q", got)
	}
}

func TestEnsureDirs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := EnsureCacheDir("lyrics")
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() || filepath.Base(dir) != "lyrics" {
		t.Fatalf("EnsureCacheDir: %v %v", fi, err)
	}
}

func TestLoadMissingFileIsDefault(t *testing.T) {
	c, warns, err := LoadFile(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || len(warns) != 0 || !reflect.DeepEqual(c, Default()) {
		t.Fatalf("got %+v %v %v", c, warns, err)
	}
}

func TestDefaultFileMatchesDefault(t *testing.T) {
	c, warns, err := Parse(DefaultFile)
	if err != nil || len(warns) != 0 {
		t.Fatalf("parse example: %v %v", warns, err)
	}
	if !reflect.DeepEqual(c, Default()) {
		t.Fatalf("example config differs from Default():\n%+v\n%+v", c, Default())
	}
}

func TestParseOverridesAndWarnings(t *testing.T) {
	src := `
theme = "mono"
accent = "#ff8800"
icons = "emoji"
art = "kitty"
sidebar_width = 3
mouse = false
colour = "typo"

[lyrics]
lrclib = false
lead_ms = 350

[lastfm]
enabled = false

[colors]
dim = "#777777"
faint = "grey"

[keys]
leader = ","
"play-pause" = ["p", "<leader><leader>"]
"next" = "]"
"bad" = 3
`
	c, warns, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if c.Theme != "mono" || c.Accent != "#ff8800" || c.Art != "kitty" || c.Mouse {
		t.Errorf("scalars: %+v", c)
	}
	if c.Icons != "auto" || c.SidebarWidth != 22 {
		t.Errorf("invalid values should fall back: icons=%q width=%d", c.Icons, c.SidebarWidth)
	}
	if !c.Lyrics.Enabled || c.Lyrics.LRCLIB || c.Lyrics.LeadMS != 350 || c.LastFM.Enabled {
		t.Errorf("tables: %+v %+v", c.Lyrics, c.LastFM)
	}
	if c.Colors["dim"] != "#777777" || c.Colors["faint"] != "" {
		t.Errorf("colors: %v", c.Colors)
	}
	if c.Keys.Leader != "," || !reflect.DeepEqual(c.Keys.Bindings["play-pause"], []string{"p", "<leader><leader>"}) ||
		!reflect.DeepEqual(c.Keys.Bindings["next"], []string{"]"}) || c.Keys.Bindings["bad"] != nil {
		t.Errorf("keys: %+v", c.Keys)
	}
	joined := strings.Join(warns, "\n")
	for _, want := range []string{`unknown key "colour"`, "icons", "sidebar_width", "colors.faint", "keys.bad"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning about %s in:\n%s", want, joined)
		}
	}
	if len(warns) != 5 {
		t.Errorf("want 5 warnings, got %d:\n%s", len(warns), joined)
	}
}

func TestWordLead(t *testing.T) {
	if c := Default(); c.Lyrics.WordLeadMS != 100 {
		t.Errorf("default word_lead_ms %d", c.Lyrics.WordLeadMS)
	}
	c, _, err := Parse("[lyrics]\nword_lead_ms = 60\n")
	if err != nil || c.Lyrics.WordLeadMS != 60 {
		t.Errorf("word_lead_ms = 60: %d %v", c.Lyrics.WordLeadMS, err)
	}
	c, warns, _ := Parse("[lyrics]\nword_lead_ms = 9000\n")
	if c.Lyrics.WordLeadMS != 100 || len(warns) != 1 || !strings.Contains(warns[0], "word_lead_ms") {
		t.Errorf("out of range: %d %v", c.Lyrics.WordLeadMS, warns)
	}
}

// sidebar_width is checked against the range the layout keeps the sidebar
// in, so a width the layout would change gets a warning.
func TestSidebarWidthRange(t *testing.T) {
	for _, tc := range []struct {
		w, want int
		warn    bool
	}{
		{13, 22, true}, {14, 14, false}, {30, 30, false}, {40, 40, false}, {41, 22, true}, {60, 22, true},
	} {
		c, warns, err := Parse(fmt.Sprintf("sidebar_width = %d\n", tc.w))
		if err != nil {
			t.Fatal(err)
		}
		warned := len(warns) == 1 && strings.Contains(warns[0], "sidebar_width") && strings.Contains(warns[0], "14..40")
		if c.SidebarWidth != tc.want || warned != tc.warn {
			t.Errorf("sidebar_width %d: got %d, warnings %q", tc.w, c.SidebarWidth, warns)
		}
	}
}

func TestParseSyntaxError(t *testing.T) {
	c, _, err := Parse("theme = ")
	if err == nil || !reflect.DeepEqual(c, Default()) {
		t.Fatalf("want error and defaults, got %v %+v", err, c)
	}
}

func TestWriteDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	wrote, err := WriteDefault(path)
	if err != nil || !wrote {
		t.Fatalf("first write: %v %v", wrote, err)
	}
	if err := os.WriteFile(path, []byte("theme = \"mono\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if wrote, err := WriteDefault(path); err != nil || wrote {
		t.Fatalf("second write should be a no-op: %v %v", wrote, err)
	}
	c, _, _ := LoadFile(path)
	if c.Theme != "mono" {
		t.Fatal("existing config was overwritten")
	}
}

func TestParseStart(t *testing.T) {
	c, warns, err := Parse(`start = "albums"`)
	if err != nil || len(warns) != 0 || c.Start != "albums" {
		t.Fatalf("start = albums: %q %v %v", c.Start, warns, err)
	}
	c, warns, err = Parse(`start = "queue"`)
	if err != nil || len(warns) != 1 || c.Start != "recent" {
		t.Fatalf("start = queue: %q %v %v", c.Start, warns, err)
	}
}

func TestAirPlayAutoReconnect(t *testing.T) {
	if !Default().AirPlay.AutoReconnect {
		t.Error("auto_reconnect should default to true")
	}
	c, warns, err := Parse("[airplay]\nauto_reconnect = false\n")
	if err != nil || len(warns) != 0 || c.AirPlay.AutoReconnect {
		t.Errorf("auto_reconnect = false: %+v %v %v", c.AirPlay, warns, err)
	}
	c, warns, err = Parse("[airplay]\nreconnect = false\n")
	if err != nil || !c.AirPlay.AutoReconnect || len(warns) != 1 || !strings.Contains(warns[0], `unknown key "airplay.reconnect"`) {
		t.Errorf("misspelt key: %+v %v %v", c.AirPlay, warns, err)
	}
	// A wrong type is a TOML error, as for every boolean: the file falls back to Default().
	c, _, err = Parse("[airplay]\nauto_reconnect = \"no\"\n")
	if err == nil || !c.AirPlay.AutoReconnect {
		t.Errorf("auto_reconnect = \"no\": %+v %v", c.AirPlay, err)
	}
}
