// Package opdshttp provides an embeddable http.Handler that exposes an
// opds.Source as an OPDS catalog, handling routing, content negotiation
// between OPDS 1.2 and 2.0, pagination, and search.
//
// The handler serves this URL layout, relative to its mount Prefix:
//
//	{prefix}/                 the root feed
//	{prefix}/feed/{id}        a feed by id
//	{prefix}/publication/{id} a single publication document
//	{prefix}/search           search results (if the Source is an opds.Searcher)
//	{prefix}/opensearch.xml   the OpenSearch description document
//	{prefix}/page/{id}        a single page image (if the Source is an opds.PageSource)
//
// Use FeedPath, PublicationPath, SearchPath and PageStreamPath (or the
// Handler's URL methods) to build hrefs in your Source that match this layout.
//
// Feeds returned by the Source should omit their self link: the handler adds
// one derived from the request URL, which correctly reflects the query
// parameters (in particular ?page) of the request. A self link a Source sets
// anyway is kept, but its href is corrected on paged requests when its page
// parameter does not match the page served.
//
// The handler never mutates the feed a Source returns — links it injects are
// added to a per-request copy — so a Source may safely return a shared or
// cached *opds.Feed from concurrent requests.
package opdshttp

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opds1"
	"github.com/ophymx/opds/opds2"
	"github.com/ophymx/opds/opensearch"
)

// Handler serves an opds.Source over HTTP.
type Handler struct {
	src            opds.Source
	searcher       opds.Searcher
	pages          opds.PageSource
	prefix         string
	defaultVersion opds.Version
	errorHandler   func(http.ResponseWriter, *http.Request, error)
}

// Option configures a Handler.
type Option func(*Handler)

// WithPrefix sets the path the handler is mounted at (e.g. "/opds"). It is
// trimmed of a trailing slash. The default is "".
func WithPrefix(prefix string) Option {
	return func(h *Handler) { h.prefix = strings.TrimRight(prefix, "/") }
}

// WithDefaultVersion sets the OPDS version used when a client expresses no
// preference via Accept or query parameter. The default is opds.Version1, which
// maximizes client compatibility.
func WithDefaultVersion(v opds.Version) Option {
	return func(h *Handler) { h.defaultVersion = v }
}

// WithErrorHandler sets a custom handler for errors returned by the Source.
// By default opds.ErrNotFound yields 404 and any other error yields 500.
func WithErrorHandler(fn func(http.ResponseWriter, *http.Request, error)) Option {
	return func(h *Handler) { h.errorHandler = fn }
}

// New returns a Handler serving src. If src also implements opds.Searcher,
// search and OpenSearch endpoints are enabled and a search link is advertised
// in feeds automatically. If src also implements opds.PageSource, the
// page-image endpoint behind OPDS-PSE stream links is enabled.
func New(src opds.Source, opts ...Option) *Handler {
	h := &Handler{src: src, defaultVersion: opds.Version1}
	if s, ok := src.(opds.Searcher); ok {
		h.searcher = s
	}
	if p, ok := src.(opds.PageSource); ok {
		h.pages = p
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// FeedPath returns the request path for the feed with the given id.
func FeedPath(prefix, id string) string {
	return strings.TrimRight(prefix, "/") + "/feed/" + id
}

// PublicationPath returns the request path for the publication with the given id.
func PublicationPath(prefix, id string) string {
	return strings.TrimRight(prefix, "/") + "/publication/" + id
}

// SearchPath returns the request path for the search endpoint.
func SearchPath(prefix string) string {
	return strings.TrimRight(prefix, "/") + "/search"
}

// PageStreamPath returns the OPDS-PSE href template for streaming the pages of
// the publication with the given id, carrying the {pageNumber} and {maxWidth}
// tokens clients substitute (see opds.Publication.Stream). The template is
// valid even when the PageSource ignores PageRequest.MaxWidth: the width
// parameter is simply unused.
func PageStreamPath(prefix, id string) string {
	return strings.TrimRight(prefix, "/") + "/page/" + id + "?page={pageNumber}&width={maxWidth}"
}

// FeedPagePath returns the request path for the given page of the feed with
// the given id. Page 1 is the plain feed path.
func FeedPagePath(prefix, id string, page int) string {
	return opds.PageHref(FeedPath(prefix, id), page)
}

// SearchPagePath returns the request path for the given page of a search,
// carrying over the query parameters (typically SearchRequest.Query, whose
// existing page parameter is replaced). Page 1 carries no page parameter.
func SearchPagePath(prefix string, query url.Values, page int) string {
	path := SearchPath(prefix)
	if enc := query.Encode(); enc != "" {
		path += "?" + enc
	}
	return opds.PageHref(path, page)
}

// RootURL returns the configured root path.
func (h *Handler) RootURL() string { return h.prefix + "/" }

// FeedURL returns the path for a feed id under this handler's prefix.
func (h *Handler) FeedURL(id string) string { return FeedPath(h.prefix, id) }

// PublicationURL returns the path for a publication id under this handler's prefix.
func (h *Handler) PublicationURL(id string) string { return PublicationPath(h.prefix, id) }

// SearchURL returns the search endpoint path under this handler's prefix.
func (h *Handler) SearchURL() string { return SearchPath(h.prefix) }

// PageStreamURL returns the OPDS-PSE href template for a publication id under
// this handler's prefix.
func (h *Handler) PageStreamURL(id string) string { return PageStreamPath(h.prefix, id) }

// FeedPageURL returns the path for a page of a feed id under this handler's prefix.
func (h *Handler) FeedPageURL(id string, page int) string { return FeedPagePath(h.prefix, id, page) }

// SearchPageURL returns the path for a page of a search under this handler's prefix.
func (h *Handler) SearchPageURL(query url.Values, page int) string {
	return SearchPagePath(h.prefix, query, page)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, h.prefix)
	rest = strings.TrimPrefix(rest, "/")

	switch {
	case rest == "" || rest == "/":
		h.serveRoot(w, r)
	case rest == "opensearch.xml":
		h.serveOpenSearch(w, r)
	case rest == "search":
		h.serveSearch(w, r)
	case strings.HasPrefix(rest, "publication/"):
		h.servePublication(w, r, strings.TrimPrefix(rest, "publication/"))
	case strings.HasPrefix(rest, "feed/"):
		h.serveFeed(w, r, strings.TrimPrefix(rest, "feed/"))
	case strings.HasPrefix(rest, "page/"):
		h.servePage(w, r, strings.TrimPrefix(rest, "page/"))
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) serveRoot(w http.ResponseWriter, r *http.Request) {
	v := h.negotiate(r)
	feed, err := h.src.Root(r.Context(), h.feedRequest(r, "", v))
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	h.writeFeed(w, r, feed, v)
}

func (h *Handler) serveFeed(w http.ResponseWriter, r *http.Request, id string) {
	v := h.negotiate(r)
	feed, err := h.src.Feed(r.Context(), h.feedRequest(r, id, v))
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	h.writeFeed(w, r, feed, v)
}

func (h *Handler) servePublication(w http.ResponseWriter, r *http.Request, id string) {
	v := h.negotiate(r)
	pub, err := h.src.Publication(r.Context(), id)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	var (
		body []byte
		ct   string
	)
	if v == opds.Version2 {
		body, err = opds2.MarshalPublication(pub)
		ct = opds.MediaTypePublication
	} else {
		body, err = opds1.MarshalEntry(pub)
		ct = opds.MediaTypeEntry
	}
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	write(w, r, ct, body)
}

func (h *Handler) serveSearch(w http.ResponseWriter, r *http.Request) {
	if h.searcher == nil {
		http.NotFound(w, r)
		return
	}
	v := h.negotiate(r)
	q := r.URL.Query()
	req := opds.SearchRequest{
		Terms:   firstNonEmpty(q.Get("q"), q.Get("query"), q.Get("searchTerms")),
		Author:  q.Get("author"),
		Title:   q.Get("title"),
		Page:    pageParam(r),
		Version: v,
		BaseURL: baseURL(r),
		Query:   q,
	}
	feed, err := h.searcher.Search(r.Context(), req)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	h.writeFeed(w, r, feed, v)
}

func (h *Handler) servePage(w http.ResponseWriter, r *http.Request, id string) {
	if h.pages == nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	number, ok := pageNumberParam(q.Get("page"))
	if !ok {
		http.Error(w, "invalid page number", http.StatusBadRequest)
		return
	}
	img, err := h.pages.Page(r.Context(), opds.PageRequest{
		ID:       id,
		Number:   number,
		MaxWidth: widthParam(q.Get("width")),
		Query:    q,
	})
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	if c, ok := img.Content.(io.Closer); ok {
		defer c.Close()
	}
	w.Header().Set("Content-Type", img.Type)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	io.Copy(w, img.Content)
}

// pageNumberParam parses the zero-based PSE page number. An absent parameter
// means the first page; a malformed or negative value is rejected.
func pageNumberParam(s string) (int, bool) {
	if s == "" {
		return 0, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// widthParam parses the expanded {maxWidth} token. Clients that do not
// support the token pass it through literally, so anything unparseable means
// "unspecified" rather than an error.
func widthParam(s string) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return 0
}

func (h *Handler) serveOpenSearch(w http.ResponseWriter, r *http.Request) {
	if h.searcher == nil {
		http.NotFound(w, r)
		return
	}
	desc := h.searcher.SearchDescription()
	template := desc.Template
	if template == "" {
		template = baseURL(r) + h.SearchURL() + "?q={searchTerms}"
	}
	body, err := opensearch.Marshal(opensearch.Description{
		ShortName:   desc.ShortName,
		Description: desc.Description,
		Template:    template,
	})
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	write(w, r, opds.MediaTypeOpenSearch, body)
}

func (h *Handler) feedRequest(r *http.Request, id string, v opds.Version) opds.FeedRequest {
	return opds.FeedRequest{
		ID:      id,
		Page:    pageParam(r),
		Version: v,
		BaseURL: baseURL(r),
		Query:   r.URL.Query(),
	}
}

func (h *Handler) writeFeed(w http.ResponseWriter, r *http.Request, f *opds.Feed, v opds.Version) {
	// Work on a copy with its own Links slice: ensureLinks adds and rewrites
	// links, and the Source's feed may be shared (e.g. cached) across requests.
	feed := *f
	feed.Links = slices.Clone(f.Links)
	f = &feed
	h.ensureLinks(r, f, v)
	var (
		body []byte
		ct   string
		err  error
	)
	if v == opds.Version2 {
		body, err = opds2.Marshal(f)
		ct = opds.MediaTypeFeed
	} else {
		body, err = opds1.Marshal(f)
		if f.IsAcquisition() {
			ct = opds.MediaTypeAcquisition
		} else {
			ct = opds.MediaTypeNavigation
		}
	}
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	write(w, r, ct, body)
}

// ensureLinks adds self and search links to a feed when they are absent, so a
// Source need not repeat boilerplate on every feed, and fills the media type
// of untyped pagination links (e.g. those added by Feed.Paged) with the feed's
// own negotiated type.
//
// The injected self href is the request URI, so it carries the page parameter
// of a paged request. A self link set by the Source with a stale or missing
// page parameter (page 2 of a feed advertising page 1 as itself) is corrected
// to the request URI rather than served wrong.
func (h *Handler) ensureLinks(r *http.Request, f *opds.Feed, v opds.Version) {
	feedType := opds.MediaTypeNavigation
	if v == opds.Version2 {
		feedType = opds.MediaTypeFeed
	} else if f.IsAcquisition() {
		feedType = opds.MediaTypeAcquisition
	}
	for i, l := range f.Links {
		if l.Type == "" && isPaginationRel(l.Rel) {
			f.Links[i].Type = feedType
		}
	}
	if i := indexRel(f.Links, opds.RelSelf); i < 0 {
		f.Links = append([]opds.Link{{Rel: opds.RelSelf, Href: r.URL.RequestURI(), Type: feedType}}, f.Links...)
	} else if p := pageParam(r); p > 1 && hrefPage(f.Links[i].Href) != p {
		f.Links[i].Href = r.URL.RequestURI()
	}
	if h.searcher != nil && !hasRel(f.Links, opds.RelSearch) {
		if v == opds.Version2 {
			f.Links = append(f.Links, opds.Link{
				Rel:       opds.RelSearch,
				Href:      searchTemplateV2(h.searcher.SearchDescription().Template, h.SearchURL()),
				Type:      opds.MediaTypeFeed,
				Templated: true,
			})
		} else {
			f.Links = append(f.Links, opds.Link{
				Rel: opds.RelSearch, Href: h.prefix + "/opensearch.xml",
				Type: opds.MediaTypeOpenSearch,
			})
		}
	}
}

// searchTemplateV2 derives the OPDS 2.0 templated search href from the Source's
// OpenSearch-style template (e.g. "/opds/search?q={searchTerms}"), converting it
// to the RFC 6570 form-style expansion OPDS 2.0 uses ("/opds/search{?q}") while
// preserving the parameter name(s) the Source chose. This keeps the 2.0 link and
// the OpenSearch 1.x document advertising the same parameters. When no template
// is configured it falls back to "{?q}", matching the OpenSearch default.
func searchTemplateV2(template, fallbackPath string) string {
	if template == "" {
		return fallbackPath + "{?q}"
	}
	// Already an RFC 6570 form-style template: use it verbatim.
	if strings.Contains(template, "{?") || strings.Contains(template, "{&") {
		return template
	}
	path, query, _ := strings.Cut(template, "?")
	keys := templatedQueryKeys(query)
	if len(keys) == 0 {
		keys = []string{"q"}
	}
	return path + "{?" + strings.Join(keys, ",") + "}"
}

// templatedQueryKeys returns, in order and without duplicates, the keys of an
// OpenSearch-style query string whose value is a template parameter (contains a
// "{...}" placeholder), e.g. "q={searchTerms}&author={atom:author}" -> [q author].
func templatedQueryKeys(query string) []string {
	var keys []string
	seen := map[string]bool{}
	for pair := range strings.SplitSeq(query, "&") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || k == "" || !strings.Contains(v, "{") {
			continue
		}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys
}

func (h *Handler) negotiate(r *http.Request) opds.Version {
	q := r.URL.Query()
	switch q.Get("version") {
	case "2", "2.0":
		return opds.Version2
	case "1", "1.2":
		return opds.Version1
	}
	switch q.Get("f") {
	case "json", "opds2":
		return opds.Version2
	case "atom", "xml", "opds1":
		return opds.Version1
	}
	accept := r.Header.Get("Accept")
	switch {
	case strings.Contains(accept, "application/opds+json"),
		strings.Contains(accept, "application/opds-publication+json"):
		return opds.Version2
	case strings.Contains(accept, "atom+xml"),
		strings.Contains(accept, "opds-catalog"):
		return opds.Version1
	}
	return h.defaultVersion
}

func (h *Handler) handleError(w http.ResponseWriter, r *http.Request, err error) {
	if h.errorHandler != nil {
		h.errorHandler(w, r, err)
		return
	}
	if errors.Is(err, opds.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func write(w http.ResponseWriter, r *http.Request, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	w.Write(body)
}

func isPaginationRel(rel string) bool {
	switch rel {
	case opds.RelNext, opds.RelPrevious, opds.RelFirst, opds.RelLast:
		return true
	}
	return false
}

func hasRel(links []opds.Link, rel string) bool { return indexRel(links, rel) >= 0 }

func indexRel(links []opds.Link, rel string) int {
	for i, l := range links {
		if l.Rel == rel {
			return i
		}
	}
	return -1
}

// hrefPage returns the 1-based page number an href's "page" query parameter
// claims, defaulting to 1 when absent or unparseable (mirroring pageParam).
func hrefPage(href string) int {
	u, err := url.Parse(href)
	if err != nil {
		return 1
	}
	if p, err := strconv.Atoi(u.Query().Get("page")); err == nil && p > 0 {
		return p
	}
	return 1
}

func pageParam(r *http.Request) int {
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		return p
	}
	return 1
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + r.Host
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
