package main

import (
	"errors"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bogdankim/encore/internal/lastfm"
	"github.com/bogdankim/encore/internal/ui"
)

func TestLoadCredentials(t *testing.T) {
	sc := lastfm.NewScrobbler(lastfm.Credentials{}, filepath.Join(t.TempDir(), "q.jsonl"), nil,
		lastfm.WithTick(0), lastfm.WithAPIRoot("http://127.0.0.1:1/"), lastfm.WithEnabled(true))
	defer sc.Close()

	var sent []tea.Msg
	creds := lastfm.Credentials{APIKey: "k", Secret: "s", SessionKey: "sk", Username: "bogdan"}
	loadCredentials(sc, func() (lastfm.Credentials, error) { return creds, nil }, func(m tea.Msg) { sent = append(sent, m) })
	if st := sc.Status(); !st.Enabled || st.User != "bogdan" {
		t.Fatalf("credentials not applied: %+v", st)
	}
	if len(sent) != 0 {
		t.Fatalf("status sent on success: %v", sent)
	}

	loadCredentials(sc, func() (lastfm.Credentials, error) { return lastfm.Credentials{}, errors.New("keychain locked") },
		func(m tea.Msg) { sent = append(sent, m) })
	if len(sent) != 1 {
		t.Fatalf("sent %v", sent)
	}
	if m, ok := sent[0].(ui.StatusMsg); !ok || !m.Err || m.Text != "last.fm: keychain locked" {
		t.Fatalf("status = %#v", sent[0])
	}
}
