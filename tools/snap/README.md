# snap

Runs a TUI in a real pty, mirrors the output into xterm.js inside headless Chrome, plays a JSON script of keys and mouse events, and saves PNG and plain-text screenshots. Use it to look at encore and drive it without a real terminal window.

## Setup

```sh
cd tools/snap && npm ci
```

It needs Node 22+, Google Chrome at `/Applications/Google Chrome.app`, and `/usr/bin/python3` (for the pty pixel size, see below).

encore's icons (Material Design, U+F0000 and up) need `fonts/SymbolsNerdFontMono-Regular.ttf`. It comes from `NerdFontsSymbolsOnly.zip` on the [nerd-fonts releases page](https://github.com/ryanoasis/nerd-fonts/releases/latest). WezTerm bundles the same font (Nerd Fonts 3.1.1 in the installed build) as its fallback. The Nerd Fonts project is MIT licensed, and each icon set keeps its own license. The Material Design Icons set is Apache 2.0. Without the file snap prints a warning and icons render as blank cells. `--symbols-font FILE` points at a copy somewhere else.

## Usage

```sh
node tools/snap/snap.mjs --cols 140 --rows 42 --cwd . \
  --script steps.json --out tools/snap/out -- go run ./cmd/encore
```

The script can also come from stdin (`--script -`) or inline (`--steps '[...]'`). Without a script, snap waits 500 ms and saves `screen.png`.

| Option | Default | |
|---|---|---|
| `--cols N`, `--rows N` | 140, 42 | terminal size |
| `--cwd DIR` | current dir | where the command runs |
| `--out DIR` | `tools/snap/out` | where shots go |
| `--font-size N` | 14 | px |
| `--font-family CSS` | `"SF Mono", "Symbols Nerd Font Mono", Menlo, monospace` | |
| `--symbols-font FILE` | `fonts/SymbolsNerdFontMono-Regular.ttf` | Nerd Font icons |
| `--line-height N` | 1.0 | |
| `--term-program NAME` | WezTerm | sets `TERM_PROGRAM` |
| `--env KEY=VAL` | | extra env, repeatable |
| `--settle MS` | 80 | quiet time before a shot or assert |
| `--log FILE` | | raw pty output, for checking escape sequences, plus `FILE.times` with the time and size of each output chunk and the time and log offset of each step |
| `--no-pixels` | | leave the pty pixel size at 0x0 |
| `--timeout MS` | 120000 | limit for the whole run |

The command gets `TERM=xterm-256color`, `COLORTERM=truecolor` and `TERM_PROGRAM=WezTerm`. Exit code is 0 when every step passes. It is 1 when a step fails, the app exits before the script ends, or the run times out, and 2 for bad arguments.

## Steps

Cells are zero-based `[col, row]`.

| Step | Does |
|---|---|
| `{"wait": 200}` | sleep, ms |
| `{"waitFor": "text", "timeout": 5000}` | wait until the text is on screen |
| `{"keys": ["j", "enter", "ctrl+d"], "delay": 40}` | send keys, `delay` ms apart |
| `{"type": "raw text"}` | send text as is |
| `{"click": [10, 5], "button": "left", "double": false}` | mouse click; button is left, right or middle |
| `{"move": [10, 5]}` | hover |
| `{"drag": {"from": [5, 30], "to": [9, 32]}}` | press, move cell by cell, release |
| `{"drag": {...}, "hold": true}` | the same without the release, for a shot mid-drag |
| `{"release": [9, 32]}` | move there and release the button a held drag pressed |
| `{"wheel": [30, 12], "dy": 3}` | scroll notches; dy > 0 is down, `dx` for sideways |
| `{"resize": [100, 30]}` | resize xterm and the pty |
| `{"shot": "name"}` | save `name.png` and `name.txt` |
| `{"shot": "name", "region": [0, 0, 40, 10], "scale": 3}` | crop to cells `[col, row, cols, rows]`; scale 3 zooms past the default 2x |
| `{"assert": "text", "timeout": 1000}` | fail unless the text shows up within the timeout |
| `{"assertNot": "text"}` | fail unless the text is gone within the timeout |
| `{"expectExit": 3000, "code": 0}` | the app should exit, optionally with this code |

Key names: any single character, `enter`, `esc`, `tab`, `shift+tab`, `backspace`, `space`, `up`, `down`, `left`, `right`, `home`, `end`, `pgup`, `pgdown`, `insert`, `delete`, `f1` to `f12`. Prefix with `ctrl+`, `alt+` or `shift+` (`ctrl+d`, `alt+x`, `ctrl+up`). Keys go straight into the pty as legacy escape sequences. Arrow keys follow the app's cursor-key mode. After a lone `esc` or an `alt+` key snap waits at least 90 ms, because Bubble Tea holds a bare ESC for 50 ms before deciding it is a key.

Mouse steps go through Chrome as real mouse events at the centre of the cell. xterm.js encodes them, so the app only sees them after it turns on mouse tracking.

Shots, asserts and `waitFor` first wait until the pty has been quiet for `--settle` ms (1.5 s at most), so a shot shows the frame the app drew after the last input. A failed step saves `fail-step<N>.png/.txt`. If the app exits early, snap saves `exit.png/.txt` and prints the last lines of its output.

## Example

```json
[
  {"waitFor": "Library", "timeout": 10000},
  {"keys": ["j", "j", "enter"]},
  {"shot": "after-enter"},
  {"click": [12, 4]},
  {"wheel": [40, 20], "dy": 5},
  {"assert": "Now Playing"},
  {"shot": "art", "region": [0, 0, 40, 20], "scale": 3},
  {"keys": ["q"]},
  {"expectExit": 2000}
]
```

## tmux

Run tmux inside snap on a private server so the tmux you use stays untouched:

```sh
node tools/snap/snap.mjs --font-size 13 --script steps.json -- \
  tmux -L encoretest -f /dev/null new-session -s t './bin/encore --demo' \; \
  new-window 'sh other.sh' \; select-window -t :0
tmux -L encoretest kill-server
```

`ctrl+b` then `n` or `p` in a `keys` step switches tmux windows. tmux sees that xterm.js draws sixel and sends sixel images on to it. It can't name xterm.js as WezTerm, but it finds `TERM_PROGRAM=WezTerm` in its global environment; `--env ENCORE_OUTER_TERM=WezTerm` forces the name.

Use `--font-size 13` for sixel. Its cells are 8 by 15 CSS px. At 14 px a cell is about 8.66 px wide, but the pty pixel size tells tmux 8, so sixel images come out narrower than their cells and drift left across a row. Real terminals have whole-pixel cells.

## Self-test

`npm run selftest` (from `tools/snap`) runs `testapp/`, a small Bubble Tea v2 program that prints every key and mouse event it gets and draws an iTerm2 image. `testapp/selftest.json` checks keys, clicks, wheel, drag, hover, resize, the image and a clean exit.

## How close it is to WezTerm

- Colours are WezTerm's built-in "Monokai (base16)" scheme, including indexed colours 16 to 21. Bold text uses the bright colour, which is the same colour in this scheme. Background opacity and blur from the WezTerm config are not reproduced.
- The font is SF Mono at 14 px. WezTerm uses 18 pt, so the PNG is smaller and glyph shapes and spacing differ a little. Cell geometry is the thing to trust.
- snap uses xterm.js's DOM renderer, which draws box and block characters from the font. WezTerm draws them itself, so joins can differ by a pixel. The WebGL renderer draws them too, but in headless Chrome its screenshots came out blank.
- The image addon renders iTerm2 (OSC 1337) and sixel. It ignores OSC 1337 images that have no `size=` field, which WezTerm accepts, so snap adds `size=` to those before xterm.js parses them.
- An image belongs to the cells it covers. Writing over those cells, or erasing them with `EL`/`DL`, removes that part of the image. WezTerm works the same way. Bubble Tea's renderer emits `\x1b[K` and `\x1b[M` when lines change, so art drawn with `tea.Raw` loses rows when nearby lines redraw. `--log` shows the exact bytes.
- node-pty cannot set the pixel size in the pty's winsize. snap starts the command through a `python3` shim that sets it with `TIOCSWINSZ`, and does the same on resize. Size = cell size in CSS px times cols and rows. xterm.js also answers `CSI 14 t` and `CSI 16 t`.
- The kitty keyboard protocol is not used. Keys arrive as legacy sequences.
