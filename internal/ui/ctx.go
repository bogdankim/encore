package ui

import "github.com/bogdankim/encore/internal/library"

// IsFavorite reports whether t is favorited, counting changes made in this
// session that the library snapshot doesn't have yet.
func (c *Ctx) IsFavorite(t *library.Track) bool {
	if t == nil {
		return false
	}
	if v, ok := c.fav[t.PID]; ok {
		return v
	}
	return t.Favorited
}

// IsFavoritePID is IsFavorite for a pid and the value Music reported.
func (c *Ctx) IsFavoritePID(pid string, reported bool) bool {
	if v, ok := c.fav[pid]; ok {
		return v
	}
	return reported
}
