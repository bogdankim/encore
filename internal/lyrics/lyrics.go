// Package lyrics finds lyrics for the playing track: Apple Music's synced
// lyrics from Music.app's URL cache, an LRC parser and LRCLIB client,
// Music.app's own lyrics as a fallback, a disk cache and per-track timing
// offsets.
package lyrics

import (
	"sort"
	"strings"
	"time"
)

// Sources of a Lyrics value.
const (
	SourceLRCLIB = "lrclib"
	SourceMusic  = "music"
	SourceDemo   = "demo"
)

// Lyrics is what the lyrics pane renders.
//
// Synced lyrics have timed Lines (Plain may also be set). Unsynced lyrics
// have only Plain; the pane scrolls it with the track position. Instrumental
// tracks have neither.
type Lyrics struct {
	Synced       bool
	Instrumental bool
	Lines        []Line // sorted by Start; only when Synced
	Plain        string
	Source       string // SourceApple, SourceLRCLIB, SourceMusic or SourceDemo
	LRCLIBID     int

	lrc  string // raw LRC text behind Lines, for the disk cache
	ttml string // raw TTML behind an Apple result, for the disk cache
}

type Line struct {
	Start time.Duration
	Text  string // empty = instrumental gap / end marker: clear the highlight
	Words []Word // nil unless the line had <mm:ss.xx> word tags or TTML word timing
}

// Word is a timed part of a line (a word, or a syllable in Apple's
// lyrics). The Texts of a line's Words, in order, are a run of its Text,
// spaces included.
type Word struct {
	Start time.Duration
	End   time.Duration // when it has been sung through; 0 when unknown
	Text  string
}

// LineAt returns the index of the line active at pos, or -1 before the first
// line and for unsynced lyrics. pos should already include the per-track
// offset and the configured lead.
func (l *Lyrics) LineAt(pos time.Duration) int {
	if l == nil || !l.Synced {
		return -1
	}
	return sort.Search(len(l.Lines), func(i int) bool { return l.Lines[i].Start > pos }) - 1
}

// PlainLines returns the unsynced text split into lines, trailing blank
// lines removed. For synced lyrics without Plain it joins the line texts.
func (l *Lyrics) PlainLines() []string {
	if l == nil {
		return nil
	}
	if l.Plain == "" && l.Synced {
		out := make([]string, len(l.Lines))
		for i, ln := range l.Lines {
			out[i] = ln.Text
		}
		return trimBlankTail(out)
	}
	text := strings.ReplaceAll(strings.TrimPrefix(l.Plain, "\ufeff"), "\r\n", "\n")
	return trimBlankTail(strings.Split(text, "\n"))
}

func trimBlankTail(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// FromText builds Lyrics from text that may be LRC or plain. Text with at
// least one timed line is treated as synced.
func FromText(text, source string) *Lyrics {
	text = strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	if text == "" {
		return nil
	}
	if lrc := ParseLRC(text); hasText(lrc.Lines) {
		return &Lyrics{Synced: true, Lines: lrc.Lines, Source: source, lrc: text}
	}
	return &Lyrics{Plain: text, Source: source}
}

func hasText(lines []Line) bool {
	for _, l := range lines {
		if strings.TrimSpace(l.Text) != "" {
			return true
		}
	}
	return false
}
