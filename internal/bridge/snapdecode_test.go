package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDecodeSnapshotMatchesJSON(t *testing.T) {
	f := NewFakeWithClock(1, func() time.Time { return FakeEpoch })
	defer f.Close()
	want, err := f.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(want)
	got, err := decodeSnapshot(bytes.NewReader(data), len(want.Tracks), len(want.Playlists))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("streamed decode differs from json.Unmarshal")
	}
	// unknown keys are skipped, nulls and missing arrays are empty
	got, err = decodeSnapshot(strings.NewReader(`{"extra":{"a":[1,2]},"generatedAt":5,"tracks":null}`), 0, 0)
	if err != nil || got.GeneratedAt != 5 || len(got.Tracks) != 0 || len(got.Playlists) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []string{``, `[]`, `{"tracks":[{`, `{"tracks":{}}`, `{"tracks":[1]}`} {
		if _, err := decodeSnapshot(strings.NewReader(bad), 0, 0); err == nil {
			t.Errorf("%q decoded", bad)
		}
	}
}

// Decoding the document in one Decode call buffers all of it before it
// parses; the streamed decode must allocate clearly less for a big library.
func TestDecodeSnapshotAllocatesLess(t *testing.T) {
	const n = 20000
	snap := &Snapshot{GeneratedAt: 1}
	for i := range n {
		snap.Tracks = append(snap.Tracks, SnapshotTrack{
			PID: fmt.Sprintf("%016X", i), Title: fmt.Sprintf("Song number %d", i), Artist: "Some Artist",
			Album: fmt.Sprintf("Album %d", i/12), AlbumPID: fmt.Sprintf("%016X", i/12), Duration: 200000, Year: 2014,
		})
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	alloc := func(f func()) uint64 {
		runtime.GC()
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		f()
		runtime.ReadMemStats(&b)
		return b.TotalAlloc - a.TotalAlloc
	}
	var whole, streamed *Snapshot
	oneShot := alloc(func() {
		whole = new(Snapshot)
		if err := json.NewDecoder(bytes.NewReader(data)).Decode(whole); err != nil {
			t.Fatal(err)
		}
	})
	stream := alloc(func() {
		var err error
		if streamed, err = decodeSnapshot(bytes.NewReader(data), n, 0); err != nil {
			t.Fatal(err)
		}
	})
	if !reflect.DeepEqual(whole, streamed) {
		t.Fatal("the two decodes differ")
	}
	t.Logf("json.Decoder.Decode %d KB, decodeSnapshot %d KB", oneShot>>10, stream>>10)
	if stream*10 > oneShot*8 {
		t.Errorf("streamed decode allocated %d KB, one-shot %d KB: want at least 20%% less", stream>>10, oneShot>>10)
	}
}
