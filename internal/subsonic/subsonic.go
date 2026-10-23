// Package subsonic talks to an (Open)Subsonic music server — Navidrome first
// of all, but Gonic, Airsonic and friends answer the same calls. Pocket
// Music never hands the server's credentials to a browser: every call,
// stream and cover goes through this client on the server.
package subsonic

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ClientName is what the server sees as the player ("c" parameter).
const ClientName = "PocketMusic"

// Client is one person's way into the server.
type Client struct {
	Base     string // http://navidrome:4533 (no /rest)
	User     string
	Password string
	HTTP     *http.Client
	// Stream is used for audio and covers: no overall timeout, a song may play for an hour.
	Stream *http.Client
}

// New makes a client. base may end in /rest or /.
func New(base, user, password string) *Client {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	base = strings.TrimSuffix(base, "/rest")
	return &Client{Base: base, User: user, Password: password,
		HTTP:   &http.Client{Timeout: 60 * time.Second},
		Stream: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 60 * time.Second, MaxIdleConnsPerHost: 8}}}
}

// Error is a failed Subsonic call.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	switch e.Code {
	case 40, 41:
		return "the music server refused the username or password"
	case 50:
		return "the music server account may not do that"
	case 70:
		return "not found on the music server"
	}
	return fmt.Sprintf("music server: %s (code %d)", e.Message, e.Code)
}

// IsAuth reports a wrong username/password.
func IsAuth(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Code == 40 || e.Code == 41)
}

// IsNotFound reports a missing item.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == 70
}

func salt() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Params returns the auth parameters plus extra ones.
func (c *Client) Params(extra url.Values) url.Values {
	v := url.Values{}
	s := salt()
	h := md5.Sum([]byte(c.Password + s))
	v.Set("u", c.User)
	v.Set("t", hex.EncodeToString(h[:]))
	v.Set("s", s)
	v.Set("v", "1.16.1")
	v.Set("c", ClientName)
	v.Set("f", "json")
	for k, vs := range extra {
		for _, x := range vs {
			v.Add(k, x)
		}
	}
	return v
}

// URL builds an authenticated URL for a call (used for streams and covers).
func (c *Client) URL(method string, extra url.Values) string {
	return c.Base + "/rest/" + method + "?" + c.Params(extra).Encode()
}

// Envelope is the parsed "subsonic-response" object.
type Envelope struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	Type          string `json:"type"`
	ServerVersion string `json:"serverVersion"`
	OpenSubsonic  bool   `json:"openSubsonic"`
	Error         *Error `json:"error"`

	Artists       *struct{ Index []Index } `json:"artists"`
	Artist        *ArtistWithAlbums        `json:"artist"`
	ArtistInfo2   *ArtistInfo              `json:"artistInfo2"`
	Album         *AlbumWithSongs          `json:"album"`
	AlbumInfo     *AlbumInfo               `json:"albumInfo"`
	AlbumList2    *struct{ Album []Album } `json:"albumList2"`
	Song          *Song                    `json:"song"`
	RandomSongs   *struct{ Song []Song }   `json:"randomSongs"`
	SongsByGenre  *struct{ Song []Song }   `json:"songsByGenre"`
	SimilarSongs2 *struct{ Song []Song }   `json:"similarSongs2"`
	TopSongs      *struct{ Song []Song }   `json:"topSongs"`
	Starred2      *struct {
		Artist []Artist
		Album  []Album
		Song   []Song
	} `json:"starred2"`
	SearchResult3 *struct {
		Artist []Artist
		Album  []Album
		Song   []Song
	} `json:"searchResult3"`
	Genres     *struct{ Genre []Genre }               `json:"genres"`
	Playlists  *struct{ Playlist []Playlist }         `json:"playlists"`
	Playlist   *PlaylistWithSongs                     `json:"playlist"`
	ScanStatus *ScanStatus                            `json:"scanStatus"`
	LyricsList *struct{ StructuredLyrics []Lyrics }   `json:"lyricsList"`
	Lyrics     *struct{ Artist, Title, Value string } `json:"lyrics"`
	PlayQueue  *PlayQueue                             `json:"playQueue"`
	NowPlaying *struct{ Entry []NowPlayingEntry }     `json:"nowPlaying"`
	Folders    *struct{ MusicFolder []MusicFolder }   `json:"musicFolders"`
	Extensions []struct {
		Name     string
		Versions []int
	} `json:"openSubsonicExtensions"`
	User *struct {
		Username   string `json:"username"`
		Email      string `json:"email"`
		AdminRole  bool   `json:"adminRole"`
		StreamRole bool   `json:"streamRole"`
	} `json:"user"`
}

// Call runs a method and returns the parsed envelope.
func (c *Client) Call(ctx context.Context, method string, params url.Values) (*Envelope, error) {
	if c.Base == "" {
		return nil, errors.New("no music server address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/rest/"+method, strings.NewReader(c.Params(params).Encode()))
	if err != nil {
		return nil, err
	}
	// formPost (OpenSubsonic) keeps the token out of access logs; plain GET is the fallback
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("music server unreachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusMethodNotAllowed || res.StatusCode == http.StatusNotFound {
		res.Body.Close()
		req, _ = http.NewRequestWithContext(ctx, http.MethodGet, c.URL(method, params), nil)
		if res, err = c.HTTP.Do(req); err != nil {
			return nil, fmt.Errorf("music server unreachable: %w", err)
		}
		defer res.Body.Close()
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("music server answered %s", res.Status)
	}
	var wrap struct {
		R Envelope `json:"subsonic-response"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 256<<20)).Decode(&wrap); err != nil {
		return nil, fmt.Errorf("music server: unreadable answer: %w", err)
	}
	if wrap.R.Status != "ok" {
		if wrap.R.Error != nil {
			return nil, wrap.R.Error
		}
		return nil, errors.New("music server: call failed")
	}
	return &wrap.R, nil
}

// ---- types ----

// Ref is an id + name (OpenSubsonic artists / albumArtists).
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Named is a {name} object (genres, moods).
type Named struct {
	Name string `json:"name"`
}

// ReplayGain values in dB.
type ReplayGain struct {
	TrackGain float64 `json:"trackGain"`
	AlbumGain float64 `json:"albumGain"`
	TrackPeak float64 `json:"trackPeak"`
	AlbumPeak float64 `json:"albumPeak"`
}

// Song is one track.
type Song struct {
	ID                 string      `json:"id"`
	Parent             string      `json:"parent"`
	Title              string      `json:"title"`
	Album              string      `json:"album"`
	Artist             string      `json:"artist"`
	Track              int         `json:"track"`
	DiscNumber         int         `json:"discNumber"`
	Year               int         `json:"year"`
	Genre              string      `json:"genre"`
	Genres             []Named     `json:"genres"`
	CoverArt           string      `json:"coverArt"`
	Size               int64       `json:"size"`
	ContentType        string      `json:"contentType"`
	Suffix             string      `json:"suffix"`
	Duration           int         `json:"duration"`
	BitRate            int         `json:"bitRate"`
	BitDepth           int         `json:"bitDepth"`
	SamplingRate       int         `json:"samplingRate"`
	Path               string      `json:"path"`
	Created            string      `json:"created"`
	AlbumID            string      `json:"albumId"`
	ArtistID           string      `json:"artistId"`
	Starred            string      `json:"starred"`
	UserRating         int         `json:"userRating"`
	PlayCount          int         `json:"playCount"`
	Played             string      `json:"played"`
	BPM                int         `json:"bpm"`
	MusicBrainzID      string      `json:"musicBrainzId"`
	ReplayGain         *ReplayGain `json:"replayGain"`
	Artists            []Ref       `json:"artists"`
	DisplayArtist      string      `json:"displayArtist"`
	AlbumArtists       []Ref       `json:"albumArtists"`
	DisplayAlbumArtist string      `json:"displayAlbumArtist"`
	Moods              []string    `json:"moods"`
}

// AlbumArtist is the song's album artist (name, id).
func (s Song) AlbumArtist() (string, string) {
	if len(s.AlbumArtists) > 0 {
		name := s.DisplayAlbumArtist
		if name == "" {
			name = s.AlbumArtists[0].Name
		}
		return name, s.AlbumArtists[0].ID
	}
	return s.Artist, s.ArtistID
}

// GenreList is every genre of the song.
func (s Song) GenreList() []string {
	var out []string
	for _, g := range s.Genres {
		if g.Name != "" {
			out = append(out, g.Name)
		}
	}
	if len(out) == 0 && s.Genre != "" {
		out = append(out, s.Genre)
	}
	return out
}

// Album is an album without songs.
type Album struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Artist        string   `json:"artist"`
	ArtistID      string   `json:"artistId"`
	CoverArt      string   `json:"coverArt"`
	SongCount     int      `json:"songCount"`
	Duration      int      `json:"duration"`
	PlayCount     int      `json:"playCount"`
	Played        string   `json:"played"`
	Created       string   `json:"created"`
	Year          int      `json:"year"`
	Genre         string   `json:"genre"`
	Genres        []Named  `json:"genres"`
	Starred       string   `json:"starred"`
	UserRating    int      `json:"userRating"`
	MusicBrainzID string   `json:"musicBrainzId"`
	IsCompilation bool     `json:"isCompilation"`
	SortName      string   `json:"sortName"`
	Artists       []Ref    `json:"artists"`
	DisplayArtist string   `json:"displayArtist"`
	ReleaseTypes  []string `json:"releaseTypes"`
	RecordLabels  []Named  `json:"recordLabels"`
	Version       string   `json:"version"`
}

// AlbumWithSongs is getAlbum's answer.
type AlbumWithSongs struct {
	Album
	Song []Song `json:"song"`
}

// Artist is an artist without albums.
type Artist struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	CoverArt       string   `json:"coverArt"`
	AlbumCount     int      `json:"albumCount"`
	Starred        string   `json:"starred"`
	UserRating     int      `json:"userRating"`
	ArtistImageURL string   `json:"artistImageUrl"`
	MusicBrainzID  string   `json:"musicBrainzId"`
	SortName       string   `json:"sortName"`
	Roles          []string `json:"roles"`
}

// ArtistWithAlbums is getArtist's answer.
type ArtistWithAlbums struct {
	Artist
	Album []Album `json:"album"`
}

// Index is one letter of getArtists.
type Index struct {
	Name   string   `json:"name"`
	Artist []Artist `json:"artist"`
}

// ArtistInfo is getArtistInfo2's answer.
type ArtistInfo struct {
	Biography      string   `json:"biography"`
	MusicBrainzID  string   `json:"musicBrainzId"`
	LastFmURL      string   `json:"lastFmUrl"`
	SmallImageURL  string   `json:"smallImageUrl"`
	MediumImageURL string   `json:"mediumImageUrl"`
	LargeImageURL  string   `json:"largeImageUrl"`
	SimilarArtist  []Artist `json:"similarArtist"`
}

// AlbumInfo is getAlbumInfo2's answer.
type AlbumInfo struct {
	Notes         string `json:"notes"`
	MusicBrainzID string `json:"musicBrainzId"`
	LastFmURL     string `json:"lastFmUrl"`
}

// Genre is one genre with counts.
type Genre struct {
	Value      string `json:"value"`
	SongCount  int    `json:"songCount"`
	AlbumCount int    `json:"albumCount"`
}

// Playlist without songs.
type Playlist struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Comment   string `json:"comment"`
	Owner     string `json:"owner"`
	Public    bool   `json:"public"`
	SongCount int    `json:"songCount"`
	Duration  int    `json:"duration"`
	Created   string `json:"created"`
	Changed   string `json:"changed"`
	CoverArt  string `json:"coverArt"`
	Readonly  bool   `json:"readonly"`
}

// PlaylistWithSongs is getPlaylist's answer.
type PlaylistWithSongs struct {
	Playlist
	Entry []Song `json:"entry"`
}

// ScanStatus is getScanStatus's answer.
type ScanStatus struct {
	Scanning    bool   `json:"scanning"`
	Count       int    `json:"count"`
	FolderCount int    `json:"folderCount"`
	LastScan    string `json:"lastScan"`
}

// Lyrics is one OpenSubsonic structured lyrics set.
type Lyrics struct {
	DisplayArtist string `json:"displayArtist"`
	DisplayTitle  string `json:"displayTitle"`
	Lang          string `json:"lang"`
	Offset        int    `json:"offset"`
	Synced        bool   `json:"synced"`
	Line          []struct {
		Start *int   `json:"start"`
		Value string `json:"value"`
	} `json:"line"`
}

// PlayQueue is the saved queue.
type PlayQueue struct {
	Current   string `json:"current"`
	Position  int64  `json:"position"`
	Changed   string `json:"changed"`
	ChangedBy string `json:"changedBy"`
	Entry     []Song `json:"entry"`
}

// NowPlayingEntry is one song someone is playing.
type NowPlayingEntry struct {
	Song
	Username   string `json:"username"`
	MinutesAgo int    `json:"minutesAgo"`
	PlayerName string `json:"playerName"`
}

// MusicFolder is a library.
type MusicFolder struct {
	ID   json.Number `json:"id"`
	Name string      `json:"name"`
}

// ---- calls ----

// Ping checks the address and the credentials and returns the server's name and version.
func (c *Client) Ping(ctx context.Context) (string, string, error) {
	r, err := c.Call(ctx, "ping", nil)
	if err != nil {
		return "", "", err
	}
	name := r.Type
	if name == "" {
		name = "subsonic"
	}
	return name, firstNonEmpty(r.ServerVersion, r.Version), nil
}

// Extensions lists the OpenSubsonic extensions the server offers.
func (c *Client) Extensions(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	r, err := c.Call(ctx, "getOpenSubsonicExtensions", nil)
	if err != nil {
		return out
	}
	for _, e := range r.Extensions {
		out[e.Name] = true
	}
	return out
}

// ScanStatus returns the library scan state.
func (c *Client) ScanStatus(ctx context.Context) (*ScanStatus, error) {
	r, err := c.Call(ctx, "getScanStatus", nil)
	if err != nil {
		return nil, err
	}
	if r.ScanStatus == nil {
		return &ScanStatus{}, nil
	}
	return r.ScanStatus, nil
}

// StartScan asks the server to look for new files.
func (c *Client) StartScan(ctx context.Context) error {
	_, err := c.Call(ctx, "startScan", nil)
	return err
}

// SearchAll pages through every song, album or artist (search3 with an empty query).
func (c *Client) SearchAll(ctx context.Context, songOffset, songCount, albumOffset, albumCount, artistOffset, artistCount int) ([]Song, []Album, []Artist, error) {
	p := url.Values{"query": {""}, "songOffset": {itoa(songOffset)}, "songCount": {itoa(songCount)},
		"albumOffset": {itoa(albumOffset)}, "albumCount": {itoa(albumCount)}, "artistOffset": {itoa(artistOffset)}, "artistCount": {itoa(artistCount)}}
	r, err := c.Call(ctx, "search3", p)
	if err != nil {
		return nil, nil, nil, err
	}
	if r.SearchResult3 == nil {
		return nil, nil, nil, nil
	}
	return r.SearchResult3.Song, r.SearchResult3.Album, r.SearchResult3.Artist, nil
}

// Search finds artists, albums and songs.
func (c *Client) Search(ctx context.Context, q string, artists, albums, songs int) ([]Artist, []Album, []Song, error) {
	r, err := c.Call(ctx, "search3", url.Values{"query": {q}, "artistCount": {itoa(artists)}, "albumCount": {itoa(albums)}, "songCount": {itoa(songs)}})
	if err != nil || r.SearchResult3 == nil {
		return nil, nil, nil, err
	}
	return r.SearchResult3.Artist, r.SearchResult3.Album, r.SearchResult3.Song, nil
}

// Artists lists every artist.
func (c *Client) Artists(ctx context.Context) ([]Artist, error) {
	r, err := c.Call(ctx, "getArtists", nil)
	if err != nil || r.Artists == nil {
		return nil, err
	}
	var out []Artist
	for _, ix := range r.Artists.Index {
		out = append(out, ix.Artist...)
	}
	return out, nil
}

// Artist returns an artist with albums.
func (c *Client) Artist(ctx context.Context, id string) (*ArtistWithAlbums, error) {
	r, err := c.Call(ctx, "getArtist", url.Values{"id": {id}})
	if err != nil {
		return nil, err
	}
	if r.Artist == nil {
		return nil, &Error{Code: 70, Message: "artist not found"}
	}
	return r.Artist, nil
}

// ArtistInfo returns biography, images and similar artists.
func (c *Client) ArtistInfo(ctx context.Context, id string, similar int) (*ArtistInfo, error) {
	r, err := c.Call(ctx, "getArtistInfo2", url.Values{"id": {id}, "count": {itoa(similar)}, "includeNotPresent": {"false"}})
	if err != nil {
		return nil, err
	}
	if r.ArtistInfo2 == nil {
		return &ArtistInfo{}, nil
	}
	return r.ArtistInfo2, nil
}

// Album returns an album with songs.
func (c *Client) Album(ctx context.Context, id string) (*AlbumWithSongs, error) {
	r, err := c.Call(ctx, "getAlbum", url.Values{"id": {id}})
	if err != nil {
		return nil, err
	}
	if r.Album == nil {
		return nil, &Error{Code: 70, Message: "album not found"}
	}
	return r.Album, nil
}

// AlbumInfo returns notes about an album.
func (c *Client) AlbumInfo(ctx context.Context, id string) (*AlbumInfo, error) {
	r, err := c.Call(ctx, "getAlbumInfo2", url.Values{"id": {id}})
	if err != nil {
		return nil, err
	}
	if r.AlbumInfo == nil {
		return &AlbumInfo{}, nil
	}
	return r.AlbumInfo, nil
}

// AlbumList returns albums by type (newest, recent, frequent, random, starred, alphabeticalByName, byYear, byGenre…).
func (c *Client) AlbumList(ctx context.Context, typ string, size, offset int, extra url.Values) ([]Album, error) {
	p := url.Values{"type": {typ}, "size": {itoa(size)}, "offset": {itoa(offset)}}
	for k, v := range extra {
		p[k] = v
	}
	r, err := c.Call(ctx, "getAlbumList2", p)
	if err != nil || r.AlbumList2 == nil {
		return nil, err
	}
	return r.AlbumList2.Album, nil
}

// Song returns one song.
func (c *Client) Song(ctx context.Context, id string) (*Song, error) {
	r, err := c.Call(ctx, "getSong", url.Values{"id": {id}})
	if err != nil {
		return nil, err
	}
	if r.Song == nil {
		return nil, &Error{Code: 70, Message: "song not found"}
	}
	return r.Song, nil
}

// SimilarSongs is the server's radio for a song or an artist (uses its Last.fm agent).
func (c *Client) SimilarSongs(ctx context.Context, id string, count int) ([]Song, error) {
	r, err := c.Call(ctx, "getSimilarSongs2", url.Values{"id": {id}, "count": {itoa(count)}})
	if err != nil || r.SimilarSongs2 == nil {
		return nil, err
	}
	return r.SimilarSongs2.Song, nil
}

// TopSongs returns an artist's most popular songs (by name).
func (c *Client) TopSongs(ctx context.Context, artist string, count int) ([]Song, error) {
	r, err := c.Call(ctx, "getTopSongs", url.Values{"artist": {artist}, "count": {itoa(count)}})
	if err != nil || r.TopSongs == nil {
		return nil, err
	}
	return r.TopSongs.Song, nil
}

// RandomSongs returns random songs, optionally of a genre or years.
func (c *Client) RandomSongs(ctx context.Context, size int, genre string, from, to int) ([]Song, error) {
	p := url.Values{"size": {itoa(size)}}
	if genre != "" {
		p.Set("genre", genre)
	}
	if from > 0 {
		p.Set("fromYear", itoa(from))
	}
	if to > 0 {
		p.Set("toYear", itoa(to))
	}
	r, err := c.Call(ctx, "getRandomSongs", p)
	if err != nil || r.RandomSongs == nil {
		return nil, err
	}
	return r.RandomSongs.Song, nil
}

// Starred returns everything the person starred.
func (c *Client) Starred(ctx context.Context) ([]Artist, []Album, []Song, error) {
	r, err := c.Call(ctx, "getStarred2", nil)
	if err != nil || r.Starred2 == nil {
		return nil, nil, nil, err
	}
	return r.Starred2.Artist, r.Starred2.Album, r.Starred2.Song, nil
}

// Genres lists genres with counts.
func (c *Client) Genres(ctx context.Context) ([]Genre, error) {
	r, err := c.Call(ctx, "getGenres", nil)
	if err != nil || r.Genres == nil {
		return nil, err
	}
	return r.Genres.Genre, nil
}

// Star stars (or unstars) songs, albums or artists.
func (c *Client) Star(ctx context.Context, on bool, kind, id string) error {
	key := map[string]string{"song": "id", "album": "albumId", "artist": "artistId"}[kind]
	if key == "" {
		return errors.New("unknown kind")
	}
	m := "star"
	if !on {
		m = "unstar"
	}
	_, err := c.Call(ctx, m, url.Values{key: {id}})
	return err
}

// Rate sets a 0–5 rating.
func (c *Client) Rate(ctx context.Context, id string, rating int) error {
	_, err := c.Call(ctx, "setRating", url.Values{"id": {id}, "rating": {itoa(rating)}})
	return err
}

// Scrobble registers a play (submission) or now-playing (!submission).
func (c *Client) Scrobble(ctx context.Context, id string, at time.Time, submission bool) error {
	p := url.Values{"id": {id}, "submission": {strconv.FormatBool(submission)}}
	if submission && !at.IsZero() {
		p.Set("time", strconv.FormatInt(at.UnixMilli(), 10))
	}
	_, err := c.Call(ctx, "scrobble", p)
	return err
}

// Lyrics returns structured (possibly synced) lyrics the server has for a song.
func (c *Client) Lyrics(ctx context.Context, id string) ([]Lyrics, error) {
	r, err := c.Call(ctx, "getLyricsBySongId", url.Values{"id": {id}})
	if err != nil || r.LyricsList == nil {
		return nil, err
	}
	return r.LyricsList.StructuredLyrics, nil
}

// Playlists lists the person's playlists (and public ones).
func (c *Client) Playlists(ctx context.Context) ([]Playlist, error) {
	r, err := c.Call(ctx, "getPlaylists", nil)
	if err != nil || r.Playlists == nil {
		return nil, err
	}
	return r.Playlists.Playlist, nil
}

// Playlist returns a playlist with songs.
func (c *Client) Playlist(ctx context.Context, id string) (*PlaylistWithSongs, error) {
	r, err := c.Call(ctx, "getPlaylist", url.Values{"id": {id}})
	if err != nil {
		return nil, err
	}
	if r.Playlist == nil {
		return nil, &Error{Code: 70, Message: "playlist not found"}
	}
	return r.Playlist, nil
}

// CreatePlaylist makes a playlist, or replaces all songs of an existing one (id != "").
func (c *Client) CreatePlaylist(ctx context.Context, id, name string, songIDs []string) (*PlaylistWithSongs, error) {
	p := url.Values{}
	if id != "" {
		p.Set("playlistId", id)
	} else {
		p.Set("name", name)
	}
	for _, s := range songIDs {
		p.Add("songId", s)
	}
	r, err := c.Call(ctx, "createPlaylist", p)
	if err != nil {
		return nil, err
	}
	if r.Playlist == nil {
		return &PlaylistWithSongs{}, nil
	}
	return r.Playlist, nil
}

// PlaylistUpdate is what updatePlaylist changes.
type PlaylistUpdate struct {
	Name, Comment *string
	Public        *bool
	Add           []string
	RemoveIndex   []int
}

// UpdatePlaylist renames, comments, adds and removes songs.
func (c *Client) UpdatePlaylist(ctx context.Context, id string, u PlaylistUpdate) error {
	p := url.Values{"playlistId": {id}}
	if u.Name != nil {
		p.Set("name", *u.Name)
	}
	if u.Comment != nil {
		p.Set("comment", *u.Comment)
	}
	if u.Public != nil {
		p.Set("public", strconv.FormatBool(*u.Public))
	}
	for _, s := range u.Add {
		p.Add("songIdToAdd", s)
	}
	for _, i := range u.RemoveIndex {
		p.Add("songIndexToRemove", itoa(i))
	}
	_, err := c.Call(ctx, "updatePlaylist", p)
	return err
}

// DeletePlaylist removes a playlist.
func (c *Client) DeletePlaylist(ctx context.Context, id string) error {
	_, err := c.Call(ctx, "deletePlaylist", url.Values{"id": {id}})
	return err
}

// SavePlayQueue stores the queue so other Subsonic players can pick it up.
func (c *Client) SavePlayQueue(ctx context.Context, ids []string, current string, positionMS int64) error {
	p := url.Values{}
	for _, id := range ids {
		p.Add("id", id)
	}
	if current != "" {
		p.Set("current", current)
		p.Set("position", strconv.FormatInt(positionMS, 10))
	}
	_, err := c.Call(ctx, "savePlayQueue", p)
	return err
}

// GetPlayQueue returns the saved queue.
func (c *Client) GetPlayQueue(ctx context.Context) (*PlayQueue, error) {
	r, err := c.Call(ctx, "getPlayQueue", nil)
	if err != nil {
		return nil, err
	}
	if r.PlayQueue == nil {
		return &PlayQueue{}, nil
	}
	return r.PlayQueue, nil
}

// NowPlaying lists what the server's players are playing (all users the account may see).
func (c *Client) NowPlaying(ctx context.Context) ([]NowPlayingEntry, error) {
	r, err := c.Call(ctx, "getNowPlaying", nil)
	if err != nil || r.NowPlaying == nil {
		return nil, err
	}
	return r.NowPlaying.Entry, nil
}

// Open starts a raw request (stream, getCoverArt, download) passing the
// browser's Range headers through.
func (c *Client) Open(ctx context.Context, method string, params url.Values, h http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL(method, params), nil)
	if err != nil {
		return nil, err
	}
	for _, k := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if v := h.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	return c.Stream.Do(req)
}

func itoa(n int) string { return strconv.Itoa(n) }

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// UpdatePlaylistComment sets a playlist's comment.
func (c *Client) UpdatePlaylistComment(ctx context.Context, id, comment string) error {
	return c.UpdatePlaylist(ctx, id, PlaylistUpdate{Comment: &comment})
}
