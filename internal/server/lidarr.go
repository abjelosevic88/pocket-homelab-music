package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-music/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// ---- Lidarr: what's missing, and getting it ----
//
// An artist page shows the artist's whole discography from Lidarr next to
// what is in the library; a missing album is one click from being searched
// for. Requests are followed every 15 minutes and the person is told when
// the album arrives. Each person reaches Lidarr their own way (through Pocket
// Core, where Lidarr is usually for admins only, or a URL and API key).

type lidarr struct {
	base, key, via string
	http           *http.Client
}

func (l *lidarr) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, l.base+"/api/v1"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", l.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := l.http.Do(req)
	if err != nil {
		return fmt.Errorf("Lidarr unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if res.StatusCode == http.StatusUnauthorized {
		return errors.New("Lidarr refused the API key")
	}
	if res.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		var e []struct {
			ErrorMessage string `json:"errorMessage"`
		}
		if json.Unmarshal(raw, &e) == nil && len(e) > 0 {
			msg = e[0].ErrorMessage
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("Lidarr: %s (%s)", msg, res.Status)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type lidarrImage struct {
	CoverType string `json:"coverType"`
	URL       string `json:"url"`
	RemoteURL string `json:"remoteUrl"`
}

type lidarrStats struct {
	AlbumCount      int     `json:"albumCount"`
	TrackFileCount  int     `json:"trackFileCount"`
	TrackCount      int     `json:"trackCount"`
	TotalTrackCount int     `json:"totalTrackCount"`
	PercentOfTracks float64 `json:"percentOfTracks"`
	SizeOnDisk      int64   `json:"sizeOnDisk"`
}

type lidarrArtist struct {
	ID                int64         `json:"id"`
	ArtistName        string        `json:"artistName"`
	ForeignArtistID   string        `json:"foreignArtistId"`
	Monitored         bool          `json:"monitored"`
	Overview          string        `json:"overview"`
	Images            []lidarrImage `json:"images"`
	Statistics        *lidarrStats  `json:"statistics"`
	QualityProfileID  int64         `json:"qualityProfileId"`
	MetadataProfileID int64         `json:"metadataProfileId"`
	RootFolderPath    string        `json:"rootFolderPath"`
}

type lidarrAlbum struct {
	ID             int64         `json:"id"`
	Title          string        `json:"title"`
	ReleaseDate    string        `json:"releaseDate"`
	AlbumType      string        `json:"albumType"`
	SecondaryTypes []any         `json:"secondaryTypes"`
	Monitored      bool          `json:"monitored"`
	ForeignAlbumID string        `json:"foreignAlbumId"`
	ArtistID       int64         `json:"artistId"`
	Images         []lidarrImage `json:"images"`
	Statistics     *lidarrStats  `json:"statistics"`
	Artist         *lidarrArtist `json:"artist"`
	Overview       string        `json:"overview"`
	Genres         []string      `json:"genres"`
}

func cover(imgs []lidarrImage, kind string) string {
	for _, im := range imgs {
		if im.CoverType == kind && im.RemoteURL != "" {
			return im.RemoteURL
		}
	}
	for _, im := range imgs {
		if im.RemoteURL != "" {
			return im.RemoteURL
		}
	}
	return ""
}

// lidarrFor is the person's way into Lidarr; nil with the reason when there is none.
func (s *Server) lidarrFor(ctx context.Context, u *store.User, st store.Settings) (*lidarr, string) {
	mode := st.LidarrMode
	if mode == "" {
		mode = "core"
		if st.LidarrKey != "" || !s.core.Configured() {
			mode = "direct"
		}
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	switch mode {
	case "off":
		return nil, "Lidarr is switched off in Settings → Lidarr"
	case "direct":
		if st.LidarrURL == "" || st.LidarrKey == "" {
			return nil, "add your Lidarr address and API key in Settings → Lidarr"
		}
		return &lidarr{base: strings.TrimRight(st.LidarrURL, "/"), key: st.LidarrKey, via: "direct", http: hc}, ""
	}
	if !s.core.Configured() {
		return nil, "there is no Pocket Core on this box: add Lidarr's address and API key in Settings → Lidarr"
	}
	cred, err := s.core.ServiceFor(ctx, u.Email, "lidarr")
	if err != nil {
		return nil, coreReason(err, "Lidarr")
	}
	if cred.URL == "" || cred.Key == "" {
		return nil, "Pocket Core has Lidarr without an API key"
	}
	return &lidarr{base: strings.TrimRight(cred.URL, "/"), key: cred.Key, via: "core", http: hc}, ""
}

func (s *Server) lidarrReq(w http.ResponseWriter, r *http.Request) *lidarr {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	l, why := s.lidarrFor(ctx, u, st)
	if l == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": why, "code": "lidarr_off"})
	}
	return l
}

func (s *Server) handleLidarrStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	l, why := s.lidarrFor(ctx, u, st)
	if l == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ready": false, "detail": why})
		return
	}
	var sys struct {
		Version string `json:"version"`
	}
	if err := l.do(ctx, http.MethodGet, "/system/status", nil, &sys); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ready": false, "detail": err.Error(), "via": l.via})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": true, "version": sys.Version, "via": l.via})
}

// lidarrArtists is every artist in Lidarr (cached a few minutes per address).
func (s *Server) lidarrArtists(ctx context.Context, l *lidarr) ([]lidarrArtist, error) {
	key := "lidarr-artists:" + l.base
	if raw, at, ok := s.store.CacheGet(ctx, key); ok {
		if t, err := time.Parse(time.RFC3339Nano, at); err == nil && time.Since(t) < 10*time.Minute {
			var as []lidarrArtist
			if json.Unmarshal([]byte(raw), &as) == nil {
				return as, nil
			}
		}
	}
	var as []lidarrArtist
	if err := l.do(ctx, http.MethodGet, "/artist", nil, &as); err != nil {
		return nil, err
	}
	for i := range as {
		as[i].Overview = "" // keep the cache small
	}
	b, _ := json.Marshal(as)
	_ = s.store.CachePut(ctx, key, string(b))
	return as, nil
}

func (s *Server) findLidarrArtist(ctx context.Context, l *lidarr, mbid, name string) (*lidarrArtist, error) {
	as, err := s.lidarrArtists(ctx, l)
	if err != nil {
		return nil, err
	}
	for i := range as {
		if mbid != "" && as[i].ForeignArtistID == mbid {
			return &as[i], nil
		}
	}
	key := store.MatchKey(name)
	for i := range as {
		if store.MatchKey(as[i].ArtistName) == key {
			return &as[i], nil
		}
	}
	return nil, nil
}

type discAlbum struct {
	LidarrID      int64  `json:"lidarr_id"`
	ForeignID     string `json:"foreign_id"`
	Title         string `json:"title"`
	Year          int    `json:"year"`
	Type          string `json:"type"`
	Secondary     bool   `json:"secondary"` // live, compilation, remix…
	Monitored     bool   `json:"monitored"`
	Have          int    `json:"have"`
	Total         int    `json:"total"`
	Cover         string `json:"cover"`
	LibraryID     string `json:"library_id"` // the album in the person's library
	State         string `json:"state"`      // have | partial | missing | downloading | requested | upcoming
	DownloadLeft  int64  `json:"download_left,omitempty"`
	DownloadTotal int64  `json:"download_total,omitempty"`
}

type queueItem struct {
	AlbumID  int64   `json:"albumId"`
	Size     float64 `json:"size"`
	SizeLeft float64 `json:"sizeleft"`
	Status   string  `json:"status"`
}

func (s *Server) lidarrQueue(ctx context.Context, l *lidarr) map[int64]queueItem {
	var page struct {
		Records []queueItem `json:"records"`
	}
	out := map[int64]queueItem{}
	if err := l.do(ctx, http.MethodGet, "/queue?pageSize=200", nil, &page); err == nil {
		for _, q := range page.Records {
			if cur, ok := out[q.AlbumID]; ok {
				q.Size += cur.Size
				q.SizeLeft += cur.SizeLeft
			}
			out[q.AlbumID] = q
		}
	}
	return out
}

func yearOf(date string) int {
	if len(date) >= 4 {
		y, _ := strconv.Atoi(date[:4])
		return y
	}
	return 0
}

// handleDiscography is an artist's albums in Lidarr, matched to the library.
func (s *Server) handleDiscography(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	a, err := s.store.GetArtist(ctx, uid, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err, "artist")
		return
	}
	l := s.lidarrReq(w, r)
	if l == nil {
		return
	}
	la, err := s.findLidarrArtist(ctx, l, a.MBID, a.Name)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if la == nil {
		writeJSON(w, http.StatusOK, map[string]any{"in_lidarr": false, "albums": []discAlbum{}, "mbid": a.MBID})
		return
	}
	var albums []lidarrAlbum
	if err := l.do(ctx, http.MethodGet, "/album?artistId="+strconv.FormatInt(la.ID, 10), nil, &albums); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	own, appears, _ := s.store.ArtistAlbums(ctx, uid, a.ID)
	lib := map[string]string{}
	for _, al := range append(own, appears...) {
		lib[store.MatchKey(al.Name)] = al.ID
		if al.MBID != "" {
			lib["mb:"+al.MBID] = al.ID
		}
	}
	queue := s.lidarrQueue(ctx, l)
	reqs, _ := s.store.Requests(ctx, uid, true)
	requested := map[int64]bool{}
	for _, rq := range reqs {
		requested[rq.LidarrAlbumID] = true
	}
	today := time.Now().Format("2006-01-02")
	out := make([]discAlbum, 0, len(albums))
	for _, al := range albums {
		d := discAlbum{LidarrID: al.ID, ForeignID: al.ForeignAlbumID, Title: al.Title, Year: yearOf(al.ReleaseDate), Type: al.AlbumType,
			Secondary: len(al.SecondaryTypes) > 0, Monitored: al.Monitored, Cover: cover(al.Images, "cover")}
		if al.Statistics != nil {
			d.Have, d.Total = al.Statistics.TrackFileCount, al.Statistics.TotalTrackCount
		}
		d.LibraryID = lib[store.MatchKey(al.Title)]
		switch {
		case d.Total > 0 && d.Have >= d.Total:
			d.State = "have"
		case d.Have > 0:
			d.State = "partial"
		case d.LibraryID != "":
			d.State = "have" // in the library without Lidarr knowing (a rip, an import)
		default:
			d.State = "missing"
		}
		if q, ok := queue[al.ID]; ok {
			d.State, d.DownloadLeft, d.DownloadTotal = "downloading", int64(q.SizeLeft), int64(q.Size)
		} else if d.State == "missing" && al.ReleaseDate > today {
			d.State = "upcoming"
		} else if d.State == "missing" && (requested[al.ID] || al.Monitored) {
			d.State = "requested"
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Secondary != out[j].Secondary {
			return !out[i].Secondary
		}
		return out[i].Year > out[j].Year
	})
	writeJSON(w, http.StatusOK, map[string]any{"in_lidarr": true, "artist": map[string]any{"id": la.ID, "name": la.ArtistName, "monitored": la.Monitored}, "albums": out})
}

// handleLidarrSearch looks up artists and albums (MusicBrainz through Lidarr) to request.
func (s *Server) handleLidarrSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"artists": []any{}, "albums": []any{}})
		return
	}
	l := s.lidarrReq(w, r)
	if l == nil {
		return
	}
	var res []struct {
		ForeignID string        `json:"foreignId"`
		Artist    *lidarrArtist `json:"artist"`
		Album     *lidarrAlbum  `json:"album"`
	}
	if err := l.do(ctx, http.MethodGet, "/search?term="+url.QueryEscape(q), nil, &res); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	type artistHit struct {
		ForeignID string `json:"foreign_id"`
		Name      string `json:"name"`
		Overview  string `json:"overview"`
		Image     string `json:"image"`
		InLidarr  bool   `json:"in_lidarr"`
		LibraryID string `json:"library_id"`
	}
	type albumHit struct {
		ForeignID       string `json:"foreign_id"`
		ArtistForeignID string `json:"artist_foreign_id"`
		Title           string `json:"title"`
		Artist          string `json:"artist"`
		Year            int    `json:"year"`
		Type            string `json:"type"`
		Cover           string `json:"cover"`
		LibraryID       string `json:"library_id"`
	}
	uid := actorID(ctx)
	artists, albums := []artistHit{}, []albumHit{}
	for _, x := range res {
		switch {
		case x.Artist != nil && x.Album == nil:
			h := artistHit{ForeignID: x.Artist.ForeignArtistID, Name: x.Artist.ArtistName, Overview: trim(x.Artist.Overview, 300), Image: cover(x.Artist.Images, "poster"), InLidarr: x.Artist.ID > 0}
			if hits, _ := s.store.Search(ctx, uid, x.Artist.ArtistName, "artist", 3); len(hits) > 0 {
				if a, err := s.store.GetArtist(ctx, uid, hits[0].ID); err == nil && store.MatchKey(a.Name) == store.MatchKey(h.Name) {
					h.LibraryID = a.ID
				}
			}
			artists = append(artists, h)
		case x.Album != nil:
			h := albumHit{ForeignID: x.Album.ForeignAlbumID, Title: x.Album.Title, Year: yearOf(x.Album.ReleaseDate), Type: x.Album.AlbumType, Cover: cover(x.Album.Images, "cover")}
			if x.Album.Artist != nil {
				h.Artist, h.ArtistForeignID = x.Album.Artist.ArtistName, x.Album.Artist.ForeignArtistID
			}
			if hits, _ := s.store.Search(ctx, uid, h.Title+" "+h.Artist, "album", 3); len(hits) > 0 {
				if a, err := s.store.GetAlbum(ctx, uid, hits[0].ID); err == nil && store.MatchKey(a.Name) == store.MatchKey(h.Title) {
					h.LibraryID = a.ID
				}
			}
			albums = append(albums, h)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"artists": head(artists, 8), "albums": head(albums, 20)})
}

// handleRequestAlbum asks Lidarr for an album: an album already in Lidarr
// (lidarr_id) is monitored and searched; otherwise its artist is added first
// (monitoring nothing else) and the album is looked up once Lidarr has it.
func (s *Server) handleRequestAlbum(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		LidarrID        int64  `json:"lidarr_id"`
		ForeignID       string `json:"foreign_id"`
		ArtistForeignID string `json:"artist_foreign_id"`
		Artist          string `json:"artist"`
		Title           string `json:"title"`
		Year            int    `json:"year"`
		Cover           string `json:"cover"`
	}
	if err := decode(r, &in); err != nil || (in.LidarrID == 0 && (in.ForeignID == "" || in.ArtistForeignID == "")) {
		writeErr(w, http.StatusBadRequest, "say which album (lidarr_id, or foreign_id with artist_foreign_id)")
		return
	}
	l := s.lidarrReq(w, r)
	if l == nil {
		return
	}
	var al lidarrAlbum
	if in.LidarrID > 0 {
		if err := l.do(ctx, http.MethodGet, "/album/"+strconv.FormatInt(in.LidarrID, 10), nil, &al); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
	} else {
		found, err := s.ensureLidarrAlbum(ctx, l, in.ArtistForeignID, in.ForeignID)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		al = *found
	}
	if err := l.do(ctx, http.MethodPut, "/album/monitor", map[string]any{"albumIds": []int64{al.ID}, "monitored": true}, nil); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := l.do(ctx, http.MethodPost, "/command", map[string]any{"name": "AlbumSearch", "albumIds": []int64{al.ID}}, nil); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	artist := in.Artist
	if al.Artist != nil {
		artist = al.Artist.ArtistName
	}
	rq := &store.Request{Artist: artist, Album: firstNonEmpty(al.Title, in.Title), Year: max(yearOf(al.ReleaseDate), in.Year), ArtistMBID: in.ArtistForeignID,
		AlbumMBID: firstNonEmpty(al.ForeignAlbumID, in.ForeignID), LidarrAlbumID: al.ID, CoverURL: firstNonEmpty(cover(al.Images, "cover"), in.Cover)}
	if err := s.store.AddRequest(ctx, u.ID, rq); err != nil {
		s.fail(w, err, "save request")
		return
	}
	s.publish(ctx, u, "music.album_requested", rq.Artist+" — "+rq.Album, map[string]any{"artist": rq.Artist, "album": rq.Album, "year": rq.Year})
	writeJSON(w, http.StatusOK, rq)
}

func (s *Server) ensureLidarrAlbum(ctx context.Context, l *lidarr, artistMBID, albumMBID string) (*lidarrAlbum, error) {
	find := func(artistID int64) *lidarrAlbum {
		var albums []lidarrAlbum
		if l.do(ctx, http.MethodGet, "/album?artistId="+strconv.FormatInt(artistID, 10), nil, &albums) != nil {
			return nil
		}
		for i := range albums {
			if albums[i].ForeignAlbumID == albumMBID {
				return &albums[i]
			}
		}
		return nil
	}
	la, err := s.findLidarrArtist(ctx, l, artistMBID, "")
	if err != nil {
		return nil, err
	}
	if la == nil {
		// add the artist with Lidarr's first quality / metadata profile and root folder, monitoring nothing
		var qps, mps []struct {
			ID int64 `json:"id"`
		}
		var roots []struct {
			Path                     string `json:"path"`
			DefaultQualityProfileID  int64  `json:"defaultQualityProfileId"`
			DefaultMetadataProfileID int64  `json:"defaultMetadataProfileId"`
		}
		if err := l.do(ctx, http.MethodGet, "/rootfolder", nil, &roots); err != nil || len(roots) == 0 {
			return nil, errors.New("Lidarr has no root folder to put music in")
		}
		_ = l.do(ctx, http.MethodGet, "/qualityprofile", nil, &qps)
		_ = l.do(ctx, http.MethodGet, "/metadataprofile", nil, &mps)
		qp, mp := roots[0].DefaultQualityProfileID, roots[0].DefaultMetadataProfileID
		if qp == 0 && len(qps) > 0 {
			qp = qps[0].ID
		}
		if mp == 0 && len(mps) > 0 {
			mp = mps[0].ID
		}
		var lookup []lidarrArtist
		if err := l.do(ctx, http.MethodGet, "/artist/lookup?term="+url.QueryEscape("lidarr:"+artistMBID), nil, &lookup); err != nil || len(lookup) == 0 {
			return nil, errors.New("Lidarr could not find that artist on MusicBrainz")
		}
		body := map[string]any{"artistName": lookup[0].ArtistName, "foreignArtistId": artistMBID, "qualityProfileId": qp, "metadataProfileId": mp,
			"rootFolderPath": roots[0].Path, "monitored": true, "monitorNewItems": "none", "addOptions": map[string]any{"monitor": "none", "searchForMissingAlbums": false}}
		var added lidarrArtist
		if err := l.do(ctx, http.MethodPost, "/artist", body, &added); err != nil {
			return nil, err
		}
		_ = s.store.CacheDelete(ctx, "lidarr-artists:"+l.base)
		la = &added
	}
	// a freshly added artist's albums arrive while Lidarr refreshes it
	for i := 0; i < 20; i++ {
		if al := find(la.ID); al != nil {
			return al, nil
		}
		if i == 0 {
			_ = l.do(ctx, http.MethodPost, "/command", map[string]any{"name": "RefreshArtist", "artistId": la.ID}, nil)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return nil, errors.New("Lidarr added the artist but does not list that album (its metadata profile may hide singles, live albums or compilations)")
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	rs, err := s.store.Requests(r.Context(), actorID(r.Context()), false)
	if err != nil {
		s.fail(w, err, "requests")
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (s *Server) handleDeleteRequest(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRequest(r.Context(), actorID(r.Context()), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete request")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleReleases lists recent and upcoming albums by artists in Lidarr (its calendar).
func (s *Server) handleReleases(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	l := s.lidarrReq(w, r)
	if l == nil {
		return
	}
	now := time.Now()
	var albums []lidarrAlbum
	q := url.Values{"start": {now.AddDate(0, 0, -45).Format("2006-01-02")}, "end": {now.AddDate(0, 0, 120).Format("2006-01-02")}, "includeArtist": {"true"}, "unmonitored": {"true"}}
	if err := l.do(ctx, http.MethodGet, "/calendar?"+q.Encode(), nil, &albums); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	// keep it to artists the person actually has in their library
	artists, _, _ := s.store.ListArtists(ctx, uid, store.ArtistFilter{Limit: 500, Sort: "plays"})
	known := map[string]string{}
	for _, a := range artists {
		known[store.MatchKey(a.Name)] = a.ID
	}
	all, _, _ := s.store.ListArtists(ctx, uid, store.ArtistFilter{Limit: 500, Offset: 500})
	for _, a := range all {
		known[store.MatchKey(a.Name)] = a.ID
	}
	type rel struct {
		discAlbum
		Artist   string `json:"artist"`
		ArtistID string `json:"artist_id"`
		Date     string `json:"date"`
	}
	out := []rel{}
	today := now.Format("2006-01-02")
	for _, al := range albums {
		name := ""
		if al.Artist != nil {
			name = al.Artist.ArtistName
		}
		d := rel{discAlbum: discAlbum{LidarrID: al.ID, ForeignID: al.ForeignAlbumID, Title: al.Title, Year: yearOf(al.ReleaseDate), Type: al.AlbumType, Monitored: al.Monitored,
			Cover: cover(al.Images, "cover"), Secondary: len(al.SecondaryTypes) > 0}, Artist: name, ArtistID: known[store.MatchKey(name)]}
		if len(al.ReleaseDate) >= 10 {
			d.Date = al.ReleaseDate[:10]
		}
		if al.Statistics != nil {
			d.Have, d.Total = al.Statistics.TrackFileCount, al.Statistics.TotalTrackCount
		}
		switch {
		case d.Total > 0 && d.Have >= d.Total:
			d.State = "have"
		case d.Date > today:
			d.State = "upcoming"
		case al.Monitored:
			d.State = "requested"
		default:
			d.State = "missing"
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	writeJSON(w, http.StatusOK, out)
}

// followRequests checks open requests in Lidarr: downloading, arrived (janitor).
func (s *Server) followRequests(ctx context.Context) {
	owners, err := s.store.OpenRequestOwners(ctx)
	if err != nil {
		return
	}
	for _, uid := range owners {
		u, err := s.store.GetUser(ctx, uid)
		if err != nil {
			continue
		}
		st, _ := s.store.GetSettings(ctx, uid)
		l, _ := s.lidarrFor(ctx, u, st)
		if l == nil {
			continue
		}
		queue := s.lidarrQueue(ctx, l)
		reqs, _ := s.store.Requests(ctx, uid, true)
		arrived := 0
		for _, rq := range reqs {
			var al lidarrAlbum
			if err := l.do(ctx, http.MethodGet, "/album/"+strconv.FormatInt(rq.LidarrAlbumID, 10), nil, &al); err != nil {
				if strings.Contains(err.Error(), "404") {
					_ = s.store.SetRequestState(ctx, uid, rq.ID, "failed", "the album is no longer in Lidarr")
				}
				continue
			}
			have, total := 0, 0
			if al.Statistics != nil {
				have, total = al.Statistics.TrackFileCount, al.Statistics.TotalTrackCount
			}
			switch q, inQueue := queue[al.ID]; {
			case total > 0 && have >= total:
				_ = s.store.SetRequestState(ctx, uid, rq.ID, "arrived", fmt.Sprintf("%d tracks", have))
				arrived++
				s.publish(ctx, u, "music.album_arrived", rq.Artist+" — "+rq.Album, map[string]any{"artist": rq.Artist, "album": rq.Album, "year": rq.Year})
				if st.NotifyArrived {
					_ = s.notifyUser(ctx, st, "New in your library", fmt.Sprintf("%s — %s is ready to play.", rq.Artist, rq.Album), "cd")
				}
			case inQueue:
				pct := 0
				if q.Size > 0 {
					pct = int((q.Size - q.SizeLeft) * 100 / q.Size)
				}
				_ = s.store.SetRequestState(ctx, uid, rq.ID, "downloading", fmt.Sprintf("%d%%", pct))
			case have > 0:
				_ = s.store.SetRequestState(ctx, uid, rq.ID, "downloading", fmt.Sprintf("%d of %d tracks", have, total))
			}
		}
		if arrived > 0 {
			// let the music server pick the files up now instead of at its next scheduled scan
			if mc, err := s.musicFor(ctx, u); err == nil {
				_ = mc.c.StartScan(ctx)
			}
		}
	}
}

// notifyUser sends one person a message through their own notification URL.
func (s *Server) notifyUser(ctx context.Context, st store.Settings, title, body, tag string) error {
	if st.NotifyURL == "" {
		return nil
	}
	return notify.New(st.NotifyURL, st.NotifyFormat).Send(ctx, notify.Message{Title: title, Body: body, Priority: "info", Tags: []string{tag}})
}
