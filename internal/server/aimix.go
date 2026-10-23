package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-music/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// ---- "Make a mix": a sentence becomes a playlist ----
//
// The AI never sees the songs. It is shown the person's genres, decades and
// favourite artists and answers with a plan (genres, artists, years, how
// familiar); the plan is then run over the library here, so the result only
// ever contains music the person has. Without AI the same plan is guessed
// from the words: genre and artist names, decades ("80s"), "loved", "new".

// aiFor is the person's AI: Pocket AI through Core, their own OpenAI-compatible key, or nil with why.
func (s *Server) aiFor(ctx context.Context, u *store.User, st store.Settings) (*ai.Client, string, string) {
	pocket, status := s.core.SelectAI(ctx, u.Email, st.AIProvider, st.OpenAIKey != "")
	if pocket != nil {
		if !status.Ready {
			return nil, "", "Pocket AI: " + status.Detail
		}
		c := ai.New("", "", "", 120*time.Second)
		c.Pocket = pocket.WithModel(st.AIPocketModel)
		return c, "pocket", ""
	}
	if st.OpenAIKey == "" {
		return nil, "", status.Detail
	}
	return ai.New(st.OpenAIBaseURL, st.OpenAIKey, st.OpenAIModel, 120*time.Second), "openai", ""
}

func validAIProvider(v string) bool { return core.ValidAIProvider(v) }

func validPocketModel(v string) bool { return core.ValidInferenceModel(v) }

func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	cl, used, why := s.aiFor(ctx, u, st)
	writeJSON(w, http.StatusOK, map[string]any{"ready": cl != nil, "provider": used, "detail": why})
}

func (s *Server) handleAIModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, status := s.core.SelectAI(ctx, userFrom(ctx).Email, "pocket", false)
	if c == nil || !status.Ready {
		writeErr(w, http.StatusServiceUnavailable, status.Detail)
		return
	}
	models, err := c.Models(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

// mixPlan is what the AI (or the word matcher) decides.
type mixPlan struct {
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Genres         []string `json:"genres"`
	Artists        []string `json:"artists"`
	ExcludeGenres  []string `json:"exclude_genres"`
	ExcludeArtists []string `json:"exclude_artists"`
	YearFrom       int      `json:"year_from"`
	YearTo         int      `json:"year_to"`
	Familiarity    string   `json:"familiarity"` // favorites | mixed | discover
	Keywords       []string `json:"keywords"`    // words to look for in song titles
	StarredOnly    bool     `json:"starred_only"`
	Count          int      `json:"count"`
}

var decadeRe = regexp.MustCompile(`(?i)\b(19|20)?([0-9])0'?s\b`)

// moods maps everyday words to genre fragments, for the plan without AI.
var moods = map[string][]string{
	"chill":      {"ambient", "chill", "downtempo", "lo-fi", "lofi", "trip hop", "trip-hop", "acoustic", "jazz", "new age", "dream pop"},
	"relax":      {"ambient", "chill", "downtempo", "acoustic", "jazz", "new age", "classical"},
	"calm":       {"ambient", "acoustic", "classical", "new age", "piano"},
	"sleep":      {"ambient", "drone", "new age", "classical", "piano"},
	"rain":       {"ambient", "trip hop", "jazz", "downtempo", "shoegaze", "post-rock"},
	"focus":      {"ambient", "classical", "post-rock", "soundtrack", "score", "instrumental", "lo-fi"},
	"study":      {"ambient", "classical", "post-rock", "soundtrack", "score", "lo-fi"},
	"work":       {"ambient", "post-rock", "soundtrack", "score", "electronic", "instrumental"},
	"workout":    {"metal", "hard rock", "punk", "electronic", "drum and bass", "hip hop", "edm"},
	"gym":        {"metal", "hard rock", "punk", "electronic", "drum and bass", "hip hop"},
	"run":        {"electronic", "drum and bass", "punk", "hard rock", "dance"},
	"party":      {"dance", "pop", "disco", "electronic", "house", "funk", "hip hop"},
	"dance":      {"dance", "disco", "house", "electronic", "funk"},
	"angry":      {"metal", "hardcore", "punk", "thrash", "death"},
	"heavy":      {"metal", "doom", "sludge", "stoner"},
	"sad":        {"doom", "post-rock", "shoegaze", "blues", "singer-songwriter", "slowcore"},
	"melancholy": {"doom", "post-rock", "shoegaze", "gothic", "darkwave"},
	"happy":      {"pop", "funk", "disco", "ska", "reggae", "indie pop"},
	"road":       {"rock", "hard rock", "classic rock", "southern rock", "blues rock", "heartland"},
	"drive":      {"rock", "synthwave", "outrun", "hard rock", "classic rock"},
	"epic":       {"soundtrack", "score", "symphonic", "power metal", "post-rock"},
	"game":       {"soundtrack", "score", "video game", "chiptune"},
	"movie":      {"soundtrack", "score"},
	"christmas":  {"christmas", "holiday"},
}

// guessPlan reads a plan out of the words alone.
func guessPlan(prompt string, genres []store.GenreCount, artists []string) mixPlan {
	p := mixPlan{Familiarity: "mixed"}
	low := " " + store.Fold(prompt) + " "
	words := strings.Fields(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 || r == '-' {
			return r
		}
		return ' '
	}, low))
	addGenre := func(name string) {
		for _, g := range p.Genres {
			if g == name {
				return
			}
		}
		p.Genres = append(p.Genres, name)
	}
	for _, g := range genres {
		fg := store.Fold(g.Name)
		if len(fg) >= 3 && strings.Contains(low, " "+fg+" ") {
			addGenre(g.Name)
			continue
		}
		// "synth" finds Synthwave and Synth-pop, "jazz" finds Jazz Fusion
		for _, w := range words {
			if len(w) >= 4 && strings.HasPrefix(fg, w) {
				addGenre(g.Name)
				break
			}
		}
	}
	pad := func(s string) string { return " " + strings.NewReplacer("-", " ", "/", " ", "&", " ").Replace(s) + " " }
	for _, w := range words {
		for mood, frags := range moods {
			// "rainy", "runs", "chilled" count; "workout" is not "work"
			if !strings.HasPrefix(w, mood) || len(w)-len(mood) > 2 {
				continue
			}
			for _, g := range genres {
				fg := pad(store.Fold(g.Name))
				for _, f := range frags {
					if strings.Contains(fg, pad(f)) {
						addGenre(g.Name)
						break
					}
				}
			}
		}
	}
	for _, a := range artists {
		if len(a) >= 3 && strings.Contains(low, " "+store.Fold(a)+" ") {
			p.Artists = append(p.Artists, a)
		}
	}
	if m := decadeRe.FindStringSubmatch(prompt); m != nil {
		d, _ := strconv.Atoi(m[2])
		century := 1900
		if m[1] == "20" || (m[1] == "" && d <= 2) {
			century = 2000
		}
		p.YearFrom, p.YearTo = century+d*10, century+d*10+9
	}
	switch {
	case strings.Contains(low, " new ") || strings.Contains(low, "never heard") || strings.Contains(low, "discover"):
		p.Familiarity = "discover"
	case strings.Contains(low, "favorite") || strings.Contains(low, "favourite") || strings.Contains(low, " best "):
		p.Familiarity = "favorites"
	}
	p.StarredOnly = strings.Contains(low, " loved ") || strings.Contains(low, " starred ")
	t := strings.TrimSpace(prompt)
	if len(t) > 60 {
		t = t[:60]
	}
	if t != "" {
		t = strings.ToUpper(t[:1]) + t[1:]
	}
	p.Title = t
	return p
}

func (s *Server) handleMakeMix(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		Prompt string `json:"prompt"`
		Count  int    `json:"count"`
		Save   bool   `json:"save"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Prompt) == "" {
		writeErr(w, http.StatusBadRequest, "say what you'd like to hear")
		return
	}
	in.Prompt = trim(in.Prompt, 400)
	if in.Count <= 0 || in.Count > 200 {
		in.Count = 40
	}
	st, _ := s.store.GetSettings(ctx, u.ID)
	genres, err := s.store.Genres(ctx, u.ID)
	if err != nil {
		s.fail(w, err, "genres")
		return
	}
	if len(genres) == 0 {
		writeErr(w, http.StatusConflict, "your library is not indexed yet")
		return
	}
	sort.Slice(genres, func(i, j int) bool { return genres[i].Songs > genres[j].Songs })
	topArtists, _, _ := s.store.ListArtists(ctx, u.ID, store.ArtistFilter{Sort: "plays", AlbumArtist: true, Limit: 80})
	bigArtists, _, _ := s.store.ListArtists(ctx, u.ID, store.ArtistFilter{Sort: "songs", AlbumArtist: true, Limit: 120})
	var artistNames []string
	seen := map[string]bool{}
	for _, a := range append(topArtists, bigArtists...) {
		if !seen[a.Name] {
			seen[a.Name] = true
			artistNames = append(artistNames, a.Name)
		}
	}
	plan := guessPlan(in.Prompt, genres, artistNames)
	used := ""
	if cl, provider, _ := s.aiFor(ctx, u, st); cl != nil {
		var gl []string
		for _, g := range head(genres, 160) {
			gl = append(gl, fmt.Sprintf("%s (%d)", g.Name, g.Songs))
		}
		system := `You plan playlists from a person's OWN music library. You are given the library's genres (with song counts) and well-known artists in it. ` +
			`Answer with ONE JSON object: {"title": short catchy playlist name, "description": one sentence, "genres": [genres from the list that fit], "artists": [artists from the list that fit, may be empty], ` +
			`"exclude_genres": [...], "exclude_artists": [...], "year_from": 0 or a year, "year_to": 0 or a year, "familiarity": "favorites" | "mixed" | "discover", ` +
			`"keywords": [a few words that might appear in fitting song titles], "starred_only": false}. ` +
			`Use ONLY genre and artist names exactly as listed. Prefer several genres over one so there is enough music. Think about mood and energy: a calm request picks calm genres.`
		prompt := fmt.Sprintf("Request: %s\n\nGenres: %s\n\nArtists: %s", in.Prompt, strings.Join(gl, "; "), strings.Join(head(artistNames, 150), "; "))
		var aiPlan mixPlan
		if err := aiJSON(ctx, cl, system, prompt, &aiPlan); err == nil && (len(aiPlan.Genres) > 0 || len(aiPlan.Artists) > 0) {
			plan, used = aiPlan, provider
		} else if err != nil {
			s.log.Warn("make a mix: AI failed, guessing from the words", "err", err)
		}
	}
	plan.Count = in.Count
	songs := s.runPlan(ctx, u.ID, st, plan)
	if len(songs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"name": plan.Title, "description": "Nothing in your library fits that. Try other words, a genre or an artist.", "songs": []store.Song{}, "plan": plan, "ai": used})
		return
	}
	name := firstNonEmpty(plan.Title, "Your mix")
	out := map[string]any{"name": name, "description": plan.Description, "songs": songs, "plan": plan, "ai": used}
	if in.Save {
		ids := make([]string, 0, len(songs))
		for _, so := range songs {
			ids = append(ids, so.ID)
		}
		if mc, err := s.musicFor(ctx, u); err == nil {
			if pl, err := mc.c.CreatePlaylist(ctx, "", name, ids); err == nil {
				comment := firstNonEmpty(plan.Description, "Made by Pocket Music from: "+in.Prompt)
				_ = mc.c.UpdatePlaylistComment(ctx, pl.ID, comment)
				out["playlist_id"] = pl.ID
			}
		}
	}
	s.publish(ctx, u, "music.mix_made", name, map[string]any{"name": name, "prompt": in.Prompt, "songs": len(songs)})
	writeJSON(w, http.StatusOK, out)
}

// runPlan picks songs from the library for a plan.
func (s *Server) runPlan(ctx context.Context, uid int64, st store.Settings, p mixPlan) []store.Song {
	lib, err := s.loadLibrary(ctx, uid, st, fmt.Sprint(uid, time.Now().UnixNano()))
	if err != nil {
		return nil
	}
	fold := func(xs []string) map[string]bool {
		m := map[string]bool{}
		for _, x := range xs {
			if x = store.Fold(x); x != "" {
				m[x] = true
			}
		}
		return m
	}
	gs, ar, xg, xa := fold(p.Genres), fold(p.Artists), fold(p.ExcludeGenres), fold(p.ExcludeArtists)
	allowAmbient := false
	for g := range gs {
		if lib.ambient[g] {
			allowAmbient = true
		}
	}
	var kws []string
	for _, k := range p.Keywords {
		if k = store.Fold(k); len(k) >= 3 {
			kws = append(kws, k)
		}
	}
	score := func(so *store.Song, loose bool) float64 {
		if p.StarredOnly && so.StarredAt == "" {
			return 0
		}
		if p.YearFrom > 0 && so.Year > 0 && so.Year < p.YearFrom || p.YearTo > 0 && so.Year > p.YearTo {
			return 0
		}
		if (p.YearFrom > 0 || p.YearTo > 0) && so.Year == 0 {
			return 0
		}
		if xa[store.Fold(so.Artist)] || xa[store.Fold(so.AlbumArtist)] {
			return 0
		}
		v := 0.0
		for _, g := range lib.genresOf(so) {
			fg := store.Fold(g)
			if xg[fg] {
				return 0
			}
			if gs[fg] {
				v += 2
			} else if loose {
				for want := range gs {
					if strings.Contains(fg, want) || strings.Contains(want, fg) {
						v += 1
						break
					}
				}
			}
		}
		if ar[store.Fold(so.Artist)] || ar[store.Fold(so.AlbumArtist)] {
			v += 3
		}
		if len(gs) == 0 && len(ar) == 0 && (p.YearFrom > 0 || p.StarredOnly) {
			v += 1 // only a decade or "loved" was asked for
		}
		t := store.Fold(so.Title)
		kw := 0.0
		for _, k := range kws {
			if strings.Contains(t, k) {
				kw += 1.5
			}
		}
		if v == 0 && !(len(gs) == 0 && len(ar) == 0 && p.YearFrom == 0 && !p.StarredOnly) {
			return 0 // title words only lean, they don't qualify a song on their own
		}
		v += kw
		if v == 0 {
			return 0
		}
		plays := float64(lib.stats[so.ID].Plays)
		switch p.Familiarity {
		case "favorites":
			v += minF(plays/3, 3)
			if so.StarredAt != "" {
				v += 2
			}
		case "discover":
			if plays == 0 {
				v += 2
			} else {
				v -= minF(plays/5, 1.5)
			}
		default:
			v += minF(plays/8, 1)
			if so.StarredAt != "" {
				v += 0.7
			}
		}
		return maxF(v, 0.1)
	}
	pick := func(loose bool) []*store.Song {
		var cands []*store.Song
		vals := map[string]float64{}
		for i := range lib.songs {
			so := &lib.songs[i]
			if !lib.usable(so, allowAmbient) {
				continue
			}
			if v := score(so, loose); v > 0 {
				cands = append(cands, so)
				vals[so.ID] = v
			}
		}
		return weighted(lib.rng, cands, func(so *store.Song) float64 { return vals[so.ID] })
	}
	cands := pick(false)
	if len(cands) < p.Count/2 {
		cands = pick(true)
	}
	per := 4
	if len(ar) > 0 && len(ar) <= 2 {
		per = p.Count // asked for an artist: don't cap them
	}
	chosen := spread(cands, per, p.Count)
	out := make([]store.Song, 0, len(chosen))
	for _, so := range chosen {
		out = append(out, *so)
	}
	return out
}

// aiJSON asks for one JSON object and tolerates code fences and chatter around it.
func aiJSON(ctx context.Context, c *ai.Client, system, prompt string, v any) error {
	txt, err := c.Complete(ctx, system, prompt, true)
	if err != nil {
		return err
	}
	if i, j := strings.Index(txt, "{"), strings.LastIndex(txt, "}"); i >= 0 && j > i {
		txt = txt[i : j+1]
	}
	if err := json.Unmarshal([]byte(txt), v); err != nil {
		return fmt.Errorf("the AI's answer was not valid JSON: %w", err)
	}
	return nil
}
