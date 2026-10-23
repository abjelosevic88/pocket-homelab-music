package server

import (
	"context"
	"math"
	"math/rand/v2"
	"time"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// ---- demo data (DEMO_DATA=true) ----
//
// The library comes from whatever music server the owner connects (the
// README screenshots use a Navidrome with Creative Commons albums, see
// scripts/demo-library). What the demo adds is a believable past: three
// months of plays shaped like a real person's week (evenings and weekends,
// a few favourite albums, discoveries), two smart playlists and a request.
// It happens once, after the owner's first library sync.

func (s *Server) seedDemo(ctx context.Context, owner int64) error {
	if _, _, done := s.store.CacheGet(ctx, "demo:seeded"); done {
		return nil
	}
	ps, _ := s.store.ListSmart(ctx, owner)
	if len(ps) == 0 {
		for _, p := range []store.SmartPlaylist{
			{Name: "Not played in a while", Description: "Songs you know but haven't heard for two months", Sort: "random", LimitCount: 60, Refresh: "daily",
				Rules: store.Rules{Match: "all", Conditions: []store.Rule{{Field: "plays", Op: "gt", Value: "0"}, {Field: "last_played", Op: "not_within", Value: "60"}}}},
			{Name: "Fresh & unplayed", Description: "Added this month, never played", Sort: "added", LimitCount: 100, Refresh: "daily",
				Rules: store.Rules{Match: "all", Conditions: []store.Rule{{Field: "added", Op: "within", Value: "30"}, {Field: "never_played", Op: "is", Value: "true"}}}},
		} {
			p := p
			if err := s.store.SaveSmart(ctx, owner, &p); err != nil {
				return err
			}
		}
	}
	return nil
}

// demoListens invents the owner's listening history once the library is indexed.
func (s *Server) demoListens(ctx context.Context, owner int64) {
	if !s.cfg.DemoData {
		return
	}
	if _, _, done := s.store.CacheGet(ctx, "demo:seeded"); done {
		return
	}
	if n, _ := s.store.CountListens(ctx, owner); n > 0 {
		_ = s.store.CachePut(ctx, "demo:seeded", "1")
		return
	}
	songs, err := s.store.QuerySongs(ctx, owner, "", "album_id, disc, track", 0)
	if err != nil || len(songs) < 20 {
		return
	}
	byAlbum := map[string][]store.Song{}
	var albums []string
	for _, so := range songs {
		if _, ok := byAlbum[so.AlbumID]; !ok {
			albums = append(albums, so.AlbumID)
		}
		byAlbum[so.AlbumID] = append(byAlbum[so.AlbumID], so)
	}
	rng := rand.New(rand.NewPCG(42, 7))
	// a few albums are favourites, the rest come up now and then
	weight := map[string]float64{}
	for i, a := range albums {
		weight[a] = 1 / math.Pow(float64(i%len(albums))+1.5, 0.9)
	}
	rng.Shuffle(len(albums), func(i, j int) { albums[i], albums[j] = albums[j], albums[i] })
	pickAlbum := func() string {
		total := 0.0
		for _, a := range albums {
			total += weight[a]
		}
		x := rng.Float64() * total
		for _, a := range albums {
			x -= weight[a]
			if x <= 0 {
				return a
			}
		}
		return albums[0]
	}
	now := time.Now()
	var ls []store.Listen
	for d := 90; d >= 0; d-- {
		day := dayStart(now.AddDate(0, 0, -d))
		sessions := 1
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			sessions = 2
		}
		if rng.Float64() < 0.15 {
			continue // a day without music
		}
		for k := 0; k < sessions; k++ {
			hour := []int{8, 13, 18, 20, 21, 22}[rng.IntN(6)]
			at := day.Add(time.Duration(hour)*time.Hour + time.Duration(rng.IntN(50))*time.Minute)
			if at.After(now) {
				continue
			}
			album := byAlbum[pickAlbum()]
			start := 0
			if rng.Float64() < 0.4 {
				start = rng.IntN(len(album))
			}
			n := 3 + rng.IntN(9)
			for i := 0; i < n && start+i < len(album); i++ {
				so := album[start+i]
				ls = append(ls, store.Listen{SongID: so.ID, Title: so.Title, Artist: so.Artist, Album: so.Album, AlbumID: so.AlbumID, ArtistID: so.ArtistID,
					Genre: so.Genre, Duration: so.Duration, PlayedAt: store.Stamp(at), MSPlayed: int64(so.Duration) * 1000, Source: "web", Device: "Demo"})
				at = at.Add(time.Duration(so.Duration) * time.Second)
			}
		}
	}
	n, err := s.store.AddListens(ctx, owner, ls)
	if err != nil {
		s.log.Warn("demo plays", "err", err)
		return
	}
	_ = s.store.CachePut(ctx, "demo:seeded", "1")
	s.log.Info("demo listening history added", "plays", n)
	if u, err := s.store.GetUser(ctx, owner); err == nil {
		// a few loved songs (saved on the demo music server, like a person would)
		if mc, err := s.musicFor(ctx, u); err == nil {
			for i := 0; i < len(songs) && i < 70; i += 9 {
				if mc.c.Star(ctx, true, "song", songs[i].ID) == nil {
					_ = s.store.SetStarred(ctx, owner, "song", songs[i].ID, true)
				}
			}
		}
		ps, _ := s.store.ListSmart(ctx, owner)
		for i := range ps {
			_ = s.refreshSmart(ctx, u, &ps[i])
		}
	}
}
