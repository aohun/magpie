package gui

// Codex rate-limit resets, announced on X by OpenAI's Codex lead and tracked
// by codex-resets.com. While a Codex account is signed in, magpie asks the
// site every five minutes; a reset newer than the one last seen pops up once,
// much as the update's notes do (whatsnew.go). The site's API is free and
// keyless, and asks only that the data it hands be credited, which the
// dialog does. Offline, or with the alert off, nothing is asked and nothing
// is shown.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/netproxy"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// resetFeed is the site's status call; a var, so a test can point it at a
// fake.
var resetFeed = "https://codex-resets.com/api/v1/status"

// resetPollEvery is how often the site is asked.
const resetPollEvery = 5 * time.Minute

// resetSeenFile holds the id of the reset last announced here.
const resetSeenFile = "codex-resets-seen"

// resetAsk bounds one ask of the site.
const resetAsk = 10 * time.Second

// the site is asked through the proxy the rest of magpie's outside calls
// take: Settings' Proxy, else the environment's, else the system's.
var resetClient = &http.Client{Transport: func() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = netproxy.Func
	return netproxy.Dispatch(t)
}()}

// resetItem is one announced reset, as the dialog shows it.
type resetItem struct {
	ID        string `json:"id"` // the X post's, or the site's observed- id
	Type      string `json:"type"` // "regular" or "banked"
	Announced string `json:"announced"` // RFC3339
	Text      string `json:"text"` // markdown, usually the post itself
	URL       string `json:"url,omitempty"` // the announcement, when the site has one
}

type resetNews struct {
	mu      sync.Mutex
	seen    string      // the id last recorded, "" before the first ask
	pending bool        // a reset newer than seen is waiting to be shown
	shown   bool        // a page has shown it
	latest  *resetItem  // the newest the site has said, kept for the dialog
}

var resets = &resetNews{}

// codexAlertGate says whether anyone uses Codex on this machine: a poll for
// a user without a Codex account is a call made for nothing.
var codexAlertGate = func() bool {
	return len(provider.Logins("codex")) > 0 || func() bool { _, ok := provider.CodexSignedIn(); return ok }()
}

// resetWanted is whether a reset after the one seen should be shown: the
// first ask only records what the site has now, so a magpie installed
// between resets doesn't wake up announcing an old one.
func resetWanted(seen, latest string) bool {
	return seen != "" && latest != "" && latest != seen
}

// start reads the reset id last recorded and polls for newer ones.
func (n *resetNews) start() {
	n.startSeen()
	go func() {
		time.Sleep(30 * time.Second) // let the app settle first
		for {
			n.poll(context.Background())
			time.Sleep(resetPollEvery)
		}
	}()
}

// startSeen reads the id last recorded, kept across restarts.
func (n *resetNews) startSeen() {
	b, _ := os.ReadFile(filepath.Join(settings.Dir(), resetSeenFile))
	n.mu.Lock()
	n.seen = strings.TrimSpace(string(b))
	n.mu.Unlock()
}

// poll asks the site once; a failure waits for the next round.
func (n *resetNews) poll(ctx context.Context) {
	if settings.Load().NoResetAlert || !codexAlertGate() {
		return
	}
	item, err := n.fetch(ctx)
	if err != nil {
		log.Println("codex resets:", err)
		return
	}
	if item == nil {
		return
	}
	n.record(item)
}

// fetch asks the site for the reset it has now: nil without an announcement.
func (n *resetNews) fetch(ctx context.Context) (*resetItem, error) {
	ctx, cancel := context.WithTimeout(ctx, resetAsk)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resetFeed, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := resetClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("%s", res.Status)
	}
	var wire struct {
		Data struct {
			LatestReset *struct {
				ID          string `json:"id"`
				ResetType   string `json:"reset_type"`
				AnnouncedAt string `json:"announced_at"`
				Text        string `json:"text"`
				Source      *struct {
					URL string `json:"url"`
				} `json:"source"`
			} `json:"latest_reset"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&wire); err != nil {
		return nil, err
	}
	r := wire.Data.LatestReset
	if r == nil || r.ID == "" {
		return nil, nil
	}
	item := &resetItem{ID: r.ID, Type: r.ResetType, Announced: r.AnnouncedAt, Text: r.Text}
	if r.Source != nil {
		item.URL = r.Source.URL
	}
	return item, nil
}

// record keeps the reset the site has, announcing it when it is newer than
// the one last seen.
func (n *resetNews) record(item *resetItem) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.latest = item
	if item.ID == n.seen {
		return
	}
	if resetWanted(n.seen, item.ID) {
		n.pending = true
	}
	n.seen = item.ID
	n.shown = false
	resetSave(item.ID)
}

// test is the dev build's Test button: the site is asked at once, whatever
// the toggle and the sign-in gate say, and what it has comes back for the
// page to draw — recorded as seen, so the poll won't announce it after.
func (n *resetNews) test(ctx context.Context) (*resetItem, error) {
	item, err := n.fetch(ctx)
	if err != nil || item == nil {
		return item, err
	}
	n.record(item)
	return item, nil
}

// resetSave records the reset id last seen, so a restart doesn't announce
// it again.
func resetSave(id string) {
	dir := settings.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, resetSeenFile), []byte(id+"\n"), 0o644); err != nil {
		log.Println("codex resets:", err)
	}
}

type resetNewsJSON struct {
	Show  bool       `json:"show"`
	Reset *resetItem `json:"reset,omitempty"`
}

// get is what a page shows: the newest reset when one is waiting to be
// seen, once; asked again, it waits no more.
func (n *resetNews) get() resetNewsJSON {
	n.mu.Lock()
	defer n.mu.Unlock()
	j := resetNewsJSON{Show: n.pending && !n.shown, Reset: n.latest}
	if j.Show {
		n.shown = true
	}
	return j
}

func resetNewsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/resetnews", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, resets.get())
	})
	// a page showing the dialog says so with seen; the id is already kept
	mux.HandleFunc("POST /api/resetnews/seen", func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusNoContent)
	})
	// the dev build's Test button: the site asked at once, its answer back
	// for the page to draw
	mux.HandleFunc("POST /api/resetnews/test", func(rw http.ResponseWriter, r *http.Request) {
		item, err := resets.test(r.Context())
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadGateway)
			return
		}
		if item == nil {
			http.Error(rw, "the site has no reset yet", http.StatusNotFound)
			return
		}
		writeJSON(rw, item)
	})
}
