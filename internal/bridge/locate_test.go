package bridge

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLocateEnv(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "encore-bridge")
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("ENCORE_BRIDGE", bin)
	if p, err := Locate(t.TempDir()); err != nil || p != bin {
		t.Fatalf("Locate = %q, %v", p, err)
	}
	t.Setenv("ENCORE_BRIDGE", bin+".missing")
	if _, err := Locate(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing override: %v", err)
	}
}

func TestExtractStablePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bin")
	p, err := extract([]byte("binary v1"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(dir, "encore-bridge") {
		t.Fatalf("path = %s", p)
	}
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("stat = %v, %v", st, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "binary v1" {
		t.Fatalf("content = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, sumName)); err != nil {
		t.Fatalf("no sum file: %v", err)
	}
}

func TestExtractReusesIdenticalFile(t *testing.T) {
	dir := t.TempDir()
	data := []byte("binary v1")
	p, err := extract(data, dir)
	if err != nil {
		t.Fatal(err)
	}
	before := inode(t, p)
	if again, err := extract(data, dir); err != nil || again != p {
		t.Fatalf("again = %q, %v", again, err)
	}
	if inode(t, p) != before {
		t.Fatal("identical file was rewritten")
	}

	// Without the sum file the file is hashed, found equal and kept.
	os.Remove(filepath.Join(dir, sumName))
	if _, err := extract(data, dir); err != nil {
		t.Fatal(err)
	}
	if inode(t, p) != before {
		t.Fatal("identical file was rewritten after hashing")
	}
	if _, err := os.Stat(filepath.Join(dir, sumName)); err != nil {
		t.Fatalf("sum not recorded after hashing: %v", err)
	}
}

func TestExtractReplacesChangedFile(t *testing.T) {
	dir := t.TempDir()
	p, err := extract([]byte("binary v1"), dir)
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(p) // stands in for a bridge running from the old file
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	before := inode(t, p)

	if _, err := extract([]byte("binary v2, longer"), dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "binary v2, longer" {
		t.Fatalf("content = %q", b)
	}
	if inode(t, p) == before {
		t.Fatal("replaced in place, not renamed over")
	}
	if b, _ := io.ReadAll(old); string(b) != "binary v1" {
		t.Fatalf("old inode now reads %q", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", st.Mode())
	}
}

func TestExtractDetectsEditWithSameSize(t *testing.T) {
	dir := t.TempDir()
	p, err := extract([]byte("binary v1"), dir)
	if err != nil {
		t.Fatal(err)
	}
	// Same size, new mtime: the recorded sum no longer applies.
	os.WriteFile(p, []byte("binary v9"), 0o755)
	later := time.Now().Add(time.Hour)
	os.Chtimes(p, later, later)
	if _, err := extract([]byte("binary v1"), dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "binary v1" {
		t.Fatalf("not rewritten: %q", b)
	}
}

func TestExtractTrustsSumForSameSizeAndMtime(t *testing.T) {
	dir := t.TempDir()
	p, err := extract([]byte("binary v1"), dir)
	if err != nil {
		t.Fatal(err)
	}
	// A stale sum file that claims different content makes extract rewrite
	// without hashing, which shows the cache is what decides.
	st, _ := os.Stat(p)
	os.WriteFile(filepath.Join(dir, sumName), []byte(sumLine([32]byte{1}, st)), 0o644)
	before := inode(t, p)
	if _, err := extract([]byte("binary v1"), dir); err != nil {
		t.Fatal(err)
	}
	if inode(t, p) == before {
		t.Fatal("sum file was not consulted")
	}
}

func TestExtractRewritesNonExecutable(t *testing.T) {
	dir := t.TempDir()
	p, err := extract([]byte("binary v1"), dir)
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, 0o644)
	if _, err := extract([]byte("binary v1"), dir); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", st.Mode())
	}
}

func TestExtractPrunes(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("x"), 0o755)
		when := time.Now().Add(-age)
		os.Chtimes(p, when, when)
	}
	write("encore-bridge-deadbeef", 0)
	write("encore-bridge-394d982b", time.Hour)
	write(tmpPrefix+"123", time.Hour) // left by a crash
	write(tmpPrefix+"456", 0)         // another encore mid-write
	write("unrelated", time.Hour)

	if _, err := extract([]byte("binary v1"), dir); err != nil {
		t.Fatal(err)
	}
	var names []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{tmpPrefix + "456", sumName, "encore-bridge", "unrelated"}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Fatalf("dir = %v, want %v", names, want)
	}
}

func TestExtractFailureKeepsOldFiles(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	stale := filepath.Join(dir, "encore-bridge-deadbeef")
	os.WriteFile(stale, []byte("old"), 0o755)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if _, err := extract([]byte("binary v1"), dir); err == nil {
		t.Fatal("extract into a read-only dir succeeded")
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("old extraction removed after a failure: %v", err)
	}
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Sys().(*syscall.Stat_t).Ino
}

func TestEmbedDirCompiles(t *testing.T) {
	// The README keeps the embed pattern matching; the binary is optional.
	if _, err := embedded.ReadFile("embed/README.md"); err != nil {
		t.Fatal(err)
	}
	_ = Embedded()
}
