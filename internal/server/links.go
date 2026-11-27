package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Music as the
// target: another Pocket app the person allowed in Core may act here for them
// with a pcx_ token, only within these scopes. Every route not listed in
// linkRoutes stays closed to it (403).

var linkOffers = []core.Offer{
	{Scope: "listening:read", Name: "See what you listen to", Description: "Read your plays, listening stats and what is playing now, for your journal, dashboards and recaps."},
	{Scope: "playlists:write", Name: "Make playlists", Description: "Find songs in your library and create or add to playlists (a trip soundtrack, a workout mix)."},
	{Scope: "music:play", Name: "Play your songs", Description: "Find songs in your library, play them and read their timed words and covers in another app (Pocket Karaoke sings along to them). Nothing is changed or copied."},
}

// portable is what Pocket Move may copy out and bring in for a person
// (docs/move.md in pocket-homelab-core); Register adds move:export/import.
var portable = &core.Portable{
	Exports: []core.PortableExport{
		{Path: "/api/v1/export.json", File: "music.json", Kind: core.KindPocket, Title: "Every song you played and your smart playlists' rules"},
		{Path: "/api/v1/history/export", File: "listens.csv", Kind: core.KindCSV, Title: "Every song you played, as a table"},
	},
	Imports: []core.PortableImport{
		{Path: "/api/v1/import", Kind: core.KindPocket, ContentType: "application/json", MaxBytes: 32 << 20, Title: "Adds the plays and smart playlists from another box; plays already here and playlists with the same name are skipped"},
	},
}

// capabilities is what Pocket Search, Pocket Day and Pocket Hub may ask here
// for a person (docs/capabilities.md in pocket-homelab-core); Register adds
// pocket:search, pocket:day and pocket:widget, and each opens only its one
// reading route (capabilities.go). Nothing can be kept here from a quick
// line: a song is not something a person types in.
var capabilities = core.Capabilities{
	Search: &core.Searchable{What: "Your albums, artists and songs, by name", Kinds: []string{"album", "artist", "song"}, Bangs: []string{"music", "song", "album"}},
	Day:    &core.DayFeed{Label: "Music", One: "song", Many: "songs", Unit: "min", What: "The songs you listened to that day and for how long"},
	Widget: &core.Widget{Title: "Recently played", What: "What is playing now and the songs you played last"},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(dashboard|history|stats|recap|devices)$`), "listening:read"},
	{"GET", regexp.MustCompile(`^/api/v1/day/\d{4}-\d{2}-\d{2}$`), "listening:read"},
	{"GET", regexp.MustCompile(`^/api/v1/(search|playlists|genres)$`), "playlists:write"},
	{"GET", regexp.MustCompile(`^/api/v1/(songs|albums|artists|playlists)/[A-Za-z0-9_-]+$`), "playlists:write"},
	{"POST", regexp.MustCompile(`^/api/v1/playlists$`), "playlists:write"},
	{"PUT", regexp.MustCompile(`^/api/v1/playlists/[A-Za-z0-9_-]+$`), "playlists:write"},
	{"GET", regexp.MustCompile(`^/api/v1/cover/[A-Za-z0-9_-]+$`), "playlists:write"},
	{"GET", regexp.MustCompile(`^/api/v1/search$`), "music:play"},
	{"GET", regexp.MustCompile(`^/api/v1/(songs|stream|lyrics|cover)/[A-Za-z0-9_-]+$`), "music:play"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	if portable.LinkAllows(scopes, r) || capabilities.LinkAllows(scopes, r) {
		return true
	}
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token, "" otherwise.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}
