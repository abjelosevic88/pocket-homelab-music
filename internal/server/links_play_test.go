package server

import (
	"net/http"
	"testing"
)

// music:play lets another app (Pocket Karaoke) find, play and read the words
// of a song — and nothing else.
func TestLinkMusicPlay(t *testing.T) {
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/v1/search", true},
		{"GET", "/api/v1/songs/abc123", true},
		{"GET", "/api/v1/stream/abc123", true},
		{"GET", "/api/v1/lyrics/abc123", true},
		{"GET", "/api/v1/cover/abc123", true},
		{"POST", "/api/v1/playlists", false},
		{"GET", "/api/v1/history", false},
		{"GET", "/api/v1/download/abc123", false},
		{"POST", "/api/v1/lyrics/abc123/save", false},
	} {
		r, _ := http.NewRequest(c.method, c.path, nil)
		if got := linkAllows([]string{"music:play"}, r); got != c.want {
			t.Errorf("%s %s = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}
