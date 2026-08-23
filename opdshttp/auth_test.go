package opdshttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opdshttp"
)

// staticAuth authenticates a single fixed user.
type staticAuth struct{ user, pass, id string }

func (a staticAuth) Authenticate(username, password string) (string, error) {
	if username == a.user && password == a.pass {
		return a.id, nil
	}
	return "", opdshttp.ErrInvalidCredentials
}

func testAuthDoc() opdshttp.AuthDocument {
	return opdshttp.AuthDocument{
		Title:         "Family Library",
		Description:   "Sign in with your library account.",
		LoginLabel:    "Account",
		PasswordLabel: "PIN",
		Links: []opds.Link{
			{Rel: "help", Href: "mailto:help@example.com"},
			{Rel: "register", Href: "https://example.com/register", Type: "text/html"},
		},
	}
}

func newAuthServer() *opdshttp.Handler {
	return opdshttp.New(memSource{prefix: "/opds"},
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, testAuthDoc()),
	)
}

// getAs performs a GET with Basic credentials.
func getAs(t *testing.T, h http.Handler, path, user, pass string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.SetBasicAuth(user, pass)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// authDoc is the decoded shape of an Authentication Document body.
type authDoc struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Links          []struct{ Rel, Href, Type string }
	Authentication []struct {
		Type   string
		Labels struct{ Login, Password string }
	}
}

func decodeAuthDoc(t *testing.T, body []byte) authDoc {
	t.Helper()
	var doc authDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("invalid Authentication Document JSON: %v\n%s", err, body)
	}
	return doc
}

// Missing credentials must produce the full two-tier 401: a Basic challenge
// for header-only clients (KOReader, Foliate) and an Authentication Document
// body for clients that render a login dialog (Thorium, Cantook).
func TestAuthChallengeShape(t *testing.T) {
	w := get(t, newAuthServer(), "/opds/", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got != `Basic realm="Family Library"` {
		t.Errorf("WWW-Authenticate = %q", got)
	}
	if ct := w.Header().Get("Content-Type"); ct != opds.MediaTypeAuthDocument {
		t.Errorf("content-type = %q, want %q", ct, opds.MediaTypeAuthDocument)
	}
	if link := w.Header().Get("Link"); !strings.Contains(link, opds.RelAuthDocument) {
		t.Errorf("Link header = %q, want rel %q", link, opds.RelAuthDocument)
	}
	doc := decodeAuthDoc(t, w.Body.Bytes())
	if doc.ID != "http://example.com/opds/auth" {
		t.Errorf("id = %q, want the absolute auth URL", doc.ID)
	}
	if doc.Title != "Family Library" || doc.Description == "" {
		t.Errorf("title/description = %q / %q", doc.Title, doc.Description)
	}
	if len(doc.Authentication) != 1 || doc.Authentication[0].Type != opds.AuthFlowBasic {
		t.Fatalf("authentication = %+v, want single %s flow", doc.Authentication, opds.AuthFlowBasic)
	}
	if l := doc.Authentication[0].Labels; l.Login != "Account" || l.Password != "PIN" {
		t.Errorf("labels = %+v", l)
	}
	if len(doc.Links) != 2 || doc.Links[1].Rel != "register" {
		t.Errorf("links = %+v", doc.Links)
	}
}

func TestAuthInvalidCredentials(t *testing.T) {
	w := getAs(t, newAuthServer(), "/opds/", "jane", "wrong")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("code = %d, want 401", w.Code)
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("missing WWW-Authenticate challenge")
	}
}

// userSource records the identity User reports inside the Source.
type userSource struct {
	memSource
	user string
	ok   bool
}

func (s *userSource) Root(ctx context.Context, req opds.FeedRequest) (*opds.Feed, error) {
	s.user, s.ok = opdshttp.User(ctx)
	return s.memSource.Root(ctx, req)
}

func TestAuthIdentityReachesSource(t *testing.T) {
	src := &userSource{memSource: memSource{prefix: "/opds"}}
	h := opdshttp.New(src,
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, testAuthDoc()),
	)
	w := getAs(t, h, "/opds/", "jane", "secret")
	if w.Code != 200 {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if !src.ok || src.user != "user-1" {
		t.Errorf("User(ctx) = %q, %v; want user-1, true", src.user, src.ok)
	}
}

func TestAuthDocumentServedWithoutCredentials(t *testing.T) {
	w := get(t, newAuthServer(), "/opds/auth", "")
	if w.Code != 200 {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != opds.MediaTypeAuthDocument {
		t.Errorf("content-type = %q", ct)
	}
	if doc := decodeAuthDoc(t, w.Body.Bytes()); doc.ID != "http://example.com/opds/auth" {
		t.Errorf("id = %q", doc.ID)
	}
}

func TestAuthDocumentUsesConfiguredBaseURL(t *testing.T) {
	h := opdshttp.New(memSource{prefix: "/opds"},
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithBaseURL("https://books.example.com"),
		opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, testAuthDoc()),
	)
	r := httptest.NewRequest(http.MethodGet, "/opds/auth", nil)
	r.Header.Set("X-Forwarded-Host", "evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if doc := decodeAuthDoc(t, w.Body.Bytes()); doc.ID != "https://books.example.com/opds/auth" {
		t.Errorf("id = %q, want the configured base, not the forwarded host", doc.ID)
	}
}

func TestAuthDocumentConfiguredIDKept(t *testing.T) {
	doc := testAuthDoc()
	doc.ID = "https://example.com/canonical/auth.json"
	h := opdshttp.New(memSource{prefix: "/opds"},
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, doc),
	)
	w := get(t, h, "/opds/auth", "")
	if got := decodeAuthDoc(t, w.Body.Bytes()).ID; got != doc.ID {
		t.Errorf("id = %q, want configured %q", got, doc.ID)
	}
}

func TestAuthLinkInjected(t *testing.T) {
	// 2.0: the injected link appears in the JSON links.
	w := getAs(t, newAuthServer(), "/opds/?version=2", "jane", "secret")
	var feed struct {
		Links []struct{ Rel, Href, Type string } `json:"links"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range feed.Links {
		if l.Rel == opds.RelAuthDocument {
			found = true
			if l.Href != "/opds/auth" || l.Type != opds.MediaTypeAuthDocument {
				t.Errorf("auth link = %+v", l)
			}
		}
	}
	if !found {
		t.Errorf("no auth document link in feed:\n%s", w.Body.String())
	}

	// 1.2: same link in the Atom rendering.
	w = getAs(t, newAuthServer(), "/opds/", "jane", "secret")
	if !strings.Contains(w.Body.String(), `rel="`+opds.RelAuthDocument+`"`) {
		t.Errorf("1.2 feed missing auth document link:\n%s", w.Body.String())
	}
}

// Without WithAuth nothing changes: no auth route, no injected link, no 401.
func TestAnonymousModeUnchanged(t *testing.T) {
	h := newServer()
	if w := get(t, h, "/opds/auth", ""); w.Code != 404 {
		t.Errorf("auth route without WithAuth: code = %d, want 404", w.Code)
	}
	w := get(t, h, "/opds/", "")
	if w.Code != 200 {
		t.Errorf("code = %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), opds.RelAuthDocument) {
		t.Errorf("auth document link injected without WithAuth:\n%s", w.Body.String())
	}
}

// erringAuth fails with a non-credential error, as a broken backend would.
type erringAuth struct{ err error }

func (a erringAuth) Authenticate(_, _ string) (string, error) { return "", a.err }

func TestAuthenticatorFailuresAreInternal(t *testing.T) {
	for name, a := range map[string]opdshttp.Authenticator{
		"backend error": erringAuth{errors.New("db down")},
		"empty user":    erringAuth{nil}, // empty user + nil err is a contract violation
	} {
		h := opdshttp.New(memSource{prefix: "/opds"},
			opdshttp.WithPrefix("/opds"),
			opdshttp.WithAuth(a, testAuthDoc()),
		)
		if w := getAs(t, h, "/opds/", "jane", "secret"); w.Code != http.StatusInternalServerError {
			t.Errorf("%s: code = %d, want 500", name, w.Code)
		}
	}
}
