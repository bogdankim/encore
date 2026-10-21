package home

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// Responses come with format[resources]=map: data holds references, and
// resources holds every object by type and id. Only the resource types
// below are decoded; concerts, venues, social profiles and the rest stay
// raw bytes and are dropped with the response.
type mapResponse struct {
	Data      []ref                      `json:"data"`
	Resources map[string]json.RawMessage `json:"resources"`
}

type ref struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type relation struct {
	Data []ref `json:"data"`
}

type resource[A any] struct {
	ID            string              `json:"id"`
	Type          string              `json:"type"`
	Attributes    A                   `json:"attributes"`
	Relationships map[string]relation `json:"relationships"`
}

// itemAttrs covers albums, playlists, stations, songs and artists.
type itemAttrs struct {
	Name          string     `json:"name"`
	ArtistName    string     `json:"artistName"`
	CuratorName   string     `json:"curatorName"`
	ContentRating string     `json:"contentRating"`
	PlaylistType  string     `json:"playlistType"`
	Kind          string     `json:"kind"`
	IsLive        bool       `json:"isLive"`
	Artwork       *artwork   `json:"artwork"`
	PlayParams    playParams `json:"playParams"`
}

type playParams struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type artwork struct {
	URL        string `json:"url"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	BgColor    string `json:"bgColor"`
	TextColor1 string `json:"textColor1"`
}

type eventAttrs struct {
	Title     string    `json:"title"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`
}

type recAttrs struct {
	Kind    string `json:"kind"`
	Display struct {
		Kind string `json:"kind"`
	} `json:"display"`
	Title *struct {
		StringForDisplay string `json:"stringForDisplay"`
	} `json:"title"`
	ResourceTypes []string `json:"resourceTypes"`
}

type recommendation struct {
	resource[recAttrs]
	Meta struct {
		Metrics struct {
			ModuleType flexString `json:"moduleType"`
		} `json:"metrics"`
	} `json:"meta"`
}

type elementAttrs struct {
	Kind          flexString `json:"editorialElementKind"`
	Name          string     `json:"name"`
	ResourceTypes []string   `json:"resourceTypes"`
}

// flexString is a JSON string or number, as a string.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	if string(b) != "null" {
		*f = flexString(b)
	}
	return nil
}

// resources decodes the resource types items come from.
type resources struct {
	items  map[string]map[string]resource[itemAttrs] // by amp-api type
	events map[string]resource[eventAttrs]
}

var itemTypes = map[string]string{
	"albums":    TypeAlbum,
	"playlists": TypePlaylist,
	"stations":  TypeStation,
	"songs":     TypeSong,
	"artists":   TypeArtist,
}

func decodeResources(raw map[string]json.RawMessage) resources {
	r := resources{items: map[string]map[string]resource[itemAttrs]{}}
	for t := range itemTypes {
		var m map[string]resource[itemAttrs]
		if b, ok := raw[t]; ok && json.Unmarshal(b, &m) == nil {
			r.items[t] = m
		}
	}
	if b, ok := raw["station-events"]; ok {
		_ = json.Unmarshal(b, &r.events)
	}
	return r
}

// item maps a reference to an Item. ok is false for types the page
// doesn't show (library items with only an id, concerts, social
// profiles, editorial cards, videos) and for references without an object.
func (r resources) item(c ref) (Item, bool) {
	typ, known := itemTypes[c.Type]
	if !known {
		return Item{}, false
	}
	o, found := r.items[c.Type][c.ID]
	if !found {
		return Item{}, false
	}
	a := o.Attributes
	if a.Name == "" {
		return Item{}, false
	}
	it := Item{Type: typ, ID: o.ID, Title: a.Name, Explicit: a.ContentRating == "explicit"}
	if a.PlayParams.ID != "" {
		it.ID = a.PlayParams.ID
	}
	if it.ID == "" {
		return Item{}, false
	}
	if w := a.Artwork; w != nil {
		it.Art = Artwork{Template: w.URL, Width: w.Width, Height: w.Height, BgColor: w.BgColor, TextColor: w.TextColor1}
	}
	switch typ {
	case TypeAlbum, TypeSong:
		it.Subtitle = a.ArtistName
	case TypePlaylist:
		it.Subtitle = a.CuratorName
		it.PlaylistType = a.PlaylistType
	case TypeStation:
		it.StationKind = a.Kind
		it.IsLive = a.IsLive
		if a.IsLive {
			it.Events = r.stationEvents(o)
		}
	}
	return it, true
}

const maxEvents = 12

func (r resources) stationEvents(o resource[itemAttrs]) []Event {
	var out []Event
	for _, e := range o.Relationships["events"].Data {
		ev, ok := r.events[e.ID]
		if !ok || ev.Attributes.EndTime.IsZero() || !ev.Attributes.EndTime.After(ev.Attributes.StartTime) {
			continue
		}
		out = append(out, Event{Title: ev.Attributes.Title, Start: ev.Attributes.StartTime.UTC(), End: ev.Attributes.EndTime.UTC()})
		if len(out) == maxEvents {
			break
		}
	}
	return out
}

func (r resources) itemsOf(refs []ref) []Item {
	var out []Item
	for _, c := range refs {
		if it, ok := r.item(c); ok {
			out = append(out, it)
		}
	}
	return out
}

// social reports whether a listen-now shelf is about other people or
// concerts near the user. Module 11 is "Friends Are Listening To", whose
// kind is plain music-recommendations; its title is localized, so the
// module type is what tells it apart.
func social(rec recommendation) bool {
	a := rec.Attributes
	for _, s := range []string{a.Kind, a.Display.Kind} {
		s = strings.ToLower(s)
		if strings.Contains(s, "friend") || strings.Contains(s, "social") || strings.Contains(s, "concert") {
			return true
		}
	}
	for _, t := range a.ResourceTypes {
		if strings.HasPrefix(t, "social") || t == "concerts" {
			return true
		}
	}
	return rec.Meta.Metrics.ModuleType == "11" || strings.HasPrefix(rec.ID, "11-")
}

// parseListenNow reads /v1/me/recommendations?name=listen-now.
func parseListenNow(body []byte) ([]Shelf, bool) {
	var r mapResponse
	if json.Unmarshal(body, &r) != nil {
		return nil, false
	}
	var recs map[string]recommendation
	if b, ok := r.Resources["personal-recommendation"]; !ok || json.Unmarshal(b, &recs) != nil {
		return nil, false
	}
	res := decodeResources(r.Resources)
	var out []Shelf
	for _, d := range r.Data {
		rec, ok := recs[d.ID]
		if !ok || social(rec) {
			continue
		}
		items := res.itemsOf(rec.Relationships["contents"].Data)
		if len(items) == 0 {
			continue
		}
		s := Shelf{ID: rec.ID, Items: items}
		if t := rec.Attributes.Title; t != nil {
			s.Title = t.StringForDisplay
		}
		switch {
		case rec.Attributes.Kind == "recently-played":
			s.Kind = KindRecentlyPlayed
		case rec.Attributes.Kind == "your-favorites":
			s.Kind = KindFavorites
		case rec.Attributes.Display.Kind == "MusicSuperHeroShelf":
			s.Kind = KindFeatured
		default:
			s.Kind = contentKind(items)
		}
		if s.Title == "" {
			s.Title = "Featured"
		}
		out = append(out, s)
	}
	return out, true
}

// contentKind names a shelf by what it holds.
func contentKind(items []Item) string {
	typ, pl := items[0].Type, items[0].PlaylistType
	for _, it := range items[1:] {
		if it.Type != typ {
			return KindMixed
		}
		if it.PlaylistType != pl {
			pl = ""
		}
	}
	switch typ {
	case TypePlaylist:
		switch pl {
		case "personal-mix":
			return KindMixes
		case "replay":
			return KindReplay
		}
		return KindPlaylists
	case TypeStation:
		return KindStations
	case TypeAlbum:
		return KindAlbums
	case TypeSong:
		return KindSongs
	}
	return KindMixed
}

// Editorial element kinds in the New tab.
const (
	elementShelf    = "326"
	elementSongList = "327"
)

// parseGroupings reads /v1/editorial/<sf>/groupings?name=music: the live
// radio stations (as Live Radio), and the tab's first song list, first
// album shelf and first playlist shelf (Best New Songs, New This Week, Updated
// Playlists in en-CA). Their names are localized, so they are found by
// kind and contents.
func parseGroupings(body []byte) ([]Shelf, bool) {
	var r mapResponse
	if json.Unmarshal(body, &r) != nil {
		return nil, false
	}
	var groupings map[string]resource[json.RawMessage]
	var elements map[string]resource[elementAttrs]
	if b, ok := r.Resources["groupings"]; !ok || json.Unmarshal(b, &groupings) != nil {
		return nil, false
	}
	if b, ok := r.Resources["editorial-elements"]; !ok || json.Unmarshal(b, &elements) != nil {
		return nil, false
	}
	res := decodeResources(r.Resources)
	var live *Shelf
	var news []Shelf
	taken := map[string]bool{}
	for _, g := range r.Data {
		for _, tab := range groupings[g.ID].Relationships["tabs"].Data {
			for _, ch := range elements[tab.ID].Relationships["children"].Data {
				el, ok := elements[ch.ID]
				if !ok {
					continue
				}
				k := string(el.Attributes.Kind)
				if k != elementShelf && k != elementSongList {
					continue
				}
				items := res.itemsOf(el.Relationships["contents"].Data)
				if len(items) == 0 {
					continue
				}
				kind := contentKind(items)
				if kind == KindStations && live == nil && liveShelf(items) {
					live = &Shelf{ID: el.ID, Title: "Live Radio", Kind: KindLive, Items: items}
					continue
				}
				if kind != KindSongs && kind != KindAlbums && kind != KindPlaylists || taken[kind] || el.Attributes.Name == "" {
					continue
				}
				taken[kind] = true
				news = append(news, Shelf{ID: el.ID, Title: el.Attributes.Name, Kind: kind, Items: items})
			}
		}
	}
	if live != nil {
		news = append(news, *live)
	}
	return news, true
}

// liveShelf: most of its stations are live.
func liveShelf(items []Item) bool {
	n := 0
	for _, it := range items {
		if it.IsLive {
			n++
		}
	}
	return n*2 > len(items)
}

// parsePlaylistTracks reads a catalog playlist response,
// /v1/catalog/<sf>/playlists/<id>, /v1/catalog/<sf>/playlists?ids=… (with
// the tracks relationship) or the first page of
// /v1/catalog/<sf>/playlists/<id>/tracks, as playlist id → song ids.
func parsePlaylistTracks(key string, body []byte) map[string][]string {
	u, err := url.Parse(key)
	if err != nil {
		return nil
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// v1 catalog <sf> playlists [id [tracks]]
	if len(parts) < 4 || parts[0] != "v1" || parts[1] != "catalog" || parts[3] != "playlists" {
		return nil
	}
	var r struct {
		Data      []resource[itemAttrs]      `json:"data"`
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if json.Unmarshal(body, &r) != nil {
		return nil
	}
	out := map[string][]string{}
	if len(parts) == 6 && parts[5] == "tracks" {
		if o := u.Query().Get("offset"); o != "" && o != "0" {
			return nil
		}
		if ids := songIDs(r.Data, nil); len(ids) > 0 {
			out[parts[4]] = ids
		}
		return out
	}
	if len(parts) > 5 {
		return nil
	}
	var songs map[string]resource[itemAttrs]
	var pls map[string]resource[itemAttrs]
	if b, ok := r.Resources["songs"]; ok {
		_ = json.Unmarshal(b, &songs)
	}
	if b, ok := r.Resources["playlists"]; ok {
		_ = json.Unmarshal(b, &pls)
	}
	for _, d := range r.Data {
		if d.Type != "playlists" {
			continue
		}
		p := d
		if full, ok := pls[d.ID]; ok && d.Relationships == nil {
			p = full // map format: data holds references
		}
		var refs []resource[itemAttrs]
		for _, t := range p.Relationships["tracks"].Data {
			refs = append(refs, resource[itemAttrs]{ID: t.ID, Type: t.Type})
		}
		if ids := songIDs(refs, songs); len(ids) > 0 {
			out[p.ID] = ids
		}
	}
	return out
}

// songIDs are the store ids of the songs among tracks; music videos are
// left out.
func songIDs(tracks []resource[itemAttrs], songs map[string]resource[itemAttrs]) []string {
	var ids []string
	for _, t := range tracks {
		if t.Type != "songs" {
			continue
		}
		id := t.ID
		if s, ok := songs[t.ID]; ok && s.Attributes.PlayParams.ID != "" {
			id = s.Attributes.PlayParams.ID
		} else if t.Attributes.PlayParams.ID != "" {
			id = t.Attributes.PlayParams.ID
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
