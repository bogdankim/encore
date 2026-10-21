package bridge

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The Makefile copies the Swift build to embed/encore-bridge. embed/README.md is
// committed so the pattern always matches and plain `go build` works without it.
//
//go:embed embed/*
var embedded embed.FS

const embeddedName = "embed/encore-bridge"

// ErrNoBinary means no bridge binary could be found: ENCORE_BRIDGE is unset, the
// Go binary was built without an embedded bridge, and there is no dev build.
var ErrNoBinary = errors.New("bridge: encore-bridge binary not found (run `make bridge` or set ENCORE_BRIDGE)")

// Embedded reports whether this build carries the bridge binary.
func Embedded() bool {
	_, err := fs.Stat(embedded, embeddedName)
	return err == nil
}

// Locate finds the bridge binary, in order:
//
//  1. $ENCORE_BRIDGE;
//  2. the embedded binary, extracted to <cacheDir>/bin/encore-bridge (see
//     extract);
//  3. a SwiftPM release build in <repo>/bridge/.build, where <repo> is found
//     from the executable's directory or the working directory (dev builds and
//     `go run`).
//
// An empty cacheDir means ~/Library/Caches/encore.
func Locate(cacheDir string) (string, error) {
	if p := os.Getenv("ENCORE_BRIDGE"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("bridge: ENCORE_BRIDGE: %w", err)
		}
		return p, nil
	}
	if data, err := embedded.ReadFile(embeddedName); err == nil && len(data) > 0 {
		if cacheDir == "" {
			cacheDir = defaultCacheDir()
		}
		return extract(data, filepath.Join(cacheDir, "bin"))
	}
	if p := devBuild(); p != "" {
		return p, nil
	}
	return "", ErrNoBinary
}

const (
	// extractName is the extracted bridge's file name. It never changes: TCC
	// records a helper without a bundle by its path, so a new path per build
	// meant a new prompt per build.
	extractName = "encore-bridge"
	// sumName records the sha256, size and mtime of the extracted file, so a
	// launch that finds the same size and mtime skips hashing it.
	sumName = ".encore-bridge.sum"
	// tmpPrefix names files being written; one left by a crash is removed
	// once it is older than tmpMaxAge.
	tmpPrefix = ".encore-bridge-"
	tmpMaxAge = time.Minute
)

// extract makes dir/encore-bridge hold data and returns its path. The file is
// replaced only when its content differs: written to a temp file in dir,
// synced, made executable and renamed over the old one. The rename gives it a
// new inode, so a bridge still running from the old file (another encore, or
// one started before an update) keeps running, and the kernel never sees a
// signed binary change in place. Two encore versions that run in turn swap
// the file back and forth. Names from older encore builds
// (encore-bridge-<sha8>) are removed.
func extract(data []byte, dir string) (string, error) {
	want := sha256.Sum256(data)
	path := filepath.Join(dir, extractName)
	if !current(dir, path, want) {
		if err := replace(dir, path, data); err != nil {
			return "", fmt.Errorf("bridge: extract: %w", err)
		}
		if st, err := os.Stat(path); err == nil {
			writeSum(dir, want, st)
		}
	}
	pruneExtracted(dir)
	return path, nil
}

// current reports whether path is an executable file whose content hashes to
// want. When the size and mtime match the sum file it trusts the recorded
// hash; otherwise it hashes the file and records the result.
func current(dir, path string, want [sha256.Size]byte) bool {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o100 == 0 {
		return false
	}
	if sum, ok := readSum(dir, st); ok {
		return sum == want
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	var got [sha256.Size]byte
	h.Sum(got[:0])
	writeSum(dir, got, st)
	return got == want
}

func replace(dir, path string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, tmpPrefix+"*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o755)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// sumLine is the sum file's content: hex sha256, size, mtime in nanoseconds.
func sumLine(sum [sha256.Size]byte, st os.FileInfo) string {
	return fmt.Sprintf("%s %d %d\n", hex.EncodeToString(sum[:]), st.Size(), st.ModTime().UnixNano())
}

// readSum returns the recorded hash if the sum file describes st.
func readSum(dir string, st os.FileInfo) ([sha256.Size]byte, bool) {
	var sum [sha256.Size]byte
	b, err := os.ReadFile(filepath.Join(dir, sumName))
	if err != nil {
		return sum, false
	}
	f := strings.Fields(string(b))
	if len(f) != 3 || f[1] != strconv.FormatInt(st.Size(), 10) || f[2] != strconv.FormatInt(st.ModTime().UnixNano(), 10) {
		return sum, false
	}
	if n, err := hex.Decode(sum[:], []byte(f[0])); err != nil || n != len(sum) {
		return sum, false
	}
	return sum, true
}

// writeSum records sum for st. It is only a cache: on failure the next launch
// hashes the file again.
func writeSum(dir string, sum [sha256.Size]byte, st os.FileInfo) {
	tmp, err := os.CreateTemp(dir, tmpPrefix+"*")
	if err != nil {
		return
	}
	_, err = tmp.WriteString(sumLine(sum, st))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, sumName))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
}

// pruneExtracted removes hash-named extractions from older encore builds and
// temp files abandoned by a crash. A temp file younger than tmpMaxAge may
// belong to another encore writing right now, so it stays.
func pruneExtracted(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		switch {
		case strings.HasPrefix(n, extractName+"-"):
			os.Remove(filepath.Join(dir, n))
		case strings.HasPrefix(n, tmpPrefix):
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > tmpMaxAge {
				os.Remove(filepath.Join(dir, n))
			}
		}
	}
}

// devBuild looks for a SwiftPM release build next to the source tree.
func devBuild() string {
	var roots []string
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			dir := filepath.Dir(exe)
			roots = append(roots, filepath.Dir(dir), dir) // bin/encore → repo, or repo/encore
		}
	}
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, "bridge", "Package.swift")); err == nil {
				roots = append(roots, dir)
				break
			}
			if parent := filepath.Dir(dir); parent == dir {
				break
			}
		}
	}
	for _, root := range roots {
		for _, rel := range []string{
			"bridge/.build/release/encore-bridge",
			"bridge/.build/arm64-apple-macosx/release/encore-bridge",
		} {
			p := filepath.Join(root, rel)
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				return p
			}
		}
	}
	return ""
}

func defaultCacheDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "encore")
	}
	return filepath.Join(os.TempDir(), "encore")
}

func defaultLogPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Library", "Logs", "encore", "bridge.log")
	}
	return filepath.Join(os.TempDir(), "encore", "bridge.log")
}
