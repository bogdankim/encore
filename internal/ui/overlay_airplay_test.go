package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
)

// ovDrive runs cmd and every command the overlay returns in response,
// feeding messages back through Update, until done reports true or the
// timeout passes. Commands run concurrently, so a slow one (a tick) doesn't
// hold up the rest.
func ovDrive(t *testing.T, o Overlay, c *Ctx, cmd tea.Cmd, done func() bool) {
	t.Helper()
	msgs := make(chan tea.Msg, 64)
	var spawn func(tea.Cmd)
	spawn = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		go func() {
			msg := cmd()
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, c := range b {
					spawn(c)
				}
				return
			}
			if msg != nil {
				msgs <- msg
			}
		}()
	}
	spawn(cmd)
	deadline := time.After(3 * time.Second)
	for !done() {
		select {
		case msg := <-msgs:
			spawn(o.Update(msg, c))
		case <-deadline:
			t.Fatal("timed out driving the overlay")
		}
	}
}

func apNew(t *testing.T, client bridge.Client) (*AirPlayOverlay, *Ctx, *ovRecorder) {
	t.Helper()
	c, h := ovTestCtx(t)
	c.Now = time.Unix(1_760_000_000, 0)
	o := NewAirPlay(Services{Client: client}).(*AirPlayOverlay)
	o.Rect(140, 42)
	ovDrive(t, o, c, o.Init(c), func() bool { return o.loaded })
	o.Rect(140, 42)
	o.Render(c)
	return o, c, h
}

func apFake(t *testing.T) *bridge.Fake {
	f := bridge.NewFakeWithClock(1, time.Now)
	t.Cleanup(func() { f.Close() })
	return f
}

func apSelected(t *testing.T, f bridge.Client) []string {
	t.Helper()
	devs, err := f.AirPlayDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range devs {
		if d.Selected {
			names = append(names, d.Name)
		}
	}
	return names
}

func apShown(o *AirPlayOverlay) []string {
	var names []string
	for _, d := range o.devs {
		if d.Selected {
			names = append(names, d.Name)
		}
	}
	return names
}

func apIndex(o *AirPlayOverlay, name string) int {
	return slices.IndexFunc(o.devs, func(d bridge.AirPlayDevice) bool { return d.Name == name })
}

func TestAirPlayRendersExactSize(t *testing.T) {
	f := apFake(t)
	c, _ := ovTestCtx(t)
	for _, s := range ovScreens {
		ovCheckSize(t, "airplay empty", NewAirPlay(Services{Client: f}), c, s[0], s[1])
		o, c, _ := apNew(t, f)
		ovCheckSize(t, "airplay", o, c, s[0], s[1])
	}
}

func TestAirPlayOpensOnTheSelectedDevice(t *testing.T) {
	o, c, _ := apNew(t, apFake(t))
	if len(o.devs) != 4 || o.devs[o.cur.Sel].Name != "MacBook Pro" {
		t.Fatalf("cursor on %d of %d devices", o.cur.Sel, len(o.devs))
	}
	out := ansi.Strip(o.Render(c))
	for _, want := range []string{"Speaker Volume", "MacBook Pro  Computer", "Living Room  HomePod", "Kitchen      Speaker", "Bedroom TV   Apple TV", "unavailable", "100"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q", want)
		}
	}
	// the kinds line up in one column
	col := -1
	for _, line := range strings.Split(out, "\n") {
		for _, k := range []string{"Computer", "HomePod", "Speaker", "Apple TV"} {
			if i := strings.Index(line, k); i >= 0 && !strings.Contains(line, "Volume") {
				if x := ansi.StringWidth(line[:i]); col >= 0 && x != col {
					t.Errorf("%s at column %d, others at %d", k, x, col)
				} else {
					col = x
				}
			}
		}
	}
	o.Action(keymap.NavBottom, 1, c)
	if o.devs[o.cur.Sel].Name != "Kitchen" {
		t.Fatalf("cursor reached %s; the unavailable TV must be skipped", o.devs[o.cur.Sel].Name)
	}
}

func TestAirPlayEnterPlaysOnlyThere(t *testing.T) {
	f := apFake(t)
	o, c, _ := apNew(t, f)
	o.Action(keymap.NavDown, 1, c)
	_, cmd := o.Action(keymap.InputSubmit, 1, c)
	if got := apShown(o); !slices.Equal(got, []string{"Living Room"}) {
		t.Fatalf("shown before the call returns: %v", got)
	}
	ovDrive(t, o, c, cmd, func() bool { return o.sel.inflight == 0 })
	if got := apSelected(t, f); !slices.Equal(got, []string{"Living Room"}) {
		t.Fatalf("Music plays on %v", got)
	}
}

func TestAirPlaySpaceAddsAndKeepsTheLastOutput(t *testing.T) {
	f := apFake(t)
	o, c, _ := apNew(t, f)
	o.Action(keymap.NavDown, 2, c)
	cmd := o.Key(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, c)
	ovDrive(t, o, c, cmd, func() bool { return o.sel.inflight == 0 })
	if got := apSelected(t, f); !slices.Equal(got, []string{"MacBook Pro", "Kitchen"}) {
		t.Fatalf("Music plays on %v", got)
	}
	cmd = o.Key(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, c)
	ovDrive(t, o, c, cmd, func() bool { return o.sel.inflight == 0 })
	o.Action(keymap.NavTop, 1, c)
	if cmd := o.Key(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, c); cmd != nil {
		t.Fatal("removing the only output made a call")
	}
	if !strings.Contains(o.note, "at least one output") {
		t.Fatalf("note %q", o.note)
	}
	if got := apSelected(t, f); !slices.Equal(got, []string{"MacBook Pro"}) {
		t.Fatalf("Music plays on %v", got)
	}
}

func TestAirPlayMouse(t *testing.T) {
	f := apFake(t)
	o, c, _ := apNew(t, f)
	kitchen := o.listY + apIndex(o, "Kitchen")
	cmd := o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 2, Y: kitchen}, c)
	ovDrive(t, o, c, cmd, func() bool { return o.sel.inflight == 0 })
	if got := apSelected(t, f); !slices.Equal(got, []string{"MacBook Pro", "Kitchen"}) {
		t.Fatalf("clicking the mark: Music plays on %v", got)
	}
	tv := o.listY + apIndex(o, "Bedroom TV")
	if cmd := o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 8, Y: tv}, c); cmd != nil {
		t.Fatal("clicking an unavailable device made a call")
	}
	living := o.listY + apIndex(o, "Living Room")
	cmd = o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 8, Y: living}, c)
	ovDrive(t, o, c, cmd, func() bool { return o.sel.inflight == 0 })
	if got := apSelected(t, f); !slices.Equal(got, []string{"MacBook Pro", "Living Room", "Kitchen"}) {
		t.Fatalf("clicking the name: Music plays on %v", got)
	}
	kitchen = o.listY + apIndex(o, "Kitchen")
	cmd = o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: 8, Y: kitchen}, c)
	ovDrive(t, o, c, cmd, func() bool { return o.sel.inflight == 0 })
	if got := apSelected(t, f); !slices.Equal(got, []string{"MacBook Pro", "Living Room"}) {
		t.Fatalf("clicking a playing device again: Music plays on %v", got)
	}
	cmd = o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseRight, X: 8, Y: living}, c)
	ovDrive(t, o, c, cmd, func() bool { return o.sel.inflight == 0 })
	if got := apSelected(t, f); !slices.Equal(got, []string{"Living Room"}) {
		t.Fatalf("right-clicking: Music plays on %v", got)
	}
}

// apCounting counts SetAirPlayVolume calls.
type apCounting struct {
	*bridge.Fake
	mu    sync.Mutex
	calls []int
}

func (a *apCounting) SetAirPlayVolume(ctx context.Context, id, v int) error {
	a.mu.Lock()
	a.calls = append(a.calls, v)
	a.mu.Unlock()
	return a.Fake.SetAirPlayVolume(ctx, id, v)
}

func (a *apCounting) sent() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}

func (a *apCounting) reset() {
	a.mu.Lock()
	a.calls = nil
	a.mu.Unlock()
}

func apVolume(t *testing.T, f bridge.Client, name string) int {
	devs, _ := f.AirPlayDevices(context.Background())
	for _, d := range devs {
		if d.Name == name {
			return d.Volume
		}
	}
	t.Fatalf("no %s", name)
	return 0
}

func TestAirPlayVolume(t *testing.T) {
	f := &apCounting{Fake: apFake(t)}
	o, c, _ := apNew(t, f)
	cmd := o.Key(tea.KeyPressMsg{Code: 'h', Text: "h"}, c)
	if o.devs[0].Volume != 95 {
		t.Fatalf("shown volume %d after h", o.devs[0].Volume)
	}
	ovDrive(t, o, c, cmd, func() bool { return o.vol[o.devs[0].PID].inflight == 0 })
	if v := apVolume(t, f, "MacBook Pro"); v != 95 {
		t.Fatalf("volume %d, want 95", v)
	}

	x0, x1 := o.meter()
	y := o.listY
	o.Mouse(MouseEvent{Kind: MousePress, Button: tea.MouseLeft, X: x0 + 1, Y: y}, c)
	o.Mouse(MouseEvent{Kind: MouseMotion, Button: tea.MouseLeft, X: x0 + 3, Y: y}, c)
	if got := o.devs[0].Volume; got != 40 {
		t.Fatalf("dragging shows %d, want 40", got)
	}
	f.reset()
	cmd = o.Mouse(MouseEvent{Kind: MouseRelease, Button: tea.MouseLeft, X: x1 + 5, Y: y + 3}, c)
	ovDrive(t, o, c, cmd, func() bool { return o.vol[o.devs[0].PID].inflight == 0 })
	if v, calls := apVolume(t, f, "MacBook Pro"), f.sent(); v != 100 || len(calls) != 1 {
		t.Fatalf("after the drag: volume %d with calls %v, want one call for 100", v, calls)
	}

	cmd = o.Mouse(MouseEvent{Kind: MouseWheel, Button: tea.MouseWheelDown, X: x0 + 2, Y: y}, c)
	ovDrive(t, o, c, cmd, func() bool { return o.vol[o.devs[0].PID].inflight == 0 })
	if v := apVolume(t, f, "MacBook Pro"); v != 95 {
		t.Fatalf("after the wheel: volume %d", v)
	}

	o.Action(keymap.NavDown, 1, c)
	if cmd := o.Key(tea.KeyPressMsg{Code: 'l', Text: "l"}, c); cmd != nil {
		t.Fatal("changed the volume of a device that isn't playing")
	}
}

func TestAirPlayVolumeLastValueWins(t *testing.T) {
	f := &apCounting{Fake: apFake(t)}
	o, c, _ := apNew(t, f)
	first := o.nudge(-5, c)
	second := o.nudge(-5, c)
	ovDrive(t, o, c, tea.Batch(second, first), func() bool { return o.vol[o.devs[0].PID].inflight == 0 })
	if v, calls := apVolume(t, f, "MacBook Pro"), f.sent(); v != 90 || len(calls) != 1 {
		t.Fatalf("volume %d after calls %v, want a single call for 90", v, calls)
	}
}

// apRenumbering renumbers every device after the first list, like Music
// does after a restart.
type apRenumbering struct {
	*bridge.Fake
	mu     sync.Mutex
	lists  int
	stale  bool
	ids    []int
	failOn string
}

func (a *apRenumbering) AirPlayDevices(ctx context.Context) ([]bridge.AirPlayDevice, error) {
	devs, err := a.Fake.AirPlayDevices(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lists++
	if a.stale {
		return devs, err
	}
	for i := range devs {
		devs[i].ID += 1000
	}
	return devs, err
}

func (a *apRenumbering) SelectAirPlay(ctx context.Context, ids []int) error {
	a.mu.Lock()
	a.ids = slices.Clone(ids)
	fail := a.failOn != ""
	a.mu.Unlock()
	if fail {
		return errors.New("timed out")
	}
	var real []int
	for _, id := range ids {
		if id < 1000 {
			return &bridge.Error{Code: bridge.ErrNotFound, Message: "Can't get AirPlay device id."}
		}
		real = append(real, id-1000)
	}
	return a.Fake.SelectAirPlay(ctx, real)
}

func (a *apRenumbering) setStale(v bool) {
	a.mu.Lock()
	a.stale = v
	a.mu.Unlock()
}

func (a *apRenumbering) state() (lists int, ids []int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lists, slices.Clone(a.ids)
}

func TestAirPlayMapsByPIDWhenIDsChange(t *testing.T) {
	f := &apRenumbering{Fake: apFake(t), stale: true}
	o, c, _ := apNew(t, f)
	f.setStale(false) // the ids o knows are now wrong
	o.Action(keymap.NavDown, 1, c)
	_, cmd := o.Action(keymap.InputSubmit, 1, c)
	ovDrive(t, o, c, cmd, func() bool { return o.sel.inflight == 0 })
	if got := apSelected(t, f.Fake); !slices.Equal(got, []string{"Living Room"}) {
		t.Fatalf("Music plays on %v", got)
	}
	if lists, ids := f.state(); lists != 2 || !slices.Equal(ids, []int{29274 + 1000}) {
		t.Fatalf("lists %d, last ids %v: want a re-list and the new id", lists, ids)
	}
	if o.real[1].ID != 29274+1000 {
		t.Fatalf("kept stale ids: %v", o.real[1].ID)
	}
}

func TestAirPlayFailureRevertsAndSaysWho(t *testing.T) {
	f := &apRenumbering{Fake: apFake(t), stale: true, failOn: "all"}
	o, c, _ := apNew(t, f)
	o.Action(keymap.NavDown, 1, c)
	_, cmd := o.Action(keymap.InputSubmit, 1, c)
	ovDrive(t, o, c, cmd, func() bool {
		lists, _ := f.state()
		return o.sel.inflight == 0 && lists >= 2
	})
	if o.note != "Living Room didn't respond" {
		t.Fatalf("note %q", o.note)
	}
	if got := apShown(o); !slices.Equal(got, []string{"MacBook Pro"}) {
		t.Fatalf("still shows %v after the failure", got)
	}
	if !strings.Contains(o.Render(c), "Living Room didn't respond") {
		t.Fatal("note not rendered")
	}
}

func TestAirPlayKeepsOptimisticStateOverStaleEvents(t *testing.T) {
	f := apFake(t)
	o, c, _ := apNew(t, f)
	stale := slices.Clone(o.real)
	o.Action(keymap.NavDown, 1, c)
	_, cmd := o.Action(keymap.InputSubmit, 1, c)
	o.Update(BridgeMsg{Event: bridge.AirPlayEvent{Devices: stale}}, c)
	if got := apShown(o); !slices.Equal(got, []string{"Living Room"}) {
		t.Fatalf("a stale event during the call reverted the choice: %v", got)
	}
	ovDrive(t, o, c, cmd, func() bool { return o.sel.inflight == 0 })
	o.Update(bridge.AirPlayEvent{Devices: stale}, c)
	if got := apShown(o); !slices.Equal(got, []string{"Living Room"}) {
		t.Fatalf("a stale event right after the call reverted the choice: %v", got)
	}
	c.Now = c.Now.Add(2 * airplayGrace)
	o.Update(bridge.AirPlayEvent{Devices: stale}, c)
	if got := apShown(o); !slices.Equal(got, []string{"MacBook Pro"}) {
		t.Fatalf("a later event should win: %v", got)
	}
}

func TestAirPlayEmptyAndClose(t *testing.T) {
	o, c, h := apNew(t, &apEmpty{Fake: apFake(t)})
	if !strings.Contains(o.Render(c), "No AirPlay devices found") {
		t.Fatal("no empty state")
	}
	o.Action(keymap.InputCancel, 1, c)
	if h.last() != "CloseOverlay" {
		t.Fatalf("esc: %v", h.calls)
	}
}

type apEmpty struct{ *bridge.Fake }

func (apEmpty) AirPlayDevices(context.Context) ([]bridge.AirPlayDevice, error) { return nil, nil }
