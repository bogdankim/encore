package lyrics

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LRC is a parsed LRC document: multiple timestamps per line, [offset:] and
// enhanced (A2) word timestamps <mm:ss.xx> are supported.
type LRC struct {
	Tags  map[string]string // ar, ti, al, au, length, by, offset, re, ve ...
	Lines []Line            // sorted by Start, stable for equal times
}

// ParseLRC never fails: malformed lines are skipped.
func ParseLRC(src string) LRC {
	src = strings.TrimPrefix(src, "\ufeff")
	out := LRC{Tags: map[string]string{}}
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
			// Each copy gets its own Words so the offset pass below shifts them once.
			ws := slices.Clone(words)
			if len(ws) > 0 && ws[0].Start < 0 {
				ws[0].Start = st // text before the first word tag starts with the line
			}
			out.Lines = append(out.Lines, Line{Start: st, Text: text, Words: ws})
		}
	}
	// Positive offset = lyrics appear sooner.
	if offset != 0 {
		for i := range out.Lines {
			out.Lines[i].Start = max(0, out.Lines[i].Start-offset)
			for j := range out.Lines[i].Words {
				w := &out.Lines[i].Words[j]
				w.Start = max(0, w.Start-offset)
				if w.End > 0 {
					w.End = max(w.Start, w.End-offset)
				}
			}
		}
	}
	sort.SliceStable(out.Lines, func(i, j int) bool { return out.Lines[i].Start < out.Lines[j].Start })
	return out
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

// parseWords splits "<00:01.00>Hello <00:01.50>world <00:02.10>" into
// words. A tag ends the word before it and starts the next; a last tag with
// no text after it only ends the last word. Text before the first tag is a
// word with Start -1, for the caller to start with the line. Returns plain
// text with tags removed.
func parseWords(s string) (string, []Word) {
	if !strings.Contains(s, "<") {
		return s, nil
	}
	var words []Word
	var text strings.Builder
	tagged := false
	for s != "" {
		if s[0] == '<' {
			if end := strings.IndexByte(s, '>'); end > 0 {
				if d, ok := parseTime(s[1:end]); ok {
					if n := len(words); n > 0 && words[n-1].Text != "" && d > words[n-1].Start {
						words[n-1].End = d
					}
					words = append(words, Word{Start: d})
					tagged = true
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
		if len(words) == 0 && strings.TrimSpace(chunk) != "" {
			words = append(words, Word{Start: -1})
		}
		if len(words) > 0 {
			words[len(words)-1].Text += chunk
		}
		s = s[next:]
	}
	if !tagged {
		words = nil
	}
	// Drop trailing end-time tags with no text (A2 lines often end with <mm:ss.xx>).
	for len(words) > 0 && words[len(words)-1].Text == "" {
		words = words[:len(words)-1]
	}
	return strings.TrimSpace(text.String()), words
}
