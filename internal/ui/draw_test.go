package ui

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// quickWidth must agree with ansi.StringWidth wherever it answers, and
// decline (ok false) for anything it can't be sure of.
func TestQuickWidthMatchesANSI(t *testing.T) {
	pool := []string{
		"a", "Z", " ", "é", "Ö", "ñ", "ł", "│", "─", "▎", "▶", "▀", "▄", "█", "…", "·", "♥", "♪", "‹", "›",
		"—", "‘", "\x1b[0m", "\x1b[38;2;250;88;106m", "\x1b[1;4m", "\x1b[48;2;1;2;3m",
		"日", "🎵", "​", "\x1b]8;;http://x\x1b\\", "\t", "\x00", "­", "ﬁ", "é",
	}
	r := rand.New(rand.NewPCG(1, 2))
	var fast int
	for range 50000 {
		var b strings.Builder
		for range 1 + r.IntN(30) {
			b.WriteString(pool[r.IntN(len(pool))])
		}
		s := b.String()
		if w, ok := quickWidth(s); ok {
			fast++
			if want := ansi.StringWidth(s); w != want {
				t.Fatalf("quickWidth(%q) = %d, ansi.StringWidth = %d", s, w, want)
			}
		}
	}
	if fast < 2000 {
		t.Errorf("only %d of 50000 random strings took the fast path", fast)
	}
}

// Every line of the views' frames measures the same both ways, and most of
// them take the fast path.
func TestQuickWidthOnFrames(t *testing.T) {
	var lines, fast int
	for _, sz := range []struct{ w, h int }{{100, 30}, {140, 42}} {
		a := newShellApp(t, sz.w, sz.h)
		for _, sec := range []struct {
			key, title string
		}{{KeySongs, "Songs"}, {KeyAlbums, "Albums"}, {KeyArtists, "Artists"}, {KeyRecent, "Recent"}, {KeyHome, "Home"}} {
			a.goSection(sec.key, sec.title)
			for _, l := range strings.Split(a.render(), "\n") {
				lines++
				if w, ok := quickWidth(l); ok {
					fast++
					if want := ansi.StringWidth(l); w != want {
						t.Fatalf("%s: quickWidth = %d, ansi.StringWidth = %d for %q", sec.title, w, want, l)
					}
				}
			}
		}
	}
	if fast*10 < lines*9 {
		t.Errorf("%d of %d frame lines took the fast path, want 90%%", fast, lines)
	}
}

func BenchmarkWidth(b *testing.B) {
	line := "\x1b[38;2;250;88;106m▎▶\x1b[0m \x1b[1mDaylight Drive\x1b[0m  \x1b[38;2;139;139;147mRedmatic · Daylight Drive - Single\x1b[0m  2:12"
	b.Run("quick", func(b *testing.B) {
		for b.Loop() {
			Width(line)
		}
	})
	b.Run("ansi", func(b *testing.B) {
		for b.Loop() {
			ansi.StringWidth(line)
		}
	})
}
