package art

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"strconv"
	"strings"
)

// CellSize is the size of one terminal cell in pixels.
type CellSize struct{ W, H int }

// DefaultCellSize is used until the terminal reports its real cell size.
var DefaultCellSize = CellSize{W: 10, H: 20}

// maxSide caps the pixel size of an encoded image; larger buys nothing on a
// terminal and costs bandwidth on every redraw.
const maxSide = 640

// IIP returns the iTerm2 inline image sequence that shows data (PNG, JPEG or
// GIF bytes) in a cols×rows cell box, keeping its aspect ratio and leaving the
// cursor where it was (doNotMoveCursor is a WezTerm extension; iTerm2 moves
// the cursor anyway, which Placement's save/restore undoes).
func IIP(data []byte, cols, rows int) string { return iip(data, cols, rows, true) }

// iip builds the sequence. The Renderer passes keepAspect=false: its images
// are already cropped to the box's pixel aspect, and stretching guarantees
// the image never spills out of its cells when the cell size is a guess
// (xterm.js fits preserved-aspect images to the width and lets them overflow
// the height).
func iip(data []byte, cols, rows int, keepAspect bool) string {
	var b strings.Builder
	n := base64.StdEncoding.EncodedLen(len(data))
	b.Grow(n + 96)
	b.WriteString("\x1b]1337;File=inline=1;size=")
	b.WriteString(strconv.Itoa(len(data)))
	b.WriteString(";width=")
	b.WriteString(strconv.Itoa(cols))
	b.WriteString(";height=")
	b.WriteString(strconv.Itoa(rows))
	if keepAspect {
		b.WriteString(";preserveAspectRatio=1;doNotMoveCursor=1:")
	} else {
		b.WriteString(";preserveAspectRatio=0;doNotMoveCursor=1:")
	}
	enc := base64.NewEncoder(base64.StdEncoding, &b)
	_, _ = enc.Write(data)
	_ = enc.Close()
	b.WriteByte('\a')
	return b.String()
}

// Placement wraps seq so it is drawn with its top-left corner at the
// zero-based cell (x, y) without disturbing the cursor or the pen: DECSC,
// CUP, seq, DECRC. The renderer's idea of the cursor stays correct.
func Placement(x, y int, seq string) string {
	return "\x1b7\x1b[" + strconv.Itoa(y+1) + ";" + strconv.Itoa(x+1) + "H" + seq + "\x1b8"
}

// pixelSize returns the pixel size for a cols×rows box, its longest side
// capped at maxSide, and at limit when that is positive and smaller.
func pixelSize(cols, rows int, cell CellSize, limit int) (w, h int) {
	if cell.W <= 0 || cell.H <= 0 {
		cell = DefaultCellSize
	}
	side := maxSide
	if limit > 0 {
		side = min(side, limit)
	}
	w, h = cols*cell.W, rows*cell.H
	if s := max(w, h); s > side {
		w, h = max(1, w*side/s), max(1, h*side/s)
	}
	return w, h
}

// DefaultQuality is the JPEG quality of images sent to the terminal.
const DefaultQuality = 90

// encodeImage encodes m for IIP: JPEG when opaque (album art, a fraction of
// the PNG size), PNG when it has transparency.
func encodeImage(m image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if n, ok := m.(*image.NRGBA); ok && opaque(n) {
		if err := jpeg.Encode(&buf, m, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func opaque(m *image.NRGBA) bool {
	for i := 3; i < len(m.Pix); i += 4 {
		if m.Pix[i] != 255 {
			return false
		}
	}
	return true
}

// ClearCells returns a sequence that blanks the cols×rows rectangle at (x, y)
// with default-coloured spaces, which removes IIP image fragments from those
// cells in WezTerm and iTerm2. Only use it on cells the frame also has blank;
// Renderer repaints whole rows from the frame instead, which is always safe.
func ClearCells(x, y, cols, rows int) string {
	var b strings.Builder
	b.WriteString("\x1b7\x1b[m")
	for r := range rows {
		b.WriteString("\x1b[" + strconv.Itoa(y+r+1) + ";" + strconv.Itoa(x+1) + "H")
		b.WriteString("\x1b[" + strconv.Itoa(cols) + "X")
	}
	b.WriteString("\x1b8")
	return b.String()
}
