package server

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
	"github.com/abjelosevic88/pocket-homelab-music/internal/subsonic"
)

// The three routes behind `capabilities` (links.go): /api/v1/pocket/search,
// /day/{date} and /widget, for Pocket Search, Pocket Day and Pocket Hub. Each
// runs as the request's person and reads only what is theirs; the SDK's
// handlers check and cap what goes in and out. They read what GET /search,
// /day/{date}, /history and /dashboard read, and change nothing: no song is
// played, no playlist touched, no play recorded.
//
// The music itself is on the music server, but a person's library is indexed
// here and their plays are kept here, so the day and the widget never ask the
// server. The search asks it only where GET /search does: while the person's
// index is still empty, with their own account and under the same limits.

const (
	dayPlays    = 1000 // plays of one day read, as GET /day/{date} reads them
	daySongs    = 199  // songs of a day listed one by one; the rest share one last line
	widgetPlays = 12   // the newest plays the widget reads
	widgetRows  = 5    // songs it shows
)

func albumURL(id string) string  { return "/albums/" + id }
func artistURL(id string) string { return "/artists/" + id }

// songURL opens a song where the app shows it: in its album (a song has no
// page of its own), else the list of songs.
func songURL(albumID string) string {
	if albumID == "" {
		return "/songs"
	}
	return albumURL(albumID)
}

// dots joins the parts of a line that are there: "Ana Band · 1986".
func dots(parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// count is a number with its word: "1 album", "3 albums"; nothing for none.
func count(n int, one, many string) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// clock is a song's length as the pages show it: "3:05", "1:02:03".
func clock(sec int) string {
	if sec <= 0 {
		return ""
	}
	if h := sec / 3600; h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, sec%3600/60, sec%60)
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// by names a song with who plays it: "Blue Hour by Ana Band".
func by(title, artist string) string {
	if artist == "" {
		return title
	}
	return title + " by " + artist
}

// listenedWords is a length of listening as the day's line says it ("1 h 20
// min"), or the number of plays when it is under a minute or not known.
func listenedWords(seconds int64, plays int) string {
	if seconds >= 60 {
		return humanMinutes(seconds)
	}
	return count(plays, "play", "plays")
}

// ---------- search ----------

// hasWord reports whether there is a letter or a digit to look for.
func hasWord(q string) bool {
	return strings.IndexFunc(q, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}

// searchWords are the words of a name as the index folds them: lower case, no accents.
func searchWords(s string) []string {
	return strings.FieldsFunc(store.Fold(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

// nameScore says how well a name answers the words: it is the name (4), the
// name starts with them (3), the name has them all (2), or they were found
// in something else on its line, as an album by its artist's name (1).
func nameScore(name string, words []string) float64 {
	have := searchWords(name)
	n, q := strings.Join(have, " "), strings.Join(words, " ")
	switch {
	case n == q:
		return 4
	case strings.HasPrefix(n, q):
		return 3
	}
	for _, w := range words {
		found := false
		for _, h := range have {
			found = found || strings.HasPrefix(h, w)
		}
		if !found {
			return 1
		}
	}
	return 2
}

// pocketSearch answers Pocket Search with the albums, artists and songs GET
// /search finds, as one list: the name that answers the words best first,
// and of equally good ones the artist, then the album, then the song. The
// lookup is the same as that route's: the person's own index, and while it
// is still empty the music server, with their own account. Someone with no
// account there (or one that no longer signs in) finds nothing; a server
// that does not answer is an error.
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	ctx, u := r.Context(), userFrom(r.Context())
	if u == nil || !hasWord(q) {
		return nil, nil
	}
	var (
		artists []store.Artist
		albums  []store.Album
		songs   []store.Song
	)
	if s.store.Sync(ctx, u.ID).SyncedAt == "" {
		mc, err := s.musicFor(ctx, u)
		if isNoMusic(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if artists, albums, songs, err = searchServer(ctx, mc, q, limit); subsonic.IsAuth(err) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
	} else {
		artists, albums, songs = s.searchIndex(ctx, u.ID, q, limit)
	}
	words := searchWords(q)
	out := make([]core.SearchItem, 0, len(artists)+len(albums)+len(songs))
	for _, a := range artists {
		out = append(out, core.SearchItem{ID: "artist:" + a.ID, Title: a.Name, Kind: "artist", URL: artistURL(a.ID),
			Snippet: dots(count(a.AlbumCount, "album", "albums"), count(a.SongCount, "song", "songs")), Score: nameScore(a.Name, words) + 0.2})
	}
	for _, a := range albums {
		year := ""
		if a.Year > 0 {
			year = strconv.Itoa(a.Year)
		}
		out = append(out, core.SearchItem{ID: "album:" + a.ID, Title: a.Name, Kind: "album", URL: albumURL(a.ID),
			Snippet: dots(a.Artist, year), Score: nameScore(a.Name, words) + 0.1})
	}
	for _, so := range songs {
		out = append(out, core.SearchItem{ID: "song:" + so.ID, Title: so.Title, Kind: "song", URL: songURL(so.AlbumID),
			Snippet: dots(so.Artist, so.Album, clock(so.Duration)), Score: nameScore(so.Title, words)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------- day ----------

// daySong is one song on a day: every play of it together.
type daySong struct {
	first   store.Listen // the earliest play
	plays   int
	seconds int64
}

// listenedSeconds is what a play counts for, as the stats count it: what was
// played, else the song's length.
func listenedSeconds(l store.Listen) int64 {
	if l.MSPlayed > 0 {
		return l.MSPlayed / 1000
	}
	return int64(l.Duration)
}

// pocketDay answers Pocket Day from the plays kept here (what GET
// /day/{date} reads): each song listened to that day once, with when it was
// first played, the minutes it got and how many times it played, so the
// minutes add up to the day's listening. The line names the artist the day
// mostly went to. The day runs in the caller's time zone when it gives one,
// else in the server's. A day with more songs than a day's list holds keeps
// the ones listened to longest and counts the rest in one last line.
func (s *Server) pocketDay(r *http.Request, date string, tz *time.Location) (*core.Day, error) {
	ctx, uid := r.Context(), actorID(r.Context())
	loc := time.Local
	if tz != nil {
		loc = tz
	}
	d, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil, err
	}
	from, to := store.Stamp(d), store.Stamp(d.AddDate(0, 0, 1))
	ls, err := s.store.Listens(ctx, uid, from, to, dayPlays, "")
	if err != nil {
		return nil, err
	}
	if len(ls) == 0 {
		return nil, nil
	}
	_, off := d.Zone()
	totals, err := s.store.PeriodTotals(ctx, uid, from, to, off/60)
	if err != nil {
		return nil, err
	}
	artists, _ := s.store.TopBy(ctx, uid, "artist", from, to, 1)
	albums, _ := s.store.TopBy(ctx, uid, "album", from, to, 1)

	// one line per song, the way the top lists tell songs apart: by title and artist
	var songs []*daySong
	byKey := map[string]*daySong{}
	for i := len(ls) - 1; i >= 0; i-- { // oldest first
		l := ls[i]
		key := strings.ToLower(l.Title) + "\x00" + strings.ToLower(l.Artist)
		x := byKey[key]
		if x == nil {
			x = &daySong{first: l}
			byKey[key] = x
			songs = append(songs, x)
		}
		x.plays++
		x.seconds += listenedSeconds(l)
	}
	var rest daySong
	if len(songs) > daySongs {
		sort.SliceStable(songs, func(i, j int) bool {
			if songs[i].seconds != songs[j].seconds {
				return songs[i].seconds > songs[j].seconds
			}
			return songs[i].plays > songs[j].plays
		})
		for _, x := range songs[daySongs:] {
			rest.plays += x.plays
			rest.seconds += x.seconds
		}
		rest.first.Title = count(len(songs)-daySongs, "more song", "more songs")
		songs = songs[:daySongs]
	}
	minutes := func(sec int64) float64 { return math.Round(float64(sec)/60*100) / 100 }

	day := &core.Day{Date: date}
	for _, x := range songs {
		l := x.first
		// no kind: Pocket Day then groups the songs by the second line, the artist
		it := core.DayItem{ID: l.SongID, Title: l.Title, Sub: l.Artist, Minutes: minutes(x.seconds), Qty: float64(x.plays), URL: songURL(l.AlbumID)}
		if t, err := time.Parse(store.StampFormat, l.PlayedAt); err == nil {
			it.Time = t.In(loc).Format("15:04")
		}
		if l.AlbumID != "" {
			it.Image = "/api/v1/cover/al-" + l.AlbumID + "?size=96"
		}
		day.Items = append(day.Items, it)
	}
	if rest.plays > 0 {
		day.Items = append(day.Items, core.DayItem{Title: rest.first.Title, Minutes: minutes(rest.seconds), Qty: float64(rest.plays), URL: "/history"})
	}

	// "Listened for 1 h 20 min, mostly Ana Band", "Listened to Ana Band for 20 min", "Listened to 3 songs"
	top := ""
	if len(artists) > 0 {
		top = artists[0].Name
	}
	long := totals.Seconds >= 60
	switch {
	case top != "" && totals.Artists == 1 && long:
		day.Summary = fmt.Sprintf("Listened to %s for %s", top, humanMinutes(totals.Seconds))
	case top != "" && totals.Artists == 1:
		day.Summary = fmt.Sprintf("Listened to %s by %s", count(totals.Songs, "song", "songs"), top)
	case long:
		day.Summary = "Listened for " + humanMinutes(totals.Seconds)
	default:
		day.Summary = "Listened to " + count(totals.Songs, "song", "songs")
	}
	if top != "" && totals.Artists > 1 {
		day.Summary += ", mostly " + top
	}
	if long {
		day.Metrics = append(day.Metrics, core.Metric{Label: "Time", Value: humanMinutes(totals.Seconds)})
	}
	day.Metrics = append(day.Metrics, core.Metric{Label: "Plays", Value: strconv.Itoa(totals.Plays)})
	if len(albums) > 0 {
		day.Metrics = append(day.Metrics, core.Metric{Label: "Top album", Value: albums[0].Name})
	}
	if totals.NewSongs > 0 {
		day.Metrics = append(day.Metrics, core.Metric{Label: "New to you", Value: count(totals.NewSongs, "song", "songs")})
	}
	return day, nil
}

// ---------- widget ----------

// pocketWidget answers Pocket Hub with what plays now, when one of the
// person's devices reported it here (what GET /dashboard says too), and the
// songs played last, each once with its artist. The numbers are today's and
// the last seven days' listening. All of it is in the app's own store: the
// newest few plays, the person's devices and one week of plays are read, and
// the music server is not asked. Someone who has played nothing gets nothing
// to show.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx, uid := r.Context(), actorID(r.Context())
	ls, err := s.store.Listens(ctx, uid, "", "", widgetPlays, "")
	if err != nil {
		return nil, err
	}
	var playing *store.Song
	for _, d := range s.deviceList(ctx, uid) {
		if d.Active && d.Playing && d.Song != nil {
			playing = d.Song
			break
		}
	}
	if len(ls) == 0 && playing == nil {
		return nil, nil
	}
	w := &core.WidgetData{URL: "/history"}
	seen := map[string]bool{}
	row := func(id, title, artist, albumID string) {
		key := strings.ToLower(title) + "\x00" + strings.ToLower(artist)
		if seen[key] || len(w.Items) == widgetRows {
			return
		}
		seen[key] = true
		it := core.WidgetItem{ID: id, Title: title, Detail: artist, URL: "/history"}
		if albumID != "" {
			it.URL = albumURL(albumID)
		}
		w.Items = append(w.Items, it)
	}
	if playing != nil {
		w.Line = "Playing now: " + by(playing.Title, playing.Artist)
		row(playing.ID, playing.Title, playing.Artist, playing.AlbumID)
	} else {
		w.Line = "Last played: " + by(ls[0].Title, ls[0].Artist)
	}
	for _, l := range ls {
		row(l.SongID, l.Title, l.Artist, l.AlbumID)
	}

	off := tzOffset(r)
	now := time.Now().In(zone(off))
	today := dayStart(now)
	days, _ := s.store.Series(ctx, uid, "day", store.Stamp(today.AddDate(0, 0, -6)), store.Stamp(today.AddDate(0, 0, 1)), off)
	var dayS, weekS int64
	dayN, weekN := 0, 0
	for _, b := range days {
		weekS, weekN = weekS+b.Seconds, weekN+b.Plays
		if b.Key == today.Format("2006-01-02") {
			dayS, dayN = b.Seconds, b.Plays
		}
	}
	if dayN > 0 {
		w.Metrics = append(w.Metrics, core.Metric{Label: "Today", Value: listenedWords(dayS, dayN)})
	}
	if weekN > 0 {
		w.Metrics = append(w.Metrics, core.Metric{Label: "Last 7 days", Value: listenedWords(weekS, weekN)})
	}
	return w, nil
}
