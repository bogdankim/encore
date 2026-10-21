package ui

import (
	"fmt"
	"testing"

	"github.com/bogdankim/encore/internal/bridge"
)

func TestPermissionsInFooter(t *testing.T) {
	a := newShellApp(t, 140, 42)
	steps := []struct {
		msg  any
		want string
	}{
		{helloMsg{hello: bridge.Hello{Automation: "pending", Library: "unknown"}}, waitingM},
		{BridgeMsg{Event: bridge.PermissionsEvent{Automation: "granted", Library: "unknown"}}, ""},
		{BridgeMsg{Event: bridge.PermissionsEvent{Automation: "pending", Library: "denied"}}, waitingM},
		{helloMsg{hello: bridge.Hello{Automation: "granted", Library: "denied"}}, libraryM},
		{BridgeMsg{Event: bridge.PermissionsEvent{Automation: "denied", Library: "denied"}}, automationM},
		{BridgeMsg{Event: bridge.PermissionsEvent{Automation: "granted", Library: "granted"}}, ""},
		{helloMsg{hello: bridge.Hello{Automation: "granted", Library: "unknown"}}, ""},
	}
	for i, s := range steps {
		a.Update(s.msg)
		if a.status.sticky != s.want {
			t.Fatalf("step %d: sticky = %q, want %q", i, a.status.sticky, s.want)
		}
	}
}

// While the Automation prompt is up, Music calls fail with -1744: the footer
// says encore is waiting, not that permission is missing.
func TestWaitingForPermissionMessage(t *testing.T) {
	consent := &bridge.Error{Code: bridge.ErrAutomationConsent, Message: "waiting for permission to control Music"}
	if got := humanize(fmt.Errorf("bridge airplay.list: %w", consent)); got != waitingM {
		t.Errorf("humanize(-1744) = %q, want %q", got, waitingM)
	}
	if got := humanize(&bridge.Error{Code: bridge.ErrAutomationDenied}); got != automationM {
		t.Errorf("humanize(-1743) = %q, want %q", got, automationM)
	}
	if got := airplayErrText(consent, ""); got != waitingM {
		t.Errorf("airplayErrText(-1744) = %q, want %q", got, waitingM)
	}

	a := newShellApp(t, 140, 42)
	a.Update(BridgeMsg{Event: bridge.PermissionsEvent{Automation: bridge.AutomationPending, Library: "unknown"}})
	if text, _ := a.footerLeft(); text != waitingM {
		t.Fatalf("footer = %q, want %q", text, waitingM)
	}
}
