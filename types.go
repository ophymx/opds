package opds

import "time"

// Feed is a version-neutral OPDS feed (an Atom feed in 1.2, a collection in
// 2.0). A feed is either a navigation feed (Navigation populated) or an
// acquisition feed (Publications populated); both may be present but most
// clients expect one or the other.
type Feed struct {
	// ID is a stable, unique identifier for the feed (e.g. a URN or URL).
	// Maps to atom:id in 1.2; used as metadata.identifier in 2.0.
	ID string
	// Title is the human-readable feed title. Required by both versions.
	Title string
	// Subtitle is an optional secondary title (atom:subtitle).
	Subtitle string
	// Updated is the last time the feed changed. Defaults to time.Now when zero.
	Updated time.Time
	// Icon is an optional URL to a feed icon (atom:icon).
	Icon string
	// Authors describe who is responsible for the feed.
	Authors []Author
	// Links are feed-level links (self, start, up, next, search, ...).
	Links []Link

	// Navigation holds the entries of a navigation feed.
	Navigation []NavEntry
	// Publications holds the entries of an acquisition feed.
	Publications []Publication
	// Groups partition an acquisition feed into labelled sections (2.0 groups;
	// emitted via opds:group links in 1.2).
	Groups []Group
	// Facets describe alternate filtered/sorted views of the same feed.
	Facets []Facet

	// Pagination. Zero values are treated as "unset" and omitted.
	TotalResults int // total number of items across all pages
	ItemsPerPage int // number of items per page
	StartIndex   int // 1-based index of the first item on this page (1.x)
	CurrentPage  int // 1-based page number (2.0)
}

// NavEntry is an entry in a navigation feed: a link to another feed or
// resource, with a title and optional description.
type NavEntry struct {
	// ID is a stable identifier. Synthesized from Href for 1.2 if empty.
	ID string
	// Title is the entry label. Required.
	Title string
	// Updated is the entry's last-modified time. Defaults to the feed's when zero.
	Updated time.Time
	// Content is an optional description of the target.
	Content string
	// Href is the URL of the target feed or resource. Required.
	Href string
	// Type is the media type of the target (e.g. MediaTypeAcquisition).
	Type string
	// Rel is an optional relation for the target link (e.g. RelSortNew,
	// RelSubsection, RelFeatured). Defaults to RelSubsection when empty.
	Rel string
	// Images are optional thumbnails/tiles for the entry.
	Images []Image
}

// Publication is a single catalog entry describing a publication and how to
// acquire it.
type Publication struct {
	// ID is a stable, unique identifier (atom:id; metadata.identifier in 2.0).
	ID string
	// Title is the publication title. Required.
	Title string
	// SortAs is an optional collation key for the title (2.0 metadata.sortAs).
	SortAs string
	// Updated is when the entry last changed (atom:updated; metadata.modified).
	Updated time.Time
	// Published is the publication date (dcterms:issued; metadata.published).
	Published time.Time
	// Languages are ISO 639 language codes.
	Languages []string
	// Identifiers are external identifiers such as ISBN URNs (dcterms:identifier).
	Identifiers []string
	// Publisher is the publishing entity.
	Publisher string
	// Authors are the publication's authors.
	Authors []Author
	// Contributors are other contributors (editors, translators, ...).
	Contributors []Author
	// Subjects are categories/genres.
	Subjects []Subject
	// Summary is a short plain-text description (atom:summary).
	Summary string
	// Description is a longer description, may contain HTML (atom:content).
	Description string
	// Rights is a copyright/licensing statement (atom:rights).
	Rights string
	// Series places the publication within a series. OPDS 2.0 only: it renders
	// as belongsTo.series in JSON but is omitted from the 1.2 Atom rendering,
	// which has no standard representation for series membership.
	Series *Series

	// Images are cover images. By convention the first is the primary cover.
	Images []Image
	// Acquisitions are the ways the publication can be acquired. An acquisition
	// feed entry should have at least one.
	Acquisitions []Acquisition
	// Links are additional links (self, alternate to the full entry, related, ...).
	Links []Link
}

// Link is a generic hypermedia link.
type Link struct {
	// Rel is the link relation (see the Rel* constants).
	Rel string
	// Href is the target URL or, when Templated is true, a URI template.
	Href string
	// Type is the media type of the target.
	Type string
	// Title is an optional human-readable label.
	Title string
	// Templated indicates Href is an RFC 6570 URI template (2.0 only).
	Templated bool
}

// Image is a cover image or thumbnail.
type Image struct {
	// Href is the image URL. Required.
	Href string
	// Type is the image media type (e.g. "image/jpeg").
	Type string
	// Width and Height are optional pixel dimensions (2.0).
	Width, Height int
	// Thumbnail marks this as a reduced-size image. In 1.2 it selects the
	// image/thumbnail relation; in 2.0 all images share the images collection.
	Thumbnail bool
}

// Author identifies a person or organization responsible for a feed or
// publication.
type Author struct {
	// Name is the display name. Required.
	Name string
	// URI optionally links to the author (a feed of their works, a homepage).
	URI string
	// SortAs is an optional collation key (2.0).
	SortAs string
}

// Subject is a category, genre or keyword.
type Subject struct {
	// Name is the human-readable label.
	Name string
	// Code is an optional controlled-vocabulary term (e.g. a BISAC code).
	Code string
	// Scheme optionally identifies the vocabulary the Code belongs to.
	Scheme string
}

// Series places a publication within a sequence. It only appears in OPDS 2.0
// output (belongsTo.series); the 1.2 Atom rendering drops it, so a catalog
// wanting series information visible to 1.x clients must fold it into another
// field (e.g. the Title or Summary).
type Series struct {
	// Name is the series title.
	Name string
	// Position is the publication's position in the series (0 if unknown).
	Position float64
}

// Acquisition describes one way to obtain a publication.
type Acquisition struct {
	// Rel is the acquisition relation. Defaults to AcquireGeneric when empty.
	Rel AcquisitionRel
	// Href is the acquisition URL. Required.
	Href string
	// Type is the media type acquired. For indirect acquisition this is the
	// media type of the intermediate resource (e.g. an HTML purchase page).
	Type string
	// Title is an optional label.
	Title string

	// Prices lists the cost(s) of acquisition. Required for AcquireBuy.
	Prices []Price
	// Indirect describes formats obtained after following the link
	// (e.g. an LCP license that yields an EPUB).
	Indirect []IndirectAcquisition
	// Availability describes lending availability (AcquireBorrow).
	Availability *Availability
	// Holds describes the reservation queue (library lending).
	Holds *Holds
	// Copies describes copy counts (library lending).
	Copies *Copies
}

// Price is a monetary amount in a specific currency.
type Price struct {
	// Currency is an ISO 4217 currency code (e.g. "USD").
	Currency string
	// Value is the amount.
	Value float64
}

// IndirectAcquisition declares a media type obtainable after following an
// acquisition link. Entries may nest to express multiple levels of indirection.
type IndirectAcquisition struct {
	// Type is the media type that will ultimately be acquired.
	Type string
	// Child holds further levels of indirection.
	Child []IndirectAcquisition
}

// Availability describes whether a borrowable publication can currently be
// obtained.
type Availability struct {
	// State is one of StateAvailable, StateUnavailable, StateReserved, StateReady.
	State string
	// Since is when the current state began (e.g. loan start). Optional.
	Since time.Time
	// Until is when the current state ends (e.g. loan or hold expiry). Optional.
	Until time.Time
}

// Holds describes a reservation queue for a borrowable publication.
type Holds struct {
	// Total is the number of holds placed.
	Total int
	// Position is the requesting user's position in the queue, if known.
	Position *int
}

// Copies describes copy counts for a borrowable publication.
type Copies struct {
	// Total is the number of copies owned.
	Total int
	// Available is the number of copies currently available to borrow.
	Available int
}

// Facet is an alternate filtered or sorted view of a feed, grouped with other
// facets under a common Group label.
type Facet struct {
	// Group names the facet group this facet belongs to (e.g. "Language").
	Group string
	// Title is the facet label (e.g. "French"). Required.
	Title string
	// Href is the URL of the filtered/sorted feed. Required.
	Href string
	// Type is the media type of the target feed.
	Type string
	// Count is an optional hint at the number of items behind the facet.
	Count int
	// Active marks the facet as the one currently applied.
	Active bool
}

// Group is a labelled section of a feed (2.0 groups). A group typically links
// to a fuller feed via Href and shows a preview of its contents.
type Group struct {
	// Title is the section label.
	Title string
	// Href is an optional link to the full feed for this group.
	Href string
	// Type is the media type of the group's full feed.
	Type string
	// Rel is an optional relation for the group's link.
	Rel string
	// Navigation holds the group's navigation entries.
	Navigation []NavEntry
	// Publications holds the group's publications.
	Publications []Publication
}

// IsAcquisition reports whether the feed is an acquisition feed (contains
// publications) as opposed to a navigation feed. It is used to select the
// correct OPDS 1.x media type.
func (f *Feed) IsAcquisition() bool {
	if len(f.Publications) > 0 {
		return true
	}
	for _, g := range f.Groups {
		if len(g.Publications) > 0 {
			return true
		}
	}
	return false
}

// when returns t if set, otherwise the provided fallback.
func when(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}
