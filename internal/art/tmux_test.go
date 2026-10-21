package art

import (
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fakeTmux answers tmux commands by their first word; unknown commands
// fail like a tmux without a server.
func fakeTmux(replies map[string]string) TmuxRunner {
	return func(args ...string) (string, error) {
		key := args[0]
		if key == "show-environment" && slices.Contains(args, "-g") {
			key = "show-environment -g"
		}
		if out, ok := replies[key]; ok {
			return out, nil
		}
		return "", errors.New("no server running")
	}
}

func TestQueryTmux(t *testing.T) {
	none := envOf()
	cases := []struct {
		name    string
		env     func(string) string
		replies map[string]string
		want    TmuxClient
		proto   Protocol
		pass    bool
	}{
		{"wezterm with sixel", none, map[string]string{
			"display-message": "WezTerm 20240203-110809-5046fc22\txterm-256color\tbpaste,clipboard,focus,RGB,sixel,sync,title",
		}, TmuxClient{OuterWezTerm, true}, ProtoIIP, true},
		{"wezterm without sixel", none, map[string]string{
			"display-message": "WezTerm 20240203\twezterm\tRGB,sync",
		}, TmuxClient{OuterWezTerm, false}, ProtoIIP, true},
		{"iterm2 by XTVERSION", none, map[string]string{
			"display-message": "iTerm2 3.5.4\txterm-256color\tRGB,sixel",
		}, TmuxClient{OuterITerm, true}, ProtoIIP, true},
		{"kitty by TERM", none, map[string]string{
			"display-message": "\txterm-kitty\tRGB,extkeys",
		}, TmuxClient{OuterKitty, false}, ProtoKitty, true},
		{"ghostty", none, map[string]string{
			"display-message": "ghostty 1.1.3\txterm-ghostty\tRGB",
		}, TmuxClient{OuterGhostty, false}, ProtoKitty, true},
		{"unknown type, session TERM_PROGRAM", none, map[string]string{
			"display-message":  "\txterm-256color\tRGB,sixel",
			"show-environment": "TERM_PROGRAM=WezTerm",
		}, TmuxClient{OuterWezTerm, true}, ProtoIIP, true},
		{"unknown type, global TERM_PROGRAM", none, map[string]string{
			"display-message":     "\txterm-256color\tRGB",
			"show-environment":    "-TERM_PROGRAM",
			"show-environment -g": "TERM_PROGRAM=iTerm.app",
		}, TmuxClient{OuterITerm, false}, ProtoIIP, true},
		{"apple terminal", none, map[string]string{
			"display-message":     "\txterm-256color\tRGB",
			"show-environment -g": "TERM_PROGRAM=Apple_Terminal",
		}, TmuxClient{"", false}, ProtoBlocks, false},
		{"override", envOf("ENCORE_OUTER_TERM", "WezTerm"), map[string]string{
			"display-message": "\txterm-256color\tRGB,sixel",
		}, TmuxClient{OuterWezTerm, true}, ProtoIIP, true},
		{"no server", none, nil, TmuxClient{}, ProtoBlocks, false},
	}
	for _, c := range cases {
		got := QueryTmux(c.env, fakeTmux(c.replies), "%3")
		if got != c.want {
			t.Errorf("%s: QueryTmux = %+v, want %+v", c.name, got, c.want)
		}
		ch := detectTmux(got)
		if ch.Protocol != c.proto || ch.Passthrough != c.pass || !ch.Tmux {
			t.Errorf("%s: detectTmux = %+v, want %v passthrough %v", c.name, ch, c.proto, c.pass)
		}
	}
}

func TestQueryTmuxTargetsPane(t *testing.T) {
	var calls [][]string
	run := func(args ...string) (string, error) {
		calls = append(calls, args)
		return "WezTerm 1\txterm\tsixel", nil
	}
	QueryTmux(envOf(), run, "%7")
	if len(calls) != 1 || !slices.Equal(calls[0][:4], []string{"display-message", "-p", "-t", "%7"}) {
		t.Fatalf("calls = %q", calls)
	}
}

// fakeOptions is a tmux with one pane option, allow-passthrough, and its
// inherited value, plus pane user options (@name).
type fakeOptions struct {
	global string
	own    string // "" when the pane has none of its own
	user   map[string]string
	log    []string
}

func (f *fakeOptions) run(args ...string) (string, error) {
	f.log = append(f.log, strings.Join(args, " "))
	unset := slices.Contains(args, "-u")
	name, value := args[len(args)-1], ""
	if args[0] == "set-option" && !unset {
		name, value = args[len(args)-2], args[len(args)-1]
	}
	if strings.HasPrefix(name, "@") {
		if f.user == nil {
			f.user = map[string]string{}
		}
		switch {
		case args[0] == "show-options":
			return f.user[name], nil
		case unset:
			delete(f.user, name)
		default:
			f.user[name] = value
		}
		return "", nil
	}
	switch {
	case args[0] == "show-options" && slices.Contains(args, "-A"):
		if f.own != "" {
			return f.own, nil
		}
		return f.global, nil
	case args[0] == "show-options":
		return f.own, nil
	case args[0] == "set-option" && unset:
		f.own = ""
		return "", nil
	case args[0] == "set-option":
		f.own = value
		return "", nil
	}
	return "", errors.New("unknown command")
}

func TestAllowPassthrough(t *testing.T) {
	// off everywhere: turned on for the pane, then unset again
	f := &fakeOptions{global: "off"}
	restore, err := AllowPassthrough(f.run, "%1")
	if err != nil || f.own != "on" {
		t.Fatalf("enable: own=%q err=%v", f.own, err)
	}
	restore()
	if f.own != "" || !strings.Contains(f.log[len(f.log)-1], "-u") {
		t.Fatalf("restore left own=%q, last command %q", f.own, f.log[len(f.log)-1])
	}

	// the pane had its own "off": put back
	f = &fakeOptions{global: "on", own: "off"}
	restore, _ = AllowPassthrough(f.run, "%1")
	if f.own != "on" {
		t.Fatalf("own off: not enabled, own=%q", f.own)
	}
	restore()
	if f.own != "off" {
		t.Fatalf("own off: restored to %q", f.own)
	}

	// already on (or all): nothing is changed
	for _, v := range []string{"on", "all"} {
		f = &fakeOptions{global: v}
		restore, _ = AllowPassthrough(f.run, "%1")
		restore()
		for _, l := range f.log {
			if strings.HasPrefix(l, "set-option") {
				t.Fatalf("global %s: changed an option: %q", v, l)
			}
		}
	}

	if _, err := AllowPassthrough(f.run, ""); err == nil {
		t.Fatal("no pane: want an error")
	}
}

// While encore holds allow-passthrough on, the pane carries a marker with its
// pid and the pane's own value; restore removes it. A marker whose process is
// gone (a crash) is put back by the next run; a live one is left alone.
func TestStalePassthroughMarker(t *testing.T) {
	f := &fakeOptions{global: "off", own: "off"}
	restore, err := AllowPassthrough(f.run, "%1")
	if err != nil {
		t.Fatal(err)
	}
	want := strconv.Itoa(os.Getpid()) + " off"
	if got := f.user[passthroughMarker]; got != want || f.own != "on" {
		t.Fatalf("marker %q own %q, want %q and on", got, f.own, want)
	}
	restore()
	if _, ok := f.user[passthroughMarker]; ok || f.own != "off" {
		t.Fatalf("after restore: marker %v own %q", f.user, f.own)
	}

	// A crashed run left the pane on, with no option of its own before. (macOS
	// pids stop at 99998, so 99999 is never alive.)
	f = &fakeOptions{global: "off", own: "on", user: map[string]string{passthroughMarker: "99999 "}}
	dead := func(int) bool { return false }
	if !RestoreStalePassthrough(f.run, "%1", dead) {
		t.Fatal("stale marker not restored")
	}
	if _, ok := f.user[passthroughMarker]; ok || f.own != "" {
		t.Fatalf("stale: marker %v own %q, want both gone", f.user, f.own)
	}

	// Its own value comes back.
	f = &fakeOptions{global: "on", own: "on", user: map[string]string{passthroughMarker: "99999 off"}}
	RestoreStalePassthrough(f.run, "%1", dead)
	if f.own != "off" {
		t.Fatalf("stale with own: own %q, want off", f.own)
	}

	// Another encore that still runs keeps its hold.
	f = &fakeOptions{global: "off", own: "on", user: map[string]string{passthroughMarker: "99999 "}}
	if RestoreStalePassthrough(f.run, "%1", func(int) bool { return true }) || f.own != "on" {
		t.Fatalf("live marker: own %q", f.own)
	}

	// No marker: nothing changes.
	f = &fakeOptions{global: "off", own: "on"}
	if RestoreStalePassthrough(f.run, "%1", dead) || f.own != "on" {
		t.Fatalf("no marker: own %q", f.own)
	}

	// Setup cleans up first, inside tmux only.
	f = &fakeOptions{global: "off", own: "on", user: map[string]string{passthroughMarker: "99999 "}}
	env := func(k string) string {
		return map[string]string{"TMUX": "/tmp/tmux-1/default,1,0", "TMUX_PANE": "%1"}[k]
	}
	_, restore, _ = Setup("blocks", env, f.run)
	restore()
	if f.own != "" {
		t.Fatalf("Setup left own %q", f.own)
	}
}
