package bridge

import (
	"encoding/json"
	"fmt"
	"io"
)

// decodeSnapshot reads a Snapshot from r one track at a time. json.Decoder's
// Decode buffers a whole value before it parses it, which for a 100,000-track
// library is a 53 MB buffer (grown by doubling) next to the 50 MB of decoded
// tracks; decoding the arrays element by element needs only the tracks.
// tracks and playlists are the counts the bridge reported, to size the slices.
func decodeSnapshot(r io.Reader, tracks, playlists int) (*Snapshot, error) {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{'); err != nil {
		return nil, err
	}
	s := new(Snapshot)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		switch key {
		case "generatedAt":
			err = dec.Decode(&s.GeneratedAt)
		case "tracks":
			s.Tracks, err = decodeArray[SnapshotTrack](dec, tracks)
		case "playlists":
			s.Playlists, err = decodeArray[SnapshotPlaylist](dec, playlists)
		default:
			var skip json.RawMessage
			err = dec.Decode(&skip)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := expectDelim(dec, '}'); err != nil {
		return nil, err
	}
	return s, nil
}

func decodeArray[T any](dec *json.Decoder, hint int) ([]T, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok == nil { // null
		return nil, nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("want an array, got %v", tok)
	}
	out := make([]T, 0, max(hint, 0))
	for dec.More() {
		var v T
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, expectDelim(dec, ']')
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("want %q, got %v", want, tok)
	}
	return nil
}
