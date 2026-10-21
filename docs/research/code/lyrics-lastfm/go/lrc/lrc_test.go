package lrc

import (
	"testing"
	"time"
)

const ms = time.Millisecond

func TestParse(t *testing.T) {
	src := "\ufeff[ar:Daft Punk]\r\n[ti:Get Lucky]\n[offset:+500]\n" +
		"[00:31.48] Paper lanterns in the rain\n" +
		"[01:00.00][00:40.5]Chorus line\n" +
		"[00:45:20]colon variant\n" +
		"[00:50.000][Bridge] bracket text\n" +
		"<not a tag line>\n" +
		"[01:10.00]<01:10.00>Word <01:10.50>sync<01:11.00>\n" +
		"[03:25.72] \n"
	l := Parse(src)
	if l.Tags["ar"] != "Daft Punk" || l.Tags["offset"] != "+500" {
		t.Fatalf("tags: %v", l.Tags)
	}
	want := []struct {
		at   time.Duration
		text string
	}{
		{30980 * ms, "Paper lanterns in the rain"},
		{40000 * ms, "Chorus line"},
		{44700 * ms, "colon variant"},
		{49500 * ms, "[Bridge] bracket text"},
		{59500 * ms, "Chorus line"},
		{69500 * ms, "Word sync"},
		{205220 * ms, ""},
	}
	if len(l.Lines) != len(want) {
		t.Fatalf("got %d lines: %+v", len(l.Lines), l.Lines)
	}
	for i, w := range want {
		if l.Lines[i].Start != w.at || l.Lines[i].Text != w.text {
			t.Errorf("line %d = %v %q, want %v %q", i, l.Lines[i].Start, l.Lines[i].Text, w.at, w.text)
		}
	}
	ws := l.Lines[5].Words
	if len(ws) != 2 || ws[0].Text != "Word " || ws[1].Start != 70000*ms {
		t.Errorf("words: %+v", ws)
	}
	if l.At(0) != -1 || l.At(45*time.Second) != 2 || l.At(10*time.Minute) != 6 {
		t.Errorf("At: %d %d %d", l.At(0), l.At(45*time.Second), l.At(10*time.Minute))
	}
}
