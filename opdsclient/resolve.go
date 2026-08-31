package opdsclient

import (
	"net/url"
	"strings"

	"github.com/ophymx/opds"
)

// Every href a decoded document carries is rewritten to an absolute URL
// against the document's own URL. A catalog is free to emit relative hrefs —
// most do — and a caller that has to remember which base each one belongs to
// gets it wrong as soon as a feed links to another feed one directory up. The
// client resolves once, at the point where the document's URL is still known,
// and hands back a feed whose links can be followed from anywhere.

func resolveFeed(f *opds.Feed, base *url.URL) {
	f.Icon = resolveHref(base, f.Icon)
	resolveAuthors(f.Authors, base)
	resolveLinks(f.Links, base)
	resolveNavs(f.Navigation, base)
	for i := range f.Publications {
		resolvePublication(&f.Publications[i], base)
	}
	for i := range f.Groups {
		g := &f.Groups[i]
		g.Href = resolveHref(base, g.Href)
		resolveNavs(g.Navigation, base)
		for j := range g.Publications {
			resolvePublication(&g.Publications[j], base)
		}
	}
	for i := range f.Facets {
		f.Facets[i].Href = resolveHref(base, f.Facets[i].Href)
	}
}

func resolvePublication(p *opds.Publication, base *url.URL) {
	resolveAuthors(p.Authors, base)
	resolveAuthors(p.Contributors, base)
	resolveLinks(p.Links, base)
	for i := range p.Images {
		p.Images[i].Href = resolveHref(base, p.Images[i].Href)
	}
	for i := range p.Acquisitions {
		p.Acquisitions[i].Href = resolveHref(base, p.Acquisitions[i].Href)
	}
	if p.PageStream != nil {
		p.PageStream.Href = resolveHref(base, p.PageStream.Href)
	}
}

func resolveNavs(navs []opds.NavEntry, base *url.URL) {
	for i := range navs {
		navs[i].Href = resolveHref(base, navs[i].Href)
		for j := range navs[i].Images {
			navs[i].Images[j].Href = resolveHref(base, navs[i].Images[j].Href)
		}
	}
}

func resolveLinks(links []opds.Link, base *url.URL) {
	for i := range links {
		links[i].Href = resolveHref(base, links[i].Href)
		if a := links[i].Authenticate; a != nil {
			a.Href = resolveHref(base, a.Href)
		}
	}
}

func resolveAuthors(authors []opds.Author, base *url.URL) {
	for i := range authors {
		authors[i].URI = resolveHref(base, authors[i].URI)
	}
}

// resolveHref makes one href absolute. A URI template — an OPDS 2.0 search
// link, an OPDS-PSE stream href — is resolved only up to its first
// placeholder: url.URL would percent-escape the braces and destroy the
// template. An href that does not parse is left as it is, since a caller can
// still show it, and a bad link is not a reason to fail a whole feed.
func resolveHref(base *url.URL, href string) string {
	if href == "" || base == nil {
		return href
	}
	prefix, suffix := href, ""
	if i := strings.IndexByte(href, '{'); i >= 0 {
		prefix, suffix = href[:i], href[i:]
	}
	ref, err := url.Parse(prefix)
	if err != nil {
		return href
	}
	return base.ResolveReference(ref).String() + suffix
}
