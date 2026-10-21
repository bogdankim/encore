package lyrics

import (
	"testing"
	"time"
)

const ms = time.Millisecond

func TestParseLRC(t *testing.T) {
	src := "\ufeff[ar:Daft Punk]\r\n[ti:Get Lucky]\n[offset:+500]\n" +
		"[00:31.48] Paper lanterns in the rain\n" +
		"[01:00.00][00:40.5]Chorus line\n" +
		"[00:45:20]colon variant\n" +
		"[00:50.000][Bridge] bracket text\n" +
		"<not a tag line>\n" +
		"[01:10.00]<01:10.00>Word <01:10.50>sync<01:11.00>\n" +
		"[03:25.72] \n"
	l := ParseLRC(src)
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
	ly := &Lyrics{Synced: true, Lines: l.Lines}
	if ly.LineAt(0) != -1 || ly.LineAt(45*time.Second) != 2 || ly.LineAt(10*time.Minute) != 6 {
		t.Errorf("LineAt: %d %d %d", ly.LineAt(0), ly.LineAt(45*time.Second), ly.LineAt(10*time.Minute))
	}
	if ly.LineAt(30980*ms) != 0 || ly.LineAt(30979*ms) != -1 {
		t.Error("LineAt boundary")
	}
}

func TestRepeatedStampsShiftWordsOnce(t *testing.T) {
	l := ParseLRC("[offset:1000]\n[00:10.00][00:20.00]<00:10.00>a <00:10.50>b\n")
	if len(l.Lines) != 2 {
		t.Fatalf("lines: %+v", l.Lines)
	}
	if got := l.Lines[0].Words[0].Start; got != 9*time.Second {
		t.Errorf("word start %v, want 9s (offset applied once)", got)
	}
}

func TestLRCWordEnds(t *testing.T) {
	ms := time.Millisecond
	l := ParseLRC("[00:10.00]<00:10.00>Hold <00:10.40>on <00:11.20>\n" +
		"[00:20.00]Lead in <00:21.00>then <00:21.50>more\n" +
		"[00:30.00]a <b> c\n")
	if len(l.Lines) != 3 {
		t.Fatalf("lines: %+v", l.Lines)
	}
	got := l.Lines[0].Words
	want := []Word{{10000 * ms, 10400 * ms, "Hold "}, {10400 * ms, 11200 * ms, "on "}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("words with a closing tag: %+v, want %+v", got, want)
	}
	// text before the first tag starts with the line; the last word has no end
	got = l.Lines[1].Words
	want = []Word{{20000 * ms, 21000 * ms, "Lead in "}, {21000 * ms, 21500 * ms, "then "}, {21500 * ms, 0, "more"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("leading text: %+v, want %+v", got, want)
	}
	if l.Lines[2].Words != nil || l.Lines[2].Text != "a <b> c" {
		t.Errorf("a line without time tags has no words: %+v", l.Lines[2])
	}
	// the offset moves ends with starts
	o := ParseLRC("[offset:500]\n[00:10.00]<00:10.00>a <00:10.40>b <00:11.00>\n")
	if w := o.Lines[0].Words; w[0].Start != 9500*ms || w[0].End != 9900*ms || w[1].End != 10500*ms {
		t.Errorf("offset: %+v", w)
	}
}

func TestLineAtUnsynced(t *testing.T) {
	var nilLyrics *Lyrics
	plain := &Lyrics{Plain: "a\nb\n\n"}
	if nilLyrics.LineAt(time.Minute) != -1 || plain.LineAt(time.Minute) != -1 {
		t.Error("unsynced LineAt should be -1")
	}
	if got := plain.PlainLines(); len(got) != 2 || got[1] != "b" {
		t.Errorf("PlainLines: %q", got)
	}
}

func TestFromText(t *testing.T) {
	if l := FromText("[00:01.00]hi\n[00:02.00]there", SourceDemo); l == nil || !l.Synced || len(l.Lines) != 2 || l.Source != SourceDemo {
		t.Errorf("synced: %+v", l)
	}
	if l := FromText("just words\nmore", SourceMusic); l == nil || l.Synced || l.Plain != "just words\nmore" {
		t.Errorf("plain: %+v", l)
	}
	if l := FromText("  \n", SourceMusic); l != nil {
		t.Errorf("blank: %+v", l)
	}
}
