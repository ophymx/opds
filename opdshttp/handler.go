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
//
// Use FeedPath, PublicationPath and SearchPath (or the Handler's URL methods)
// to build hrefs in your Source that match this layout.
package opdshttp

import (
	"errors"
	"net/http"
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
// in feeds automatically.
func New(src opds.Source, opts ...Option) *Handler {
	h := &Handler{src: src, defaultVersion: opds.Version1}
	if s, ok := src.(opds.Searcher); ok {
		h.searcher = s
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

// RootURL returns the configured root path.
func (h *Handler) RootURL() string { return h.prefix + "/" }

// FeedURL returns the path for a feed id under this handler's prefix.
func (h *Handler) FeedURL(id string) string { return FeedPath(h.prefix, id) }

// PublicationURL returns the path for a publication id under this handler's prefix.
func (h *Handler) PublicationURL(id string) string { return PublicationPath(h.prefix, id) }

// SearchURL returns the search endpoint path under this handler's prefix.
func (h *Handler) SearchURL() string { return SearchPath(h.prefix) }

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
// Source need not repeat boilerplate on every feed.
func (h *Handler) ensureLinks(r *http.Request, f *opds.Feed, v opds.Version) {
	if !hasRel(f.Links, opds.RelSelf) {
		selfType := opds.MediaTypeNavigation
		if v == opds.Version2 {
			selfType = opds.MediaTypeFeed
		} else if f.IsAcquisition() {
			selfType = opds.MediaTypeAcquisition
		}
		f.Links = append([]opds.Link{{Rel: opds.RelSelf, Href: r.URL.RequestURI(), Type: selfType}}, f.Links...)
	}
	if h.searcher != nil && !hasRel(f.Links, opds.RelSearch) {
		if v == opds.Version2 {
			f.Links = append(f.Links, opds.Link{
				Rel: opds.RelSearch, Href: h.SearchURL() + "{?query}",
				Type: opds.MediaTypeFeed, Templated: true,
			})
		} else {
			f.Links = append(f.Links, opds.Link{
				Rel: opds.RelSearch, Href: h.prefix + "/opensearch.xml",
				Type: opds.MediaTypeOpenSearch,
			})
		}
	}
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

func hasRel(links []opds.Link, rel string) bool {
	for _, l := range links {
		if l.Rel == rel {
			return true
		}
	}
	return false
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
