package lyrics

import (
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ttmlBreakMin is the gap between two lines of a stanza long enough to
// clear the highlight, as an empty line would in LRC.
const ttmlBreakMin = 4 * time.Second

var errNoTTMLLines = errors.New("lyrics: TTML has no lines")

type ttmlLine struct {
	begin, end time.Duration
	timed      bool
	div        int
	text       strings.Builder
	words      []Word
}

// ParseTTML reads Apple Music's lyrics TTML: <body><div><p begin end>, the
// words in <span begin end> when the timing is "Word". A <div> is a stanza.
// Background vocals (ttm:role="x-bg") stay in the line after the lead
// vocal; translations and transliterations are left out. Lyrics without
// timing come back as Plain. Source is left for the caller.
func ParseTTML(src string) (*Lyrics, error) {
	d := xml.NewDecoder(strings.NewReader(src))
	d.Strict = false

	var (
		lines  []*ttmlLine
		cur    *ttmlLine
		div    = -1
		inBody bool
		skip   int // depth inside elements whose text isn't lyrics

		// open spans inside the current line: their begin (when timed) and
		// a serial, so each timed span makes one word
		spans    []ttmlSpan
		serial   int
		wordSpan = -1
	)
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if skip > 0 {
				skip++
				continue
			}
			switch t.Name.Local {
			case "head":
				skip = 1
			case "body":
				inBody = true
			case "div":
				if inBody {
					div++
				}
			case "p":
				if !inBody {
					continue
				}
				cur = &ttmlLine{div: max(div, 0)}
				if b, ok := ttmlTime(attr(t, "begin")); ok {
					cur.begin, cur.timed = b, true
					cur.end = cur.begin
					if e, ok := ttmlTime(attr(t, "end")); ok && e >= b {
						cur.end = e
					}
				}
				lines = append(lines, cur)
				spans, wordSpan = spans[:0], -1
			case "span":
				if cur == nil {
					continue
				}
				switch attr(t, "role") {
				case "x-translation", "x-roman":
					skip = 1
					continue
				case "x-bg":
					if s := cur.text.String(); s != "" && !strings.HasSuffix(s, " ") {
						cur.appendText(" ")
					}
				}
				serial++
				sp := ttmlSpan{id: serial}
				sp.begin, sp.timed = ttmlTime(attr(t, "begin"))
				if e, ok := ttmlTime(attr(t, "end")); ok && sp.timed && e > sp.begin {
					sp.end = e
				}
				spans = append(spans, sp)
			case "br":
				if cur != nil {
					cur.appendText(" ")
				}
			}
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			switch t.Name.Local {
			case "body":
				inBody = false
			case "p":
				cur = nil
			case "span":
				if len(spans) > 0 {
					spans = spans[:len(spans)-1]
				}
			}
		case xml.CharData:
			if skip > 0 || cur == nil {
				continue
			}
			s := string(t)
			if n := len(spans); n > 0 && spans[n-1].timed && strings.TrimSpace(s) != "" {
				if sp := spans[n-1]; sp.id != wordSpan {
					wordSpan = sp.id
					cur.words = append(cur.words, Word{Start: sp.begin, End: sp.end})
				}
			}
			cur.appendText(s)
		}
	}
	return buildTTML(lines, src)
}

type ttmlSpan struct {
	id         int
	begin, end time.Duration // end is 0 when missing
	timed      bool
}

// appendText adds s to the line and, as in LRC, to the last word, with
// runs of white space as one space.
func (l *ttmlLine) appendText(s string) {
	s = collapseSpace(s)
	if strings.HasPrefix(s, " ") && (l.text.Len() == 0 || strings.HasSuffix(l.text.String(), " ")) {
		s = s[1:]
	}
	if s == "" {
		return
	}
	l.text.WriteString(s)
	if n := len(l.words); n > 0 {
		l.words[n-1].Text += s
	}
}

func collapseSpace(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

func attr(e xml.StartElement, local string) string {
	for _, a := range e.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func buildTTML(ps []*ttmlLine, src string) (*Lyrics, error) {
	ps = slices.DeleteFunc(ps, func(p *ttmlLine) bool { return strings.TrimSpace(p.text.String()) == "" })
	if len(ps) == 0 {
		return nil, errNoTTMLLines
	}
	timed := true
	for _, p := range ps {
		timed = timed && p.timed
	}
	if !timed {
		var b strings.Builder
		for i, p := range ps {
			if i > 0 {
				b.WriteByte('\n')
				if p.div != ps[i-1].div {
					b.WriteByte('\n')
				}
			}
			b.WriteString(strings.Join(strings.Fields(p.text.String()), " "))
		}
		return &Lyrics{Plain: b.String(), ttml: src}, nil
	}
	lines := make([]Line, 0, len(ps)*5/4+1)
	for i, p := range ps {
		words := slices.DeleteFunc(p.words, func(w Word) bool { return strings.TrimSpace(w.Text) == "" })
		if len(words) > 0 {
			words[0].Text = strings.TrimLeft(words[0].Text, " ")
			words[len(words)-1].Text = strings.TrimRight(words[len(words)-1].Text, " ")
		} else {
			words = nil
		}
		lines = append(lines, Line{Start: p.begin, Text: strings.TrimSpace(p.text.String()), Words: words})
		// An empty line clears the highlight: after the last line, between
		// stanzas and in a long gap.
		switch {
		case i == len(ps)-1:
			lines = append(lines, Line{Start: p.end})
		case ps[i+1].div != p.div:
			lines = append(lines, Line{Start: min(p.end, ps[i+1].begin)})
		case ps[i+1].begin-p.end >= ttmlBreakMin:
			lines = append(lines, Line{Start: p.end})
		}
	}
	slices.SortStableFunc(lines, func(a, b Line) int {
		switch {
		case a.Start < b.Start:
			return -1
		case a.Start > b.Start:
			return 1
		}
		return 0
	})
	return &Lyrics{Synced: true, Lines: lines, ttml: src}, nil
}

// ttmlTime parses a TTML clock value: "12.345", "1:02.345", "00:01:02.345",
// optionally with an "s" suffix.
func ttmlTime(s string) (time.Duration, bool) {
	s = strings.TrimSuffix(strings.TrimSpace(s), "s")
	if s == "" {
		return 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, false
	}
	var total time.Duration
	for i, p := range parts {
		last := i == len(parts)-1
		whole, frac, _ := strings.Cut(p, ".")
		if whole == "" && !(last && frac != "") || (!last && frac != "") {
			return 0, false
		}
		n := 0
		if whole != "" {
			var err error
			if n, err = strconv.Atoi(whole); err != nil || n < 0 || (i > 0 && n >= 60) {
				return 0, false
			}
		}
		total = total*60 + time.Duration(n)*time.Second
		if last && frac != "" {
			if len(frac) > 9 {
				frac = frac[:9]
			}
			f, err := strconv.Atoi(frac)
			if err != nil || f < 0 {
				return 0, false
			}
			for range 9 - len(frac) {
				f *= 10
			}
			total += time.Duration(f)
		}
	}
	return total, true
}
