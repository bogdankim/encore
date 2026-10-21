// Command artspike checks album art rendering inside a Bubble Tea v2 program:
// a scrolling list next to a fixed art rectangle, an overlay that covers part
// of the art, and resizes.
//
//	go run ./cmd/artspike [--proto auto|iip|kitty|blocks|off]
//
// Keys: j/k or the wheel scroll, n next image, o overlay, q quit.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/art"
)

type testImage struct {
	name string
	png  []byte
}

type model struct {
	r       *art.Renderer
	images  []testImage
	cur     int
	w, h    int
	cursor  int
	off     int
	items   []string
	overlay bool
	cell    art.CellSize
}

func main() {
	proto := flag.String("proto", "auto", "art protocol: auto, iip, kitty, sixel, blocks or off")
	flag.Parse()

	ch, restore, err := art.Setup(*proto, os.Getenv, nil)
	if err != nil {
		restore()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	r := art.NewRenderer(art.Options{Protocol: ch.Protocol, Passthrough: ch.Passthrough, Tmux: ch.Tmux})
	m := &model{r: r, images: testImages(), cell: art.DefaultCellSize}
	for i := range 200 {
		m.items = append(m.items, fmt.Sprintf("%03d  %s", i+1, titles[i%len(titles)]))
	}
	prog := tea.NewProgram(m, tea.WithOutput(art.NewOutput(os.Stdout, r)))
	_, err = prog.Run()
	restore()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (m *model) Init() tea.Cmd { return tea.Raw(art.CellSizeQuery) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if c, ok := art.CellSizeFromMsg(msg); ok {
		m.cell = c
		m.r.SetCellSize(c)
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.r.Resize(msg.Width, msg.Height)
		m.clampScroll()
		return m, tea.Raw(art.CellSizeQuery)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			m.move(1)
		case "k", "up":
			m.move(-1)
		case "ctrl+d":
			m.move(m.listRows() / 2)
		case "ctrl+u":
			m.move(-m.listRows() / 2)
		case "n":
			m.cur = (m.cur + 1) % len(m.images)
		case "o":
			m.overlay = !m.overlay
		}
	case tea.MouseWheelMsg:
		switch msg.Mouse().Button {
		case tea.MouseWheelDown:
			m.off += 3
		case tea.MouseWheelUp:
			m.off -= 3
		}
		m.clampScroll()
	}
	return m, nil
}

func (m *model) move(d int) {
	m.cursor = max(0, min(len(m.items)-1, m.cursor+d))
	if m.cursor < m.off {
		m.off = m.cursor
	}
	if rows := m.listRows(); m.cursor >= m.off+rows {
		m.off = m.cursor - rows + 1
	}
	m.clampScroll()
}

func (m *model) clampScroll() {
	m.off = max(0, min(m.off, len(m.items)-m.listRows()))
}

func (m *model) listRows() int { return max(1, m.h-3) }

// artBox returns the art rectangle: square art in cells twice as tall as wide.
func (m *model) artBox() (x, y, cols, rows int) {
	cols = max(8, min(40, m.w-36))
	rows = cols / 2
	if rows > m.h-4 {
		rows = max(2, m.h-4)
		cols = rows * 2
	}
	return m.w - cols - 2, 2, cols, rows
}

var (
	dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8B8B93"))
	accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#FA586A"))
	sel    = lipgloss.NewStyle().Background(lipgloss.Color("#26262B"))
	faint  = lipgloss.Color("#4A4A52")
)

func (m *model) View() tea.View {
	if m.w == 0 || m.h == 0 {
		return tea.NewView("")
	}
	ax, ay, ac, ar := m.artBox()
	img := m.images[m.cur]
	key := img.name
	artLines := strings.Split(m.r.Block(key, img.png, ac, ar), "\n")

	lines := make([]string, m.h)
	lines[0] = fit(" "+lipgloss.NewStyle().Bold(true).Render("artspike")+dim.Render(fmt.Sprintf("  %s · cell %dx%d · %s (%d/%d)",
		m.r.Protocol(), m.cell.W, m.cell.H, img.name, m.cur+1, len(m.images))), m.w)
	listW := max(10, ax-3)
	for i := range m.listRows() {
		y := i + 1
		idx := m.off + i
		row := ""
		if idx < len(m.items) {
			text := "  " + m.items[idx]
			if idx == m.cursor {
				row = sel.Render(accent.Render("▎") + fit(" "+m.items[idx], listW-1))
			} else {
				row = fit(text, listW)
			}
		}
		line := fit(row, listW) + "   "
		if y >= ay && y < ay+ar && y-ay < len(artLines) {
			line += artLines[y-ay]
		}
		lines[y] = fit(line, m.w)
	}
	lines[m.h-1] = fit(dim.Render(" j/k scroll · wheel · n next image · o overlay · q quit"), m.w)
	m.r.Place(key, ax, ay, ac, ar)

	if m.overlay {
		bw, bh := min(34, m.w-4), 7
		bx, by := max(0, ax-bw/2), min(m.h-bh-1, ay+ar/2)
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(faint).
			Width(bw).Height(bh).Padding(1, 2).
			Render("Overlay\n" + dim.Render("covers part of the art; o closes it"))
		for i, b := range strings.Split(box, "\n") {
			if y := by + i; y >= 0 && y < m.h {
				lines[y] = paste(lines[y], b, bx, m.w)
			}
		}
		m.r.Occlude(bx, by, lipgloss.Width(box), lipgloss.Height(box))
	}

	s := strings.Join(lines, "\n")
	m.r.Frame(s)
	v := tea.NewView(s)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// fit pads or truncates s to exactly w cells.
func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "")
	if d := w - ansi.StringWidth(s); d > 0 {
		s += strings.Repeat(" ", d)
	}
	return s
}

// paste draws over on top of line at column x.
func paste(line, over string, x, w int) string {
	ow := ansi.StringWidth(over)
	return fit(ansi.Truncate(line, x, "")+"\x1b[m", x) + over + "\x1b[m" + ansi.TruncateLeft(fit(line, w), x+ow, "")
}

var titles = []string{
	"Daylight Drive", "Glass Harbour", "Northern Lines", "Paper Moons", "Slow Static",
	"Copper Coast", "Afterglow (Live)", "Quiet Engine", "Saltwater Hymn", "Low Orbit",
}

// testImages are generated stand-ins for album art, square PNGs like the
// bridge produces.
func testImages() []testImage {
	const n = 512
	gen := []struct {
		name string
		f    func(x, y float64) color.NRGBA
	}{
		{"sunset", func(x, y float64) color.NRGBA {
			sun := math.Hypot(x-0.5, y-0.62)
			c := lerp(color.NRGBA{250, 88, 106, 255}, color.NRGBA{40, 30, 90, 255}, y)
			if sun < 0.18 {
				c = lerp(color.NRGBA{255, 220, 120, 255}, c, sun/0.18*0.4)
			}
			if y > 0.7 {
				c = lerp(c, color.NRGBA{10, 20, 40, 255}, (y-0.7)/0.3)
			}
			return c
		}},
		{"rings", func(x, y float64) color.NRGBA {
			d := math.Hypot(x-0.5, y-0.5)
			if int(d*24)%2 == 0 {
				return color.NRGBA{230, 230, 220, 255}
			}
			return color.NRGBA{30, 60, 120, 255}
		}},
		{"checker", func(x, y float64) color.NRGBA {
			if (int(x*8)+int(y*8))%2 == 0 {
				return color.NRGBA{240, 240, 240, 255}
			}
			return color.NRGBA{20, 20, 24, 255}
		}},
		{"gradient", func(x, y float64) color.NRGBA {
			return color.NRGBA{uint8(255 * x), uint8(255 * y), uint8(255 * (1 - x)), 255}
		}},
	}
	out := make([]testImage, len(gen))
	for i, g := range gen {
		m := image.NewNRGBA(image.Rect(0, 0, n, n))
		for y := range n {
			for x := range n {
				m.SetNRGBA(x, y, g.f((float64(x)+0.5)/n, (float64(y)+0.5)/n))
			}
		}
		var b bytes.Buffer
		_ = png.Encode(&b, m)
		out[i] = testImage{g.name, b.Bytes()}
	}
	return out
}

func lerp(a, b color.NRGBA, t float64) color.NRGBA {
	t = max(0, min(1, t))
	mix := func(p, q uint8) uint8 { return uint8(float64(p)*(1-t) + float64(q)*t) }
	return color.NRGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 255}
}
