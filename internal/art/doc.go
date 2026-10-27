// Package art turns album artwork into terminal output: iTerm2 inline images
// (WezTerm, iTerm2, also inside tmux through passthrough), sixel (inside
// tmux when passthrough is refused, or by choice), kitty unicode placeholders
// (kitty, Ghostty) or truecolor half blocks (everything else).
//
// # Using it from a Bubble Tea program
//
//	ch, restore, _ := art.Setup(cfg.ArtProtocol, os.Getenv, nil)
//	defer restore() // puts tmux's allow-passthrough back
//	r := art.NewRenderer(art.Options{Protocol: ch.Protocol, Passthrough: ch.Passthrough, Tmux: ch.Tmux, TilePrefix: "tile:"})
//	if ch.Tmux && r.Fragile() {
//		defer art.WatchTmux(r, nil, os.Getenv("TMUX_PANE"))() // hooks for tmux redraws
//	}
//	prog := tea.NewProgram(model, tea.WithOutput(art.NewOutput(os.Stdout, r)))
//
// In the model:
//
//   - Init: return tea.Raw(art.CellSizeQuery). In Update, feed
//     art.CellSizeFromMsg(msg) to r.SetCellSize and tea.WindowSizeMsg to
//     r.Resize (and query the cell size again after a resize).
//   - When artwork bytes arrive (in the tea.Cmd that fetched them), call
//     r.Prepare(key, png, cols, rows) so the decode and encode happen off
//     the UI goroutine.
//   - In View: put r.Block(key, nil, cols, rows) in the layout. For IIP and
//     sixel, also
//     call r.Place(key, x, y, cols, rows) with the block's screen cell, and
//     r.Occlude(x, y, w, h) for every overlay drawn on top. Call
//     r.Frame(content) last with the exact string handed to tea.NewView.
//   - Set View.AltScreen; images are only drawn on the alternate screen.
//
// Sixel images go through the same Output as IIP. Inside tmux (art =
// "sixel", or when tmux refuses passthrough), tmux stores them and redraws
// them itself, but it drops every image on a line that is erased, keeps at
// most TmuxImageLimit of them, and counts their height in six-pixel bands;
// see tmux.go, sixel.go and Renderer.Compose. When it drops
// one it redraws the whole pane, and until that redraw is done it throws
// away passthrough output for the pane. So the Renderer predicts when its
// output makes tmux drop a sixel image and holds passthrough images back
// until tmux is done (Renderer.TmuxRedrawn), and it sends nothing through
// passthrough while the pane is hidden (Renderer.SetPane, WatchTmux).
//
// # Why IIP needs an Output
//
// Kitty placeholders and half blocks are text, so Bubble Tea's cell-diffing
// renderer handles them like anything else. An IIP image is not part of the
// frame: it is drawn over blank cells and lives in those cells (WezTerm,
// iTerm2 and xterm.js all attach image tiles to cells). Bubble Tea v2's
// renderer (ultraviolet's TerminalRenderer) believes those cells are blank
// spaces and, while diffing, freely:
//
//   - erases them: EL (\x1b[K) when the rest of a line becomes blank, EL 1,
//     ECH, ED below (\x1b[J) when the bottom of the screen becomes blank, and
//     a full \x1b[H\x1b[2J on resize, clearScreen and entering the alt screen;
//   - repaints whole rows from column 0 (EL then text) when a row holds wide
//     or width-ambiguous characters;
//   - shifts them sideways with ICH/DCH;
//   - scrolls them with hardware scrolling in the alt screen: LF at the bottom
//     row, SU/SD, IL/DL and DECSTBM regions, whenever lines moved up or down
//     (any list scroll). Image tiles travel with their cells, so the image
//     moves and leaves fragments outside its box.
//
// tea.Raw cannot fix this: raw output is flushed before the frame on each
// tick, so a redraw sent from Update is wiped by the same tick's frame, and
// nothing tells the model when its frame reached the terminal.
//
// Output wraps the terminal writer instead. It follows the cursor through
// every byte the renderer writes and records which cells were printed over,
// erased, shifted or scrolled. After each write it redraws exactly the images
// whose cells were hit, plus new or moved ones, repaints rows where an image
// left fragments behind (from the frame passed to Frame), and crops images
// around occluders. When Bubble Tea uses synchronized output (mode 2026,
// which it enables after the terminal reports support, as WezTerm does) the
// images go inside the frame's update, so no frame is shown without them. A
// frame that only changes the image behind an unchanged blank block produces
// no write, so Output also redraws after a short timer.
//
// Each redraw first erases the image's cells (ECH) so tiles never stack, and
// uses preserveAspectRatio=0 with an image already cropped to the box's pixel
// aspect, so it can never spill out of its cells when the cell size is a
// guess (xterm.js fits preserved-aspect images to the width and lets them
// overflow the height).
//
// Bubble Tea's renderer repaints a line that holds a wide character on every
// frame: EL from column 0, then every cell up to the last non-blank one. The
// Output rewrites those writes so they leave the cells of images that stay
// alone: the EL becomes erases around them, and spaces over them become
// cursor moves. Inside tmux, on a line with a sixel image, erases become
// spaces, which tmux does not treat as an erase of the line.
//
// Bubble Tea also scrolls rows in hardware when their text moved up or down,
// and rows beside a block of art are blank there, so a lyric line advancing
// next to full-screen art scrolls the art too. When a scroll (LF or RI at a
// margin, SU, SD, IL, DL), or an insert or delete of characters, would move
// the cells of an image that stays, the Output drops it and repaints the
// rows' text from the frame around the image after the write; what the
// renderer wrote there in between is overwritten. The image keeps its cells
// and is not sent again.
//
// Cost: an image is re-sent only when its own cells were touched. Moving a
// cursor in a list next to it, updating text on the same rows, or scrolling
// those rows costs nothing.
//
// # Scrolling images in the terminal
//
// An image that moves (a grid scrolling a row) would be sent again at its
// new place. Directly in a terminal that attaches images to cells (WezTerm,
// iTerm2), inside a synchronized update, the Output scrolls them instead:
// planScroll finds a vertical shift of rows that brings the new frame's
// pieces in place (same image, same column, rows higher or lower), and the
// Output writes DECSTBM, SU or SD and DECSTBM again ahead of Bubble Tea's
// frame. Bubble Tea does not know the rows moved, so every scrolled row is
// then written from the frame (only the stretches that differ from what the
// scroll left there, see scrollPlan.shows), around the images that stay. Of
// the new pieces only what the scroll did not bring is drawn: a row of tiles
// that came into view, and the rest of a tile that was cut off at the bottom
// before. A scroll that would cut an image in two is not planned (xterm.js
// drops such an image), nor one over an image that holds still among the
// rows, nor one in a frame that scrolls or clears the screen itself. Inside
// tmux, tmux owns the screen and nothing is scrolled. ENCORE_IMAGE_SCROLL=off
// turns it off.
package art
