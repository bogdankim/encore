package bridge

import (
	"os"
	"path/filepath"
	"sync"
)

// maxLogSize is when the bridge log is rotated. One old file (<path>.1) is kept.
const maxLogSize = 5 << 20

// rotatingFile is an append-only log file that rotates itself at a size limit.
// It is safe for concurrent use. Write errors are swallowed: logging must never
// take the bridge down.
type rotatingFile struct {
	path string
	max  int64

	mu   sync.Mutex
	f    *os.File
	size int64
}

func openRotatingFile(path string, max int64) (*rotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	r := &rotatingFile{path: path, max: max}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *rotatingFile) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil && r.size > 0 && r.size+int64(len(b)) > r.max {
		r.f.Close()
		r.f = nil
		os.Rename(r.path, r.path+".1")
		if err := r.open(); err != nil {
			r.f = nil
		}
	}
	if r.f == nil {
		return len(b), nil
	}
	n, _ := r.f.Write(b)
	r.size += int64(n)
	return len(b), nil
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
