package opdsclient_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opdsclient"
	"github.com/ophymx/opds/opdshttp"
)

// The client is exercised against the library's own server, so every test here
// covers a real request over a real connection in both wire versions rather
// than a decode of a hand-written fixture. Where a catalog the library does
// not control behaves differently — a wrong Content-Type, Komga's Cantook
// progression shape — a bare httptest handler stands in for it.

const prefix = "/opds"

// catalog is a small Source that also searches and streams pages.
type catalog struct{}

func (catalog) Root(_ context.Context, _ opds.FeedRequest) (*opds.Feed, error) {
	return opds.NewFeed("urn:root", "Root").
		At(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)).
		AddNav("New", opdshttp.FeedPath(prefix, "new"), opds.MediaTypeAcquisition, opds.RelSortNew), nil
}

func (c catalog) Feed(_ context.Context, req opds.FeedRequest) (*opds.Feed, error) {
	switch req.ID {
	case "new":
		f := opds.NewFeed("urn:feed:new", "New").Add(*book())
		if req.Page < 2 {
			f.Next(opdshttp.FeedPagePath(prefix, "new", 2), opds.MediaTypeAcquisition)
		}
		return f, nil
	case "empty":
		return opds.NewFeed("urn:feed:empty", "Empty"), nil
	}
	return nil, opds.ErrNotFound
}

func (catalog) Publication(_ context.Context, id string) (*opds.Publication, error) {
	if id != "b1" {
		return nil, opds.ErrNotFound
	}
	return book(), nil
}

func (catalog) Search(_ context.Context, req opds.SearchRequest) (*opds.Feed, error) {
	f := opds.NewFeed("urn:search", "Results for "+req.Terms)
	if strings.Contains(req.Terms, "book") {
		f.Add(*book())
	}
	return f, nil
}

func (catalog) SearchDescription() opds.SearchDescription {
	return opds.SearchDescription{ShortName: "Catalog", Description: "Search the catalog"}
}

func (catalog) Page(_ context.Context, req opds.PageRequest) (*opds.PageImage, error) {
	if req.ID != "b1" || req.Number != 3 {
		return nil, opds.ErrNotFound
	}
	return &opds.PageImage{
		Type:    "image/jpeg",
		Content: strings.NewReader(fmt.Sprintf("page-%d-w%d", req.Number, req.MaxWidth)),
	}, nil
}

// book uses relative hrefs throughout, which is what makes the resolution the
// client performs observable.
func book() *opds.Publication {
	return opds.NewPublication("b1", "A Book").
		By("Jane").
		UpdatedAt(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)).
		Describe("About the book.").
		Cover("../covers/b1.jpg", "image/jpeg").
		OpenAccess("../files/b1.epub", "application/epub+zip").
		Stream(opdshttp.PageStreamPath(prefix, "b1"), "image/jpeg", 10)
}

type staticAuth struct{}

func (staticAuth) Authenticate(user, pass string) (string, error) {
	if user == "jane" && pass == "secret" {
		return "user-1", nil
	}
	return "", opdshttp.ErrInvalidCredentials
}

func newServer(t *testing.T, opts ...opdshttp.Option) *httptest.Server {
	t.Helper()
	opts = append([]opdshttp.Option{opdshttp.WithPrefix(prefix)}, opts...)
	srv := httptest.NewServer(opdshttp.New(catalog{}, opts...))
	t.Cleanup(srv.Close)
	return srv
}

func newClient(t *testing.T, srv *httptest.Server, opts ...opdsclient.Option) *opdsclient.Client {
	t.Helper()
	c, err := opdsclient.New(srv.URL+prefix+"/", opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// bothVersions runs a test against a client preferring each wire version, so
// every traversal below is asserted to behave identically over Atom and JSON.
func bothVersions(t *testing.T, fn func(t *testing.T, version opds.Version, opts ...opdsclient.Option)) {
	t.Helper()
	for _, v := range []opds.Version{opds.Version1, opds.Version2} {
		t.Run(v.String(), func(t *testing.T) {
			fn(t, v, opdsclient.WithVersion(v))
		})
	}
}

func TestNewRejectsRelativeBase(t *testing.T) {
	for _, base := range []string{"/opds/", "example.com/opds", ""} {
		if _, err := opdsclient.New(base); err == nil {
			t.Errorf("New(%q) succeeded, want an error", base)
		}
	}
}

func TestRootAndNavigation(t *testing.T) {
	bothVersions(t, func(t *testing.T, _ opds.Version, opts ...opdsclient.Option) {
		srv := newServer(t)
		c := newClient(t, srv, opts...)
		f, err := c.Root(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if f.Title != "Root" {
			t.Errorf("title = %q", f.Title)
		}
		if len(f.Navigation) != 1 {
			t.Fatalf("navigation = %+v", f.Navigation)
		}
		n := f.Navigation[0]
		if n.Title != "New" || n.Rel != opds.RelSortNew {
			t.Errorf("nav = %+v", n)
		}
		if want := srv.URL + "/opds/feed/new"; n.Href != want {
			t.Errorf("nav href = %q, want the absolute %q", n.Href, want)
		}

		sub, err := c.Feed(context.Background(), n.Href)
		if err != nil {
			t.Fatal(err)
		}
		if len(sub.Publications) != 1 || sub.Publications[0].Title != "A Book" {
			t.Fatalf("publications = %+v", sub.Publications)
		}
	})
}

func TestHrefsAreResolvedAgainstTheDocument(t *testing.T) {
	bothVersions(t, func(t *testing.T, _ opds.Version, opts ...opdsclient.Option) {
		srv := newServer(t)
		c := newClient(t, srv, opts...)
		f, err := c.Feed(context.Background(), "feed/new")
		if err != nil {
			t.Fatal(err)
		}
		p := f.Publications[0]
		// The feed lives at /opds/feed/new, so "../covers/b1.jpg" is /opds/covers/b1.jpg.
		if want := srv.URL + "/opds/covers/b1.jpg"; p.Images[0].Href != want {
			t.Errorf("cover = %q, want %q", p.Images[0].Href, want)
		}
		if want := srv.URL + "/opds/files/b1.epub"; p.Acquisitions[0].Href != want {
			t.Errorf("acquisition = %q, want %q", p.Acquisitions[0].Href, want)
		}
	})
}

func TestPageStreamTemplateSurvivesResolution(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv, opdsclient.WithVersion(opds.Version1)) // PSE is a 1.x extension
	f, err := c.Feed(context.Background(), "feed/new")
	if err != nil {
		t.Fatal(err)
	}
	ps := f.Publications[0].PageStream
	if ps == nil {
		t.Fatal("no page stream")
	}
	if !strings.HasPrefix(ps.Href, srv.URL) || !strings.Contains(ps.Href, "{pageNumber}") {
		t.Fatalf("stream href = %q, want absolute with its template intact", ps.Href)
	}
	if ps.PageCount != 10 {
		t.Errorf("page count = %d, want 10", ps.PageCount)
	}

	res, err := c.Page(context.Background(), ps, 3, 800)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if string(body) != "page-3-w800" {
		t.Errorf("page body = %q", body)
	}
	if res.Type != "image/jpeg" {
		t.Errorf("page type = %q", res.Type)
	}

	if _, err := opdsclient.PageURL(ps, 10, 0); err == nil {
		t.Error("want an error for a page past the count")
	}
}

func TestPublicationDocument(t *testing.T) {
	bothVersions(t, func(t *testing.T, _ opds.Version, opts ...opdsclient.Option) {
		srv := newServer(t)
		c := newClient(t, srv, opts...)
		p, err := c.Publication(context.Background(), opdshttp.PublicationPath(prefix, "b1"))
		if err != nil {
			t.Fatal(err)
		}
		if p.Title != "A Book" {
			t.Errorf("title = %q", p.Title)
		}
		if !strings.HasPrefix(p.Acquisitions[0].Href, srv.URL) {
			t.Errorf("acquisition href = %q, want absolute", p.Acquisitions[0].Href)
		}
	})
}

func TestOpenFetchesArbitraryResources(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)
	res, err := c.Open(context.Background(), opdshttp.AuthPath(prefix))
	if err == nil {
		res.Body.Close()
		t.Fatal("want an error: the catalog has no auth document configured")
	}
	if !errors.Is(err, opds.ErrNotFound) {
		t.Errorf("err = %v, want a not-found", err)
	}
}

func TestNotFoundIsSentinel(t *testing.T) {
	bothVersions(t, func(t *testing.T, _ opds.Version, opts ...opdsclient.Option) {
		c := newClient(t, newServer(t), opts...)
		_, err := c.Feed(context.Background(), "feed/missing")
		if !errors.Is(err, opds.ErrNotFound) {
			t.Fatalf("err = %v, want opds.ErrNotFound", err)
		}
		var httpErr *opdsclient.Error
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
			t.Errorf("err = %#v, want an *Error carrying 404", err)
		}
	})
}

func TestPagingWithNext(t *testing.T) {
	bothVersions(t, func(t *testing.T, _ opds.Version, opts ...opdsclient.Option) {
		c := newClient(t, newServer(t), opts...)
		pages := 0
		for f, err := c.Feed(context.Background(), "feed/new"); ; {
			if err != nil {
				t.Fatal(err)
			}
			if f == nil {
				break
			}
			pages++
			if pages > 5 {
				t.Fatal("next link never ran out")
			}
			f, err = c.Next(context.Background(), f)
		}
		if pages != 2 {
			t.Errorf("walked %d pages, want 2", pages)
		}
	})
}

func TestSearch(t *testing.T) {
	bothVersions(t, func(t *testing.T, v opds.Version, opts ...opdsclient.Option) {
		c := newClient(t, newServer(t), opts...)
		root, err := c.Root(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		// 1.x advertises an OpenSearch description document that has to be
		// fetched; 2.0 puts the template in the link. Either way:
		d, err := c.SearchDescription(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		wantPlaceholder := "{searchTerms}"
		if v == opds.Version2 {
			// 2.0 advertises an RFC 6570 form-style expansion of the query
			// key instead of a named OpenSearch parameter.
			wantPlaceholder = "{?q}"
		}
		if !strings.Contains(d.Template, wantPlaceholder) {
			t.Errorf("template = %q, want %s", d.Template, wantPlaceholder)
		}
		results, err := c.Search(context.Background(), root, "book")
		if err != nil {
			t.Fatal(err)
		}
		if len(results.Publications) != 1 {
			t.Fatalf("results = %+v", results.Publications)
		}
		empty, err := c.Search(context.Background(), root, "nothing")
		if err != nil {
			t.Fatal(err)
		}
		if len(empty.Publications) != 0 {
			t.Errorf("results = %+v, want none", empty.Publications)
		}
	})
}

func TestSearchOnFeedWithoutSearchLink(t *testing.T) {
	c := newClient(t, newServer(t))
	if _, err := c.Search(context.Background(), &opds.Feed{Title: "No search"}, "x"); !errors.Is(err, opdsclient.ErrNoSearch) {
		t.Errorf("err = %v, want ErrNoSearch", err)
	}
}

// TestDetectsFormatWithoutContentType covers the catalogs that serve feeds as
// text/xml or application/json: the body's first byte decides.
func TestDetectsFormatWithoutContentType(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body, wantTitle string
	}{
		{"atom as text/xml", "text/xml", atomFeedBody, "Catalog"},
		{"json as application/json", "application/json", jsonFeedBody, "Catalog"},
		{"no content type at all", "", jsonFeedBody, "Catalog"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			c, err := opdsclient.New(srv.URL + "/")
			if err != nil {
				t.Fatal(err)
			}
			f, err := c.Root(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if f.Title != tc.wantTitle {
				t.Errorf("title = %q, want %q", f.Title, tc.wantTitle)
			}
		})
	}
}

func TestRejectsUnrecognizedFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "not a catalog")
	}))
	defer srv.Close()
	c, err := opdsclient.New(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Root(context.Background()); !errors.Is(err, opdsclient.ErrUnrecognizedFormat) {
		t.Errorf("err = %v, want ErrUnrecognizedFormat", err)
	}
}

func TestRequestingATemplateIsRefused(t *testing.T) {
	c := newClient(t, newServer(t))
	_, err := c.Feed(context.Background(), "/opds/search{?q}")
	if err == nil || !strings.Contains(err.Error(), "template") {
		t.Errorf("err = %v, want a complaint about the URI template", err)
	}
}

const atomFeedBody = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:c</id><title>Catalog</title><updated>2026-01-02T03:04:05Z</updated>
</feed>`

const jsonFeedBody = `{"metadata": {"title": "Catalog"}, "links": [], "navigation": []}`

// TestFeedOnAPublicationURL covers a caller following a link without knowing
// whether it leads to a feed or a single publication — which OPDS does not
// always make clear — in both versions.
func TestFeedOnAPublicationURL(t *testing.T) {
	bothVersions(t, func(t *testing.T, _ opds.Version, opts ...opdsclient.Option) {
		c := newClient(t, newServer(t), opts...)
		f, err := c.Feed(context.Background(), opdshttp.PublicationPath(prefix, "b1"))
		if err != nil {
			t.Fatal(err)
		}
		if len(f.Publications) != 1 || f.Publications[0].Title != "A Book" {
			t.Fatalf("publications = %+v, want the document wrapped as a one-entry feed", f.Publications)
		}
	})
}
