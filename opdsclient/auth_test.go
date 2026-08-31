package opdsclient_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opdsclient"
	"github.com/ophymx/opds/opdshttp"
)

func authDoc() opdshttp.AuthDocument {
	return opdshttp.AuthDocument{
		Title:         "Example Library",
		Description:   "Enter your library card number and PIN.",
		LoginLabel:    "Library card",
		PasswordLabel: "PIN",
		Links: []opds.Link{
			{Rel: "logo", Href: "/logo.png", Type: "image/png"},
			{Rel: "help", Href: "mailto:help@example.com"},
		},
	}
}

func newAuthServer(t *testing.T, opts ...opdshttp.Option) *httptest.Server {
	t.Helper()
	return newServer(t, append([]opdshttp.Option{
		opdshttp.WithAuth(staticAuth{}, authDoc()),
	}, opts...)...)
}

func TestUnauthorizedCarriesTheAuthenticationDocument(t *testing.T) {
	bothVersions(t, func(t *testing.T, _ opds.Version, opts ...opdsclient.Option) {
		c := newClient(t, newAuthServer(t), opts...)
		_, err := c.Root(context.Background())
		if !errors.Is(err, opdsclient.ErrUnauthorized) {
			t.Fatalf("err = %v, want ErrUnauthorized", err)
		}
		var httpErr *opdsclient.Error
		if !errors.As(err, &httpErr) {
			t.Fatalf("err = %#v, want an *Error", err)
		}
		if httpErr.Auth == nil {
			t.Fatal("the challenge carried no Authentication Document")
		}
		if httpErr.Auth.Title != "Example Library" {
			t.Errorf("title = %q", httpErr.Auth.Title)
		}
		if httpErr.Auth.LoginLabel != "Library card" || httpErr.Auth.PasswordLabel != "PIN" {
			t.Errorf("labels = %q / %q", httpErr.Auth.LoginLabel, httpErr.Auth.PasswordLabel)
		}
		if !httpErr.Auth.SupportsBasic() {
			t.Error("document does not offer the Basic flow")
		}
	})
}

func TestCredentialsAuthenticate(t *testing.T) {
	bothVersions(t, func(t *testing.T, _ opds.Version, opts ...opdsclient.Option) {
		opts = append(opts, opdsclient.WithBasicAuth("jane", "secret"))
		c := newClient(t, newAuthServer(t), opts...)
		f, err := c.Root(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if f.Title != "Root" {
			t.Errorf("title = %q", f.Title)
		}
	})
}

func TestWrongCredentialsAreUnauthorized(t *testing.T) {
	c := newClient(t, newAuthServer(t), opdsclient.WithBasicAuth("jane", "wrong"))
	if _, err := c.Root(context.Background()); !errors.Is(err, opdsclient.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestDiscoverAuth(t *testing.T) {
	// Configured credentials must not change the answer: discovery reports
	// what the catalog asks of a client that has none.
	c := newClient(t, newAuthServer(t), opdsclient.WithBasicAuth("jane", "secret"))
	doc, err := c.DiscoverAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if doc == nil {
		t.Fatal("want a document")
	}
	if doc.Title != "Example Library" || len(doc.Authentication) != 1 {
		t.Errorf("doc = %+v", doc)
	}
	if doc.Authentication[0].Type != opds.AuthFlowBasic {
		t.Errorf("flow = %q", doc.Authentication[0].Type)
	}
}

func TestDiscoverAuthOnOpenCatalog(t *testing.T) {
	c := newClient(t, newServer(t))
	doc, err := c.DiscoverAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if doc != nil {
		t.Errorf("doc = %+v, want nil for a catalog needing no credentials", doc)
	}
}

func TestAuthDocumentFetchedDirectly(t *testing.T) {
	srv := newAuthServer(t)
	c := newClient(t, srv)
	doc, err := c.AuthDocument(context.Background(), opdshttp.AuthPath(prefix))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Description != "Enter your library card number and PIN." {
		t.Errorf("description = %q", doc.Description)
	}
	if len(doc.Links) != 2 {
		t.Fatalf("links = %+v", doc.Links)
	}
	if want := srv.URL + "/logo.png"; doc.Links[0].Href != want {
		t.Errorf("logo href = %q, want the absolute %q", doc.Links[0].Href, want)
	}
	if doc.Links[1].Href != "mailto:help@example.com" {
		t.Errorf("help href = %q, want the mailto left alone", doc.Links[1].Href)
	}
}

// TestAuthDocumentURLFromFeed covers the hint a 2.0 feed carries, which is
// what lets a client offer to log in without first spending a 401.
func TestAuthDocumentURLFromFeed(t *testing.T) {
	srv := newAuthServer(t, opdshttp.WithProgression(opdshttp.NewMemProgressionStore()))
	c := newClient(t, srv,
		opdsclient.WithVersion(opds.Version2),
		opdsclient.WithBasicAuth("jane", "secret"))
	f, err := c.Feed(context.Background(), "feed/new")
	if err != nil {
		t.Fatal(err)
	}
	got := opdsclient.AuthDocumentURL(f)
	if want := srv.URL + opdshttp.AuthPath(prefix); got != want {
		t.Errorf("AuthDocumentURL = %q, want %q", got, want)
	}
}

// TestDiscoverAuthFollowsLinkHeader covers a catalog that challenges without a
// document body, advertising it with the Link header OPDS also allows.
func TestDiscoverAuthFollowsLinkHeader(t *testing.T) {
	mux := http.NewServeMux()
	var docURL string
	mux.HandleFunc("/auth.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", opds.MediaTypeAuthDocument)
		io.WriteString(w, `{"id":"`+docURL+`","title":"Elsewhere","authentication":[{"type":"http://opds-spec.org/auth/basic"}]}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `</auth.json>; rel="`+opds.RelAuthDocument+`"; type="`+opds.MediaTypeAuthDocument+`"`)
		w.Header().Set("WWW-Authenticate", `Basic realm="Elsewhere"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	docURL = srv.URL + "/auth.json"

	c, err := opdsclient.New(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := c.DiscoverAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if doc == nil || doc.Title != "Elsewhere" {
		t.Fatalf("doc = %+v", doc)
	}
}

// TestCredentialsStayOnTheCatalogHost is the reason a Client is built for one
// catalog: a feed may link anywhere, and following such a link must not hand
// the catalog's password to a third party.
func TestCredentialsStayOnTheCatalogHost(t *testing.T) {
	var got string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "image/jpeg")
		io.WriteString(w, "cover")
	}))
	defer other.Close()

	c := newClient(t, newAuthServer(t), opdsclient.WithBasicAuth("jane", "secret"))
	res, err := c.Open(context.Background(), other.URL+"/cover.jpg")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got != "" {
		t.Errorf("sent %q to another host, want no Authorization header", got)
	}
}
