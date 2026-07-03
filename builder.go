package opds

import (
	"net/url"
	"strconv"
	"time"
)

// This file provides fluent builders that make constructing feeds and
// publications concise. They mutate and return the receiver, so calls chain:
//
//	f := opds.NewFeed("urn:feed:root", "My Library").
//		Self("/opds", MediaTypeNavigation).
//		Start("/opds").
//		AddNav("New", "/opds/new", MediaTypeAcquisition, RelSortNew)

// NewFeed returns a feed with the given id and title and Updated set to now.
func NewFeed(id, title string) *Feed {
	return &Feed{ID: id, Title: title, Updated: time.Now()}
}

// At sets the feed's Updated timestamp.
func (f *Feed) At(t time.Time) *Feed {
	f.Updated = t
	return f
}

// SubtitledBy sets the feed subtitle.
func (f *Feed) SubtitledBy(s string) *Feed {
	f.Subtitle = s
	return f
}

// By adds an author to the feed.
func (f *Feed) By(name string) *Feed {
	f.Authors = append(f.Authors, Author{Name: name})
	return f
}

// Link adds a feed-level link.
func (f *Feed) Link(rel, href, mediaType string) *Feed {
	f.Links = append(f.Links, Link{Rel: rel, Href: href, Type: mediaType})
	return f
}

// Self adds a self link.
func (f *Feed) Self(href, mediaType string) *Feed { return f.Link(RelSelf, href, mediaType) }

// Start adds a start (catalog root) link.
func (f *Feed) Start(href string) *Feed { return f.Link(RelStart, href, MediaTypeNavigation) }

// Up adds an up (parent) link.
func (f *Feed) Up(href, mediaType string) *Feed { return f.Link(RelUp, href, mediaType) }

// SearchLink adds a search link. For 1.x mediaType should be
// MediaTypeOpenSearch; for 2.0 use MediaTypeFeed with a templated href.
func (f *Feed) SearchLink(href, mediaType string, templated bool) *Feed {
	f.Links = append(f.Links, Link{Rel: RelSearch, Href: href, Type: mediaType, Templated: templated})
	return f
}

// Page sets pagination links and counters. self/next/prev hrefs that are empty
// are skipped. total and perPage are recorded as counters; start is the 1-based
// index of the first item on this page.
func (f *Feed) Page(total, perPage, start int) *Feed {
	f.TotalResults = total
	f.ItemsPerPage = perPage
	f.StartIndex = start
	if perPage > 0 {
		f.CurrentPage = (start-1)/perPage + 1
	}
	return f
}

// Prev adds a previous-page link.
func (f *Feed) Prev(href, mediaType string) *Feed { return f.Link(RelPrevious, href, mediaType) }

// Next adds a next-page link.
func (f *Feed) Next(href, mediaType string) *Feed { return f.Link(RelNext, href, mediaType) }

// Paged records the current page and adds previous/next pagination links
// derived from baseHref, the feed's unpaged href (a query string is allowed
// and preserved, e.g. a search href carrying its terms). A previous link is
// added when page > 1 and a next link when hasNext; hrefs are built with
// PageHref, so page 1 is baseHref itself. If ItemsPerPage is already set (see
// Page), StartIndex is derived when unset. The pagination links carry no media
// type; feeds served through opdshttp get it filled with the feed's own type.
func (f *Feed) Paged(baseHref string, page int, hasNext bool) *Feed {
	if page < 1 {
		page = 1
	}
	f.CurrentPage = page
	if f.ItemsPerPage > 0 && f.StartIndex == 0 {
		f.StartIndex = (page-1)*f.ItemsPerPage + 1
	}
	if page > 1 {
		f.Links = append(f.Links, Link{Rel: RelPrevious, Href: PageHref(baseHref, page-1)})
	}
	if hasNext {
		f.Links = append(f.Links, Link{Rel: RelNext, Href: PageHref(baseHref, page+1)})
	}
	return f
}

// PageHref returns href with its "page" query parameter set to page,
// preserving any other query parameters. For page 1 (or lower) the parameter
// is removed instead, so the first page and the unpaged href are the same URL.
// An unparseable href is returned unchanged.
func PageHref(href string, page int) string {
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	q := u.Query()
	if page <= 1 {
		q.Del("page")
	} else {
		q.Set("page", strconv.Itoa(page))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// AddNav appends a navigation entry. rel may be empty (defaults to subsection).
func (f *Feed) AddNav(title, href, mediaType, rel string) *Feed {
	f.Navigation = append(f.Navigation, NavEntry{
		Title: title, Href: href, Type: mediaType, Rel: rel,
	})
	return f
}

// AddNavEntry appends a fully specified navigation entry.
func (f *Feed) AddNavEntry(e NavEntry) *Feed {
	f.Navigation = append(f.Navigation, e)
	return f
}

// Add appends one or more publications to the feed.
func (f *Feed) Add(pubs ...Publication) *Feed {
	f.Publications = append(f.Publications, pubs...)
	return f
}

// AddFacet appends a facet to the feed.
func (f *Feed) AddFacet(group, title, href, mediaType string, count int, active bool) *Feed {
	f.Facets = append(f.Facets, Facet{
		Group: group, Title: title, Href: href, Type: mediaType, Count: count, Active: active,
	})
	return f
}

// AddGroup appends a group to the feed.
func (f *Feed) AddGroup(g Group) *Feed {
	f.Groups = append(f.Groups, g)
	return f
}

// NewPublication returns a publication with the given id and title and Updated
// set to now.
func NewPublication(id, title string) *Publication {
	return &Publication{ID: id, Title: title, Updated: time.Now()}
}

// By adds an author.
func (p *Publication) By(name string) *Publication {
	p.Authors = append(p.Authors, Author{Name: name})
	return p
}

// Author adds a fully specified author.
func (p *Publication) Author(a Author) *Publication {
	p.Authors = append(p.Authors, a)
	return p
}

// In sets one or more languages.
func (p *Publication) In(langs ...string) *Publication {
	p.Languages = append(p.Languages, langs...)
	return p
}

// PublishedAt sets the publication date.
func (p *Publication) PublishedAt(t time.Time) *Publication {
	p.Published = t
	return p
}

// UpdatedAt sets the entry's last-modified time.
func (p *Publication) UpdatedAt(t time.Time) *Publication {
	p.Updated = t
	return p
}

// From sets the publisher.
func (p *Publication) From(publisher string) *Publication {
	p.Publisher = publisher
	return p
}

// ISBN adds an ISBN identifier as a URN.
func (p *Publication) ISBN(isbn string) *Publication {
	p.Identifiers = append(p.Identifiers, "urn:isbn:"+isbn)
	return p
}

// Identifier adds a raw identifier.
func (p *Publication) Identifier(id string) *Publication {
	p.Identifiers = append(p.Identifiers, id)
	return p
}

// About adds a subject/category.
func (p *Publication) About(name string) *Publication {
	p.Subjects = append(p.Subjects, Subject{Name: name})
	return p
}

// Categorize adds a subject with a controlled-vocabulary code and scheme.
func (p *Publication) Categorize(name, code, scheme string) *Publication {
	p.Subjects = append(p.Subjects, Subject{Name: name, Code: code, Scheme: scheme})
	return p
}

// Summarize sets the short summary.
func (p *Publication) Summarize(s string) *Publication {
	p.Summary = s
	return p
}

// Describe sets the long description.
func (p *Publication) Describe(s string) *Publication {
	p.Description = s
	return p
}

// PartOf places the publication in a series at the given position.
func (p *Publication) PartOf(series string, position float64) *Publication {
	p.Series = &Series{Name: series, Position: position}
	return p
}

// Cover adds a primary cover image.
func (p *Publication) Cover(href, mediaType string) *Publication {
	p.Images = append(p.Images, Image{Href: href, Type: mediaType})
	return p
}

// Thumbnail adds a thumbnail image.
func (p *Publication) Thumbnail(href, mediaType string) *Publication {
	p.Images = append(p.Images, Image{Href: href, Type: mediaType, Thumbnail: true})
	return p
}

// Acquire appends a fully specified acquisition.
func (p *Publication) Acquire(a Acquisition) *Publication {
	p.Acquisitions = append(p.Acquisitions, a)
	return p
}

// OpenAccess adds an open-access (free download) acquisition.
func (p *Publication) OpenAccess(href, mediaType string) *Publication {
	return p.Acquire(Acquisition{Rel: AcquireOpenAccess, Href: href, Type: mediaType})
}

// Buy adds a buy acquisition with a single price.
func (p *Publication) Buy(href, mediaType, currency string, value float64) *Publication {
	return p.Acquire(Acquisition{
		Rel: AcquireBuy, Href: href, Type: mediaType,
		Prices: []Price{{Currency: currency, Value: value}},
	})
}

// Borrow adds a borrow acquisition with the given availability state.
func (p *Publication) Borrow(href, mediaType, state string) *Publication {
	return p.Acquire(Acquisition{
		Rel: AcquireBorrow, Href: href, Type: mediaType,
		Availability: &Availability{State: state},
	})
}

// Sample adds a sample/preview acquisition.
func (p *Publication) Sample(href, mediaType string) *Publication {
	return p.Acquire(Acquisition{Rel: AcquireSample, Href: href, Type: mediaType})
}

// Link adds an arbitrary link to the publication.
func (p *Publication) Link(rel, href, mediaType string) *Publication {
	p.Links = append(p.Links, Link{Rel: rel, Href: href, Type: mediaType})
	return p
}
