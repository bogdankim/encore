package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/keymap"
)

// RightPane is the optional pane right of the content: lyrics or Up Next.
// Render draws exactly w×h cells; Action and Mouse work like View's.
type RightPane interface {
	Render(w, h int, c *Ctx) string
	Action(a keymap.Action, n int, c *Ctx) (handled bool, cmd tea.Cmd)
	Mouse(ev MouseEvent, c *Ctx) tea.Cmd
}

// A RightPane may also implement:
//
//	KeyContext() keymap.Context        // keymap.Lyrics or keymap.Queue while focused
//	Update(msg tea.Msg, c *Ctx) tea.Cmd // every non-input message (track changes, fetch results)
//	FooterHints(c *Ctx) []FooterHint     // footer hints while focused, as for views

// rightKind says which right pane is open.
type rightKind uint8

const (
	rightNone rightKind = iota
	rightLyrics
	rightQueue
)

// paneCtors build the real panes; until they register, a placeholder shows.
var paneCtors struct {
	Lyrics func(s Services) RightPane
	Queue  func(s Services) RightPane
}

// placeholderPane stands in for a pane that isn't built yet.
type placeholderPane struct {
	title, note string
	ctx         keymap.Context
}

func (p placeholderPane) Render(w, h int, c *Ctx) string {
	lines := []string{titleLine(c, w, p.title, "")}
	for i := 1; i < h; i++ {
		if i == h/3 {
			lines = append(lines, Center(c.Theme.Dim.Render(p.note), w))
		} else {
			lines = append(lines, "")
		}
	}
	return Lines(lines, w, h)
}

func (placeholderPane) Action(keymap.Action, int, *Ctx) (bool, tea.Cmd) { return false, nil }
func (placeholderPane) Mouse(MouseEvent, *Ctx) tea.Cmd                  { return nil }
func (p placeholderPane) KeyContext() keymap.Context                    { return p.ctx }
