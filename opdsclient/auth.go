package opdsclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/internal/wire"
)

// AuthDocument fetches and decodes the OPDS Authentication Document at href.
func (c *Client) AuthDocument(ctx context.Context, href string) (*opds.AuthDocument, error) {
	resp, u, err := c.do(ctx, http.MethodGet, href, opds.MediaTypeAuthDocument+", application/json;q=0.9", "", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readBody(resp.Body, maxSmallDocument)
	if err != nil {
		return nil, fmt.Errorf("opdsclient: %s: %w", u, err)
	}
	doc, err := wire.ParseAuthDocument(body)
	if err != nil {
		return nil, fmt.Errorf("opdsclient: %s: %w", u, err)
	}
	resolveAuthDocument(doc, u)
	return doc, nil
}

// DiscoverAuth returns the Authentication Document the catalog wants clients
// to use, which is how an application learns whether to prompt for credentials
// and what to label the fields. It asks the root anonymously — deliberately,
// so the answer does not depend on whether the configured credentials happen
// to work — and reads the document out of the 401 the catalog answers with,
// following the challenge's Link header if the body did not carry one.
//
// A catalog that needs no credentials answers the root, and DiscoverAuth
// reports nil with a nil error.
func (c *Client) DiscoverAuth(ctx context.Context) (*opds.AuthDocument, error) {
	anon := *c
	anon.user, anon.pass = "", ""
	resp, _, err := anon.do(ctx, http.MethodGet, c.base.String(), c.feedAccept(), "", nil)
	if err == nil {
		resp.Body.Close()
		return nil, nil
	}
	var httpErr *Error
	if !errors.As(err, &httpErr) || !errors.Is(err, ErrUnauthorized) {
		return nil, err
	}
	if httpErr.Auth != nil {
		return httpErr.Auth, nil
	}
	// The challenge did not carry the document; OPDS also advertises it with a
	// Link header, which is the other place a catalog is allowed to put it.
	href := httpErr.authLink
	if href == "" {
		return nil, err
	}
	return anon.AuthDocument(ctx, href)
}

// AuthDocumentURL returns the Authentication Document a feed advertises,
// either through a feed-level link or through the authenticate hint on any
// link in it. It reports "" when the feed advertises none.
func AuthDocumentURL(f *opds.Feed) string {
	for _, l := range f.Links {
		if l.Rel == opds.RelAuthDocument {
			return l.Href
		}
		if l.Authenticate != nil && l.Authenticate.Href != "" {
			return l.Authenticate.Href
		}
	}
	for _, p := range f.Publications {
		for _, l := range p.Links {
			if l.Authenticate != nil && l.Authenticate.Href != "" {
				return l.Authenticate.Href
			}
		}
	}
	return ""
}

func resolveAuthDocument(d *opds.AuthDocument, base *url.URL) {
	resolveLinks(d.Links, base)
	for i := range d.Authentication {
		resolveLinks(d.Authentication[i].Links, base)
	}
}

// parseLinkHeader returns the href of the first link in an RFC 8288 Link
// header carrying the given relation.
func parseLinkHeader(header, rel string) string {
	for field := range strings.SplitSeq(header, ",") {
		parts := strings.Split(field, ";")
		href := strings.TrimSpace(parts[0])
		href = strings.TrimPrefix(href, "<")
		href = strings.TrimSuffix(href, ">")
		for _, param := range parts[1:] {
			k, v, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || strings.TrimSpace(k) != "rel" {
				continue
			}
			if strings.Trim(strings.TrimSpace(v), `"`) == rel {
				return href
			}
		}
	}
	return ""
}
