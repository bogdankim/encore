// Command testapp is a throwaway Bubble Tea v2 program used to check that
// tools/snap delivers keys, mouse events and iTerm2 inline images correctly.
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/sys/unix"
)

const (
	imgCol, imgRow = 70, 3 // zero-based cell where the image goes
	imgW, imgH     = 20, 10
)

type imageMsg struct{}

type model struct {
	w, h    int
	key     string
	mouse   string
	history []string
	events  int
	pixels  string
}

func (m model) Init() tea.Cmd { return nil }

func (m *model) log(s string) {
	m.events++
	m.history = append(m.history, fmt.Sprintf("%03d %s", m.events, s))
	if len(m.history) > 24 {
		m.history = m.history[len(m.history)-24:]
	}
}

func mouseLine(kind string, e tea.Mouse) string {
	return fmt.Sprintf("%s x=%d y=%d button=%s mod=%d", kind, e.X, e.Y, e.Button, e.Mod)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		if ws, err := unix.IoctlGetWinsize(0, unix.TIOCGWINSZ); err == nil {
			m.pixels = fmt.Sprintf("%dx%d px", ws.Xpixel, ws.Ypixel)
		}
		return m, tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return imageMsg{} })
	case imageMsg:
		return m, tea.Raw(imageSeq())
	case tea.KeyPressMsg:
		m.key = msg.String()
		m.log("key " + m.key)
		switch m.key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "i":
			return m, tea.Raw(imageSeq())
		}
	case tea.MouseClickMsg:
		m.mouse = mouseLine("click", msg.Mouse())
		m.log(m.mouse)
	case tea.MouseReleaseMsg:
		m.mouse = mouseLine("release", msg.Mouse())
		m.log(m.mouse)
	case tea.MouseWheelMsg:
		m.mouse = mouseLine("wheel", msg.Mouse())
		m.log(m.mouse)
	case tea.MouseMotionMsg:
		m.mouse = mouseLine("motion", msg.Mouse())
		if msg.Mouse().Button != tea.MouseNone {
			m.log(m.mouse) // only log drags; hover just updates the line
		}
	}
	return m, nil
}

func (m model) View() tea.View {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#a6e22e")).Render("snap testapp")
	var b strings.Builder
	fmt.Fprintf(&b, "%s  size=%dx%d  winsize=%s\n", title, m.w, m.h, m.pixels)
	fmt.Fprintf(&b, "key:   %s\n", m.key)
	fmt.Fprintf(&b, "mouse: %s\n", m.mouse)
	b.WriteString("ansi:  ")
	for i := 0; i < 16; i++ {
		b.WriteString(lipgloss.NewStyle().Background(lipgloss.Color(fmt.Sprint(i))).Render(fmt.Sprintf("%2d", i)))
	}
	b.WriteString("\n")
	b.WriteString("true:  ")
	for i := 0; i < 32; i++ {
		c := fmt.Sprintf("#%02x%02x%02x", 255-i*8, i*8, 128)
		b.WriteString(lipgloss.NewStyle().Background(lipgloss.Color(c)).Render(" "))
	}
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("red") + " " +
		lipgloss.NewStyle().Bold(true).Render("bold") + " " +
		lipgloss.NewStyle().Italic(true).Render("italic") + " " +
		lipgloss.NewStyle().Underline(true).Render("underline") + " " +
		lipgloss.NewStyle().Faint(true).Render("faint") + "  ▀▄█░▒▓ ●○ ♪ ✓ … 日本語\n")
	// Nerd Font Material Design icons, each followed by "|" to show it takes one cell.
	icons := lipgloss.NewStyle().Foreground(lipgloss.Color("#f92672")).Render("\U000F040A|\U000F03E4|\U000F04AD|\U000F02D1|")
	b.WriteString("icons: " + icons + "  play pause next heart\n")
	b.WriteString("cells: a|b|c|d|\n")
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("4")).Padding(0, 1).
		Render(strings.Join(m.history, "\n") + strings.Repeat("\n", max(0, 23-len(m.history))))
	b.WriteString(box)
	b.WriteString("\n" + lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Render("┌ thin box ┐"))
	return tea.View{Content: b.String(), AltScreen: true, MouseMode: tea.MouseModeAllMotion}
}

// imageSeq draws a small generated PNG with the iTerm2 inline image protocol
// at a fixed cell, saving and restoring the cursor around it.
func imageSeq() string {
	img := image.NewRGBA(image.Rect(0, 0, 80, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 80; x++ {
			c := color.RGBA{uint8(x * 3), uint8(y * 3), 200, 255}
			if (x/10+y/10)%2 == 0 {
				c = color.RGBA{249, 38, 114, 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	data := base64.StdEncoding.EncodeToString(buf.Bytes())
	return fmt.Sprintf("\x1b7\x1b[%d;%dH\x1b]1337;File=inline=1;size=%d;width=%d;height=%d;preserveAspectRatio=0:%s\x07\x1b8",
		imgRow+1, imgCol+1, buf.Len(), imgW, imgH, data)
}

func main() {
	if _, err := tea.NewProgram(model{}).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
