package opdshttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opdshttp"
)

// memSource is a minimal in-memory Source + Searcher used to exercise the handler.
type memSource struct{ prefix string }

func (m memSource) Root(_ context.Context, _ opds.FeedRequest) (*opds.Feed, error) {
	return opds.NewFeed("urn:root", "Root").
		AddNav("New", opdshttp.FeedPath(m.prefix, "new"), opds.MediaTypeAcquisition, opds.RelSortNew), nil
}

func (m memSource) Feed(_ context.Context, req opds.FeedRequest) (*opds.Feed, error) {
	if req.ID != "new" {
		return nil, opds.ErrNotFound
	}
	p := opds.NewPublication("urn:b1", "A Book").By("Jane").OpenAccess("/b1.epub", "application/epub+zip")
	return opds.NewFeed("urn:feed:new", "New").Add(*p), nil
}

func (m memSource) Publication(_ context.Context, id string) (*opds.Publication, error) {
	if id != "b1" {
		return nil, opds.ErrNotFound
	}
	return opds.NewPublication("urn:b1", "A Book").By("Jane").OpenAccess("/b1.epub", "application/epub+zip"), nil
}

func (m memSource) Search(_ context.Context, req opds.SearchRequest) (*opds.Feed, error) {
	f := opds.NewFeed("urn:search", "Results for "+req.Terms)
	if req.Terms == "book" {
		p := opds.NewPublication("urn:b1", "A Book").OpenAccess("/b1.epub", "application/epub+zip")
		f.Add(*p)
	}
	return f, nil
}

func (m memSource) SearchDescription() opds.SearchDescription {
	return opds.SearchDescription{ShortName: "Catalog", Description: "Search the catalog"}
}

func newServer() *opdshttp.Handler {
	return opdshttp.New(memSource{prefix: "/opds"}, opdshttp.WithPrefix("/opds"))
}

func get(t *testing.T, h http.Handler, path, accept string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRootDefaultsToAtom(t *testing.T) {
	w := get(t, newServer(), "/opds/", "")
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "kind=navigation") {
		t.Errorf("content-type = %q, want navigation", ct)
	}
	if !strings.Contains(w.Body.String(), "<?xml") {
		t.Errorf("expected XML body")
	}
	// search link auto-injected (source is a Searcher).
	if !strings.Contains(w.Body.String(), `rel="search"`) {
		t.Errorf("expected auto search link:\n%s", w.Body.String())
	}
}

func TestRootNegotiatesJSON(t *testing.T) {
	w := get(t, newServer(), "/opds/", opds.MediaTypeFeed)
	if ct := w.Header().Get("Content-Type"); ct != opds.MediaTypeFeed {
		t.Errorf("content-type = %q", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := doc["navigation"]; !ok {
		t.Errorf("expected navigation collection: %v", doc)
	}
}

func TestVersionQueryOverride(t *testing.T) {
	// Accept asks for Atom, but ?version=2 wins.
	w := get(t, newServer(), "/opds/?version=2", "application/atom+xml")
	if ct := w.Header().Get("Content-Type"); ct != opds.MediaTypeFeed {
		t.Errorf("content-type = %q, want JSON", ct)
	}
}

func TestAcquisitionContentType(t *testing.T) {
	w := get(t, newServer(), "/opds/feed/new", "")
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "kind=acquisition") {
		t.Errorf("content-type = %q, want acquisition", ct)
	}
}

func TestPublicationDoc(t *testing.T) {
	w := get(t, newServer(), "/opds/publication/b1", opds.MediaTypeFeed)
	if ct := w.Header().Get("Content-Type"); ct != opds.MediaTypePublication {
		t.Errorf("content-type = %q", ct)
	}
}

func TestNotFound(t *testing.T) {
	if w := get(t, newServer(), "/opds/feed/missing", ""); w.Code != 404 {
		t.Errorf("code = %d, want 404", w.Code)
	}
	if w := get(t, newServer(), "/opds/publication/missing", ""); w.Code != 404 {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

func TestSearch(t *testing.T) {
	w := get(t, newServer(), "/opds/search?q=book", opds.MediaTypeFeed)
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	var doc map[string]any
	json.Unmarshal(w.Body.Bytes(), &doc)
	if pubs, _ := doc["publications"].([]any); len(pubs) != 1 {
		t.Errorf("expected 1 result, got %v", doc["publications"])
	}
}

func TestOpenSearchDescription(t *testing.T) {
	w := get(t, newServer(), "/opds/opensearch.xml", "")
	if ct := w.Header().Get("Content-Type"); ct != opds.MediaTypeOpenSearch {
		t.Errorf("content-type = %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<ShortName>Catalog</ShortName>") {
		t.Errorf("missing ShortName:\n%s", body)
	}
	if !strings.Contains(body, "{searchTerms}") {
		t.Errorf("missing search template:\n%s", body)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/opds/", nil)
	w := httptest.NewRecorder()
	newServer().ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d", w.Code)
	}
}

// tmplSource overrides the search template to exercise template propagation.
type tmplSource struct {
	memSource
	template string
}

func (s tmplSource) SearchDescription() opds.SearchDescription {
	return opds.SearchDescription{ShortName: "Catalog", Template: s.template}
}

// searchHrefV2 extracts the templated search href advertised in a 2.0 feed.
func searchHrefV2(t *testing.T, h http.Handler) string {
	t.Helper()
	w := get(t, h, "/opds/?version=2", "")
	var doc struct {
		Links []struct {
			Rel       string `json:"rel"`
			Href      string `json:"href"`
			Templated bool   `json:"templated"`
		} `json:"links"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, l := range doc.Links {
		if l.Rel == "search" {
			if !l.Templated {
				t.Errorf("search link should be templated")
			}
			return l.Href
		}
	}
	t.Fatalf("no search link in:\n%s", w.Body.String())
	return ""
}

// Default (no template): the 2.0 link and the OpenSearch doc must advertise the
// same parameter (q), per the search-issue.md report.
func TestSearchParamDefaultsConsistent(t *testing.T) {
	h := newServer()
	if href := searchHrefV2(t, h); !strings.HasSuffix(href, "{?q}") {
		t.Errorf("2.0 search href = %q, want suffix {?q}", href)
	}
	os := get(t, h, "/opds/opensearch.xml", "").Body.String()
	if !strings.Contains(os, "q={searchTerms}") {
		t.Errorf("opensearch doc should advertise q={searchTerms}:\n%s", os)
	}
}

// A Source's configured template parameter name must be reflected in the 2.0
// link (form-style), not hardcoded.
func TestSearchTemplateHonoredV2(t *testing.T) {
	h := opdshttp.New(
		tmplSource{memSource{prefix: "/opds"}, "/opds/search?query={searchTerms}"},
		opdshttp.WithPrefix("/opds"),
	)
	if href := searchHrefV2(t, h); href != "/opds/search{?query}" {
		t.Errorf("2.0 search href = %q, want /opds/search{?query}", href)
	}
}

// Multiple templated parameters become a single form-style expansion.
func TestSearchTemplateMultiParam(t *testing.T) {
	h := opdshttp.New(
		tmplSource{memSource{prefix: "/opds"}, "/opds/search?q={searchTerms}&author={author}"},
		opdshttp.WithPrefix("/opds"),
	)
	if href := searchHrefV2(t, h); href != "/opds/search{?q,author}" {
		t.Errorf("2.0 search href = %q, want /opds/search{?q,author}", href)
	}
}

func TestPagePathHelpers(t *testing.T) {
	if got := opdshttp.FeedPagePath("/opds", "new", 1); got != "/opds/feed/new" {
		t.Errorf("FeedPagePath page 1 = %q, want /opds/feed/new", got)
	}
	if got := opdshttp.FeedPagePath("/opds", "new", 3); got != "/opds/feed/new?page=3" {
		t.Errorf("FeedPagePath page 3 = %q, want /opds/feed/new?page=3", got)
	}
	q := url.Values{"q": {"dune"}, "page": {"2"}}
	if got := opdshttp.SearchPagePath("/opds", q, 3); got != "/opds/search?page=3&q=dune" {
		t.Errorf("SearchPagePath = %q, want /opds/search?page=3&q=dune", got)
	}
	if got := opdshttp.SearchPagePath("/opds", q, 1); got != "/opds/search?q=dune" {
		t.Errorf("SearchPagePath page 1 = %q, want /opds/search?q=dune", got)
	}
}

// pagedSource serves a feed that uses Feed.Paged, leaving link types empty for
// the handler to fill.
type pagedSource struct{ memSource }

func (s pagedSource) Feed(_ context.Context, req opds.FeedRequest) (*opds.Feed, error) {
	p := opds.NewPublication("urn:b1", "A Book").OpenAccess("/b1.epub", "application/epub+zip")
	return opds.NewFeed("urn:feed:new", "New").Add(*p).
		Paged(opdshttp.FeedPath("/opds", req.ID), req.Page, true), nil
}

func TestHandlerFillsPaginationLinkTypes(t *testing.T) {
	h := opdshttp.New(pagedSource{memSource{prefix: "/opds"}}, opdshttp.WithPrefix("/opds"))
	w := get(t, h, "/opds/feed/new?page=2&version=2", "")
	var doc struct {
		Links []struct{ Rel, Href, Type string } `json:"links"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	types := map[string]string{}
	hrefs := map[string]string{}
	for _, l := range doc.Links {
		types[l.Rel] = l.Type
		hrefs[l.Rel] = l.Href
	}
	if hrefs["next"] != "/opds/feed/new?page=3" || hrefs["previous"] != "/opds/feed/new" {
		t.Errorf("pagination hrefs = next %q, previous %q", hrefs["next"], hrefs["previous"])
	}
	if types["next"] != opds.MediaTypeFeed || types["previous"] != opds.MediaTypeFeed {
		t.Errorf("pagination types = next %q, previous %q, want %q", types["next"], types["previous"], opds.MediaTypeFeed)
	}

	// The 1.2 rendering fills the acquisition feed type instead.
	w = get(t, h, "/opds/feed/new?page=2", "")
	if !strings.Contains(w.Body.String(), `rel="next" href="/opds/feed/new?page=3" type="`+opds.MediaTypeAcquisition+`"`) {
		t.Errorf("1.2 next link missing or untyped:\n%s", w.Body.String())
	}
}

// selfSource sets its own self link, the way a Source naturally would from the
// builder — without the page parameter of the request.
type selfSource struct {
	memSource
	selfHref string
}

func (s selfSource) Feed(_ context.Context, _ opds.FeedRequest) (*opds.Feed, error) {
	return opds.NewFeed("urn:feed:new", "New").
		Self(s.selfHref, opds.MediaTypeNavigation), nil
}

func selfHrefV2(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	w := get(t, h, path, opds.MediaTypeFeed)
	var doc struct {
		Links []struct{ Rel, Href string } `json:"links"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, l := range doc.Links {
		if l.Rel == "self" {
			return l.Href
		}
	}
	t.Fatalf("no self link in:\n%s", w.Body.String())
	return ""
}

func TestSelfLinkCorrectedOnPagedRequest(t *testing.T) {
	h := opdshttp.New(
		selfSource{memSource{prefix: "/opds"}, "/opds/feed/new"},
		opdshttp.WithPrefix("/opds"),
	)
	// Unpaged (and page 1) requests keep the Source's self link.
	if href := selfHrefV2(t, h, "/opds/feed/new"); href != "/opds/feed/new" {
		t.Errorf("unpaged self = %q, want /opds/feed/new", href)
	}
	// Page 2 must not advertise page 1 as itself.
	if href := selfHrefV2(t, h, "/opds/feed/new?page=2"); href != "/opds/feed/new?page=2" {
		t.Errorf("paged self = %q, want /opds/feed/new?page=2", href)
	}
}

func TestSelfLinkWithMatchingPageKept(t *testing.T) {
	// A self href already carrying the served page (e.g. a canonical absolute
	// URL) is left alone.
	h := opdshttp.New(
		selfSource{memSource{prefix: "/opds"}, "https://example.com/opds/feed/new?page=2"},
		opdshttp.WithPrefix("/opds"),
	)
	if href := selfHrefV2(t, h, "/opds/feed/new?page=2"); href != "https://example.com/opds/feed/new?page=2" {
		t.Errorf("self = %q, want the Source's absolute href kept", href)
	}
}

// cachedSource returns the same *opds.Feed value for every request, the way a
// Source with a static catalog naturally would.
type cachedSource struct {
	memSource
	feed *opds.Feed
}

func (s cachedSource) Root(_ context.Context, _ opds.FeedRequest) (*opds.Feed, error) {
	return s.feed, nil
}

func TestHandlerDoesNotMutateSourceFeed(t *testing.T) {
	src := cachedSource{memSource{prefix: "/opds"}, opds.NewFeed("urn:root", "Root")}
	h := opdshttp.New(src, opdshttp.WithPrefix("/opds"))

	// A 2.0 request injects self and templated-search links; they must land in
	// a copy, not the shared feed.
	get(t, h, "/opds/?version=2&page=2", "")
	if len(src.feed.Links) != 0 {
		t.Fatalf("handler mutated the Source's feed links: %+v", src.feed.Links)
	}

	// A subsequent 1.2 request must still get the OpenSearch link, not a stale
	// 2.0 templated link left over from the previous request.
	body := get(t, h, "/opds/", "").Body.String()
	if !strings.Contains(body, `href="/opds/opensearch.xml"`) {
		t.Errorf("1.2 feed missing OpenSearch link:\n%s", body)
	}
}

// pageSource serves one-page-per-byte "images" so tests can verify which page
// was requested and how the width hint was parsed.
type pageSource struct{ memSource }

func (s pageSource) Page(_ context.Context, req opds.PageRequest) (*opds.PageImage, error) {
	if req.ID != "c1" || req.Number >= 3 {
		return nil, opds.ErrNotFound
	}
	body := strings.NewReader(fmt.Sprintf("page %d width %d", req.Number, req.MaxWidth))
	return &opds.PageImage{Type: "image/jpeg", Content: body}, nil
}

func newPageServer() *opdshttp.Handler {
	return opdshttp.New(pageSource{memSource{prefix: "/opds"}}, opdshttp.WithPrefix("/opds"))
}

func TestPageStreamServesPages(t *testing.T) {
	w := get(t, newPageServer(), "/opds/page/c1?page=2&width=1404", "")
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("content-type = %q, want image/jpeg", ct)
	}
	if body := w.Body.String(); body != "page 2 width 1404" {
		t.Errorf("body = %q", body)
	}
}

func TestPageStreamParamDefaults(t *testing.T) {
	// No page parameter means page 0; a literal unexpanded {maxWidth} token
	// (from a client that does not support it) means width unspecified.
	w := get(t, newPageServer(), "/opds/page/c1?width=%7BmaxWidth%7D", "")
	if body := w.Body.String(); body != "page 0 width 0" {
		t.Errorf("body = %q, want page 0 width 0", body)
	}
}

func TestPageStreamBadPageNumber(t *testing.T) {
	for _, path := range []string{
		"/opds/page/c1?page=%7BpageNumber%7D", // unexpanded token
		"/opds/page/c1?page=-1",
	} {
		if w := get(t, newPageServer(), path, ""); w.Code != 400 {
			t.Errorf("GET %s code = %d, want 400", path, w.Code)
		}
	}
}

func TestPageStreamNotFound(t *testing.T) {
	if w := get(t, newPageServer(), "/opds/page/c1?page=3", ""); w.Code != 404 {
		t.Errorf("out-of-range page code = %d, want 404", w.Code)
	}
	if w := get(t, newPageServer(), "/opds/page/missing", ""); w.Code != 404 {
		t.Errorf("unknown publication code = %d, want 404", w.Code)
	}
}

func TestPageStreamDisabledWithoutPageSource(t *testing.T) {
	// memSource does not implement opds.PageSource.
	if w := get(t, newServer(), "/opds/page/c1?page=0", ""); w.Code != 404 {
		t.Errorf("code = %d, want 404 without PageSource", w.Code)
	}
}

func TestPageStreamPathTemplate(t *testing.T) {
	want := "/opds/page/c1?page={pageNumber}&width={maxWidth}"
	if got := opdshttp.PageStreamPath("/opds", "c1"); got != want {
		t.Errorf("PageStreamPath = %q, want %q", got, want)
	}
	if got := newPageServer().PageStreamURL("c1"); got != want {
		t.Errorf("PageStreamURL = %q, want %q", got, want)
	}
}

func TestSearchDisabledWithoutSearcher(t *testing.T) {
	// Wrapping in a struct that embeds only opds.Source hides the Searcher
	// methods, so the handler must not enable search.
	src := struct{ opds.Source }{memSource{prefix: "/opds"}}
	h := opdshttp.New(src, opdshttp.WithPrefix("/opds"))
	if w := get(t, h, "/opds/search?q=book", ""); w.Code != 404 {
		t.Errorf("search should be 404 without Searcher, got %d", w.Code)
	}
	if w := get(t, h, "/opds/", ""); strings.Contains(w.Body.String(), `rel="search"`) {
		t.Errorf("should not advertise search without Searcher")
	}
}
