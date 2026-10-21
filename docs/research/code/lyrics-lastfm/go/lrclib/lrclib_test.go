package lrclib

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestClean(t *testing.T) {
	cases := map[string]string{
		"Get Lucky (feat. Pharrell Williams & Nile Rodgers)": "Get Lucky",
		"Bohemian Rhapsody - Remastered 2011":                "Bohemian Rhapsody",
		"Bohemian Rhapsody (2011 Remaster)":                  "Bohemian Rhapsody",
		"Song [feat. X]":                                     "Song",
		"Get Lucky (Radio Edit) [feat. Pharrell Williams]":   "Get Lucky (Radio Edit)",
		"Come Together - 2019 Mix":                           "Come Together - 2019 Mix",
		"Here Comes the Sun - Remastered":                    "Here Comes the Sun",
	}
	for in, want := range cases {
		if got := CleanTitle(in); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"Lady Gaga & Bruno Mars": "Lady Gaga", "Calvin Harris, Rihanna": "Calvin Harris",
		"Daft Punk": "Daft Punk", "Silk Sonic feat. Bruno Mars": "Silk Sonic",
	} {
		if got := PrimaryArtist(in); got != want {
			t.Errorf("PrimaryArtist(%q) = %q, want %q", in, got, want)
		}
	}
}

// LRCLIB_LIVE=1 go test ./lrclib -run Live -v
func TestLiveFind(t *testing.T) {
	if os.Getenv("LRCLIB_LIVE") == "" {
		t.Skip("set LRCLIB_LIVE=1")
	}
	c := &Client{UserAgent: "encore-research v0.0.1 (https://github.com/example/encore)"}
	for _, tc := range []struct {
		track, artist, album string
		dur                  float64
	}{
		{"Get Lucky (feat. Pharrell Williams & Nile Rodgers)", "Daft Punk", "Random Access Memories (Deluxe Edition)", 369},
		{"Alors on danse", "Stromae", "Alors on danse - Single", 206},
		{"Avril 14th", "Aphex Twin", "Drukqs", 125},
		{"Not A Song 123", "Nobody Real Xyz", "", 200},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		start := time.Now()
		r, err := c.Find(ctx, tc.track, tc.artist, tc.album, tc.dur)
		cancel()
		if err != nil {
			t.Logf("%-40.40s -> %v (%v)", tc.track, err, time.Since(start).Round(time.Millisecond))
			continue
		}
		t.Logf("%-40.40s -> id=%d %q/%q dur=%.0f synced=%v instr=%v (%v)", tc.track, r.ID, r.TrackName, r.AlbumName, r.Duration, r.Synced(), r.Instrumental, time.Since(start).Round(time.Millisecond))
		time.Sleep(300 * time.Millisecond)
	}
}
