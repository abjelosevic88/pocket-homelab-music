package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
	"github.com/abjelosevic88/pocket-homelab-music/internal/subsonic"
)

// ---- the library index ----
//
// Each person's library (what THEIR account sees) is copied into SQLite: the
// whole index is fetched with search3's empty query, 500 at a time, after
// every new scan on the server, and swapped in in one transaction. Stars
// change without a scan, so they are refreshed on every check. Playing never
// waits on the index: streams, covers and lyrics go to the server directly.

const syncPage = 500

func (s *Server) isSyncing(uid int64) bool {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return s.syncing[uid]
}

// syncSoon rebuilds (force) or checks a person's index in the background.
func (s *Server) syncSoon(uid int64, force bool) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		if err := s.syncUser(ctx, uid, force); err != nil {
			s.log.Warn("library sync", "user", uid, "err", err)
		}
	}()
}

// syncAll checks everybody's index (janitor).
func (s *Server) syncAll(ctx context.Context) {
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if err := s.syncUser(ctx, id, false); err != nil && !isNoMusic(err) {
			s.log.Warn("library sync", "user", id, "err", err)
		}
	}
}

// syncUser brings one person's index up to date.
func (s *Server) syncUser(ctx context.Context, uid int64, force bool) error {
	s.syncMu.Lock()
	if s.syncing[uid] {
		s.syncMu.Unlock()
		return nil
	}
	s.syncing[uid] = true
	s.syncMu.Unlock()
	defer func() {
		s.syncMu.Lock()
		delete(s.syncing, uid)
		s.syncMu.Unlock()
	}()

	u, err := s.store.GetUser(ctx, uid)
	if err != nil {
		return err
	}
	mc, err := s.musicFor(ctx, u)
	if err != nil {
		return err
	}
	st := s.store.Sync(ctx, uid)
	scan, err := mc.c.ScanStatus(ctx)
	if err != nil {
		s.store.SetSyncState(ctx, uid, "error", 0, err.Error())
		return err
	}
	fresh := st.ServerKey == mc.key && st.LastScan == scan.LastScan && st.SyncedAt != ""
	if fresh {
		if t, err := time.Parse(time.RFC3339Nano, st.SyncedAt); err == nil && time.Since(t) > 24*time.Hour {
			fresh = false // once a day anyway: ratings and play counts drift
		}
	}
	if !force && (fresh || (scan.Scanning && st.SyncedAt != "")) {
		s.refreshStars(ctx, uid, mc)
		return nil
	}
	return s.rebuildIndex(ctx, u, mc, scan)
}

func (s *Server) refreshStars(ctx context.Context, uid int64, mc *musicConn) {
	artists, albums, songs, err := mc.c.Starred(ctx)
	if err != nil {
		return
	}
	m := func(kind string, n int, get func(int) (string, string)) {
		set := make(map[string]string, n)
		for i := 0; i < n; i++ {
			id, at := get(i)
			set[id] = at
		}
		_ = s.store.ApplyStarred(ctx, uid, kind, set)
	}
	m("song", len(songs), func(i int) (string, string) { return songs[i].ID, songs[i].Starred })
	m("album", len(albums), func(i int) (string, string) { return albums[i].ID, albums[i].Starred })
	m("artist", len(artists), func(i int) (string, string) { return artists[i].ID, artists[i].Starred })
}

func (s *Server) rebuildIndex(ctx context.Context, u *store.User, mc *musicConn, scan *subsonic.ScanStatus) error {
	uid := u.ID
	started := time.Now()
	s.store.SetSyncState(ctx, uid, "syncing", 0, "")
	s.hub.send(uid, "sync", map[string]any{"state": "syncing", "progress": 0})
	fail := func(err error) error {
		s.store.SetSyncState(ctx, uid, "error", 0, err.Error())
		s.hub.send(uid, "sync", map[string]any{"state": "error", "error": err.Error()})
		return err
	}
	// fetch everything first: the database has one connection, and a write
	// transaction held open across network calls would stall every request
	total := max(scan.Count, 1)
	artistSongs := map[string]int{}
	var songs []store.Song
	for off := 0; ; off += syncPage {
		page, _, _, err := mc.c.SearchAll(ctx, off, syncPage, 0, 0, 0, 0)
		if err != nil {
			return fail(err)
		}
		for _, so := range page {
			songs = append(songs, indexSong(so))
			_, aaID := so.AlbumArtist()
			artistSongs[aaID]++
			if so.ArtistID != aaID {
				artistSongs[so.ArtistID]++
			}
		}
		p := min(85, len(songs)*85/total)
		s.store.SetSyncState(ctx, uid, "syncing", p, "")
		s.hub.send(uid, "sync", map[string]any{"state": "syncing", "progress": p, "songs": len(songs)})
		if len(page) < syncPage {
			break
		}
	}
	var albums []store.Album
	for off := 0; ; off += syncPage {
		_, page, _, err := mc.c.SearchAll(ctx, 0, 0, off, syncPage, 0, 0)
		if err != nil {
			return fail(err)
		}
		for _, a := range page {
			albums = append(albums, indexAlbum(a))
		}
		if len(page) < syncPage {
			break
		}
	}
	var artists []store.Artist
	for off := 0; ; off += syncPage {
		_, _, page, err := mc.c.SearchAll(ctx, 0, 0, 0, 0, off, syncPage)
		if err != nil {
			return fail(err)
		}
		for _, a := range page {
			artists = append(artists, store.Artist{ID: a.ID, Name: a.Name, SortName: firstNonEmpty(a.SortName, strings.ToLower(a.Name)), AlbumCount: a.AlbumCount,
				SongCount: artistSongs[a.ID], MBID: a.MusicBrainzID, StarredAt: a.Starred, Rating: a.UserRating})
		}
		if len(page) < syncPage {
			break
		}
	}
	s.store.SetSyncState(ctx, uid, "syncing", 90, "")
	w, err := s.store.BeginLibrary(ctx, uid)
	if err != nil {
		return fail(err)
	}
	committed := false
	defer func() {
		if !committed {
			w.Rollback()
		}
	}()
	for _, so := range songs {
		if err := w.Song(ctx, so); err != nil {
			return fail(err)
		}
	}
	for _, a := range albums {
		if err := w.Album(ctx, a); err != nil {
			return fail(err)
		}
	}
	for _, a := range artists {
		if err := w.Artist(ctx, a); err != nil {
			return fail(err)
		}
	}
	if err := w.Commit(ctx, mc.key, scan.LastScan); err != nil {
		return fail(err)
	}
	committed = true
	if uid == s.store.OwnerID(ctx) {
		s.demoListens(ctx, uid)
	}
	linked, _ := s.store.RelinkListens(ctx, uid)
	st := s.store.Sync(ctx, uid)
	s.log.Info("library index rebuilt", "user", u.Email, "songs", st.Songs, "albums", st.Albums, "artists", st.Artists, "relinked_plays", linked, "took", time.Since(started).Round(time.Millisecond).String())
	s.hub.send(uid, "sync", map[string]any{"state": "idle", "progress": 100, "songs": st.Songs, "albums": st.Albums, "artists": st.Artists})
	return nil
}

func indexSong(so subsonic.Song) store.Song {
	aa, aaID := so.AlbumArtist()
	gs := so.GenreList()
	out := store.Song{ID: so.ID, Title: so.Title, Album: so.Album, AlbumID: so.AlbumID, Artist: firstNonEmpty(so.DisplayArtist, so.Artist), ArtistID: so.ArtistID,
		AlbumArtist: aa, AlbumArtistID: aaID, Genres: store.JoinGenres(gs), Year: so.Year, Track: so.Track, Disc: so.DiscNumber, Duration: so.Duration,
		Bitrate: so.BitRate, Suffix: strings.ToLower(so.Suffix), Size: so.Size, BitDepth: so.BitDepth, SampleRate: so.SamplingRate, Path: so.Path,
		AddedAt: so.Created, MBID: so.MusicBrainzID, BPM: so.BPM, StarredAt: so.Starred, Rating: so.UserRating, ServerPlays: so.PlayCount}
	if len(gs) > 0 {
		out.Genre = gs[0]
	}
	if rg := so.ReplayGain; rg != nil && (rg.TrackGain != 0 || rg.AlbumGain != 0) {
		tg, ag, pk := rg.TrackGain, rg.AlbumGain, rg.TrackPeak
		out.RGTrack, out.RGAlbum = &tg, &ag
		if pk > 0 {
			out.RGPeak = &pk
		}
	}
	return out
}

func indexAlbum(a subsonic.Album) store.Album {
	var gs []string
	for _, g := range a.Genres {
		if g.Name != "" {
			gs = append(gs, g.Name)
		}
	}
	if len(gs) == 0 && a.Genre != "" {
		gs = []string{a.Genre}
	}
	out := store.Album{ID: a.ID, Name: a.Name, SortName: firstNonEmpty(a.SortName, strings.ToLower(a.Name)), Artist: firstNonEmpty(a.DisplayArtist, a.Artist), ArtistID: a.ArtistID,
		Year: a.Year, Genres: store.JoinGenres(gs), SongCount: a.SongCount, Duration: a.Duration, AddedAt: a.Created, Compilation: a.IsCompilation,
		MBID: a.MusicBrainzID, StarredAt: a.Starred, Rating: a.UserRating, ServerPlays: a.PlayCount}
	if len(gs) > 0 {
		out.Genre = gs[0]
	}
	return out
}

// ---- API ----

func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	uid := actorID(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"sync": s.store.Sync(r.Context(), uid), "syncing": s.isSyncing(uid)})
}

// handleSyncNow rebuilds the caller's index now (and can ask the server to scan first).
func (s *Server) handleSyncNow(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if queryBool(r, "scan") {
		mc := s.music(w, r)
		if mc == nil {
			return
		}
		if err := mc.c.StartScan(r.Context()); err != nil {
			writeErr(w, http.StatusBadGateway, fmt.Sprintf("the music server would not scan: %v", err))
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"scanning": true})
		return
	}
	s.syncSoon(u.ID, true)
	writeJSON(w, http.StatusAccepted, map[string]any{"syncing": true})
}
