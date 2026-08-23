package opds

// Media types used by OPDS catalogs.
const (
	// MediaTypeNavigation is the media type of an OPDS 1.x navigation feed.
	MediaTypeNavigation = "application/atom+xml;profile=opds-catalog;kind=navigation"
	// MediaTypeAcquisition is the media type of an OPDS 1.x acquisition feed.
	MediaTypeAcquisition = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	// MediaTypeEntry is the media type of a standalone OPDS 1.x entry document.
	MediaTypeEntry = "application/atom+xml;type=entry;profile=opds-catalog"

	// MediaTypeFeed is the media type of an OPDS 2.0 feed.
	MediaTypeFeed = "application/opds+json"
	// MediaTypePublication is the media type of an OPDS 2.0 publication.
	MediaTypePublication = "application/opds-publication+json"

	// MediaTypeOpenSearch is the media type of an OpenSearch description document.
	MediaTypeOpenSearch = "application/opensearchdescription+xml"

	// MediaTypeAuthDocument is the media type of an OPDS Authentication Document
	// (see https://drafts.opds.io/authentication-for-opds-1.0.html).
	MediaTypeAuthDocument = "application/opds-authentication+json"
)

// XML namespace URIs used by OPDS 1.x (Atom) feeds.
const (
	NSAtom       = "http://www.w3.org/2005/Atom"
	NSOPDS       = "http://opds-spec.org/2010/catalog"
	NSDCTerms    = "http://purl.org/dc/terms/"
	NSOpenSearch = "http://a9.com/-/spec/opensearch/1.1/"
	NSThreading  = "http://purl.org/syndication/thread/1.0"
	// NSPSE is the OPDS Page Streaming Extension namespace (see PageStream).
	NSPSE = "http://vaemendis.net/opds-pse/ns"
)

// Standard (RFC 5988 / Atom) and OPDS-specific link relations.
//
// The structural relations (RelSelf, RelStart, ...) are bare tokens shared by
// both OPDS versions. The relations carrying the "http://opds-spec.org/"
// prefix are OPDS-specific and identical across versions 1.2 and 2.0.
const (
	RelSelf       = "self"
	RelStart      = "start"
	RelUp         = "up"
	RelNext       = "next"
	RelPrevious   = "previous"
	RelFirst      = "first"
	RelLast       = "last"
	RelSearch     = "search"
	RelAlternate  = "alternate"
	RelRelated    = "related"
	RelSubsection = "subsection"
	RelCollection = "collection"

	RelImage     = "http://opds-spec.org/image"
	RelThumbnail = "http://opds-spec.org/image/thumbnail"

	// RelPageStream is the OPDS-PSE page streaming relation (see PageStream).
	RelPageStream = "http://vaemendis.net/opds-pse/stream"

	// RelAuthDocument advertises the catalog's OPDS Authentication Document
	// (see opdshttp.WithAuth).
	RelAuthDocument = "http://opds-spec.org/auth/document"

	RelFacet         = "http://opds-spec.org/facet"
	RelGroup         = "http://opds-spec.org/group"
	RelSortNew       = "http://opds-spec.org/sort/new"
	RelSortPopular   = "http://opds-spec.org/sort/popular"
	RelFeatured      = "http://opds-spec.org/featured"
	RelRecommended   = "http://opds-spec.org/recommended"
	RelShelf         = "http://opds-spec.org/shelf"
	RelSubscriptions = "http://opds-spec.org/subscriptions"
	RelCrawlable     = "http://opds-spec.org/crawlable"
)

// Authentication flow type URIs used in an OPDS Authentication Document
// (see https://drafts.opds.io/authentication-for-opds-1.0.html).
const (
	// AuthFlowBasic is the HTTP Basic Authentication flow.
	AuthFlowBasic = "http://opds-spec.org/auth/basic"
)

// AcquisitionRel identifies how a publication may be acquired. The values are
// the full relation URIs used (identically) in OPDS 1.2 and 2.0.
type AcquisitionRel string

const (
	// AcquireGeneric is a generic acquisition relation; the acquisition method
	// is unspecified.
	AcquireGeneric AcquisitionRel = "http://opds-spec.org/acquisition"
	// AcquireOpenAccess is freely accessible without payment or authentication.
	AcquireOpenAccess AcquisitionRel = "http://opds-spec.org/acquisition/open-access"
	// AcquireBuy must be purchased; carries at least one Price.
	AcquireBuy AcquisitionRel = "http://opds-spec.org/acquisition/buy"
	// AcquireBorrow is borrowed for a limited period (library lending).
	AcquireBorrow AcquisitionRel = "http://opds-spec.org/acquisition/borrow"
	// AcquireSample provides a sample or preview of the publication.
	AcquireSample AcquisitionRel = "http://opds-spec.org/acquisition/sample"
	// AcquireSubscribe is acquired through a subscription.
	AcquireSubscribe AcquisitionRel = "http://opds-spec.org/acquisition/subscribe"
)

// Availability states for library lending (used by AcquireBorrow links).
const (
	// StateAvailable means the publication can be borrowed immediately.
	StateAvailable = "available"
	// StateUnavailable means all copies are loaned out.
	StateUnavailable = "unavailable"
	// StateReserved means the user holds a reservation in the queue.
	StateReserved = "reserved"
	// StateReady means a hold is ready to be borrowed by the user.
	StateReady = "ready"
)

// Version identifies an OPDS wire format version.
type Version int

const (
	// Version1 is OPDS 1.2 (Atom XML).
	Version1 Version = iota
	// Version2 is OPDS 2.0 (JSON).
	Version2
)

func (v Version) String() string {
	switch v {
	case Version2:
		return "2.0"
	default:
		return "1.2"
	}
}
