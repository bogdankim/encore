package art

import (
	"image"
	"strconv"
	"strings"
)

// Blocks renders img as rows lines of cols half-block cells: '▀' with the
// top pixel as foreground and the bottom pixel as background. A cell is about
// twice as tall as it is wide, so the grid covers cols×(2·rows) square-ish
// pixels; square art wants rows = cols/2. The image is centre-cropped to that
// aspect rather than stretched. Transparent pixels show the terminal
// background. Every line is exactly cols cells wide and ends with an SGR
// reset, so the result is a valid lipgloss block.
func Blocks(img image.Image, cols, rows int) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}
	if img == nil {
		return blank(cols, rows)
	}
	h := rows * 2
	m := resample(img, coverRect(img.Bounds(), cols, h), cols, h)

	var b strings.Builder
	b.Grow(rows * cols * 40)
	for y := range rows {
		if y > 0 {
			b.WriteByte('\n')
		}
		var pen cellPen
		for x := range cols {
			tr, tg, tb, ta := m.at(x, 2*y)
			br, bg, bb, ba := m.at(x, 2*y+1)
			top, bot := ta >= 128, ba >= 128
			switch {
			case top && bot:
				pen.set(&b, rgb{tr, tg, tb}, rgb{br, bg, bb}, true, true)
				b.WriteString("▀")
			case top:
				pen.set(&b, rgb{tr, tg, tb}, rgb{}, true, false)
				b.WriteString("▀")
			case bot:
				pen.set(&b, rgb{br, bg, bb}, rgb{}, true, false)
				b.WriteString("▄")
			default:
				pen.set(&b, rgb{}, rgb{}, false, false)
				b.WriteByte(' ')
			}
		}
		if pen.dirty {
			b.WriteString("\x1b[m")
		}
	}
	return b.String()
}

type rgb struct{ r, g, b uint8 }

// cellPen tracks the SGR state within one line so repeated colours are not
// re-sent.
type cellPen struct {
	fg, bg       rgb
	hasFg, hasBg bool
	dirty        bool
}

func (p *cellPen) set(b *strings.Builder, fg, bg rgb, hasFg, hasBg bool) {
	if (p.hasFg && !hasFg) || (p.hasBg && !hasBg) {
		b.WriteString("\x1b[m")
		p.hasFg, p.hasBg, p.dirty = false, false, false
	}
	needFg := hasFg && (!p.hasFg || p.fg != fg)
	needBg := hasBg && (!p.hasBg || p.bg != bg)
	if !needFg && !needBg {
		return
	}
	b.WriteString("\x1b[")
	if needFg {
		writeColor(b, 38, fg)
	}
	if needBg {
		if needFg {
			b.WriteByte(';')
		}
		writeColor(b, 48, bg)
	}
	b.WriteByte('m')
	p.fg, p.bg, p.hasFg, p.hasBg, p.dirty = fg, bg, hasFg, hasBg, true
}

func writeColor(b *strings.Builder, kind int, c rgb) {
	var buf [24]byte
	s := strconv.AppendInt(buf[:0], int64(kind), 10)
	s = append(s, ";2;"...)
	s = strconv.AppendUint(s, uint64(c.r), 10)
	s = append(s, ';')
	s = strconv.AppendUint(s, uint64(c.g), 10)
	s = append(s, ';')
	s = strconv.AppendUint(s, uint64(c.b), 10)
	b.Write(s)
}

// blank returns rows lines of cols spaces.
func blank(cols, rows int) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}
	line := strings.Repeat(" ", cols)
	return strings.Repeat(line+"\n", rows-1) + line
}
