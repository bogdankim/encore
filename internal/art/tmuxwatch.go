package art

import (
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// tmux redraws a pane on its own: when a client switches to its window or
// attaches, on resizes and layout changes, after copy mode. Each redraw
// wipes passthrough images. With focus-events on, the pane hears about some
// of these as focus changes, but not all, and focus-events is off by default.
// So WatchTmux installs hooks for these events on the pane's own session,
// window and pane; each sends encore SIGUSR1, and encore draws its
// passthrough images again once tmux is done.

// hookTag marks the hooks encore installs, so a later run can find the ones
// a run that crashed left behind.
const hookTag = "encore-art"

// tmuxHook is a hook encore installs. scope is the set-hook flag for its
// option scope: "" for the session, "-w" for the window, "-p" for the pane.
type tmuxHook struct {
	scope, name string
}

var tmuxHooks = []tmuxHook{
	{"", "session-window-changed"},  // a window switch in the session
	{"", "client-attached"},         // attach
	{"", "client-session-changed"},  // a client switched to the session
	{"", "client-resized"},          // the terminal was resized
	{"-w", "window-layout-changed"}, // panes split, closed, resized or zoomed
	{"-p", "pane-mode-changed"},     // copy mode and other modes, in or out
}

// paneTargets are the session, window and pane ids of a pane.
type paneTargets struct {
	session, window, pane string
}

func (t paneTargets) of(scope string) string {
	switch scope {
	case "-w":
		return t.window
	case "-p":
		return t.pane
	}
	return t.session
}

func queryTargets(run TmuxRunner, pane string) (paneTargets, bool) {
	out, err := run("display-message", "-p", "-t", pane, "#{session_id} #{window_id} #{pane_id}")
	f := strings.Fields(out)
	if err != nil || len(f) != 3 {
		return paneTargets{}, false
	}
	return paneTargets{f[0], f[1], f[2]}, true
}

// hookCommand is the hook's tmux command: signal pid, but only while pid is
// still an encore (pids get reused, and SIGUSR1 kills most programs).
func hookCommand(pid int) string {
	p := strconv.Itoa(pid)
	return `run-shell -b ": ` + hookTag + `; ps -p ` + p + ` -o comm= | grep -q encore && kill -USR1 ` + p + `"`
}

var hookPID = regexp.MustCompile(`kill -USR1 (\d+)`)

// hookEntry is one element of a hook array, as show-hooks lists it:
// "name[index] command".
type hookEntry struct {
	name  string // name[index]
	pid   int    // the encore it signals; 0 when it is not one of ours
	value string
}

func listHooks(run TmuxRunner, scope, target string) []hookEntry {
	args := []string{"show-hooks"}
	if scope != "" {
		args = append(args, scope)
	}
	out, err := run(append(args, "-t", target)...)
	if err != nil {
		return nil
	}
	var hs []hookEntry
	for _, l := range strings.Split(out, "\n") {
		name, value, ok := strings.Cut(l, " ")
		if !ok || !strings.HasSuffix(name, "]") {
			continue
		}
		h := hookEntry{name: name, value: value}
		if m := hookPID.FindStringSubmatch(value); m != nil && strings.Contains(value, hookTag) {
			h.pid, _ = strconv.Atoi(m[1])
		}
		hs = append(hs, h)
	}
	return hs
}

func unsetHook(run TmuxRunner, scope, target, name string) {
	args := []string{"set-hook"}
	if scope != "" {
		args = append(args, scope)
	}
	_, _ = run(append(args, "-u", "-t", target, name)...)
}

// installedHook is a hook element encore added: where, and under which
// name[index].
type installedHook struct {
	scope, target, name string
}

// installHooks appends hookCommand(pid) to each of tmuxHooks on the pane's
// session, window and pane, and returns the elements it added.
func installHooks(run TmuxRunner, t paneTargets, pid int) []installedHook {
	cmd := hookCommand(pid)
	var got []installedHook
	for _, h := range tmuxHooks {
		target := t.of(h.scope)
		args := []string{"set-hook", "-a"}
		if h.scope != "" {
			args = append(args, h.scope)
		}
		if _, err := run(append(args, "-t", target, h.name, cmd)...); err != nil {
			continue
		}
		for _, e := range listHooks(run, h.scope, target) {
			if e.pid == pid && strings.HasPrefix(e.name, h.name+"[") {
				got = append(got, installedHook{h.scope, target, e.name})
			}
		}
	}
	return got
}

// removeHooks unsets the elements installHooks added, each only while it
// still holds encore's command for pid (the user may have changed it).
func removeHooks(run TmuxRunner, hooks []installedHook, pid int) {
	type scoped struct{ scope, target string }
	lists := map[scoped][]hookEntry{}
	for _, h := range hooks {
		k := scoped{h.scope, h.target}
		if _, ok := lists[k]; !ok {
			lists[k] = listHooks(run, h.scope, h.target)
		}
		for _, e := range lists[k] {
			if e.name == h.name && e.pid == pid {
				unsetHook(run, h.scope, h.target, h.name)
			}
		}
	}
}

// RemoveStaleHooks removes the hooks an encore that is no longer running
// left on pane's session, window and pane (it crashed or was killed before
// it could), and reports how many. alive nil means the process table.
func RemoveStaleHooks(run TmuxRunner, pane string, alive func(pid int) bool) int {
	if pane == "" {
		return 0
	}
	if alive == nil {
		alive = processAlive
	}
	t, ok := queryTargets(run, pane)
	if !ok {
		return 0
	}
	n := 0
	for _, scope := range []string{"", "-w", "-p"} {
		target := t.of(scope)
		for _, e := range listHooks(run, scope, target) {
			if e.pid != 0 && (e.pid == os.Getpid() || !alive(e.pid)) {
				unsetHook(run, scope, target, e.name)
				n++
			}
		}
	}
	return n
}

// paneFormat asks tmux whether the pane is on screen, how it sits in its
// window, and where its top-left cell is on the terminal.
const paneFormat = "#{window_active}\t#{session_attached}\t#{window_zoomed_flag}\t#{pane_active}\t#{pane_in_mode}\t" +
	"#{pane_width}\t#{window_width}\t#{window_bigger}\t#{pane_left}\t#{pane_top}\t" +
	"#{window_offset_x}\t#{window_offset_y}\t#{status}\t#{status-position}"

// QueryPane asks tmux whether a client shows pane, whether it is narrower
// than its window, and where it is on the terminal.
func QueryPane(run TmuxRunner, pane string) (PaneState, bool) {
	out, err := run("display-message", "-p", "-t", pane, paneFormat)
	f := strings.Split(strings.TrimSpace(out), "\t")
	if err != nil || len(f) < 7 {
		return PaneState{}, false
	}
	for len(f) < 14 {
		f = append(f, "")
	}
	n := make([]int, len(f))
	for i, s := range f {
		n[i], _ = strconv.Atoi(s)
	}
	active, attached, zoomed, paneActive, inMode, pw, ww := n[0], n[1], n[2], n[3], n[4], n[5], n[6]
	st := PaneState{
		Hidden: active != 1 || attached == 0 || (zoomed == 1 && paneActive != 1) || inMode != 0,
		Split:  pw < ww || n[7] == 1,
	}
	if f[8] != "" && f[9] != "" {
		// the status line takes the top rows when it is on top: "on" is
		// one line, "2" to "5" that many
		top := 0
		if f[13] == "top" {
			switch f[12] {
			case "on":
				top = 1
			case "off", "":
			default:
				top = n[12]
			}
		}
		st.Origin, st.Located = Cell{X: n[8] - n[10], Y: n[9] - n[11] + top}, true
	}
	return st, true
}

// pollEvery is how often WatchTmux asks tmux whether the pane is shown, for
// changes no hook reports (a pane moved to another session, a second
// client).
const pollEvery = 2 * time.Second

// WatchTmux keeps r's passthrough images (Renderer.Fragile) in step with
// tmux for pane ($TMUX_PANE): it installs hooks that send this process
// SIGUSR1 whenever tmux redraws the pane on its own, and on each signal and
// every pollEvery asks tmux whether the pane is shown (Renderer.SetPane).
// After a signal r draws its passthrough images again (Renderer.TmuxRedrawn).
// Nothing is drawn through passthrough while the pane is hidden, so images
// never land on another window. stop removes exactly the hooks it added.
// run nil means ExecTmux.
func WatchTmux(r *Renderer, run TmuxRunner, pane string) (stop func()) {
	if run == nil {
		run = ExecTmux
	}
	t, ok := queryTargets(run, pane)
	if !ok {
		return func() {}
	}
	if st, ok := QueryPane(run, pane); ok {
		r.SetPane(st)
	}
	// listen before the hooks exist: SIGUSR1 would otherwise end the process
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGUSR1)
	pid := os.Getpid()
	hooks := installHooks(run, t, pid)
	r.mu.Lock()
	r.watched = len(hooks) > 0
	r.mu.Unlock()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		tick := time.NewTicker(pollEvery)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-sig:
				if st, ok := QueryPane(run, pane); ok {
					r.SetPane(st)
				}
				r.TmuxRedrawn()
			case <-tick.C:
				if st, ok := QueryPane(run, pane); ok {
					r.SetPane(st)
				}
			}
		}
	}()
	return func() {
		close(done)
		<-finished
		r.mu.Lock()
		r.watched = false
		r.mu.Unlock()
		removeHooks(run, hooks, pid)
		// a hook that already ran may still deliver its signal
		signal.Ignore(syscall.SIGUSR1)
	}
}
