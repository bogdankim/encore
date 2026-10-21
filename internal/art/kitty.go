package art

import (
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi/kitty"
)

// Kitty graphics with unicode placeholders
// (https://sw.kovidgoyal.net/kitty/graphics-protocol/#unicode-placeholders):
// the image is uploaded once out of band, a virtual placement of cols×rows
// cells is created for it, and the layout contains ordinary text: U+10EEEE per
// cell with row and column diacritics, the image id in the foreground colour.
// The terminal draws the image wherever that text is, so the diffing renderer
// can move, overwrite and redraw it like any other text.

// maxKittyCells is the largest row/column index the diacritics table encodes.
const maxKittyCells = 297

// KittyTransmit uploads PNG data as image id: a=t, f=100, q=2 (no replies),
// base64 split into chunks of at most 4096 bytes with m=1 on all but the last.
func KittyTransmit(id uint32, png []byte) string {
	data := base64.StdEncoding.EncodeToString(png)
	var b strings.Builder
	b.Grow(len(data) + len(data)/kitty.MaxChunkSize*16 + 64)
	first := true
	for first || len(data) > 0 {
		n := min(len(data), kitty.MaxChunkSize)
		chunk := data[:n]
		data = data[n:]
		more := "0"
		if len(data) > 0 {
			more = "1"
		}
		b.WriteString("\x1b_G")
		if first {
			b.WriteString("a=t,f=100,t=d,i=" + strconv.FormatUint(uint64(id), 10) + ",q=2,")
			first = false
		} else {
			b.WriteString("q=2,")
		}
		b.WriteString("m=" + more + ";")
		b.WriteString(chunk)
		b.WriteString("\x1b\\")
	}
	return b.String()
}

// KittyPlace creates the virtual placement (U=1) that placeholder cells of
// image id refer to, cols×rows cells.
func KittyPlace(id uint32, cols, rows int) string {
	return "\x1b_Ga=p,U=1,i=" + strconv.FormatUint(uint64(id), 10) +
		",c=" + strconv.Itoa(cols) + ",r=" + strconv.Itoa(rows) + ",q=2\x1b\\"
}

// KittyDelete frees image id and its placements in the terminal.
func KittyDelete(id uint32) string {
	return "\x1b_Ga=d,d=I,i=" + strconv.FormatUint(uint64(id), 10) + ",q=2\x1b\\"
}

// KittyPlaceholders returns rows lines of cols placeholder cells for image id
// (id must fit in 24 bits; it is carried as a truecolor foreground). Each
// cell spells out its row and column so no inference rules are involved.
func KittyPlaceholders(id uint32, cols, rows int) string {
	cols, rows = min(cols, maxKittyCells), min(rows, maxKittyCells)
	if cols <= 0 || rows <= 0 {
		return ""
	}
	fg := "\x1b[38;2;" + strconv.Itoa(int(id>>16&0xff)) + ";" + strconv.Itoa(int(id>>8&0xff)) + ";" + strconv.Itoa(int(id&0xff)) + "m"
	var b strings.Builder
	b.Grow(rows * (cols*8 + len(fg) + 4))
	for r := range rows {
		if r > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(fg)
		rd := kitty.Diacritic(r)
		for c := range cols {
			b.WriteRune(kitty.Placeholder)
			b.WriteRune(rd)
			b.WriteRune(kitty.Diacritic(c))
		}
		b.WriteString("\x1b[m")
	}
	return b.String()
}
