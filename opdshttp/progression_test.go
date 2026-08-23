package opdshttp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
