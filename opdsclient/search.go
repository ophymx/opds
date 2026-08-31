package opdsclient

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opensearch"
)

// ErrNoSearch reports that a feed advertises no search interface.
var ErrNoSearch = errors.New("opdsclient: feed advertises no search link")

// SearchDescription returns the search interface a feed advertises, as a
// template a caller can expand itself. The two versions advertise it
// differently and this hides the difference: OPDS 1.x links to an OpenSearch
// description document, which is fetched and parsed, while OPDS 2.0 puts the
// URI template in the search link itself.
func (c *Client) SearchDescription(ctx context.Context, f *opds.Feed) (opensearch.Description, error) {
	var link opds.Link
	for _, l := range f.Links {
		if l.Rel == opds.RelSearch {
			link = l
			break
		}
	}
	if link.Href == "" {
		return opensearch.Description{}, ErrNoSearch
	}
	if !opensearch.IsDescription(link.Type) {
		return opensearch.Description{
			ShortName:  link.Title,
			Template:   link.Href,
			ResultType: link.Type,
		}, nil
	}
	body, u, _, err := c.fetchDocument(ctx, link.Href, opds.MediaTypeOpenSearch+", application/xml;q=0.9")
	if err != nil {
		return opensearch.Description{}, err
	}
	d, err := opensearch.Unmarshal(body)
	if err != nil {
		return opensearch.Description{}, fmt.Errorf("opdsclient: %s: %w", u, err)
	}
	d.Template = resolveHref(u, d.Template)
	return d, nil
}

// Search runs the catalog's search for terms and returns the result feed.
func (c *Client) Search(ctx context.Context, f *opds.Feed, terms string) (*opds.Feed, error) {
	return c.SearchWith(ctx, f, map[string]string{"searchTerms": terms})
}

// SearchWith runs a search with explicit template parameters, for the
// narrower interfaces catalogs advertise — {atom:author} and {atom:title} in
// 1.x, their equivalents in a 2.0 template. Names are given without a
// namespace prefix: "searchTerms", "author", "title". Parameters the template
// does not mention are ignored, and ones it mentions but the caller omits are
// left out of the request.
//
// The two versions do not name the search terms the same way. OpenSearch has
// the standard {searchTerms}, but an OPDS 2.0 link expands a query key the
// catalog chose — "{?q}" here, "{?query}" there — and the parameter's
// OpenSearch name is nowhere in the document. So "searchTerms" is also offered
// under the handful of keys catalogs actually use, which is what lets one call
// search either version.
func (c *Client) SearchWith(ctx context.Context, f *opds.Feed, params map[string]string) (*opds.Feed, error) {
	d, err := c.SearchDescription(ctx, f)
	if err != nil {
		return nil, err
	}
	return c.Feed(ctx, opensearch.Expand(d.Template, withSearchAliases(params)))
}

// searchTermsAliases are the query keys OPDS 2.0 catalogs expand the free-text
// search parameter under.
var searchTermsAliases = []string{"q", "query", "search", "terms", "keyword"}

// withSearchAliases copies params, adding the alias keys for the free-text
// terms. Explicit entries always win: a caller who knows the catalog's own
// parameter name can still supply it directly.
func withSearchAliases(params map[string]string) map[string]string {
	terms, ok := params["searchTerms"]
	if !ok {
		return params
	}
	out := make(map[string]string, len(params)+len(searchTermsAliases))
	for _, alias := range searchTermsAliases {
		out[alias] = terms
	}
	maps.Copy(out, params)
	return out
}

// Follow fetches the feed linked from f with the given relation — opds.RelNext
// and opds.RelPrevious to page, opds.RelStart and opds.RelUp to navigate. It
// reports a nil feed and a nil error when the feed carries no such link, so a
// paging loop ends without a sentinel comparison.
func (c *Client) Follow(ctx context.Context, f *opds.Feed, rel string) (*opds.Feed, error) {
	for _, l := range f.Links {
		if l.Rel == rel {
			return c.Feed(ctx, l.Href)
		}
	}
	return nil, nil
}

// Next fetches the next page of a paged feed, or nil when there is none:
//
//	for f, err := c.Root(ctx); f != nil && err == nil; f, err = c.Next(ctx, f) {
//		...
//	}
func (c *Client) Next(ctx context.Context, f *opds.Feed) (*opds.Feed, error) {
	return c.Follow(ctx, f, opds.RelNext)
}
