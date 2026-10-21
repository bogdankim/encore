package ui

import (
	"strconv"
	"strings"
	"time"
)

// clock formats a track position or length: "3:07", "1:02:03".
func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d / time.Second)
	h, m, sec := s/3600, s/60%60, s%60
	if h > 0 {
		return strconv.Itoa(h) + ":" + pad2(m) + ":" + pad2(sec)
	}
	return strconv.Itoa(m) + ":" + pad2(sec)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// clockWidth is the widest clock() of any time up to d, so a time field can
// keep a fixed width while it ticks.
func clockWidth(d time.Duration) int {
	switch {
	case d >= 10*time.Hour:
		return 8
	case d >= time.Hour:
		return 7
	case d >= 10*time.Minute:
		return 5
	}
	return 4
}

// count formats n with thousands separators: "3,158".
func count(n int) string {
	if n < 0 {
		return "-" + count(-n)
	}
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// plural formats "1 song", "3,158 songs".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return count(n) + " " + many
}

// total is encore's one duration formatter for a list of tracks, rounded
// to the minute the way Apple Music does: "42 min", "1 hr 2 min", "3 hr",
// "31 hr 5 min" (hours up to two days), then "15 days".
func total(d time.Duration) string {
	mins := int((d + 30*time.Second) / time.Minute)
	switch {
	case mins < 1:
		if d <= 0 {
			return ""
		}
		return "1 min"
	case mins < 60:
		return strconv.Itoa(mins) + " min"
	case mins < 48*60:
		h, m := mins/60, mins%60
		if m == 0 {
			return strconv.Itoa(h) + " hr"
		}
		return strconv.Itoa(h) + " hr " + strconv.Itoa(m) + " min"
	}
	return strconv.Itoa((mins+12*60)/(24*60)) + " days"
}

// songsMeta is the dim count next to a list's name: "17 songs · 1 hr 2 min".
// Every view that shows a song count and a length uses it.
func songsMeta(n int, d time.Duration) string {
	return joinMeta(plural(n, "song", "songs"), total(d))
}

// joinMeta joins non-empty parts with " · ".
func joinMeta(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += " · "
		}
		out += p
	}
	return out
}

// shortDate formats an added date compactly: "Oct 12, 2026".
func shortDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("Jan 2, 2006")
}

func itoa(n int) string { return strconv.Itoa(n) }

// deleteWord removes the last word (and the spaces after it) from s, like
// ctrl+w in a shell.
func deleteWord(s string) string {
	r := []rune(s)
	i := len(r)
	for i > 0 && r[i-1] == ' ' {
		i--
	}
	for i > 0 && r[i-1] != ' ' {
		i--
	}
	return string(r[:i])
}

// singleLine flattens pasted text for a one-line input.
func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
