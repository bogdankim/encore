package lyrics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeMusicCache is a stand-in for Music's Cache.db with the tables and
// columns AppleCache reads, and no others.
type fakeMusicCache struct {
	t   *testing.T
	dir string
	db  *sqliteDB // stays open, so writes sit in the WAL as with Music
	id  int64
}

func newFakeMusicCache(t *testing.T) *fakeMusicCache {
	t.Helper()
	if !sqliteAvailable {
		t.Skip("no SQLite in this build")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "fsCachedData"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := openSQLite("file:"+filepath.Join(dir, "Cache.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	f := &fakeMusicCache{t: t, dir: dir, db: db, id: 100}
	f.exec(`PRAGMA journal_mode=WAL;
		PRAGMA wal_autocheckpoint=0;
		CREATE TABLE cfurl_cache_response(entry_ID INTEGER PRIMARY KEY AUTOINCREMENT UNIQUE, request_key TEXT UNIQUE, time_stamp NOT NULL DEFAULT CURRENT_TIMESTAMP);
		CREATE TABLE cfurl_cache_receiver_data(entry_ID INTEGER PRIMARY KEY, isDataOnFS INTEGER, receiver_data BLOB);`)
	return f
}

func (f *fakeMusicCache) exec(sql string) {
	f.t.Helper()
	if err := f.db.Exec(sql); err != nil {
		f.t.Fatal(err)
	}
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// add stores a response for key; onFS puts the body in fsCachedData as
// Music does for larger ones.
func (f *fakeMusicCache) add(key, at string, body []byte, onFS bool) int64 {
	f.t.Helper()
	f.id++
	data := fmt.Sprintf("CAST(%s AS BLOB)", sqlQuote(string(body)))
	if onFS {
		name := fmt.Sprintf("0000-%d", f.id)
		if err := os.WriteFile(filepath.Join(f.dir, "fsCachedData", name), body, 0o644); err != nil {
			f.t.Fatal(err)
		}
		data = sqlQuote(name)
	}
	isFS := 0
	if onFS {
		isFS = 1
	}
	f.exec(fmt.Sprintf(`INSERT INTO cfurl_cache_response(entry_ID, request_key, time_stamp) VALUES (%d, %s, %s);
		INSERT INTO cfurl_cache_receiver_data(entry_ID, isDataOnFS, receiver_data) VALUES (%d, %d, %s);`,
		f.id, sqlQuote(key), sqlQuote(at), f.id, isFS, data))
	// Music's writes land within the same second; make sure the stat sees one.
	bump(f.t, filepath.Join(f.dir, "Cache.db-wal"))
	return f.id
}

func (f *fakeMusicCache) remove(id int64) {
	f.exec(fmt.Sprintf(`DELETE FROM cfurl_cache_response WHERE entry_ID = %d; DELETE FROM cfurl_cache_receiver_data WHERE entry_ID = %d;`, id, id))
	bump(f.t, filepath.Join(f.dir, "Cache.db-wal"))
}

var bumpClock = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func bump(t *testing.T, path string) {
	bumpClock = bumpClock.Add(time.Second)
	if err := os.Chtimes(path, bumpClock, bumpClock); err != nil {
		t.Fatal(err)
	}
}

func lyricsKey(id string) string {
	return "https://amp-api.music.apple.com/v1/catalog/ca/songs?filter%5Bequivalents%5D=" + id +
		"&art%5Burl%5D=c,f&extend%5Bsyllable-lyrics%5D=ttmlLocalizations&include=syllable-lyrics&omit%5Bresource%5D=autos"
}

// songBody is an amp-api response for one song, with ttml as its
// syllable-lyrics ("" for a song without lyrics). answer is the id the
// catalog answers with, asked the id in the request.
func songBody(asked, answer, name, artist string, dur time.Duration, ttml string) []byte {
	rel := []any{}
	if ttml != "" {
		rel = append(rel, map[string]any{
			"id": answer, "type": "syllable-lyrics",
			"attributes": map[string]any{"ttmlLocalizations": ttml, "playParams": map[string]any{"catalogId": answer, "kind": "lyric"}},
		})
	}
	b, _ := json.Marshal(map[string]any{
		"data": []any{map[string]any{
			"id": answer, "type": "songs",
			"attributes": map[string]any{
				"name": name, "artistName": artist, "albumName": "Some Album",
				"durationInMillis": dur.Milliseconds(), "hasLyrics": ttml != "",
				"playParams": map[string]any{"id": answer, "kind": "song"},
			},
			"relationships": map[string]any{"syllable-lyrics": map[string]any{"data": rel}},
		}},
		"meta": map[string]any{"filters": map[string]any{"equivalents": map[string]any{
			asked: []any{map[string]any{"id": answer, "type": "songs"}},
		}}},
	})
	return b
}

const ttmlLineTimed = `<tt xmlns="http://www.w3.org/ns/ttml" xmlns:itunes="http://music.apple.com/lyric-ttml-internal" itunes:timing="Line"><body><div><p begin="1.000" end="3.000">Invented words</p></div></body></tt>`

func TestAppleCacheFind(t *testing.T) {
	f := newFakeMusicCache(t)
	a := NewAppleCache(f.dir)
	q := Query{Title: "Paper Lanterns (2019 Remaster)", Artist: "The Nobodies feat. Someone", Duration: 201 * time.Second}

	if l := a.Find(q); l != nil {
		t.Fatalf("empty cache: %+v", l)
	}

	// Music fetches the lyrics a moment after the track starts.
	f.add(lyricsKey("111"), "2026-10-20 15:00:00", songBody("111", "222", "Paper Lanterns", "The Nobodies", 200*time.Second, ttmlWords), true)
	l := a.Find(q)
	if l == nil || !l.Synced || l.Source != SourceApple || l.Lines[0].Text != "Paper lanterns & rain" {
		t.Fatalf("metadata match: %+v", l)
	}
	if a.Find(q) != l {
		t.Error("an unchanged cache should give the same parsed lyrics")
	}

	for name, q := range map[string]Query{
		"asked id":    {StoreID: "111"},
		"answered id": {StoreID: "222", Title: "Something Else", Artist: "Other"},
	} {
		if a.Find(q) == nil {
			t.Errorf("%s: no match", name)
		}
	}
	for name, q := range map[string]Query{
		"too long":     {Title: "Paper Lanterns", Artist: "The Nobodies", Duration: 203 * time.Second},
		"other artist": {Title: "Paper Lanterns", Artist: "Somebody", Duration: 200 * time.Second},
		"other title":  {Title: "Paper Boats", Artist: "The Nobodies", Duration: 200 * time.Second},
		"other id":     {StoreID: "333"},
	} {
		if l := a.Find(q); l != nil {
			t.Errorf("%s: matched %+v", name, l)
		}
	}

	// A song Apple has no lyrics for, inline in the row.
	f.add(lyricsKey("444"), "2026-10-20 15:04:00", songBody("444", "444", "Quiet", "The Nobodies", 90*time.Second, ""), false)
	if l := a.Find(Query{StoreID: "444"}); l != nil {
		t.Errorf("no lyrics: %+v", l)
	}

	// A newer response for the same song wins; /songs/ID requests count.
	id := f.add("https://amp-api.music.apple.com/v1/catalog/ca/songs/222?include=syllable-lyrics", "2026-10-20 15:10:00",
		songBody("222", "222", "Paper Lanterns", "The Nobodies", 200*time.Second, ttmlLineTimed), false)
	if l := a.Find(Query{StoreID: "222"}); l == nil || l.Lines[0].Text != "Invented words" {
		t.Errorf("newer response: %+v", l)
	}
	f.remove(id)
	if l := a.Find(Query{StoreID: "222"}); l == nil || l.Lines[0].Text != "Paper lanterns & rain" {
		t.Errorf("after eviction: %+v", l)
	}
}

func TestAppleCacheIgnores(t *testing.T) {
	f := newFakeMusicCache(t)
	body := songBody("1", "1", "Song", "Artist", 100*time.Second, ttmlLineTimed)
	// Not a lyrics request, and a file name that leaves fsCachedData.
	f.add("https://amp-api.music.apple.com/v1/catalog/ca/songs?filter%5Bequivalents%5D=1", "2026-10-20 15:00:00", body, false)
	if err := os.WriteFile(filepath.Join(f.dir, "outside"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	f.id++
	f.exec(fmt.Sprintf(`INSERT INTO cfurl_cache_response(entry_ID, request_key, time_stamp) VALUES (%d, %s, '2026-10-20 15:00:00');
		INSERT INTO cfurl_cache_receiver_data(entry_ID, isDataOnFS, receiver_data) VALUES (%d, 1, '../outside');`, f.id, sqlQuote(lyricsKey("2")), f.id))
	a := NewAppleCache(f.dir)
	if l := a.Find(Query{Title: "Song", Artist: "Artist", Duration: 100 * time.Second}); l != nil {
		t.Errorf("found %+v", l)
	}
	if NewAppleCache(t.TempDir()).Find(Query{Title: "Song", Artist: "Artist"}) != nil {
		t.Error("no database: found lyrics")
	}
}

func newAppleService(t *testing.T) (*Service, *fakeLRCLIB, *fakeMusicCache) {
	t.Helper()
	m := newFakeMusicCache(t)
	f, c := newFake(t)
	s := NewService(Options{CacheDir: t.TempDir(), LRCLIB: c, Apple: NewAppleCache(m.dir), Now: newClock().Now})
	t.Cleanup(s.Close)
	return s, f, m
}

func TestAppleBeatsCachedLRCLIB(t *testing.T) {
	s, f, m := newAppleService(t)
	ctx := context.Background()
	f.get["Song|Artist|Album"] = &Record{ID: 9, SyncedLyrics: str("[00:01.00]from lrclib")}

	l, err := s.Get(ctx, q1)
	if err != nil || l.Source != SourceLRCLIB {
		t.Fatalf("before Music fetched: %+v %v", l, err)
	}
	if s.Apple(q1) != nil {
		t.Fatal("Apple: lyrics before Music has any")
	}

	// Music fetches its lyrics; the LRCLIB answer in the disk cache doesn't
	// hide them.
	id := m.add(lyricsKey("5"), "2026-10-20 15:00:00", songBody("5", "5", "Song", "Artist", 201*time.Second, ttmlLineTimed), true)
	n := len(f.requests())
	if l := s.Apple(q1); l == nil || l.Source != SourceApple {
		t.Fatalf("Apple: %+v", l)
	}
	l, err = s.Get(ctx, q1)
	if err != nil || l.Source != SourceApple || l.Lines[0].Text != "Invented words" {
		t.Fatalf("after Music fetched: %+v %v", l, err)
	}
	if len(f.requests()) != n {
		t.Error("Apple's lyrics should not wait for LRCLIB")
	}

	// Music evicts them; encore's cache keeps them.
	m.remove(id)
	if l, err := s.Get(ctx, q1); err != nil || l.Source != SourceApple {
		t.Fatalf("after eviction: %+v %v", l, err)
	}
}

func TestAppleDuringLRCLIBLookup(t *testing.T) {
	s, f, m := newAppleService(t)
	ctx := context.Background()
	f.get["Song|Artist|Album"] = &Record{ID: 9, SyncedLyrics: str("[00:01.00]from lrclib")}
	f.delay = 300 * time.Millisecond

	done := make(chan *Lyrics)
	go func() {
		l, _ := s.Get(ctx, q1)
		done <- l
	}()
	time.Sleep(50 * time.Millisecond)
	m.add(lyricsKey("5"), "2026-10-20 15:00:00", songBody("5", "5", "Song", "Artist", 200*time.Second, ttmlLineTimed), false)
	if s.Apple(q1) == nil {
		t.Fatal("Apple: no lyrics")
	}
	if l := <-done; l.Source != SourceApple {
		t.Errorf("the lookup in flight answered %q, want Apple's", l.Source)
	}
	m.remove(m.id)
	if l, _ := s.Get(ctx, q1); l.Source != SourceApple {
		t.Errorf("LRCLIB's late answer replaced Apple's in the cache: %q", l.Source)
	}
}

func TestBetterPrefersApple(t *testing.T) {
	apple := &Lyrics{Synced: true, Lines: []Line{{Text: "a"}}, Source: SourceApple}
	lrclib := &Lyrics{Synced: true, Lines: []Line{{Text: "b"}}, Source: SourceLRCLIB}
	plainApple := &Lyrics{Plain: "a", Source: SourceApple}
	switch {
	case !Better(apple, lrclib):
		t.Error("Apple synced should beat LRCLIB synced")
	case Better(lrclib, apple), Better(apple, apple):
		t.Error("nothing beats Apple of the same kind")
	case Better(plainApple, lrclib):
		t.Error("plain never beats synced")
	}
}

func TestApplePlainKeepsSynced(t *testing.T) {
	s, f, m := newAppleService(t)
	ctx := context.Background()
	f.get["Song|Artist|Album"] = &Record{ID: 9, SyncedLyrics: str("[00:01.00]from lrclib")}
	if l, _ := s.Get(ctx, q1); l == nil || l.Source != SourceLRCLIB {
		t.Fatalf("LRCLIB first: %+v", l)
	}
	untimed := `<tt xmlns="http://www.w3.org/ns/ttml" xmlns:itunes="http://music.apple.com/lyric-ttml-internal" itunes:timing="None"><body><div><p>Words</p></div></body></tt>`
	m.add(lyricsKey("5"), "2026-10-20 15:00:00", songBody("5", "5", "Song", "Artist", 200*time.Second, untimed), false)
	if l := s.Apple(q1); l != nil {
		t.Errorf("Apple's plain lyrics offered over LRCLIB's synced: %+v", l)
	}
	if l, _ := s.Get(ctx, q1); l == nil || l.Source != SourceLRCLIB || !l.Synced {
		t.Errorf("Get: %+v", l)
	}
}
