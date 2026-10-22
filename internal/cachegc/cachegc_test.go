package cachegc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, dir, name string, size int, age time.Duration) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-age)
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, dir string) map[string]bool {
	ents, _ := os.ReadDir(dir)
	m := map[string]bool{}
	for _, e := range ents {
		m[e.Name()] = true
	}
	return m
}

func TestPruneAge(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "old.png", 10, 100*24*time.Hour)
	write(t, dir, "new.png", 10, time.Hour)
	write(t, dir, "fresh.tmp", 10, time.Second)
	write(t, dir, "stale.tmp", 10, 2*time.Hour)
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	files, freed := Prune(dir, 90*24*time.Hour, 0)
	got := names(t, dir)
	if files != 2 || freed != 20 || got["old.png"] || got["stale.tmp"] || !got["new.png"] || !got["fresh.tmp"] || !got["sub"] {
		t.Fatalf("removed %d files %d bytes; left %v", files, freed, got)
	}
}

func TestPruneSize(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a", 100, 5*time.Hour)
	write(t, dir, "b", 100, 4*time.Hour)
	write(t, dir, "c", 100, 3*time.Hour)
	write(t, dir, "d", 100, 2*time.Hour)
	Prune(dir, 0, 250)
	got := names(t, dir)
	if got["a"] || got["b"] || !got["c"] || !got["d"] {
		t.Fatalf("left %v; the two oldest should go", got)
	}
}

func TestTouch(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a", 1, 48*time.Hour)
	write(t, dir, "b", 1, time.Hour)
	Touch(filepath.Join(dir, "a"))
	Touch(filepath.Join(dir, "b"))
	a, _ := os.Stat(filepath.Join(dir, "a"))
	b, _ := os.Stat(filepath.Join(dir, "b"))
	if time.Since(a.ModTime()) > time.Minute {
		t.Fatal("a not touched")
	}
	if time.Since(b.ModTime()) < 30*time.Minute {
		t.Fatal("b touched though recent")
	}
}

func TestRemoveGlob(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.png", 1, time.Second)
	write(t, dir, "b.png", 1, 3*time.Hour)
	write(t, dir, "c.jpg", 1, 3*time.Hour)
	os.Mkdir(filepath.Join(dir, "d.png"), 0o755)
	if n := RemoveGlob(filepath.Join(dir, "*.png"), time.Hour); n != 1 {
		t.Fatalf("removed %d, want only the old png", n)
	}
	if got := names(t, dir); !got["a.png"] || got["b.png"] || !got["c.jpg"] || !got["d.png"] {
		t.Fatalf("left %v", got)
	}
	if n := RemoveGlob(filepath.Join(dir, "*.png"), 0); n != 1 || names(t, dir)["a.png"] {
		t.Fatalf("removed %d with no age limit", n)
	}
}
