package art

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Inside tmux, images either go through tmux as sixel, which tmux keeps in
// its own grid and redraws itself (window and pane switches, detach and
// attach, popups, copy mode), or are passed through to the terminal outside,
// which tmux knows nothing about. Passthrough images vanish whenever tmux
// redraws the pane and need allow-passthrough; WatchTmux draws them again.
//
// A sixel image tmux keeps is fragile in another way: tmux drops it when
// anything erases, inserts or deletes characters on one of its lines or
// scrolls it, and then redraws the whole pane, which sends every kept image
// again and wipes every passthrough image. A track change (a new player bar
// image) or a lyric line scrolling past full-screen art did exactly that.
// tmux also keeps at most nine sixel images across the server, and sixel is
// 3 to 6 times the bytes of JPEG. So on WezTerm and iTerm2 every image goes
// as an iTerm2 image through passthrough, placed with the pane's offset, and
// sixel is only used when tmux refuses passthrough or art = "sixel". kitty
// and Ghostty (no sixel) get kitty placeholders through passthrough; those
// are text, so tmux redraws them like any other text.

// TmuxRunner runs tmux with args and returns its standard output.
type TmuxRunner func(args ...string) (string, error)

// tmuxTimeout bounds every tmux query, so a wedged server cannot hold up
// startup.
const tmuxTimeout = 500 * time.Millisecond

// ExecTmux runs the tmux binary on PATH. It talks to the server in $TMUX.
func ExecTmux(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", args...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

// Outer terminal names Resolve recognises inside tmux.
const (
	OuterWezTerm = "WezTerm"
	OuterITerm   = "iTerm2"
	OuterKitty   = "kitty"
	OuterGhostty = "ghostty"
)

// outerName classifies a terminal type (XTVERSION reply such as "WezTerm
// 20240203-110809-5046fc22"), TERM value or TERM_PROGRAM value.
func outerName(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "wezterm"):
		return OuterWezTerm
	case strings.Contains(s, "iterm"):
		return OuterITerm
	case strings.Contains(s, "ghostty"):
		return OuterGhostty
	case strings.Contains(s, "kitty"):
		return OuterKitty
	}
	return ""
}

// TmuxClient is what tmux knows about the terminal it is attached to.
type TmuxClient struct {
	// Outer is the terminal outside tmux (OuterWezTerm, …), or "".
	Outer string
	// Sixel: tmux draws sixel images on this client.
	Sixel bool
}

// QueryTmux asks tmux about the client attached to pane (usually
// $TMUX_PANE; "" means tmux's current client). The terminal is the client's
// XTVERSION reply (#{client_termtype}), then its TERM (#{client_termname}),
// then TERM_PROGRAM from the session or global environment, which holds the
// terminal tmux was started from. ENCORE_OUTER_TERM in env overrides the
// name, for testing in terminals tmux cannot identify.
func QueryTmux(env func(string) string, run TmuxRunner, pane string) TmuxClient {
	var c TmuxClient
	args := []string{"display-message", "-p"}
	if pane != "" {
		args = append(args, "-t", pane)
	}
	args = append(args, "#{client_termtype}\t#{client_termname}\t#{client_termfeatures}")
	if out, err := run(args...); err == nil {
		f := strings.Split(out, "\t")
		for len(f) < 3 {
			f = append(f, "")
		}
		c.Outer = outerName(f[0])
		if c.Outer == "" {
			c.Outer = outerName(f[1])
		}
		c.Sixel = hasFeature(f[2], "sixel")
	}
	if c.Outer == "" {
		for _, scope := range [][]string{{"show-environment"}, {"show-environment", "-g"}} {
			args := scope
			if pane != "" && len(scope) == 1 {
				args = append(args, "-t", pane)
			}
			out, err := run(append(args, "TERM_PROGRAM")...)
			if v, ok := strings.CutPrefix(out, "TERM_PROGRAM="); err == nil && ok {
				if c.Outer = outerName(v); c.Outer != "" {
					break
				}
			}
		}
	}
	if o := env("ENCORE_OUTER_TERM"); o != "" {
		c.Outer = outerName(o)
	}
	return c
}

func hasFeature(list, f string) bool {
	for _, x := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(x), f) {
			return true
		}
	}
	return false
}

// detectTmux picks the protocol for a tmux client.
func detectTmux(c TmuxClient) Choice {
	ch := Choice{Outer: c.Outer, Tmux: true, TmuxSixel: c.Sixel}
	switch c.Outer {
	case OuterWezTerm, OuterITerm:
		ch.Protocol, ch.Passthrough = ProtoIIP, true
	case OuterKitty, OuterGhostty:
		ch.Protocol, ch.Passthrough = ProtoKitty, true
	default:
		ch.Protocol = ProtoBlocks
	}
	return ch
}

// passthroughMarker is a pane user option encore sets while it holds
// allow-passthrough on for the pane: "<pid> <the pane's own value>", the
// value empty when the pane had none. restore removes it. A run that crashed
// or was killed leaves it behind, and the next run in the pane puts the
// option back (RestoreStalePassthrough).
const passthroughMarker = "@encore-passthrough"

// AllowPassthrough turns on allow-passthrough for pane alone (a pane option,
// tmux 3.3 and later) when it is off there, so image sequences reach the
// terminal. restore puts the pane back as it was: the pane's own value, or
// none, so it follows the window and global options again. When passthrough
// is already on, nothing changes and restore does nothing.
func AllowPassthrough(run TmuxRunner, pane string) (restore func(), err error) {
	nop := func() {}
	if pane == "" {
		return nop, errors.New("art: no tmux pane ($TMUX_PANE is empty)")
	}
	eff, err := run("show-options", "-p", "-v", "-A", "-t", pane, "allow-passthrough")
	if err != nil {
		return nop, err
	}
	if v := strings.TrimSpace(eff); v == "on" || v == "all" {
		return nop, nil
	}
	own, err := run("show-options", "-p", "-v", "-t", pane, "allow-passthrough")
	if err != nil {
		return nop, err
	}
	own = strings.TrimSpace(own)
	if _, err := run("set-option", "-p", "-t", pane, "allow-passthrough", "on"); err != nil {
		return nop, err
	}
	_, _ = run("set-option", "-p", "-t", pane, passthroughMarker, strconv.Itoa(os.Getpid())+" "+own)
	return func() {
		putBackPassthrough(run, pane, own)
		_, _ = run("set-option", "-p", "-u", "-t", pane, passthroughMarker)
	}, nil
}

// putBackPassthrough sets pane's allow-passthrough to own, or unsets it when
// own is "".
func putBackPassthrough(run TmuxRunner, pane, own string) {
	if own != "" {
		_, _ = run("set-option", "-p", "-t", pane, "allow-passthrough", own)
		return
	}
	_, _ = run("set-option", "-p", "-u", "-t", pane, "allow-passthrough")
}

// RestoreStalePassthrough puts pane's allow-passthrough back when an encore
// that is no longer running left its marker there (it crashed or was killed
// before it could), and reports whether it did. alive nil means the process
// table. Best effort: tmux errors are ignored.
func RestoreStalePassthrough(run TmuxRunner, pane string, alive func(pid int) bool) bool {
	if pane == "" {
		return false
	}
	if alive == nil {
		alive = processAlive
	}
	m, err := run("show-options", "-p", "-q", "-v", "-t", pane, passthroughMarker)
	if err != nil || strings.TrimSpace(m) == "" {
		return false
	}
	pidText, own, _ := strings.Cut(strings.TrimSpace(m), " ")
	if pid, err := strconv.Atoi(pidText); err == nil && pid != os.Getpid() && alive(pid) {
		return false // another encore in this pane holds it
	}
	putBackPassthrough(run, pane, strings.TrimSpace(own))
	_, _ = run("set-option", "-p", "-u", "-t", pane, passthroughMarker)
	return true
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Setup is Resolve plus what the choice needs from tmux: with passthrough,
// allow-passthrough is turned on for this pane ($TMUX_PANE) alone. Call
// restore before exiting to put the pane option back. When auto detection
// picked passthrough and tmux refuses it (tmux older than 3.3), art falls
// back to sixel where tmux draws it, every image sixel as with art = "sixel",
// and to half blocks elsewhere. Inside tmux it first puts back a pane option
// and removes hooks an earlier run left behind when it crashed.
func Setup(setting string, env func(string) string, run TmuxRunner) (ch Choice, restore func(), err error) {
	if run == nil {
		run = ExecTmux
	}
	if env("TMUX") != "" {
		RestoreStalePassthrough(run, env("TMUX_PANE"), nil)
		RemoveStaleHooks(run, env("TMUX_PANE"), nil)
	}
	ch, err = Resolve(setting, env, run)
	restore = func() {}
	if !ch.Passthrough {
		return ch, restore, err
	}
	restore, perr := AllowPassthrough(run, env("TMUX_PANE"))
	if perr == nil {
		return ch, restore, err
	}
	if s := strings.ToLower(strings.TrimSpace(setting)); s == "" || s == "auto" {
		if ch.Protocol == ProtoIIP && ch.TmuxSixel {
			ch.Protocol, ch.Passthrough = ProtoSixel, false
			return ch, restore, err
		}
		return Choice{Protocol: ProtoBlocks, Tmux: true, Outer: ch.Outer, TmuxSixel: ch.TmuxSixel}, restore, err
	}
	return ch, restore, errors.Join(err, fmt.Errorf("art: tmux allow-passthrough: %w", perr))
}
