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

// refBox is what Box did before it avoided copying: split, fit each line,
// join.
func refBox(s string, w, h int) string {
	if h <= 0 || w <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	var b strings.Builder
	for i := 0; i < h; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i < len(lines) {
			b.WriteString(Fit(lines[i], w))
		} else {
			b.WriteString(strings.Repeat(" ", w))
		}
	}
	return b.String()
}

func TestBoxAndLinesMatchTheReference(t *testing.T) {
	pool := []string{"a", "bc", " ", "é", "日", "\x1b[1m", "\x1b[0m", "▀", "…", "\n", "\n", "xyz"}
	r := rand.New(rand.NewPCG(3, 4))
	for range 5000 {
		var b strings.Builder
		for range r.IntN(40) {
			b.WriteString(pool[r.IntN(len(pool))])
		}
		s := b.String()
		w, h := 1+r.IntN(30), 1+r.IntN(6)
		if got, want := Box(s, w, h), refBox(s, w, h); got != want {
			t.Fatalf("Box(%q, %d, %d) = %q, want %q", s, w, h, got, want)
		}
		if got, want := Lines(strings.Split(s, "\n"), w, h), refBox(s, w, h); got != want {
			t.Fatalf("Lines(%q, %d, %d) = %q, want %q", s, w, h, got, want)
		}
		// a line list in which a line holds a newline takes the slow path
		if got, want := Lines([]string{s, "tail"}, w, h), refBox(s+"\ntail", w, h); got != want {
			t.Fatalf("Lines with embedded newlines (%q, %d, %d) = %q, want %q", s, w, h, got, want)
		}
	}
	exact := strings.Repeat(strings.Repeat("ab", 5)+"\n", 3) + strings.Repeat("ab", 5)
	if Box(exact, 10, 4) != exact {
		t.Fatal("an exact box changed")
	}
}
