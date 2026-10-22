package art

import (
	"fmt"
	"strings"
	"testing"
)

// A tile whose decoded image was evicted still draws and still counts as
// ready while its encoded payload is kept.
func TestPayloadOutlivesDecodedImage(t *testing.T) {
	r := NewRenderer(Options{Protocol: ProtoIIP, Cell: CellSize{W: 20, H: 42}})
	data := pngBytes(t, testArt(64, 64))
	if r.Ready("tile:a", 10, 5) {
		t.Fatal("ready before it was prepared")
	}
	if err := r.Prepare("tile:a", data, 10, 5); err != nil {
		t.Fatal(err)
	}
	if !r.Ready("tile:a", 10, 5) || r.Ready("tile:a", 11, 5) {
		t.Fatal("ready must be per size")
	}
	// push the decoded image out; the payload stays
	for i := range imagesMax + 1 {
		if err := r.Prepare(fmt.Sprintf("tile:other%d", i), data, 10, 5); err != nil {
			t.Fatal(err)
		}
	}
	if r.Has("tile:a") {
		t.Fatal("the decoded image should have been evicted")
	}
	if !r.Ready("tile:a", 10, 5) {
		t.Fatal("payload lost with the decoded image")
	}

	r.Place("tile:a", 4, 2, 10, 5)
	r.Frame(strings.Repeat("\n", 23))
	var dmg damage
	dmg.all = true
	if out := r.ops(&dmg); !strings.Contains(out, "1337;File=") {
		t.Fatalf("the tile was not drawn from its payload: %.60q", out)
	}

	// a cut piece (an overlay over part of the tile) needs the image: it
	// waits instead of being marked drawn
	r.Occlude(8, 2, 6, 5)
	r.Place("tile:a", 4, 2, 10, 5)
	r.Frame(strings.Repeat("\n", 23))
	if out := r.ops(&dmg); strings.Contains(out, "1337;File=") {
		t.Fatalf("a cut piece was drawn without the image: %.60q", out)
	}
	for p := range r.shown {
		if p.key == "tile:a" {
			t.Fatalf("%v counts as drawn", p)
		}
	}
}

// Sixel and kitty need the decoded image, so Ready means Has there.
func TestReadyWithoutPayloads(t *testing.T) {
	r := NewRenderer(Options{Protocol: ProtoBlocks})
	if r.Ready("k", 6, 3) {
		t.Fatal("ready before it was prepared")
	}
	r.Prepare("k", pngBytes(t, testArt(32, 32)), 6, 3)
	if !r.Ready("k", 6, 3) {
		t.Fatal("not ready after Prepare")
	}
}

func TestPayloadBudget(t *testing.T) {
	c := newPayloadLRU()
	chunk := make([]byte, 1<<20)
	for i := range 20 {
		c.put(pieceKey{box: boxKey{key: fmt.Sprint(i)}}, chunk)
	}
	if c.used > payloadsBudget || c.len() < payloadsBudget/len(chunk)-1 {
		t.Fatalf("payload cache holds %d bytes in %d entries, budget %d", c.used, c.len(), payloadsBudget)
	}
}
