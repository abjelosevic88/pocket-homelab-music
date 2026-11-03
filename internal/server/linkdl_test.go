package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-music/internal/config"
	"github.com/abjelosevic88/pocket-homelab-music/internal/ytdlp"
)

func TestCleanTitle(t *testing.T) {
	cases := []struct{ in, artist, want string }{
		{"Solitude of the Night | Silent Hill Inspired Ambience", "Midwich Music", "Solitude of the Night"},
		{"Longing for the cold // Coldcore Ambient Playlist", "Midwich Music", "Longing for the cold"},
		{"False Space - Silent Hill inspired ambience music", "Survival Spheres", "False Space"},
		{"Lake Bells - Midwich Music", "Midwich Music", "Lake Bells"},
		{"Midwich Music - Lake Bells (3 Hours)", "Midwich Music", "Lake Bells"},
		{"Rainy Night (Official Video)", "Someone", "Rainy Night"},
		{"Northern Lights [1 Hour Version]", "Someone", "Northern Lights"},
	}
	for _, c := range cases {
		if got := cleanTitle(c.in, c.artist); got != c.want {
			t.Errorf("cleanTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSuggest(t *testing.T) {
	sg := suggestFrom(&ytdlp.Info{Type: "video", Title: "When love turns into fog | Silent Hill Inspired Ambience / Fogcore", Channel: "Midwich Music",
		UploadDate: "20250123", URL: "https://www.youtube.com/watch?v=x", ExtractorKey: "Youtube"})
	if sg.Kind != "track" || sg.Artist != "Midwich Music" || sg.Album != "When love turns into fog" || sg.Year != 2025 || len(sg.Tracks) != 1 || sg.Tracks[0].Title != "When love turns into fog" {
		t.Fatalf("single: %+v", sg)
	}
	// YouTube Music knows the real fields
	sg = suggestFrom(&ytdlp.Info{Type: "video", Title: "Ignore me", Channel: "Lustmord - Topic", Artist: "Lustmord", Album: "Dark Matter", Track: "Subspace", ReleaseYear: 2016})
	if sg.Artist != "Lustmord" || sg.Album != "Dark Matter" || sg.Tracks[0].Title != "Subspace" || sg.Year != 2016 {
		t.Fatalf("music: %+v", sg)
	}
	sg = suggestFrom(&ytdlp.Info{Type: "playlist", Title: "Album - Heresy", Channel: "", ExtractorKey: "YoutubeTab", Entries: []ytdlp.Entry{
		{ID: "a", Title: "Lustmord - Black Star", Channel: "Lustmord - Topic"}, {ID: "b", Title: "Babylon", Channel: "Lustmord - Topic"}}})
	if sg.Kind != "playlist" || sg.Artist != "Lustmord" || sg.Album != "Heresy" || len(sg.Tracks) != 2 || sg.Tracks[0].Title != "Black Star" || sg.Tracks[0].URL != "https://www.youtube.com/watch?v=a" {
		t.Fatalf("playlist: %+v", sg)
	}
}

func TestPlace(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "Ambient", "Midwich Music", "2024 - Lake Bells"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "Ambient", "Midwich Music", "2024 - Lake Bells", "01. Lake Bells.opus"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "Ambient", "Midwich Music", "2024 - Lake Bells", "02. More.m4a"), []byte("x"), 0o644))
	s := &Server{cfg: &config.Config{MusicDir: dir}}
	pl, err := s.place("ambient", "midwich music", "lake bells", 2025)
	must(err)
	if pl.Folder != "Ambient/Midwich Music/2024 - Lake Bells" || pl.ArtistNew || pl.AlbumNew || pl.NextTrack != 3 {
		t.Fatalf("existing: %+v", pl)
	}
	pl, _ = s.place("ambient", "Midwich Music", "Fog / Rain", 2025)
	if pl.Folder != "Ambient/Midwich Music/2025 - Fog - Rain" || !pl.AlbumNew || pl.NextTrack != 1 {
		t.Fatalf("new album: %+v", pl)
	}
	pl, _ = s.place("music", "New Band", "First", 0)
	if pl.Folder != "New Band/First" || !pl.ArtistNew {
		t.Fatalf("music root: %+v", pl)
	}
	if _, err := s.place("../x", "a", "b", 0); err == nil {
		t.Fatal("unknown root accepted")
	}
	if p, ok := s.inMusicDir("Ambient/../../etc"); ok && !filepath.HasPrefix(p, dir) {
		t.Fatal("escaped")
	}
}
