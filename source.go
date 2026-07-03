package opds

import (
	"context"
	"errors"
	"net/url"
)

// ErrNotFound is returned by a Source when a requested feed or publication does
// not exist. The HTTP layer maps it to 404 Not Found.
var ErrNotFound = errors.New("opds: not found")

// Source is the backend a catalog implements. It returns version-neutral Feed
// and Publication values; the library handles serialization to OPDS 1.2 or 2.0,
// content negotiation, and HTTP wiring.
//
// Implementations are responsible for the URLs (hrefs) they place in feeds:
// the library does not rewrite them. Use Feed.Paged with a base href built
// from the opdshttp path helpers (FeedPath, FeedPagePath, SearchPagePath) to
// emit pagination links, or construct hrefs however suits the backend.
type Source interface {
	// Root returns the catalog's root feed (usually a navigation feed).
	Root(ctx context.Context, req FeedRequest) (*Feed, error)

	// Feed returns the feed identified by req.ID. It should return ErrNotFound
	// if no such feed exists.
	Feed(ctx context.Context, req FeedRequest) (*Feed, error)

	// Publication returns the full entry for a single publication. It should
	// return ErrNotFound if no such publication exists. A Source that never
	// serves standalone publication documents may return ErrNotFound always.
	Publication(ctx context.Context, id string) (*Publication, error)
}

// Searcher is an optional interface a Source may also implement to support
// search. When present, the HTTP layer advertises a search link and routes
// search requests to it.
type Searcher interface {
	// Search returns a feed of results for the given request.
	Search(ctx context.Context, req SearchRequest) (*Feed, error)

	// SearchDescription returns metadata describing the search interface,
	// used to generate the OpenSearch document and the 2.0 search link.
	SearchDescription() SearchDescription
}

// FeedRequest carries the parameters of a request for a feed.
type FeedRequest struct {
	// ID identifies the requested feed (empty for the root). For the HTTP layer
	// this is the path segment after the feed prefix.
	ID string
	// Page is the requested 1-based page number (1 if unspecified).
	Page int
	// Version is the OPDS version the response will be encoded in, as
	// negotiated by the caller. Implementations may use it to tailor hrefs.
	Version Version
	// BaseURL is the absolute base URL of the catalog (scheme://host), if known.
	BaseURL string
	// Query holds the raw query parameters of the request (facet selections,
	// sort orders, and so on).
	Query url.Values
}

// SearchRequest carries the parameters of a search.
type SearchRequest struct {
	// Terms is the free-text query (the OpenSearch {searchTerms}).
	Terms string
	// Author optionally narrows the search by author.
	Author string
	// Title optionally narrows the search by title.
	Title string
	// Page is the requested 1-based page number (1 if unspecified).
	Page int
	// Version is the negotiated OPDS version of the response.
	Version Version
	// BaseURL is the absolute base URL of the catalog, if known.
	BaseURL string
	// Query holds the raw query parameters of the request.
	Query url.Values
}

// SearchDescription describes a catalog's search interface. It drives the
// OpenSearch description document (1.x) and the templated search link (2.0).
type SearchDescription struct {
	// ShortName is a brief name for the search engine (OpenSearch ShortName).
	ShortName string
	// Description is a human-readable description of the search.
	Description string
	// Template is the search URL template using RFC 6570 / OpenSearch syntax,
	// e.g. "/search?q={searchTerms}". If it contains no parameters the library
	// appends "?q={searchTerms}". Extra params such as {author} and {title}
	// are advertised when present.
	Template string
}
