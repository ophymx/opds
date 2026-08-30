package opdshttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opdshttp"
)

func newProgressionServer() *opdshttp.Handler {
	return opdshttp.New(memSource{prefix: "/opds"},
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, testAuthDoc()),
		opdshttp.WithProgression(opdshttp.NewMemProgressionStore()),
	)
}

// do performs a request with Basic credentials and an optional body.
func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.SetBasicAuth("jane", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const validProgression = `{
	"title": "Chapter 3",
	"modified": "2026-08-01T10:00:00Z",
	"device": {"id": "urn:uuid:0000-01", "name": "Kobo Elipsa"},
	"progression": 0.42,
	"references": ["chapter3.html#:~:text=meanwhile"]
}`

func TestProgressionGetEmptyBeforeFirstPut(t *testing.T) {
	w := do(t, newProgressionServer(), http.MethodGet, "/opds/progression/urn:b1", "")
	if w.Code != 200 {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("body = %q, want the draft's empty payload", w.Body.String())
	}
}

func TestProgressionPutRoundTrip(t *testing.T) {
	h := newProgressionServer()
	w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", validProgression)
	if w.Code != http.StatusCreated {
		t.Fatalf("first PUT code = %d, want 201: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != opds.MediaTypeProgression {
		t.Errorf("PUT content-type = %q, want %q", ct, opds.MediaTypeProgression)
	}

	w = do(t, h, http.MethodGet, "/opds/progression/urn:b1", "")
	if w.Code != 200 {
		t.Fatalf("GET code = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != opds.MediaTypeProgression {
		t.Errorf("GET content-type = %q", ct)
	}
	var doc struct {
		Title       string
		Modified    string
		Progression float64
		Device      struct{ ID, Name string }
		References  []string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, w.Body.String())
	}
	if doc.Progression != 0.42 || doc.Modified != "2026-08-01T10:00:00Z" {
		t.Errorf("progression/modified = %v / %q", doc.Progression, doc.Modified)
	}
	if doc.Device.Name != "Kobo Elipsa" || doc.Title != "Chapter 3" {
		t.Errorf("device/title = %+v / %q", doc.Device, doc.Title)
	}
	// references pass through opaquely.
	if len(doc.References) != 1 || !strings.Contains(doc.References[0], "chapter3") {
		t.Errorf("references = %v", doc.References)
	}

	// A newer update replaces: 200, not 201.
	newer := strings.Replace(validProgression, "2026-08-01T10:00:00Z", "2026-08-02T09:00:00Z", 1)
	if w = do(t, h, http.MethodPut, "/opds/progression/urn:b1", newer); w.Code != 200 {
		t.Errorf("newer PUT code = %d, want 200: %s", w.Code, w.Body.String())
	}
}

func TestProgressionPutStaleRejected(t *testing.T) {
	h := newProgressionServer()
	do(t, h, http.MethodPut, "/opds/progression/urn:b1", validProgression)

	older := strings.Replace(validProgression, "2026-08-01T10:00:00Z", "2026-07-01T10:00:00Z", 1)
	w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", older)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale PUT code = %d, want 409", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content-type = %q, want problem+json", ct)
	}
	var problem struct{ Type, Title string }
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Type != "https://registry.opds.io/error#progression-date" || problem.Title == "" {
		t.Errorf("problem = %+v", problem)
	}

	// The stored progression is untouched.
	w = do(t, h, http.MethodGet, "/opds/progression/urn:b1", "")
	if !strings.Contains(w.Body.String(), "2026-08-01T10:00:00Z") {
		t.Errorf("stale PUT overwrote the store:\n%s", w.Body.String())
	}
}

// An equal modified timestamp is an idempotent replay, not a stale update.
func TestProgressionPutIdempotent(t *testing.T) {
	h := newProgressionServer()
	do(t, h, http.MethodPut, "/opds/progression/urn:b1", validProgression)
	if w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", validProgression); w.Code != 200 {
		t.Errorf("replayed PUT code = %d, want 200", w.Code)
	}
}

func TestProgressionPutInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"malformed JSON":      `{`,
		"progression missing": `{"modified": "2026-08-01T10:00:00Z", "device": {"id": "urn:d", "name": "D"}}`,
		"progression > 1":     `{"modified": "2026-08-01T10:00:00Z", "device": {"id": "urn:d", "name": "D"}, "progression": 1.2}`,
		"progression < 0":     `{"modified": "2026-08-01T10:00:00Z", "device": {"id": "urn:d", "name": "D"}, "progression": -0.1}`,
		"modified missing":    `{"device": {"id": "urn:d", "name": "D"}, "progression": 0.5}`,
		"modified not 3339":   `{"modified": "yesterday", "device": {"id": "urn:d", "name": "D"}, "progression": 0.5}`,
		"device missing":      `{"modified": "2026-08-01T10:00:00Z", "progression": 0.5}`,
		"device name missing": `{"modified": "2026-08-01T10:00:00Z", "device": {"id": "urn:d"}, "progression": 0.5}`,
	} {
		w := do(t, newProgressionServer(), http.MethodPut, "/opds/progression/urn:b1", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400", name, w.Code)
			continue
		}
		var problem struct{ Type string }
		if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
			t.Errorf("%s: invalid problem body: %v", name, err)
		} else if problem.Type != "https://registry.opds.io/error#progression-invalid-payload" {
			t.Errorf("%s: problem type = %q", name, problem.Type)
		}
	}
}

// A progression of exactly 0 or 1 is valid (finished/unstarted books).
func TestProgressionBoundsInclusive(t *testing.T) {
	h := newProgressionServer()
	for i, body := range []string{
		`{"modified": "2026-08-01T10:00:00Z", "device": {"id": "urn:d", "name": "D"}, "progression": 0}`,
		`{"modified": "2026-08-02T10:00:00Z", "device": {"id": "urn:d", "name": "D"}, "progression": 1}`,
	} {
		if w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", body); w.Code != 201 && w.Code != 200 {
			t.Errorf("PUT %d: code = %d, want 2xx: %s", i, w.Code, w.Body.String())
		}
	}
}

// Progression is per-user: the routes sit behind the auth middleware.
func TestProgressionRequiresCredentials(t *testing.T) {
	h := newProgressionServer()
	if w := get(t, h, "/opds/progression/urn:b1", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("GET without credentials: code = %d, want 401", w.Code)
	}
	r := httptest.NewRequest(http.MethodPut, "/opds/progression/urn:b1", strings.NewReader(validProgression))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("PUT without credentials: code = %d, want 401", w.Code)
	}
}

// Progressions are keyed per user: one user's position is invisible to another.
type twoUserAuth struct{}

func (twoUserAuth) Authenticate(username, password string) (string, error) {
	if password == "pw" {
		return username, nil
	}
	return "", opdshttp.ErrInvalidCredentials
}

func TestProgressionIsolatedPerUser(t *testing.T) {
	h := opdshttp.New(memSource{prefix: "/opds"},
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithAuth(twoUserAuth{}, testAuthDoc()),
		opdshttp.WithProgression(opdshttp.NewMemProgressionStore()),
	)
	doAs := func(user, method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/opds/progression/urn:b1", strings.NewReader(body))
		r.SetBasicAuth(user, "pw")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := doAs("alice", http.MethodPut, validProgression); w.Code != 201 {
		t.Fatalf("alice PUT code = %d", w.Code)
	}
	if w := doAs("bob", http.MethodGet, ""); w.Body.Len() != 0 {
		t.Errorf("bob sees alice's progression: %s", w.Body.String())
	}
	if w := doAs("alice", http.MethodGet, ""); w.Body.Len() == 0 {
		t.Errorf("alice's progression lost")
	}
}

// Concurrent PUTs must serialize through the staleness check: once the newest
// document lands, no older one may overwrite it, in any interleaving.
func TestProgressionConcurrentPutsKeepNewest(t *testing.T) {
	h := newProgressionServer()
	base := `{"modified":"2026-08-%02dT10:00:00Z","device":{"id":"urn:d","name":"D"},"progression":0.5}`
	var wg sync.WaitGroup
	for day := 1; day <= 20; day++ {
		wg.Go(func() {
			w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", fmt.Sprintf(base, day))
			if w.Code != 200 && w.Code != 201 && w.Code != http.StatusConflict {
				t.Errorf("day %d: code = %d", day, w.Code)
			}
		})
	}
	wg.Wait()
	w := do(t, h, http.MethodGet, "/opds/progression/urn:b1", "")
	if !strings.Contains(w.Body.String(), "2026-08-20T10:00:00Z") {
		t.Errorf("an older concurrent PUT won:\n%s", w.Body.String())
	}
}

func TestProgressionDisabledWithoutStore(t *testing.T) {
	h := newAuthServer() // auth, but no WithProgression
	if w := do(t, h, http.MethodGet, "/opds/progression/urn:b1", ""); w.Code != 404 {
		t.Errorf("GET code = %d, want 404 without a store", w.Code)
	}
	w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", validProgression)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT code = %d, want 405 without a store", w.Code)
	}
}

func TestProgressionMethodNotAllowed(t *testing.T) {
	w := do(t, newProgressionServer(), http.MethodDelete, "/opds/progression/urn:b1", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, want 405", w.Code)
	}
	if allow := w.Header().Get("Allow"); allow != "GET, HEAD, PUT" {
		t.Errorf("Allow = %q, want GET, HEAD, PUT", allow)
	}
	// Other routes keep their read-only Allow set.
	w = do(t, newProgressionServer(), http.MethodPut, "/opds/feed/new", "{}")
	if allow := w.Header().Get("Allow"); w.Code != 405 || allow != "GET, HEAD" {
		t.Errorf("feed PUT: code = %d, Allow = %q", w.Code, allow)
	}
}

func TestWithProgressionRequiresAuth(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("New with WithProgression but no WithAuth should panic")
		}
	}()
	opdshttp.New(memSource{prefix: "/opds"},
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithProgression(opdshttp.NewMemProgressionStore()),
	)
}

func TestProgressionLinkInjected(t *testing.T) {
	h := newProgressionServer()

	// 2.0 feed: each publication carries the endpoint link.
	w := do(t, h, http.MethodGet, "/opds/feed/new?version=2", "")
	var feed struct {
		Publications []struct {
			Links []struct{ Rel, Href, Type string } `json:"links"`
		} `json:"publications"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	if len(feed.Publications) != 1 {
		t.Fatalf("publications = %d", len(feed.Publications))
	}
	found := false
	for _, l := range feed.Publications[0].Links {
		if l.Rel == opds.RelProgression {
			found = true
			if l.Href != "/opds/progression/urn:b1" || l.Type != opds.MediaTypeProgression {
				t.Errorf("progression link = %+v", l)
			}
		}
	}
	if !found {
		t.Errorf("no progression link on publication:\n%s", w.Body.String())
	}

	// 1.2 feed and standalone publication document carry it too.
	for _, path := range []string{"/opds/feed/new", "/opds/publication/b1"} {
		if w := do(t, h, http.MethodGet, path, ""); !strings.Contains(w.Body.String(), opds.RelProgression) {
			t.Errorf("%s: no progression link:\n%s", path, w.Body.String())
		}
	}

	// Without a store nothing is injected.
	if w := do(t, newAuthServer(), http.MethodGet, "/opds/feed/new", ""); strings.Contains(w.Body.String(), opds.RelProgression) {
		t.Errorf("progression link injected without a store")
	}
}

// The injection must land in a per-request copy, not the Source's shared feed.
func TestProgressionInjectionDoesNotMutateSourceFeed(t *testing.T) {
	pub := opds.NewPublication("urn:b1", "A Book").OpenAccess("/b1.epub", "application/epub+zip")
	feed := opds.NewFeed("urn:root", "Root").Add(*pub)
	feed.Groups = []opds.Group{{Title: "Featured", Publications: []opds.Publication{*pub}}}
	src := cachedSource{memSource{prefix: "/opds"}, feed}
	h := opdshttp.New(src,
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, testAuthDoc()),
		opdshttp.WithProgression(opdshttp.NewMemProgressionStore()),
	)
	if w := do(t, h, http.MethodGet, "/opds/", ""); !strings.Contains(w.Body.String(), opds.RelProgression) {
		t.Fatalf("no progression link served:\n%s", w.Body.String())
	}
	if n := len(src.feed.Publications[0].Links); n != 0 {
		t.Errorf("handler mutated the Source's publication links: %+v", src.feed.Publications[0].Links)
	}
	if n := len(src.feed.Groups[0].Publications[0].Links); n != 0 {
		t.Errorf("handler mutated the Source's group publication links: %+v", src.feed.Groups[0].Publications[0].Links)
	}
}

// The draft asks catalogs to advertise the endpoint with an authenticate hint
// so a client can reach for credentials without spending a 401 round-trip.
func TestProgressionLinkAuthenticateHint(t *testing.T) {
	h := newProgressionServer()
	w := do(t, h, http.MethodGet, "/opds/feed/new?version=2", "")
	var feed struct {
		Publications []struct {
			Links []struct {
				Rel, Href, Type string
				Properties      struct {
					Authenticate struct{ Href, Type string } `json:"authenticate"`
				} `json:"properties"`
			} `json:"links"`
		} `json:"publications"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	if len(feed.Publications) != 1 {
		t.Fatalf("publications = %d", len(feed.Publications))
	}
	var hinted int
	for _, l := range feed.Publications[0].Links {
		if l.Rel != opds.RelProgression && l.Rel != opds.RelProgressionCantook {
			if l.Properties.Authenticate.Href != "" {
				t.Errorf("unrelated link %q carries an authenticate hint", l.Rel)
			}
			continue
		}
		hinted++
		a := l.Properties.Authenticate
		if a.Href != "/opds/auth" || a.Type != opds.MediaTypeAuthDocument {
			t.Errorf("%s authenticate hint = %+v, want /opds/auth (%s)", l.Rel, a, opds.MediaTypeAuthDocument)
		}
	}
	if hinted != 2 {
		t.Errorf("hinted progression links = %d, want 2 (draft + Cantook alias)", hinted)
	}
}

// forbiddingStore refuses writes with one of the handler's 403 sentinels.
type forbiddingStore struct {
	opdshttp.ProgressionStore
	err error
}

func (s forbiddingStore) SetProgression(ctx context.Context, user, id string, p *opds.Progression) error {
	return fmt.Errorf("store: %w", s.err)
}

func TestProgressionStoreRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"incorrect user", opdshttp.ErrProgressionIncorrectUser, "https://registry.opds.io/error#progression-incorrect-user"},
		{"locked", opdshttp.ErrProgressionLocked, "https://registry.opds.io/error#progression-locked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := opdshttp.New(memSource{prefix: "/opds"},
				opdshttp.WithPrefix("/opds"),
				opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, testAuthDoc()),
				opdshttp.WithProgression(forbiddingStore{opdshttp.NewMemProgressionStore(), tc.err}),
			)
			w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", validProgression)
			if w.Code != http.StatusForbidden {
				t.Fatalf("code = %d, want 403: %s", w.Code, w.Body.String())
			}
			checkProblem(t, w, tc.want)
		})
	}
}

// failingStore fails with an error the handler cannot classify.
type failingStore struct{ opdshttp.ProgressionStore }

func (failingStore) Progression(ctx context.Context, user, id string) (*opds.Progression, error) {
	return nil, errors.New("disk on fire")
}

// The draft requires a Problem Details payload for every error other than 401,
// including the ones it does not enumerate.
func TestProgressionUnclassifiedErrorIsProblem(t *testing.T) {
	h := opdshttp.New(memSource{prefix: "/opds"},
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, testAuthDoc()),
		opdshttp.WithProgression(failingStore{opdshttp.NewMemProgressionStore()}),
	)
	w := do(t, h, http.MethodGet, "/opds/progression/urn:b1", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", w.Code)
	}
	checkProblem(t, w, "about:blank")
}

func TestProgressionMethodNotAllowedIsProblem(t *testing.T) {
	w := do(t, newProgressionServer(), http.MethodDelete, "/opds/progression/urn:b1", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, want 405", w.Code)
	}
	checkProblem(t, w, "about:blank")
}

// checkProblem asserts an RFC 7807 body with the given type and a non-empty
// title, which the draft requires of every error payload.
func checkProblem(t *testing.T, w *httptest.ResponseRecorder, wantType string) {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content-type = %q, want application/problem+json", ct)
	}
	var problem struct{ Type, Title string }
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatalf("problem body: %v (%s)", err, w.Body.String())
	}
	if problem.Type != wantType {
		t.Errorf("type = %q, want %q", problem.Type, wantType)
	}
	if problem.Title == "" {
		t.Errorf("problem has no title: %s", w.Body.String())
	}
}

// A device with a badly wrong clock must not be able to store a timestamp no
// honest update can beat — the failure that locks a publication behind a
// permanent 409.
func TestProgressionRejectsFutureTimestamp(t *testing.T) {
	h := newProgressionServer()
	future := fmt.Sprintf(`{"modified":%q,"device":{"id":"urn:uuid:0000-01","name":"Bad clock"},"progression":0.5}`,
		time.Now().Add(72*time.Hour).UTC().Format(time.RFC3339))
	w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", future)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400: %s", w.Code, w.Body.String())
	}
	checkProblem(t, w, "https://registry.opds.io/error#progression-invalid-payload")

	// Skew inside the allowance is still accepted: clients are not required
	// to keep better time than the tolerance.
	near := fmt.Sprintf(`{"modified":%q,"device":{"id":"urn:uuid:0000-01","name":"Slightly fast"},"progression":0.5}`,
		time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	if w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", near); w.Code != http.StatusCreated {
		t.Errorf("in-tolerance PUT code = %d, want 201: %s", w.Code, w.Body.String())
	}
}

// A record already poisoned by a future timestamp (stored before the check
// existed, or written directly by the application) must not stay locked.
func TestProgressionHealsPoisonedRecord(t *testing.T) {
	store := opdshttp.NewMemProgressionStore()
	if err := store.SetProgression(context.Background(), "user-1", "urn:b1", &opds.Progression{
		Progression: 0.9,
		Modified:    time.Now().AddDate(50, 0, 0),
		Device:      opds.Device{ID: "urn:uuid:0000-99", Name: "Bad clock"},
	}); err != nil {
		t.Fatal(err)
	}
	h := opdshttp.New(memSource{prefix: "/opds"},
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, testAuthDoc()),
		opdshttp.WithProgression(store),
	)
	w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", validProgression)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (the poisoned record must not win): %s", w.Code, w.Body.String())
	}
	p, err := store.Progression(context.Background(), "user-1", "urn:b1")
	if err != nil || p.Progression != 0.42 {
		t.Errorf("stored = %+v, %v; want the honest update", p, err)
	}
}

func TestProgressionSkewConfigurable(t *testing.T) {
	h := opdshttp.New(memSource{prefix: "/opds"},
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, testAuthDoc()),
		opdshttp.WithProgression(opdshttp.NewMemProgressionStore()),
		opdshttp.WithProgressionSkew(0), // disabled
	)
	far := fmt.Sprintf(`{"modified":%q,"device":{"id":"urn:uuid:0000-01","name":"x"},"progression":0.5}`,
		time.Now().AddDate(10, 0, 0).UTC().Format(time.RFC3339))
	if w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", far); w.Code != http.StatusCreated {
		t.Errorf("with the check disabled, code = %d, want 201: %s", w.Code, w.Body.String())
	}
}

// The draft types device.id as a URI and references as URI references. What
// the endpoint accepts it must be able to serve back, so both are enforced.
func TestProgressionRejectsUnservableFields(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"device id is not a URI", `{"modified":"2026-08-01T10:00:00Z","device":{"id":"device-123","name":"x"},"progression":0.5}`},
		{"empty reference", `{"modified":"2026-08-01T10:00:00Z","device":{"id":"urn:uuid:1","name":"x"},"progression":0.5,"references":[""]}`},
		{"unparseable reference", "{\"modified\":\"2026-08-01T10:00:00Z\",\"device\":{\"id\":\"urn:uuid:1\",\"name\":\"x\"},\"progression\":0.5,\"references\":[\"a\\u007fb\"]}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := do(t, newProgressionServer(), http.MethodPut, "/opds/progression/urn:b1", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400: %s", w.Code, w.Body.String())
			}
			checkProblem(t, w, "https://registry.opds.io/error#progression-invalid-payload")
		})
	}
}

// Reading positions are per-user and mutable; no cache should keep a copy.
func TestProgressionResponsesAreNotStored(t *testing.T) {
	h := newProgressionServer()
	for _, w := range []*httptest.ResponseRecorder{
		do(t, h, http.MethodGet, "/opds/progression/urn:b1", ""),
		do(t, h, http.MethodPut, "/opds/progression/urn:b1", validProgression),
		doReadium(t, h, http.MethodGet, "/opds/progression/urn:b1", ""),
	} {
		if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", cc)
		}
	}
}

// A store refusal is protocol vocabulary, so it keeps its 403 even when the
// application has installed an error handler for unclassified failures.
func TestProgressionRefusalSurvivesErrorHandler(t *testing.T) {
	called := false
	h := opdshttp.New(memSource{prefix: "/opds"},
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, testAuthDoc()),
		opdshttp.WithProgression(forbiddingStore{opdshttp.NewMemProgressionStore(), opdshttp.ErrProgressionLocked}),
		opdshttp.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
			called = true
			w.WriteHeader(http.StatusTeapot)
		}),
	)
	w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", validProgression)
	if w.Code != http.StatusForbidden || called {
		t.Errorf("code = %d, error handler called = %v; want 403 without the hook", w.Code, called)
	}
}
