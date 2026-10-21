package search

import (
	"slices"
	"testing"
)

func TestHighlight(t *testing.T) {
	got := Highlight("vale pap", "Paper Lanterns", "Juniper Vale", "Paper Lanterns")
	if want := []int{0, 1, 2}; !slices.Equal(got[0], want) {
		t.Errorf("title = %v, want %v (the earlier field wins)", got[0], want)
	}
	if want := []int{8, 9, 10, 11}; !slices.Equal(got[1], want) {
		t.Errorf("artist = %v, want %v", got[1], want)
	}
	if got[2] != nil {
		t.Errorf("album = %v, want nothing", got[2])
	}
	if got := Highlight("é", "Café"); !slices.Equal(got[0], []int{3}) {
		t.Errorf("folded = %v", got[0])
	}
	if got := Highlight("", "x"); got[0] != nil {
		t.Errorf("empty query = %v", got)
	}
}
