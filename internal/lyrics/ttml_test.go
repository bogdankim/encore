package lyrics

import (
	"testing"
	"time"
)

func TestTTMLTime(t *testing.T) {
	ms := time.Millisecond
	for _, c := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"12.345", 12345 * ms, true},
		{"1:02.345", 62345 * ms, true},
		{"00:01:02.345", 62345 * ms, true},
		{"01:00:00", time.Hour, true},
		{"75.5", 75500 * ms, true},
		{"4.2s", 4200 * ms, true},
		{".5", 500 * ms, true},
		{"0", 0, true},
		{"1:02.3456789999", 62*time.Second + 345678999, true},
		{"", 0, false},
		{"1:60.0", 0, false},
		{"1.5:02", 0, false},
		{"a:02", 0, false},
		{"1:2:3:4", 0, false},
		{"-1.0", 0, false},
	} {
		got, ok := ttmlTime(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ttmlTime(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// Invented lyrics in the shape Music's cache has them.
const ttmlWords = `<tt xmlns="http://www.w3.org/ns/ttml" xmlns:itunes="http://music.apple.com/lyric-ttml-internal" xmlns:ttm="http://www.w3.org/ns/ttml#metadata" itunes:timing="Word" xml:lang="en">` +
	`<head><metadata><ttm:agent type="person" xml:id="v1"><ttm:name type="full">Nobody</ttm:name></ttm:agent>` +
	`<iTunesMetadata xmlns="http://music.apple.com/lyric-ttml-internal" leadingSilence="0.020"><translations><translation lang="fr"><text for="L1">Pas ici</text></translation></translations>` +
	`<songwriters><songwriter>Someone</songwriter></songwriters></iTunesMetadata></metadata></head>` +
	`<body dur="1:10.000"><div begin="10.000" end="16.000" itunes:songPart="Verse">` +
	`<p begin="10.000" end="12.500" itunes:key="L1" ttm:agent="v1"><span begin="10.000" end="10.400">Paper</span> <span begin="10.400" end="10.900">lan</span><span begin="10.900" end="11.500">terns</span> <span begin="11.500" end="12.500">&amp; rain</span></p>` +
	`<p begin="13.000" end="16.000" itunes:key="L2" ttm:agent="v1"><span begin="13.000" end="14.000">Hold</span> <span begin="14.000" end="15.000">on</span><span ttm:role="x-bg"><span begin="15.000" end="16.000">(hold)</span></span><span ttm:role="x-translation">Tiens</span></p>` +
	`</div><div begin="16.000" end="1:05.000" itunes:songPart="Chorus">` +
	`<p begin="16.000" end="18.000" itunes:key="L3" ttm:agent="v1"><span begin="16.000" end="18.000">Again</span></p>` +
	`<p begin="1:00.000" end="1:05.000" itunes:key="L4" ttm:agent="v1"><span begin="1:00.000" end="1:05.000">Late</span></p>` +
	`</div></body></tt>`

func TestParseTTMLWords(t *testing.T) {
	l, err := ParseTTML(ttmlWords)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Synced || l.Plain != "" {
		t.Fatalf("synced=%v plain=%q", l.Synced, l.Plain)
	}
	s := time.Second
	want := []struct {
		start time.Duration
		text  string
		words []string
	}{
		{10 * s, "Paper lanterns & rain", []string{"Paper ", "lan", "terns ", "& rain"}},
		{13 * s, "Hold on (hold)", []string{"Hold ", "on ", "(hold)"}},
		{16 * s, "", nil}, // between stanzas
		{16 * s, "Again", []string{"Again"}},
		{18 * s, "", nil}, // a 42 s gap
		{60 * s, "Late", []string{"Late"}},
		{65 * s, "", nil}, // after the last line
	}
	if len(l.Lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(l.Lines), len(want), l.Lines)
	}
	for i, w := range want {
		g := l.Lines[i]
		if g.Start != w.start || g.Text != w.text || len(g.Words) != len(w.words) {
			t.Errorf("line %d = %v %q %d words; want %v %q %d", i, g.Start, g.Text, len(g.Words), w.start, w.text, len(w.words))
			continue
		}
		for j, ww := range w.words {
			if g.Words[j].Text != ww {
				t.Errorf("line %d word %d = %q, want %q", i, j, g.Words[j].Text, ww)
			}
		}
	}
	if w := l.Lines[0].Words; w[1].Start != 10400*time.Millisecond || w[2].Start != 10900*time.Millisecond {
		t.Errorf("syllable starts: %+v", w)
	}
	// each span's end is kept, the background vocal's inner span's too
	ms := time.Millisecond
	for _, c := range []struct {
		line, word int
		start, end time.Duration
	}{
		{0, 0, 10000 * ms, 10400 * ms},
		{0, 2, 10900 * ms, 11500 * ms},
		{0, 3, 11500 * ms, 12500 * ms},
		{1, 2, 15000 * ms, 16000 * ms},
	} {
		if w := l.Lines[c.line].Words[c.word]; w.Start != c.start || w.End != c.end {
			t.Errorf("line %d word %d %q: %v–%v, want %v–%v", c.line, c.word, w.Text, w.Start, w.End, c.start, c.end)
		}
	}
	if got := l.LineAt(13500 * time.Millisecond); got != 1 {
		t.Errorf("LineAt(13.5s) = %d, want 1", got)
	}
	if got := l.LineAt(16 * time.Second); got != 3 {
		t.Errorf("LineAt(16s) = %d, want 3 (the stanza break is never current)", got)
	}
}

func TestParseTTMLWordWithoutEnd(t *testing.T) {
	src := `<tt xmlns="http://www.w3.org/ns/ttml" itunes:timing="Word"><body><div>` +
		`<p begin="1" end="3"><span begin="1">no</span> <span begin="2" end="1.5">back</span></p></div></body></tt>`
	l, err := ParseTTML(src)
	if err != nil {
		t.Fatal(err)
	}
	if w := l.Lines[0].Words; len(w) != 2 || w[0].End != 0 || w[1].End != 0 {
		t.Errorf("a missing or backward end should be 0: %+v", w)
	}
}

func TestParseTTMLLines(t *testing.T) {
	src := `<tt xmlns="http://www.w3.org/ns/ttml" xmlns:itunes="http://music.apple.com/lyric-ttml-internal" itunes:timing="Line"><head><metadata/></head>` +
		`<body><div begin="1.5" end="9"><p begin="1.5" end="4">First   line</p><p begin="00:00:04.250" end="9">Second line</p></div></body></tt>`
	l, err := ParseTTML(src)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Synced || len(l.Lines) != 3 {
		t.Fatalf("%+v", l)
	}
	if l.Lines[0].Text != "First line" || l.Lines[0].Words != nil || l.Lines[1].Start != 4250*time.Millisecond {
		t.Errorf("lines: %+v", l.Lines)
	}
}

func TestParseTTMLUntimed(t *testing.T) {
	src := `<tt xmlns="http://www.w3.org/ns/ttml" xmlns:itunes="http://music.apple.com/lyric-ttml-internal" itunes:timing="None"><body>` +
		`<div><p>One</p><p>Two</p></div><div><p>Three</p></div></body></tt>`
	l, err := ParseTTML(src)
	if err != nil {
		t.Fatal(err)
	}
	if l.Synced || l.Plain != "One\nTwo\n\nThree" {
		t.Fatalf("synced=%v plain=%q", l.Synced, l.Plain)
	}
}

func TestParseTTMLBad(t *testing.T) {
	for _, src := range []string{"", "<tt><body></body></tt>", "<tt><body><div><p begin=\"1\">"} {
		if l, err := ParseTTML(src); err == nil {
			t.Errorf("ParseTTML(%q) = %+v, want an error", src, l)
		}
	}
}
