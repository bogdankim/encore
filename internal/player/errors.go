package player

import (
	"context"
	"errors"

	"github.com/bogdankim/encore/internal/bridge"
)

// Messages the player records in Snapshot.LastError on its own.
const (
	MsgSkippedUnavailable = "Skipped an unavailable track"
	MsgUnavailable        = "That track isn't available"
	MsgNothingPlayable    = "None of these tracks are available"
	MsgWaitingPermission  = "Waiting for permission to control Music…"
	MsgBadStation         = "That isn't a station encore can play"
	MsgStationFailed      = "The station didn't start"
)

// humanize turns a bridge error into a short message for the status line. It
// returns "" for errors that aren't worth showing.
func humanize(err error) string {
	if err == nil || errors.Is(err, context.Canceled) {
		return ""
	}
	var be *bridge.Error
	if errors.As(err, &be) {
		switch be.Code {
		case bridge.ErrAutomationDenied:
			return "encore needs permission to control Music (System Settings → Privacy & Security → Automation)"
		case bridge.ErrAutomationConsent:
			return MsgWaitingPermission
		case bridge.ErrMusicNotRunning:
			return "Music isn't running"
		case bridge.ErrNotFound:
			return "Music couldn't find that track"
		}
		if be.Message != "" {
			return be.Message
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Music didn't respond in time"
	}
	return err.Error()
}
