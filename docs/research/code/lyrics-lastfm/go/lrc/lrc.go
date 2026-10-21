// Package lrc parses LRC lyrics, including multiple timestamps per line,
// [offset:], and enhanced (A2) word timestamps <mm:ss.xx>.
package lrc

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

type Word struct {
	Start time.Duration
	Text  string
}

type Line struct {
	Start time.Duration
	Text  string // empty = instrumental gap / end marker: clear the highlight
	Words []Word // nil unless the line had <mm:ss.xx> word tags
}

type Lyrics struct {
	Tags  map[string]string // ar, ti, al, au, length, by, offset, re, ve ...
	Lines []Line            // sorted by Start, stable for equal times
}

// Parse never fails: malformed lines are skipped.
func Parse(src string) Lyrics {
	src = strings.TrimPrefix(src, "\ufeff")
	out := Lyrics{Tags: map[string]string{}}
	var offset time.Duration
	for raw := range strings.SplitSeq(src, "\n") {
		s := strings.TrimRight(raw, "\r \t")
		var stamps []time.Duration
		for strings.HasPrefix(s, "[") {
			end := strings.IndexByte(s, ']')
			if end < 0 {
				break
			}
			tag := s[1:end]
			if d, ok := parseTime(tag); ok {
				stamps = append(stamps, d)
			} else if k, v, ok := strings.Cut(tag, ":"); ok && len(stamps) == 0 && isTagKey(k) {
				k = strings.ToLower(strings.TrimSpace(k))
				v = strings.TrimSpace(v)
				out.Tags[k] = v
				if k == "offset" {
					if ms, err := strconv.Atoi(strings.TrimPrefix(v, "+")); err == nil {
						offset = time.Duration(ms) * time.Millisecond
					}
				}
			} else {
				break // "[Chorus]" etc. after a timestamp is lyric text
			}
			s = s[end+1:]
		}
		if len(stamps) == 0 {
			continue // tag line, blank line, or untimed text
		}
		text, words := parseWords(strings.TrimPrefix(s, " "))
		for _, st := range stamps {
			out.Lines = append(out.Lines, Line{Start: st, Text: text, Words: words})
		}
	}
	// Positive offset = lyrics appear sooner.
	if offset != 0 {
		for i := range out.Lines {
			out.Lines[i].Start = max(0, out.Lines[i].Start-offset)
			for j := range out.Lines[i].Words {
				out.Lines[i].Words[j].Start = max(0, out.Lines[i].Words[j].Start-offset)
			}
		}
	}
	sort.SliceStable(out.Lines, func(i, j int) bool { return out.Lines[i].Start < out.Lines[j].Start })
	return out
}

// At returns the index of the line active at pos, or -1 before the first line.
func (l Lyrics) At(pos time.Duration) int {
	return sort.Search(len(l.Lines), func(i int) bool { return l.Lines[i].Start > pos }) - 1
}

func isTagKey(k string) bool {
	if k == "" || len(k) > 16 {
		return false
	}
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '#') {
			return false
		}
	}
	return true
}

// parseTime accepts m:ss, mm:ss.x, mm:ss.xx, mm:ss.xxx, mm:ss:xx and h:mm:ss.xx.
func parseTime(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var frac string
	last := parts[len(parts)-1]
	if sec, f, ok := strings.Cut(last, "."); ok {
		parts[len(parts)-1], frac = sec, f
	} else if len(parts) == 3 && len(parts[2]) == 2 && len(parts[1]) == 2 && len(parts[0]) <= 3 {
		// [mm:ss:xx] variant (colon before hundredths)
		frac, parts = parts[2], parts[:2]
	}
	var total time.Duration
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" {
			return 0, false
		}
		total = total*60 + time.Duration(n)
	}
	if n, _ := strconv.Atoi(parts[len(parts)-1]); n >= 60 {
		return 0, false
	}
	total *= time.Second
	if frac != "" {
		if len(frac) > 3 {
			frac = frac[:3]
		}
		n, err := strconv.Atoi(frac)
		if err != nil {
			return 0, false
		}
		for i := len(frac); i < 3; i++ {
			n *= 10
		}
		total += time.Duration(n) * time.Millisecond
	}
	return total, true
}

// parseWords splits "<00:01.00>Hello <00:01.50>world" into words.
// Returns plain text with tags removed.
func parseWords(s string) (string, []Word) {
	if !strings.Contains(s, "<") {
		return s, nil
	}
	var words []Word
	var text strings.Builder
	for s != "" {
		if s[0] == '<' {
			if end := strings.IndexByte(s, '>'); end > 0 {
				if d, ok := parseTime(s[1:end]); ok {
					words = append(words, Word{Start: d})
					s = s[end+1:]
					continue
				}
			}
		}
		next := strings.IndexByte(s[1:], '<') + 1
		if next == 0 {
			next = len(s)
		}
		chunk := s[:next]
		text.WriteString(chunk)
		if len(words) > 0 {
			words[len(words)-1].Text += chunk
		}
		s = s[next:]
	}
	// Drop trailing end-time tags with no text (A2 lines often end with <mm:ss.xx>).
	for len(words) > 0 && words[len(words)-1].Text == "" {
		words = words[:len(words)-1]
	}
	return strings.TrimSpace(text.String()), words
}
