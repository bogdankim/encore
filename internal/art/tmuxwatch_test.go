package art

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// fakeHooks is a tmux with hook arrays per scope and target, and one pane
// %1 in window @2 of session $3.
type fakeHooks struct {
	hooks map[string]map[string][]string // "scope target" -> name -> elements ("" = unset)
	pane  string                         // reply to the pane format
	log   []string
}

func newFakeHooks() *fakeHooks {
	return &fakeHooks{hooks: map[string]map[string][]string{}, pane: "1\t1\t0\t1\t0\t80\t80\t0"}
}

func (f *fakeHooks) run(args ...string) (string, error) {
	f.log = append(f.log, strings.Join(args, " "))
	scope, target, unset, appendTo := "", "", false, false
	var rest []string
	for i := 1; i < len(args); i++ {
		switch a := args[i]; a {
		case "-w", "-p":
			scope = a
		case "-u":
			unset = true
		case "-a":
			appendTo = true
		case "-t":
			i++
			target = args[i]
		default:
			rest = append(rest, a)
		}
	}
	key := scope + " " + target
	switch args[0] {
	case "display-message":
		if strings.Contains(rest[len(rest)-1], "session_id") {
			return "$3 @2 %1", nil
		}
		return f.pane, nil
	case "set-hook":
		if f.hooks[key] == nil {
			f.hooks[key] = map[string][]string{}
		}
		if unset {
			name, idx, _ := strings.Cut(strings.TrimSuffix(rest[0], "]"), "[")
			var i int
			fmt.Sscan(idx, &i)
			if i < len(f.hooks[key][name]) {
				f.hooks[key][name][i] = ""
			}
			return "", nil
		}
		if appendTo {
			f.hooks[key][rest[0]] = append(f.hooks[key][rest[0]], rest[1])
		}
		return "", nil
	case "show-hooks":
		var out []string
		for name, els := range f.hooks[key] {
			for i, v := range els {
				if v != "" {
					out = append(out, fmt.Sprintf("%s[%d] %s", name, i, v))
				}
			}
		}
		slices.Sort(out)
		return strings.Join(out, "\n"), nil
	}
	return "", fmt.Errorf("unknown command %q", args[0])
}

func (f *fakeHooks) all() []string {
	var out []string
	for key, names := range f.hooks {
		for name, els := range names {
			for _, v := range els {
				if v != "" {
					out = append(out, key+" "+name+" "+v)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestTmuxHooksInstallAndRemove(t *testing.T) {
	f := newFakeHooks()
	// the user's own hook stays untouched
	f.hooks[" $3"] = map[string][]string{"session-window-changed": {"display-message mine"}}
	tg, ok := queryTargets(f.run, "%1")
	if !ok || tg != (paneTargets{"$3", "@2", "%1"}) {
		t.Fatalf("targets %+v %v", tg, ok)
	}
	pid := os.Getpid()
	got := installHooks(f.run, tg, pid)
	if len(got) != len(tmuxHooks) {
		t.Fatalf("installed %d hooks, want %d: %+v", len(got), len(tmuxHooks), got)
	}
	for _, h := range got {
		if h.scope == "" && strings.HasPrefix(h.name, "session-window-changed") && h.name != "session-window-changed[1]" {
			t.Fatalf("appended at %s, want index 1 after the user's hook", h.name)
		}
	}
	// window hooks on the window, pane hooks on the pane
	if els := f.hooks["-w @2"]["window-layout-changed"]; len(els) != 1 || !strings.Contains(els[0], fmt.Sprintf("kill -USR1 %d", pid)) {
		t.Fatalf("window hook %q", els)
	}
	if els := f.hooks["-p %1"]["pane-mode-changed"]; len(els) != 1 {
		t.Fatalf("pane hook %q", els)
	}
	if c := hookCommand(pid); !strings.HasPrefix(c, `run-shell -b "`) || !strings.Contains(c, hookTag) || !strings.Contains(c, "grep -q encore") {
		t.Fatalf("hook command %q", c)
	}

	removeHooks(f.run, got, pid)
	if all := f.all(); !slices.Equal(all, []string{" $3 session-window-changed display-message mine"}) {
		t.Fatalf("after remove: %q", all)
	}
}

func TestTmuxHooksRemoveOnlyOurs(t *testing.T) {
	f := newFakeHooks()
	tg, _ := queryTargets(f.run, "%1")
	pid := os.Getpid()
	got := installHooks(f.run, tg, pid)
	// the user replaced one element with their own command meanwhile
	f.hooks[" $3"]["client-attached"][0] = "display-message replaced"
	removeHooks(f.run, got, pid)
	if all := f.all(); !slices.Equal(all, []string{" $3 client-attached display-message replaced"}) {
		t.Fatalf("after remove: %q", all)
	}
}

func TestRemoveStaleHooks(t *testing.T) {
	f := newFakeHooks()
	tg, _ := queryTargets(f.run, "%1")
	installHooks(f.run, tg, 99999)  // a crashed run (never a live pid on macOS)
	installHooks(f.run, tg, 424242) // another encore that still runs
	f.hooks[" $3"]["client-attached"] = append(f.hooks[" $3"]["client-attached"], "display-message mine")
	alive := func(pid int) bool { return pid == 424242 }
	if n := RemoveStaleHooks(f.run, "%1", alive); n != len(tmuxHooks) {
		t.Fatalf("removed %d, want %d", n, len(tmuxHooks))
	}
	for _, h := range f.all() {
		if strings.Contains(h, "99999") {
			t.Fatalf("stale hook left: %q", h)
		}
	}
	if n := len(f.all()); n != len(tmuxHooks)+1 {
		t.Fatalf("%d hooks left, want the live encore's %d and the user's", n, len(tmuxHooks))
	}
	if RemoveStaleHooks(f.run, "", alive) != 0 {
		t.Fatal("no pane: removed hooks")
	}
}

func TestQueryPane(t *testing.T) {
	row := func(f ...string) string { return strings.Join(f, "\t") }
	at := func(x, y int) PaneState { return PaneState{Origin: Cell{x, y}, Located: true} }
	cases := []struct {
		reply string
		want  PaneState
	}{
		{row("1", "1", "0", "1", "0", "80", "80", "0", "0", "0", "", "", "on", "bottom"), at(0, 0)},
		{row("1", "1", "0", "0", "0", "80", "80", "0", "0", "0", "", "", "on", "bottom"), at(0, 0)}, // another pane active, no zoom: still shown
		{row("0", "1", "0", "1", "0", "80", "80", "0", "0", "0"), PaneState{Hidden: true, Located: true}},
		{row("1", "0", "0", "1", "0", "80", "80", "0"), PaneState{Hidden: true}}, // detached
		{row("1", "1", "1", "0", "0", "80", "80", "0"), PaneState{Hidden: true}}, // another pane zoomed
		{row("1", "1", "1", "1", "0", "80", "80", "0"), PaneState{}},             // this pane zoomed
		{row("1", "1", "0", "1", "1", "80", "80", "0"), PaneState{Hidden: true}}, // copy mode
		{row("1", "1", "0", "1", "0", "40", "80", "0"), PaneState{Split: true}},  // side by side
		{row("1", "1", "0", "1", "0", "80", "80", "1"), PaneState{Split: true}},  // window larger than the client
		{row("1", "1", "0", "1", "0", "80", "80"), PaneState{}},                  // no client: window_bigger empty
		{row("1", "1", "0", "1", "0", "40", "80", "", "41", "0", "", "", "on", "top"), PaneState{Split: true, Origin: Cell{41, 1}, Located: true}},
		{row("1", "1", "0", "1", "0", "40", "80", "1", "41", "10", "20", "3", "2", "top"), PaneState{Split: true, Origin: Cell{21, 9}, Located: true}},
		{row("1", "1", "0", "1", "0", "80", "80", "", "0", "5", "", "", "off", "top"), at(0, 5)},
	}
	f := newFakeHooks()
	for _, c := range cases {
		f.pane = c.reply
		got, ok := QueryPane(f.run, "%1")
		if !ok || got != c.want {
			t.Errorf("%q: %+v %v, want %+v", c.reply, got, ok, c.want)
		}
	}
	f.pane = ""
	if _, ok := QueryPane(f.run, "%1"); ok {
		t.Error("empty reply parsed")
	}
}

// Setup on WezTerm with sixel: every image through passthrough, which it
// turns on for the pane; where tmux refuses that, every image is sixel.
func TestSetupImagesThroughPassthrough(t *testing.T) {
	env := func(k string) string {
		return map[string]string{"TMUX": "/tmp/tmux-1/default,1,0", "TMUX_PANE": "%1"}[k]
	}
	f := &fakeOptions{global: "off"}
	run := func(args ...string) (string, error) {
		if args[0] == "display-message" {
			return "WezTerm 20240203\txterm-256color\tRGB,sixel", nil
		}
		return f.run(args...)
	}
	ch, restore, err := Setup("auto", env, run)
	if err != nil || ch.Protocol != ProtoIIP || !ch.Passthrough || f.own != "on" {
		t.Fatalf("Setup = %+v, %v; own %q", ch, err, f.own)
	}
	restore()
	if f.own != "" {
		t.Fatalf("restore left own %q", f.own)
	}

	refuse := func(args ...string) (string, error) {
		if args[0] == "display-message" {
			return "WezTerm 20240203\txterm-256color\tRGB,sixel", nil
		}
		return "", fmt.Errorf("invalid option: allow-passthrough")
	}
	ch, _, err = Setup("auto", env, refuse)
	if err != nil || ch.Protocol != ProtoSixel || ch.Passthrough {
		t.Fatalf("refused: %+v, %v; want sixel without passthrough", ch, err)
	}

	noSixel := func(args ...string) (string, error) {
		if args[0] == "display-message" {
			return "WezTerm 20240203	xterm-256color	RGB", nil
		}
		return "", fmt.Errorf("invalid option: allow-passthrough")
	}
	ch, _, err = Setup("auto", env, noSixel)
	if err != nil || ch.Protocol != ProtoBlocks || ch.Passthrough {
		t.Fatalf("refused without sixel: %+v, %v; want half blocks", ch, err)
	}
}
