package art

import (
	"fmt"
	"strings"
)

// Protocol is a way of putting album art on the terminal.
type Protocol int

const (
	// ProtoOff draws no art; Block returns blank cells.
	ProtoOff Protocol = iota
	// ProtoBlocks draws half blocks ('▀') with truecolor fg/bg. Works everywhere.
	ProtoBlocks
	// ProtoIIP is the iTerm2 inline image protocol (iTerm2, WezTerm), drawn over a
	// reserved blank rectangle after the frame.
	ProtoIIP
	// ProtoKitty is the kitty graphics protocol with unicode placeholders (kitty,
	// Ghostty): the image becomes text, so it is safe in a diffing renderer.
	ProtoKitty
	// ProtoSixel is sixel, drawn like IIP over a reserved blank rectangle.
	// Inside tmux, tmux keeps sixel images in its own grid and draws them
	// again whenever it redraws the pane, but drops one whenever a line it
	// covers changes (see tmux.go).
	ProtoSixel
)

// Placed reports whether images are drawn over blank cells after the frame
// (IIP and sixel) rather than being part of the text (kitty, blocks).
func (p Protocol) Placed() bool { return p == ProtoIIP || p == ProtoSixel }

func (p Protocol) String() string {
	switch p {
	case ProtoOff:
		return "off"
	case ProtoBlocks:
		return "blocks"
	case ProtoIIP:
		return "iip"
	case ProtoKitty:
		return "kitty"
	case ProtoSixel:
		return "sixel"
	}
	return fmt.Sprintf("Protocol(%d)", int(p))
}

// Detect picks the best protocol for the terminal described by env (usually
// os.Getenv) outside tmux. Inside tmux, Resolve asks tmux instead.
func Detect(env func(string) string) Protocol {
	switch env("TERM_PROGRAM") {
	case "WezTerm", "iTerm.app":
		return ProtoIIP
	case "ghostty":
		return ProtoKitty
	}
	if env("KITTY_WINDOW_ID") != "" || env("TERM") == "xterm-kitty" || env("TERM") == "xterm-ghostty" {
		return ProtoKitty
	}
	return ProtoBlocks
}

// Choice is how art is drawn, from Resolve.
type Choice struct {
	Protocol Protocol
	// Passthrough wraps image sequences for tmux. They only reach the
	// terminal with allow-passthrough on (see AllowPassthrough), and tmux
	// redraws wipe IIP images, so the app should Invalidate after them.
	Passthrough bool
	// Tmux: encore runs inside tmux.
	Tmux bool
	// Outer is the terminal outside tmux when tmux could tell (OuterWezTerm,
	// …), or "".
	Outer string
	// TmuxSixel: tmux draws sixel on the attached terminal, the fallback when
	// it refuses passthrough.
	TmuxSixel bool
}

// Resolve turns the config setting ("auto", "iip", "kitty", "sixel",
// "blocks", "off"; empty means auto) into a Choice. Inside tmux ($TMUX set),
// auto asks tmux about the attached terminal through run (nil means
// ExecTmux): WezTerm and iTerm2 get IIP through passthrough, kitty and
// Ghostty kitty placeholders through passthrough, anything else half
// blocks. An explicit
// iip or kitty inside tmux is passed through too; sixel never is, tmux draws
// it itself.
func Resolve(setting string, env func(string) string, run TmuxRunner) (Choice, error) {
	inTmux := env("TMUX") != ""
	ch := Choice{Tmux: inTmux}
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case "", "auto":
		if !inTmux {
			ch.Protocol = Detect(env)
			return ch, nil
		}
		if run == nil {
			run = ExecTmux
		}
		return detectTmux(QueryTmux(env, run, env("TMUX_PANE"))), nil
	case "iip", "iterm", "iterm2":
		ch.Protocol, ch.Passthrough = ProtoIIP, inTmux
	case "kitty":
		ch.Protocol, ch.Passthrough = ProtoKitty, inTmux
	case "sixel":
		ch.Protocol = ProtoSixel
	case "blocks", "halfblocks":
		ch.Protocol = ProtoBlocks
	case "off", "none":
		ch.Protocol = ProtoOff
	default:
		ch.Protocol = ProtoBlocks
		return ch, fmt.Errorf("art: unknown protocol %q (want auto, iip, kitty, sixel, blocks or off)", setting)
	}
	return ch, nil
}
