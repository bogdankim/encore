package bridge

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"strings"
	"unicode"
)

func pidRand(pid, salt string) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(pid))
	h.Write([]byte(salt))
	return rand.New(rand.NewPCG(h.Sum64(), 0x6c79726963)) // "lyric"
}

func instrumental(t *SnapshotTrack) bool {
	switch t.Genre {
	case "Classical", "Ambient":
		return true
	}
	return strings.Contains(t.Title, "Interlude") || strings.Contains(t.Title, "Intermission")
}

// script guesses the lyrics language from the artist's name.
func script(name string) string {
	for _, r := range name {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han):
			return "ja"
		case unicode.Is(unicode.Cyrillic, r):
			return "ru"
		case unicode.Is(unicode.Hangul, r):
			return "ko"
		}
	}
	return "latin"
}

// songSections writes an invented song: verses, a repeated chorus and a bridge,
// cycling until the caller stops taking sections.
func songSections(t *SnapshotTrack, r *rand.Rand) func() []string {
	pick := func(list []string) string { return list[r.IntN(len(list))] }
	sc := script(t.AlbumArtist + t.Artist)
	line := func() string {
		if sc != "latin" && r.IntN(5) > 0 {
			return pick(nlLines[sc])
		}
		obj := strings.ToLower(pick(wNoun))
		if r.IntN(3) == 0 {
			obj = strings.ToLower(pick(wAdj)) + " " + obj
		}
		return pick(lyrOpen) + " " + obj + " " + pick(lyrEnd)
	}
	verse := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = line()
		}
		return out
	}
	hook := pick(lyrHook)
	if sc != "latin" && r.IntN(2) == 0 {
		hook = pick(nlLines[sc])
	}
	chorus := []string{hook, line(), hook, pick(lyrHook)}
	plan := []func() []string{
		func() []string { return verse(4) },
		func() []string { return chorus },
		func() []string { return verse(4) },
		func() []string { return chorus },
		func() []string { return verse(2) },
		func() []string { return chorus },
	}
	i := 0
	return func() []string {
		s := plan[i%len(plan)]()
		i++
		return s
	}
}

func lrcTime(sec float64) string {
	cs := int(sec*100 + 0.5)
	return fmt.Sprintf("[%02d:%02d.%02d]", cs/6000, cs/100%60, cs%100)
}

// syncedLyrics returns LRC for t, or "" for instrumentals and about a third of
// the other tracks.
func syncedLyrics(t *SnapshotTrack) string {
	r := pidRand(t.PID, "lrc")
	if instrumental(t) || r.IntN(3) == 0 {
		return ""
	}
	dur := float64(t.Duration) / 1000
	next := songSections(t, r)
	gap := 3.4 + r.Float64()*1.6
	end := dur - 6

	// two thirds of the songs are timed word by word, like Apple's lyrics;
	// a separate stream keeps the other songs' lyrics as they were
	wr := pidRand(t.PID, "words")
	words := wr.IntN(3) != 0

	var b strings.Builder
	fmt.Fprintf(&b, "[ar:%s]\n[ti:%s]\n[al:%s]\n[length:%d:%02d]\n", t.Artist, t.Title, t.Album, int(dur)/60, int(dur)%60)
	at := 6 + r.Float64()*12
	for at < end {
		for _, l := range next() {
			if at >= end {
				break
			}
			step := gap * (0.85 + r.Float64()*0.3)
			if words {
				l = wordTags(l, at, step*(0.7+wr.Float64()*0.15), wr)
			}
			b.WriteString(lrcTime(at) + l + "\n")
			at += step
		}
		// An empty line clears the highlight during the instrumental break.
		b.WriteString(lrcTime(at) + "\n")
		at += 3 + r.Float64()*6
	}
	return b.String()
}

// wordTags times the words of l across d seconds from at, in enhanced LRC:
// "<00:12.00>Hold <00:12.40>on <00:13.10>". A word takes time for its length,
// give or take, and the last tag is when the line has been sung through.
func wordTags(l string, at, d float64, r *rand.Rand) string {
	ws := strings.Fields(l)
	if len(ws) == 0 {
		return l
	}
	weights := make([]float64, len(ws))
	total := 0.0
	for i, w := range ws {
		weights[i] = float64(len([]rune(w))+1) * (0.8 + r.Float64()*0.4)
		total += weights[i]
	}
	var b strings.Builder
	t := at
	for i, w := range ws {
		b.WriteString(wordTime(t) + w)
		if i < len(ws)-1 {
			b.WriteByte(' ')
		}
		t += d * weights[i] / total
	}
	b.WriteString(wordTime(t))
	return b.String()
}

func wordTime(sec float64) string {
	cs := int(sec*100 + 0.5)
	return fmt.Sprintf("<%02d:%02d.%02d>", cs/6000, cs/100%60, cs%100)
}

// plainLyrics stands in for Music's own lyrics property: empty for streamed
// tracks and most others, a few verses for some purchased and local ones.
func plainLyrics(t *SnapshotTrack) string {
	r := pidRand(t.PID, "plain")
	if t.Kind == "subscription" || instrumental(t) || r.IntN(4) != 0 {
		return ""
	}
	next := songSections(t, r)
	parts := make([]string, 6)
	for i := range parts {
		parts[i] = strings.Join(next(), "\n")
	}
	return strings.Join(parts, "\n\n")
}
