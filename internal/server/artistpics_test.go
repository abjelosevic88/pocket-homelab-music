package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

func testJPEG(w, h int) []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil)
	return b.Bytes()
}

// fakePictureAPIs stands in for Deezer and fanart.tv (and their image hosts).
func fakePictureAPIs(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var searches atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/deezer/search/artist":
			searches.Add(1)
			var data []map[string]any
			if strings.Contains(r.URL.Query().Get("q"), "Ana Band") {
				data = []map[string]any{
					// more fans, but someone else
					{"name": "Ana Bandits", "picture_xl": srv.URL + "/img/other/1000x1000-000000-80-0-0.jpg", "nb_fan": 9000},
					{"name": "ana band", "picture_xl": srv.URL + "/img/ana/1000x1000-000000-80-0-0.jpg", "nb_fan": 12},
					// no photo
					{"name": "Ana Band", "picture_xl": srv.URL + "/images/artist//1000x1000-000000-80-0-0.jpg", "nb_fan": 50},
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case r.URL.Path == "/img/ana/1500x1500-000000-80-0-0.jpg":
			_, _ = w.Write(testJPEG(1200, 1200))
		case r.URL.Path == "/fanart/0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0":
			if r.URL.Query().Get("api_key") != "fan-key" {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"artistbackground": []map[string]any{
					{"url": strings.Replace(srv.URL, "http://", "https://", 1) + "/never", "likes": "1"},
					{"url": "https://example.com/low-likes.jpg", "likes": "0"},
				},
			})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &searches
}

func TestArtistPictureFromDeezer(t *testing.T) {
	s, _, tok, h := testServer(t)
	api, searches := fakePictureAPIs(t)
	defer func(d string) { deezerAPI = d }(deezerAPI)
	deezerAPI = api.URL + "/deezer"
	s.cfg.ArtistPictures = true

	req := httptest.NewRequest("GET", "/api/v1/artists/ar1/picture", nil)
	req.Header.Set("Authorization", "Bearer "+tok["ana"])
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("picture: %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(rec.Body.Bytes())); err != nil || cfg.Width != 1200 {
		t.Fatalf("got %+v %v, want the 1200 px one", cfg, err)
	}
	// kept: asked once
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || searches.Load() != 1 {
		t.Fatalf("second time: %d, searches %d", rec.Code, searches.Load())
	}

	// someone without a photo: 404, and not asked again soon
	for range 2 {
		req = httptest.NewRequest("GET", "/api/v1/artists/ar9/picture", nil)
		req.Header.Set("Authorization", "Bearer "+tok["ben"])
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 404 {
			t.Fatalf("no photo: %d", rec.Code)
		}
	}
	if searches.Load() != 2 {
		t.Fatalf("searches = %d, want 2 (the miss is remembered)", searches.Load())
	}

	// not in this person's library
	req = httptest.NewRequest("GET", "/api/v1/artists/ar1/picture", nil)
	req.Header.Set("Authorization", "Bearer "+tok["ben"])
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("someone else's artist: %d", rec.Code)
	}
}

func TestArtistPictureFanartFirst(t *testing.T) {
	s, _, _, _ := testServer(t)
	api, _ := fakePictureAPIs(t)
	defer func(f, d string) { fanartAPI, deezerAPI = f, d }(fanartAPI, deezerAPI)
	fanartAPI, deezerAPI = api.URL+"/fanart", api.URL+"/deezer"
	s.cfg.ArtistPictures = true
	s.cfg.FanartAPIKey = "fan-key"

	a := &store.Artist{Name: "Ana Band", MBID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"}
	src := s.artistPicSources(context.Background(), a)
	if len(src) != 3 || !strings.HasSuffix(src[0], "/never") || src[1] != "https://example.com/low-likes.jpg" || !strings.Contains(src[2], "/img/ana/1500x1500") {
		t.Fatalf("sources = %v, want fanart.tv by likes, then Deezer", src)
	}
	// fanart.tv's pictures don't load here: Deezer's is used
	p, ok := s.largeArtistPicture(context.Background(), a)
	if !ok || !strings.HasSuffix(p, "mb-0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0") {
		t.Fatalf("picture = %q %v", p, ok)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}

	// without a key fanart.tv isn't asked
	s.cfg.FanartAPIKey = ""
	if src := s.artistPicSources(context.Background(), a); len(src) != 1 {
		t.Fatalf("no key: sources = %v", src)
	}
}

func TestArtistPicturesOff(t *testing.T) {
	s, _, tok, h := testServer(t)
	api, searches := fakePictureAPIs(t)
	defer func(d string) { deezerAPI = d }(deezerAPI)
	deezerAPI = api.URL + "/deezer"
	if code, _ := call(t, h, tok["ana"], "GET", "/api/v1/artists/ar1/picture", nil); code != 404 || searches.Load() != 0 {
		t.Fatalf("off: %d, searches %d", code, searches.Load())
	}
	_ = s
}
