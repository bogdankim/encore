package ui

import (
	"github.com/bogdankim/encore/internal/bridge"
	"github.com/bogdankim/encore/internal/library"
	"github.com/bogdankim/encore/internal/search"
)

func init() {
	overlayCtors.Search = func(s Services, scope search.Scope) Overlay { return NewSearchIn(scope, s.Catalog) }
	overlayCtors.Palette = func(Services) Overlay { return NewPalette() }
	overlayCtors.Help = func(Services) Overlay { return NewHelp() }
	overlayCtors.Menu = func(_ Services, x, y int, tracks []*library.Track) Overlay { return NewMenu(x, y, tracks) }
	overlayCtors.AirPlay = NewAirPlay
	overlayCtors.Lastfm = func(s Services) Overlay {
		if s.Scrobbler == nil {
			if _, demo := s.Client.(*bridge.Fake); demo {
				// --demo has no scrobbler; walk through the setup
				// against a stand-in last.fm instead.
				return NewLastfmSetupEnv(nil, demoLastfmEnv())
			}
			return nil
		}
		return NewLastfmSetup(s.Scrobbler)
	}
}
