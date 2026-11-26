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
var fanartAsked atomic.Int32

func fakePictureAPIs(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	fanartAsked.Store(0)
	var searches atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/deezer/search/artist":
			searches.Add(1)
			if strings.Contains(r.URL.Query().Get("q"), "Busy") {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": 4, "message": "Quota limit exceeded"}})
				return
			}
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
			fanartAsked.Add(1)
			if r.URL.Query().Get("api_key") != "fan-key" && r.URL.Query().Get("client_key") != "fan-personal" {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"artistbackground": []map[string]any{
					{"url": srv.URL + "/never", "likes": "1"},
					{"url": "https://example.com/low-likes.jpg", "likes": "0"},
				},
			})
		case r.URL.Path == "/fanart/11111111-2222-3333-4444-555555555555":
			fanartAsked.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"artistbackground": []map[string]any{{"url": srv.URL + "/fanart-img/wide.jpg", "likes": "3"}}})
		case r.URL.Path == "/fanart-img/wide.jpg":
			_, _ = w.Write(testJPEG(1920, 1080))
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
	src, sure, answered := s.artistPicSources(context.Background(), a)
	if !sure || !answered || len(src) != 3 || !strings.HasSuffix(src[0], "/never") || src[1] != "https://example.com/low-likes.jpg" || !strings.Contains(src[2], "/img/ana/1500x1500") {
		t.Fatalf("sources = %v, want fanart.tv by likes, then Deezer", src)
	}
	// a personal key alone is sent as client_key
	s.cfg.FanartAPIKey, s.cfg.FanartClientKey = "", "fan-personal"
	if _, _, answered := s.artistPicSources(context.Background(), a); !answered {
		t.Fatal("personal key: fanart.tv not answered")
	}
	s.cfg.FanartAPIKey, s.cfg.FanartClientKey = "fan-key", ""
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
	if src, _, _ := s.artistPicSources(context.Background(), a); len(src) != 1 {
		t.Fatalf("no key: sources = %v", src)
	}
}

// A Deezer photo found before there was a key: with the key, fanart.tv is asked once and its wide one replaces it.
func TestArtistPictureUpgradesToFanart(t *testing.T) {
	s, _, _, _ := testServer(t)
	api, searches := fakePictureAPIs(t)
	defer func(f, d string) { fanartAPI, deezerAPI = f, d }(fanartAPI, deezerAPI)
	fanartAPI, deezerAPI = api.URL+"/fanart", api.URL+"/deezer"
	s.cfg.ArtistPictures = true
	a := &store.Artist{Name: "Ana Band", MBID: "11111111-2222-3333-4444-555555555555"}
	width := func(p string) int {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		c, _, _ := image.DecodeConfig(f)
		return c.Width
	}
	p, ok := s.largeArtistPicture(context.Background(), a)
	if !ok || width(p) != 1200 || fanartAsked.Load() != 0 {
		t.Fatalf("without a key: %q %v, fanart asked %d", p, ok, fanartAsked.Load())
	}
	s.cfg.FanartAPIKey = "fan-key"
	p, ok = s.largeArtistPicture(context.Background(), a)
	if !ok || width(p) != 1920 || fanartAsked.Load() != 1 {
		t.Fatalf("with a key: %v %d, fanart asked %d", ok, width(p), fanartAsked.Load())
	}
	before := searches.Load()
	if p, ok = s.largeArtistPicture(context.Background(), a); !ok || width(p) != 1920 || fanartAsked.Load() != 1 || searches.Load() != before {
		t.Fatalf("third time: asked again (fanart %d, deezer %d→%d)", fanartAsked.Load(), before, searches.Load())
	}
}

func TestArtistPictureRateLimitIsNotAMiss(t *testing.T) {
	s, _, _, _ := testServer(t)
	api, searches := fakePictureAPIs(t)
	defer func(d string) { deezerAPI = d }(deezerAPI)
	deezerAPI = api.URL + "/deezer"
	s.cfg.ArtistPictures = true
	a := &store.Artist{Name: "Busy Band"}
	for range 2 {
		if _, ok := s.largeArtistPicture(context.Background(), a); ok {
			t.Fatal("a picture from a rate limit")
		}
	}
	if searches.Load() != 2 {
		t.Fatalf("searches = %d, want 2: a rate limit isn't remembered as no photo", searches.Load())
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
