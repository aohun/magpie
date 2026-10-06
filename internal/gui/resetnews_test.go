package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

// While a Codex account is signed in, the site is asked every five minutes,
// and a reset newer than the one last recorded is shown once. The first ask
// only records what the site has now; offline, the alert off, and no Codex
// account signed in, nothing is asked and nothing is shown.
func TestResetNewsAnnounced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	dir := filepath.Join(home, "magpie")
	id := "2106131810921136451"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"latest_reset": map[string]any{"id": id, "reset_type": "regular",
				"announced_at": "2026-10-02T21:18:48.000Z", "text": "Reset all propagated. Enjoy.",
				"source": map[string]any{"type": "x_post", "author": "thsottiaux", "url": "https://x.com/thsottiaux/status/" + id}},
		}})
	}))
	defer srv.Close()
	oldFeed, oldGate := resetFeed, codexAlertGate
	defer func() { resetFeed, codexAlertGate = oldFeed, oldGate }()
	resetFeed, codexAlertGate = srv.URL, func() bool { return true }
	seen := func() string {
		b, _ := os.ReadFile(filepath.Join(dir, resetSeenFile))
		return strings.TrimSpace(string(b))
	}
	poll := func(n *resetNews) resetNewsJSON {
		n.poll(context.Background())
		return n.get()
	}

	// the first ask records the reset the site has now, without announcing it
	n := &resetNews{}
	if j := poll(n); j.Show || j.Reset == nil || j.Reset.ID != id || seen() != id {
		t.Fatalf("first ask: %+v", j)
	}
	// the same reset again: nothing
	if j := poll(n); j.Show {
		t.Fatalf("same reset: %+v", j)
	}
	// a newer reset: shown once, and kept for a restart not to repeat it
	id = "2106999999999999999"
	if j := poll(n); !j.Show || j.Reset.ID != id || j.Reset.Type != "regular" || j.Reset.URL == "" {
		t.Fatalf("newer reset: %+v", j)
	}
	if j := n.get(); j.Show {
		t.Fatalf("shown twice: %+v", j)
	}
	if s := seen(); s != id {
		t.Fatalf("kept %q", s)
	}
	// a restart reads the reset last recorded: still nothing to announce
	n = &resetNews{}
	n.startSeen()
	if j := poll(n); j.Show {
		t.Fatalf("after restart: %+v", j)
	}
	// the site without a reset yet: nothing, and what was kept is kept
	id = ""
	if j := poll(n); j.Show || seen() != "2106999999999999999" {
		t.Fatalf("no reset: %+v, kept %q", j, seen())
	}
}

// A poll is skipped with the alert off or without a Codex account signed in.
func TestResetNewsQuiet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	asks := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { asks++ }))
	defer srv.Close()
	oldFeed, oldGate := resetFeed, codexAlertGate
	defer func() { resetFeed, codexAlertGate = oldFeed, oldGate }()
	resetFeed, codexAlertGate = srv.URL, func() bool { return false }
	n := &resetNews{}
	n.poll(context.Background())
	if asks != 0 {
		t.Fatalf("asked without Codex: %d", asks)
	}
	codexAlertGate = func() bool { return true }
	os.MkdirAll(settings.Dir(), 0o755)
	os.WriteFile(filepath.Join(settings.Dir(), "settings.json"), []byte(`{"noResetAlert":true}`), 0o644)
	n.poll(context.Background())
	if asks != 0 {
		t.Fatalf("asked with the alert off: %d", asks)
	}
}

// The routes: a page asks for what to show and says once it has shown it.
func TestResetNewsRoutes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	oldGate := codexAlertGate
	defer func() { codexAlertGate = oldGate }()
	codexAlertGate = func() bool { return true }
	resets.mu.Lock()
	oldSeen, oldPending, oldShown, oldLatest := resets.seen, resets.pending, resets.shown, resets.latest
	resets.seen, resets.pending, resets.shown = "1", true, false
	resets.latest = &resetItem{ID: "1", Type: "regular", Announced: "2026-10-02T21:18:48.000Z", Text: "Reset all propagated. Enjoy."}
	resets.mu.Unlock()
	defer func() {
		resets.mu.Lock()
		resets.seen, resets.pending, resets.shown, resets.latest = oldSeen, oldPending, oldShown, oldLatest
		resets.mu.Unlock()
	}()
	mux := http.NewServeMux()
	resetNewsRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/resetnews", nil))
	var j resetNewsJSON
	json.NewDecoder(rec.Body).Decode(&j)
	if !j.Show || j.Reset == nil || j.Reset.ID != "1" || j.Reset.Text == "" {
		t.Fatalf("get: %+v", j)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/resetnews/seen", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("seen: %d", rec.Code)
	}
	if j := resets.get(); j.Show {
		t.Fatalf("after seen: %+v", j)
	}
}

// The dev build's Test button: POST /api/resetnews/test asks the site at
// once, whatever the toggle says, and what it has comes back and counts as
// seen, so the poll doesn't announce it after; a site that can't be asked
// is an error the page says.
func TestResetNewsTestRoute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	dir := filepath.Join(home, "magpie")
	id := "2106131810921136451"
	up := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"latest_reset": map[string]any{"id": id, "reset_type": "banked", "announced_at": "2026-10-02T21:18:48.000Z", "text": "Reset all propagated. Enjoy."},
		}})
	}))
	defer srv.Close()
	oldFeed := resetFeed
	defer func() { resetFeed = oldFeed }()
	resetFeed = srv.URL
	// the alert is off and no Codex account is signed in: the button asks all the same
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"noResetAlert":true}`), 0o644)
	mux := http.NewServeMux()
	resetNewsRoutes(mux)
	post := func() (int, resetItem) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/resetnews/test", nil))
		var item resetItem
		json.NewDecoder(rec.Body).Decode(&item)
		return rec.Code, item
	}
	code, item := post()
	if code != http.StatusOK || item.ID != id || item.Type != "banked" {
		t.Fatalf("test: %d %+v", code, item)
	}
	resets.mu.Lock()
	seen, pending, shown := resets.seen, resets.pending, resets.shown
	resets.mu.Unlock()
	if seen != id || pending || shown {
		t.Fatalf("recorded: seen %q pending %v shown %v", seen, pending, shown)
	}
	// a second ask of the same reset comes back all the same
	if code, _ := post(); code != http.StatusOK {
		t.Fatalf("again: %d", code)
	}
	// the site unreachable: an error, and what was kept is kept
	up = false
	srv.Close()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/resetnews/test", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("down: %d", rec.Code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, resetSeenFile)); strings.TrimSpace(string(b)) != id {
		t.Fatalf("kept %q", b)
	}
}
