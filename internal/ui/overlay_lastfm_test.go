package ui

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/lastfm"
)

const (
	lfTestKey    = "5f3c2a9be1d84c07a6f0b2e93d1c7a48"
	lfTestSecret = "9a0e6b1d27c84f3fb5d6e0a1c2b3d4e5"
)

// lfAPI is a last.fm stand-in for the desktop auth calls. Keys starting
// with "bad" are unknown; getSession says "not yet" (error 14) approveAfter
// times, then approves user "rj".
type lfAPI struct {
	srv          *httptest.Server
	approveAfter int32 // -1: never
	polls        atomic.Int32
}

func newLfAPI(t *testing.T, approveAfter int32) *lfAPI {
	a := &lfAPI{approveAfter: approveAfter}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		p := r.Form
		if lastfm.Sign(p, lfTestSecret) != p.Get("api_sig") {
			w.Write([]byte(`{"error":13,"message":"Invalid method signature supplied"}`))
			return
		}
		switch p.Get("method") {
		case "auth.getToken":
			if strings.HasPrefix(p.Get("api_key"), "bad") {
				w.Write([]byte(`{"error":10,"message":"Invalid API key"}`))
				return
			}
			w.Write([]byte(`{"token":"tok"}`))
		case "auth.getSession":
			n := a.polls.Add(1)
			if a.approveAfter < 0 || n <= a.approveAfter {
				w.Write([]byte(`{"error":14,"message":"Unauthorized Token"}`))
				return
			}
			w.Write([]byte(`{"session":{"name":"rj","key":"sk","subscriber":0}}`))
		}
	}))
	t.Cleanup(a.srv.Close)
	return a
}

// lfWorld records what the overlay did outside the UI.
type lfWorld struct {
	mu      sync.Mutex
	stored  lastfm.Credentials
	saved   int
	deleted int
	opened  []string
	env     map[string]string
}

func (w *lfWorld) lfEnv(api *lfAPI, timeout time.Duration) LastfmEnv {
	return LastfmEnv{
		Auth: &lastfm.Auth{Root: api.srv.URL + "/2.0/", Interval: 5 * time.Millisecond, Timeout: timeout},
		Load: func() (lastfm.Credentials, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.stored, nil
		},
		Save: func(c lastfm.Credentials) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.stored = c
			w.saved++
			return nil
		},
		Delete: func() error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.stored = lastfm.Credentials{}
			w.deleted++
			return nil
		},
		Open: func(url string) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.opened = append(w.opened, url)
			return nil
		},
		Getenv: func(k string) string { return w.env[k] },
	}
}

func (w *lfWorld) snapshot() (stored lastfm.Credentials, saved, deleted int, opened []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stored, w.saved, w.deleted, slices.Clone(w.opened)
}

func lfScrobbler(t *testing.T, creds lastfm.Credentials) *lastfm.Scrobbler {
	sc := lastfm.NewScrobbler(creds, filepath.Join(t.TempDir(), "queue.json"), nil, lastfm.WithTick(0), lastfm.WithAPIRoot("http://127.0.0.1:1/"))
	t.Cleanup(func() { sc.Close() })
	return sc
}

func lfNew(t *testing.T, sc *lastfm.Scrobbler, env LastfmEnv) (*LastfmOverlay, *Ctx, *ovRecorder) {
	t.Helper()
	c, h := ovTestCtx(t)
	c.Focused = true
	o := NewLastfmSetupEnv(sc, env).(*LastfmOverlay)
	o.Rect(140, 42)
	o.started = true // tests load explicitly
	return o, c, h
}

func lfTypeInto(o *LastfmOverlay, c *Ctx, e lfElem, text string) {
	o.setFocus(e)
	ovType(o, c, text)
}

func lfText(o *LastfmOverlay, c *Ctx) string { return ansi.Strip(o.Render(c)) }

func TestLastfmRendersExactSize(t *testing.T) {
	api := newLfAPI(t, 0)
	w := &lfWorld{}
	ready := lastfm.Credentials{APIKey: lfTestKey, Secret: lfTestSecret, SessionKey: "sk", Username: "rj"}
	for _, s := range ovScreens {
		o, c, _ := lfNew(t, lfScrobbler(t, lastfm.Credentials{}), w.lfEnv(api, 0))
		ovCheckSize(t, "lastfm setup", o, c, s[0], s[1])
		o.mode, o.since = lfWaiting, c.Now
		ovCheckSize(t, "lastfm waiting", o, c, s[0], s[1])
		o, c, _ = lfNew(t, lfScrobbler(t, ready), w.lfEnv(api, 0))
		ovCheckSize(t, "lastfm connected", o, c, s[0], s[1])
	}
}

func TestLastfmConnectsAfterApproval(t *testing.T) {
	api := newLfAPI(t, 2)
	w := &lfWorld{}
	sc := lfScrobbler(t, lastfm.Credentials{})
	o, c, _ := lfNew(t, sc, w.lfEnv(api, time.Second))

	if !strings.Contains(lfText(o, c), "1  Create an API account") {
		t.Fatalf("setup:\n%s", lfText(o, c))
	}
	cmd := o.activate(lfOpen, c)
	ovSettle(o, c, cmd)
	if o.focus != lfKey || !o.stepDone(1) {
		t.Fatalf("after opening the page: focus %d", o.focus)
	}
	ovType(o, c, lfTestKey)
	o.Action(keymap.InputNextScope, 1, c)
	ovType(o, c, lfTestSecret)
	if !strings.Contains(lfText(o, c), strings.Repeat("•", 32)) {
		t.Fatal("the secret isn't masked")
	}
	_, cmd = o.Action(keymap.InputSubmit, 1, c)
	if o.mode != lfChecking {
		t.Fatalf("mode %d after connect", o.mode)
	}
	ovDrive(t, o, c, cmd, func() bool { return o.mode == lfConnected || o.mode == lfSetup })
	if o.mode != lfConnected {
		t.Fatalf("not connected: %q", o.err)
	}
	stored, saved, _, opened := w.snapshot()
	if saved != 1 || stored.Username != "rj" || stored.SessionKey != "sk" || stored.APIKey != lfTestKey {
		t.Fatalf("saved %d: %+v", saved, stored)
	}
	if st := sc.Status(); st.User != "rj" || !st.Enabled {
		t.Fatalf("scrobbler status %+v", st)
	}
	if api.polls.Load() < 3 {
		t.Fatalf("approved after %d polls", api.polls.Load())
	}
	if len(opened) != 2 || opened[0] != lastfm.AccountCreateURL || !strings.Contains(opened[1], "token=tok") {
		t.Fatalf("opened %v", opened)
	}
	if !strings.Contains(lfText(o, c), "Scrobbling as rj") {
		t.Fatalf("connected:\n%s", lfText(o, c))
	}
}

func TestLastfmInvalidKey(t *testing.T) {
	api := newLfAPI(t, 0)
	w := &lfWorld{}
	o, c, _ := lfNew(t, lfScrobbler(t, lastfm.Credentials{}), w.lfEnv(api, time.Second))
	lfTypeInto(o, c, lfKey, "bad"+lfTestKey[3:])
	lfTypeInto(o, c, lfSecret, lfTestSecret)
	cmd := o.activate(lfConnect, c)
	ovDrive(t, o, c, cmd, func() bool { return o.mode == lfSetup })
	if o.errStep != 2 || o.focus != lfKey || !strings.Contains(lfText(o, c), "doesn't recognise this API key") {
		t.Fatalf("step %d focus %d err %q", o.errStep, o.focus, o.err)
	}
	ovType(o, c, "x")
	if o.err != "" {
		t.Fatal("editing the key keeps the old error")
	}

	o.key.SetValue(lfTestKey)
	o.secret.SetValue("0" + lfTestSecret[1:])
	cmd = o.activate(lfConnect, c)
	ovDrive(t, o, c, cmd, func() bool { return o.mode == lfSetup })
	if !strings.Contains(o.err, "secret doesn't belong") {
		t.Fatalf("wrong secret: %q", o.err)
	}
}

func TestLastfmNeedsBothFields(t *testing.T) {
	o, c, _ := lfNew(t, lfScrobbler(t, lastfm.Credentials{}), (&lfWorld{}).lfEnv(newLfAPI(t, 0), 0))
	lfTypeInto(o, c, lfKey, lfTestKey)
	if cmd := o.activate(lfConnect, c); cmd != nil || o.mode != lfSetup {
		t.Fatal("connected without a secret")
	}
	if o.focus != lfSecret || o.errStep != 2 {
		t.Fatalf("focus %d step %d", o.focus, o.errStep)
	}
}

func TestLastfmApprovalTimesOut(t *testing.T) {
	api := newLfAPI(t, -1)
	w := &lfWorld{}
	o, c, _ := lfNew(t, lfScrobbler(t, lastfm.Credentials{}), w.lfEnv(api, 60*time.Millisecond))
	o.key.SetValue(lfTestKey)
	o.secret.SetValue(lfTestSecret)
	cmd := o.activate(lfConnect, c)
	waited := false
	ovDrive(t, o, c, cmd, func() bool {
		if o.mode == lfWaiting && o.focus == lfReopen && strings.Contains(lfText(o, c), "Waiting for approval") {
			waited = true
		}
		return o.mode == lfSetup
	})
	if !waited {
		t.Fatal("never showed the wait")
	}
	if !strings.Contains(o.err, "timed out") || o.errStep != 3 || o.focus != lfConnect {
		t.Fatalf("err %q step %d focus %d", o.err, o.errStep, o.focus)
	}
	if _, saved, _, _ := w.snapshot(); saved != 0 {
		t.Fatal("saved after a timeout")
	}
}

func TestLastfmEscCancelsTheWait(t *testing.T) {
	api := newLfAPI(t, -1)
	w := &lfWorld{}
	o, c, h := lfNew(t, lfScrobbler(t, lastfm.Credentials{}), w.lfEnv(api, time.Minute))
	o.key.SetValue(lfTestKey)
	o.secret.SetValue(lfTestSecret)
	ovDrive(t, o, c, o.activate(lfConnect, c), func() bool { return o.mode == lfWaiting && api.polls.Load() > 0 })
	o.Action(keymap.InputCancel, 1, c)
	if o.cancel != nil || h.last() != "CloseOverlay" {
		t.Fatalf("esc: %v", h.calls)
	}
	n := api.polls.Load()
	time.Sleep(50 * time.Millisecond)
	if m := api.polls.Load(); m > n+1 {
		t.Fatalf("still polling after esc: %d → %d", n, m)
	}
}

func TestLastfmEnvironmentSkipsTheKeyStep(t *testing.T) {
	api := newLfAPI(t, 0)
	w := &lfWorld{env: map[string]string{lastfm.EnvKey: lfTestKey, lastfm.EnvSecret: lfTestSecret}}
	o, c, _ := lfNew(t, lfScrobbler(t, lastfm.Credentials{}), w.lfEnv(api, time.Second))
	if !slices.Equal(o.elems(), []lfElem{lfConnect}) || o.focus != lfConnect {
		t.Fatalf("elems %v focus %d", o.elems(), o.focus)
	}
	out := lfText(o, c)
	if !strings.Contains(out, "from "+lastfm.EnvKey) || !strings.Contains(out, "✓  Paste your API key") {
		t.Fatalf("env setup:\n%s", out)
	}
	ovDrive(t, o, c, o.activate(lfConnect, c), func() bool { return o.mode == lfConnected })
}

func TestLastfmPaste(t *testing.T) {
	o, c, _ := lfNew(t, lfScrobbler(t, lastfm.Credentials{}), (&lfWorld{}).lfEnv(newLfAPI(t, 0), 0))
	o.Update(tea.PasteMsg{Content: "Application name\tencore\nAPI key\t" + lfTestKey + "\nShared secret\t" + lfTestSecret + "\n"}, c)
	if k, s := o.values(); k != lfTestKey || s != lfTestSecret || o.focus != lfConnect {
		t.Fatalf("block paste: %q %q focus %d", k, s, o.focus)
	}

	o, c, _ = lfNew(t, lfScrobbler(t, lastfm.Credentials{}), (&lfWorld{}).lfEnv(newLfAPI(t, 0), 0))
	o.Update(tea.PasteMsg{Content: "  " + lfTestKey + "\n"}, c)
	if k, _ := o.values(); k != lfTestKey || o.focus != lfKey {
		t.Fatalf("paste with no field focused: %q focus %d", k, o.focus)
	}
	o.setFocus(lfSecret)
	o.Update(tea.PasteMsg{Content: "Shared secret: " + lfTestSecret}, c)
	if _, s := o.values(); s != lfTestSecret {
		t.Fatalf("secret %q", s)
	}
}

func TestLastfmPrefillsStoredKey(t *testing.T) {
	w := &lfWorld{stored: lastfm.Credentials{APIKey: lfTestKey, Secret: lfTestSecret}}
	o, c, _ := lfNew(t, lfScrobbler(t, lastfm.Credentials{}), w.lfEnv(newLfAPI(t, 0), 0))
	o.started = false
	ovDrive(t, o, c, o.Init(c), func() bool { k, _ := o.values(); return k != "" })
	if o.focus != lfConnect {
		t.Fatalf("focus %d, want Connect", o.focus)
	}
}

func TestLastfmConnectedControls(t *testing.T) {
	ready := lastfm.Credentials{APIKey: lfTestKey, Secret: lfTestSecret, SessionKey: "sk", Username: "rj"}
	sc := lfScrobbler(t, ready)
	w := &lfWorld{}
	o, c, _ := lfNew(t, sc, w.lfEnv(newLfAPI(t, 0), 0))
	out := lfText(o, c)
	if !strings.Contains(out, "Scrobbling as rj") || !strings.Contains(out, "Up to date") {
		t.Fatalf("connected:\n%s", out)
	}
	o.Key(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, c)
	if sc.Status().Enabled || !strings.Contains(lfText(o, c), "Off until encore quits") {
		t.Fatal("scrobbling still on")
	}
	o.activate(lfToggle, c)
	if !sc.Status().Enabled {
		t.Fatal("scrobbling still off")
	}

	o.Key(tea.KeyPressMsg{Code: 'j', Text: "j"}, c)
	o.Key(tea.KeyPressMsg{Code: 'j', Text: "j"}, c)
	if o.focus != lfDisconnect {
		t.Fatalf("focus %d", o.focus)
	}
	if _, cmd := o.Action(keymap.InputSubmit, 1, c); cmd != nil || !o.armed {
		t.Fatal("disconnected without asking")
	}
	_, cmd := o.Action(keymap.InputSubmit, 1, c)
	ovDrive(t, o, c, cmd, func() bool { return o.mode == lfSetup })
	if _, _, deleted, _ := w.snapshot(); deleted != 1 || sc.Status().User != "" {
		t.Fatalf("deleted %d, scrobbler %+v", deleted, sc.Status())
	}
}

func TestLastfmReauthorizeAndMouse(t *testing.T) {
	ready := lastfm.Credentials{APIKey: lfTestKey, Secret: lfTestSecret, SessionKey: "sk", Username: "rj"}
	api := newLfAPI(t, 0)
	w := &lfWorld{stored: ready}
	o, c, _ := lfNew(t, lfScrobbler(t, ready), w.lfEnv(api, time.Second))
	o.started = false
	ovDrive(t, o, c, o.Init(c), func() bool { k, _ := o.values(); return k != "" })
	o.Render(c)
	y := slices.Index(o.hits, lfReauth)
	if y < 0 {
		t.Fatal("no Re-authorize row")
	}
	o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 5, Y: y}, c)
	if o.mode != lfSetup || o.focus != lfConnect {
		t.Fatalf("mode %d focus %d", o.mode, o.focus)
	}
	o.Rect(140, 42)
	o.Render(c)
	y = slices.Index(o.hits, lfConnect)
	cmd := o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 8, Y: y}, c)
	ovDrive(t, o, c, cmd, func() bool { return o.mode == lfConnected })
}

func TestLastfmDemoFlowConnects(t *testing.T) {
	c, _ := ovTestCtx(t)
	demoLastfmWorld = &demoLastfm{}
	env := demoLastfmEnv()
	env.Auth.Interval = 5 * time.Millisecond
	o := NewLastfmSetupEnv(nil, env).(*LastfmOverlay)
	o.Rect(140, 42)
	ovDrive(t, o, c, o.Init(c), func() bool { return o.started })
	o.key.SetValue("0123456789abcdef0123456789abcdef")
	o.secret.SetValue("fedcba9876543210fedcba9876543210")
	ovDrive(t, o, c, o.connect(), func() bool { return o.mode == lfConnected })
	if o.user != "encore-demo" {
		t.Fatalf("user %q", o.user)
	}
	if creds, _ := env.Load(); creds.SessionKey != "demo" {
		t.Fatalf("stored %+v", creds)
	}
}
