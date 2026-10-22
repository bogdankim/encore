package ui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/keymap"
	"github.com/bogdankim/encore/internal/player"
	"github.com/bogdankim/encore/internal/theme"
)

// The fake bridge drops the AirPlay route the way macOS does after sleep and
// reconnects it; the footer says so, as a warning when it didn't work.
func TestAirPlayReconnectInFooter(t *testing.T) {
	lib := shellLibrary(t)
	f := bridge.NewFakeWithClock(1, time.Now)
	pl := player.New(f, lib.Track)
	t.Cleanup(func() { pl.Close(); f.Close() })
	th := shellTheme()
	a := New(Options{
		Client: f, Player: pl, Library: lib, Keys: keymap.New(),
		Theme: func(bool) *theme.Theme { return th },
	})
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 42})

	ctx := context.Background()
	if err := f.SelectAirPlay(ctx, []int{29274}); err != nil {
		t.Fatal(err)
	}
	if err := f.Play(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		ok   bool
		want string
		tone tone
	}{
		{true, "Reconnected to Living Room", toneDim},
		{false, "Couldn’t reconnect to Living Room", toneWarn},
	} {
		if !f.DropAirPlayRoute(c.ok) {
			t.Fatal("the fake didn't reconnect")
		}
		feedUntilRepaired(t, a, f)
		if text, tn := a.footerLeft(); text != c.want || tn != c.tone {
			t.Errorf("footer = %q (tone %v), want %q (tone %v)", text, tn, c.want, c.tone)
		}
	}
}

// feedUntilRepaired hands the fake's events to the app, as main does, up to
// the AirPlayRepairedEvent.
func feedUntilRepaired(t *testing.T, a *App, f *bridge.Fake) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev := <-f.Events():
			if _, ok := ev.(bridge.PlayerEvent); ok {
				continue
			}
			a.Update(BridgeMsg{Event: ev})
			if _, ok := ev.(bridge.AirPlayRepairedEvent); ok {
				return
			}
		case <-timeout:
			t.Fatal("no AirPlayRepairedEvent")
		}
	}
}

func TestReconnectedText(t *testing.T) {
	for _, c := range []struct {
		ev   bridge.AirPlayRepairedEvent
		want string
	}{
		{bridge.AirPlayRepairedEvent{Devices: []string{"Living Room", "Kitchen"}, OK: true}, "Reconnected to Living Room and Kitchen"},
		{bridge.AirPlayRepairedEvent{Devices: []string{"A", "B", "C"}}, "Couldn’t reconnect to A, B and C"},
		{bridge.AirPlayRepairedEvent{OK: true}, "Reconnected to AirPlay"},
	} {
		if got := reconnectedText(c.ev); got != c.want {
			t.Errorf("%+v: %q, want %q", c.ev, got, c.want)
		}
	}
}
