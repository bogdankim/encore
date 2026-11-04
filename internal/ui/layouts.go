package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bogdankim/encore/internal/config"
)

// Pages with an album grid, as saved in the layouts file. Recently Added,
// Albums and Artists use their section keys. Every artist page opened on its
// own, library or Apple Music, shares one choice.
const (
	layoutArtist    = "artist"
	layoutEssential = ".essential" // suffix: an artist page's Essential Albums shelf
)

const (
	layoutGridName = "grid"
	layoutListName = "list"

	layoutsSaveDelay = 500 * time.Millisecond
)

// DefaultLayoutsPath is ~/Library/Application Support/encore/layouts.json.
func DefaultLayoutsPath() string { return filepath.Join(config.StateDir(), "layouts.json") }

// Layouts remembers whether each page shows its albums as a grid or a list. A
// page with no saved choice is a grid. Set only touches memory and schedules a
// save, so it's cheap to call from a key handler; call Flush before exiting.
// A nil Layouts remembers nothing, and an empty path keeps choices in memory.
type Layouts struct {
	path string

	mu    sync.Mutex
	pages map[string]string // page -> "grid" or "list"
	timer *time.Timer
	dirty bool

	saveMu sync.Mutex // serialises file writes
}

// LoadLayouts reads path; a missing or unreadable file starts empty.
func LoadLayouts(path string) *Layouts {
	l := &Layouts{path: path, pages: map[string]string{}}
	if path == "" {
		return l
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &l.pages)
		if l.pages == nil {
			l.pages = map[string]string{}
		}
	}
	return l
}

// List reports whether page shows the list layout.
func (l *Layouts) List(page string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pages[page] == layoutListName
}

// Set records the choice for page.
func (l *Layouts) Set(page string, list bool) {
	if l == nil || page == "" {
		return
	}
	name := layoutGridName
	if list {
		name = layoutListName
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pages[page] == name {
		return
	}
	l.pages[page] = name
	if l.path == "" {
		return
	}
	l.dirty = true
	if l.timer == nil {
		l.timer = time.AfterFunc(layoutsSaveDelay, func() { l.Flush() })
	} else {
		l.timer.Reset(layoutsSaveDelay)
	}
}

// Flush writes pending changes now.
func (l *Layouts) Flush() error {
	if l == nil {
		return nil
	}
	l.saveMu.Lock()
	defer l.saveMu.Unlock()
	l.mu.Lock()
	if !l.dirty {
		l.mu.Unlock()
		return nil
	}
	if l.timer != nil {
		l.timer.Stop()
	}
	data, err := json.MarshalIndent(l.pages, "", "  ")
	l.dirty = false
	l.mu.Unlock()
	if err == nil {
		if err = config.EnsureDir(filepath.Dir(l.path)); err == nil {
			err = config.WriteFileAtomic(l.path, data, 0o644)
		}
	}
	if err != nil {
		l.mu.Lock()
		l.dirty = true
		l.mu.Unlock()
	}
	return err
}

// layoutKeeper is a view with album grids whose layout is remembered.
type layoutKeeper interface {
	keepLayouts(l *Layouts)
}

func (v *AlbumsView) keepLayouts(l *Layouts)        { v.grid.keepLayout(l, v.key) }
func (v *ArtistsView) keepLayouts(l *Layouts)       { v.page.keepLayouts(l, KeyArtists) }
func (v *ArtistView) keepLayouts(l *Layouts)        { v.page.keepLayouts(l, layoutArtist) }
func (v *CatalogArtistView) keepLayouts(l *Layouts) { v.page.keepLayouts(l, layoutArtist) }

func (p *artistPage) keepLayouts(l *Layouts, page string) {
	p.grid.keepLayout(l, page)
	p.ess.keepLayout(l, page+layoutEssential)
}

// keepLayout shows the choice saved for page, and saves the grid's changes
// under it from now on.
func (g *AlbumGrid) keepLayout(l *Layouts, page string) {
	g.layouts, g.layoutPage = l, page
	if on := l.List(page); on != g.list {
		g.list = on
		g.ensureVisible()
	}
}
