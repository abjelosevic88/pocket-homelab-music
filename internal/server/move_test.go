package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-music/internal/config"
)

// moveServer is testServer behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for ana@ or ben@.
func moveServer(t *testing.T) (map[string]string, http.Handler) {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "listening:read"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"music","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	_, _, tok, h := testServer(t, func(c *config.Config) { c.CoreURL = coreSrv.URL })
	return tok, h
}

func rawCall(t *testing.T, h http.Handler, tok, method, path, ctype string, body []byte) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func TestMoveLinkScopes(t *testing.T) {
	_, h := moveServer(t)
	exp, imp, read := "pcx_ben_export", "pcx_ben_import", "pcx_ben_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "GET", "/api/v1/history/export", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{exp, "GET", "/api/v1/smart", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{imp, "POST", "/api/v1/listens", 403},
		{imp, "POST", "/api/v1/smart", 403},
		{imp, "POST", "/api/v1/imports", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
	} {
		if code, body := rawCall(t, h, c.tok, c.method, c.path, "application/json", []byte("{}")); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, body := rawCall(t, h, exp, "GET", core.PortablePath, "", nil)
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 2 || len(p.Imports) != 1 || p.Imports[0].ContentType != "application/json" {
		t.Fatalf("portable: %s", body)
	}
}

// Move brings Ana's plays and smart playlists to Ben: the import adds once,
// and nothing of one person shows up for the other.
func TestMoveImportAddsOnce(t *testing.T) {
	tok, h := moveServer(t)
	call(t, h, tok["ana"], "POST", "/api/v1/listens", map[string]any{"song_id": "s1", "played_at": time.Now().Add(-time.Hour).UnixMilli(), "ms_played": 200000})
	if code, out := call(t, h, tok["ana"], "POST", "/api/v1/smart", map[string]any{"name": "Synthwave", "sort": "title", "limit_count": 10,
		"rules": map[string]any{"match": "all", "conditions": []map[string]any{{"field": "genre", "op": "is", "value": "synthwave"}}}}); code != 200 {
		t.Fatalf("smart: %d %v", code, out)
	}

	code, export := rawCall(t, h, "pcx_ana_export", "GET", "/api/v1/export.json", "", nil)
	if code != 200 || !strings.Contains(string(export), "Blue Hour") || !strings.Contains(string(export), "Synthwave") {
		t.Fatalf("ana's export: %d %s", code, export)
	}
	if _, csv := rawCall(t, h, "pcx_ana_export", "GET", "/api/v1/history/export", "", nil); !strings.Contains(string(csv), "Blue Hour") {
		t.Fatalf("ana's csv: %s", csv)
	}
	if _, ben := rawCall(t, h, "pcx_ben_export", "GET", "/api/v1/export.json", "", nil); strings.Contains(string(ben), "Blue Hour") || strings.Contains(string(ben), "Synthwave") {
		t.Fatalf("ben's export holds ana's music: %s", ben)
	}

	for run, want := range []float64{1, 0} {
		code, body := rawCall(t, h, "pcx_ben_import", "POST", "/api/v1/import", "application/json", export)
		var m map[string]float64
		_ = json.Unmarshal(body, &m)
		if code != 200 || m["listens"] != want || m["smart_playlists"] != want {
			t.Fatalf("import run %d: %d %s", run+1, code, body)
		}
	}
	_, ben := call(t, h, tok["ben"], "GET", "/api/v1/history", nil)
	_, ana := call(t, h, tok["ana"], "GET", "/api/v1/history", nil)
	if ben["total"].(float64) != 1 || ana["total"].(float64) != 1 {
		t.Fatalf("history: ben %v, ana %v", ben["total"], ana["total"])
	}
	_, benSmart := call(t, h, tok["ben"], "GET", "/api/v1/smart", nil)
	_, anaSmart := call(t, h, tok["ana"], "GET", "/api/v1/smart", nil)
	if len(benSmart["_list"].([]any)) != 1 || len(anaSmart["_list"].([]any)) != 1 {
		t.Fatalf("smart: ben %v, ana %v", benSmart, anaSmart)
	}
}
