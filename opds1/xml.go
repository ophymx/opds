package opds1

import "encoding/xml"

// atomFeed is the XML representation of an OPDS 1.2 feed. Namespace prefixes are
// declared as literal attributes on the root and referenced by literal,
// colon-prefixed element/attribute names elsewhere (the standard approach for
// mixed-namespace documents with encoding/xml).
type atomFeed struct {
	XMLName      xml.Name `xml:"feed"`
	Xmlns        string   `xml:"xmlns,attr"`
	XmlnsOPDS    string   `xml:"xmlns:opds,attr"`
	XmlnsDCTerms string   `xml:"xmlns:dcterms,attr"`
	XmlnsOS      string   `xml:"xmlns:opensearch,attr"`
	XmlnsThr     string   `xml:"xmlns:thr,attr"`
	XmlnsPSE     string   `xml:"xmlns:pse,attr,omitempty"`

	ID       string       `xml:"id"`
	Title    string       `xml:"title"`
	Subtitle string       `xml:"subtitle,omitempty"`
	Updated  string       `xml:"updated"`
	Icon     string       `xml:"icon,omitempty"`
	Authors  []atomAuthor `xml:"author"`
	Links    []atomLink   `xml:"link"`

	TotalResults *int `xml:"opensearch:totalResults,omitempty"`
	ItemsPerPage *int `xml:"opensearch:itemsPerPage,omitempty"`
	StartIndex   *int `xml:"opensearch:startIndex,omitempty"`

	Entries []atomEntry `xml:"entry"`
}

// entryDoc is a standalone OPDS entry document.
type entryDoc struct {
	XMLName      xml.Name `xml:"entry"`
	Xmlns        string   `xml:"xmlns,attr"`
	XmlnsOPDS    string   `xml:"xmlns:opds,attr"`
	XmlnsDCTerms string   `xml:"xmlns:dcterms,attr"`
	XmlnsPSE     string   `xml:"xmlns:pse,attr,omitempty"`
	atomEntry
}

type atomEntry struct {
	ID           string         `xml:"id"`
	Title        string         `xml:"title"`
	Updated      string         `xml:"updated"`
	Authors      []atomAuthor   `xml:"author"`
	Contributors []atomAuthor   `xml:"contributor"`
	Languages    []string       `xml:"dcterms:language,omitempty"`
	Issued       string         `xml:"dcterms:issued,omitempty"`
	Publisher    string         `xml:"dcterms:publisher,omitempty"`
	Identifiers  []string       `xml:"dcterms:identifier,omitempty"`
	Categories   []atomCategory `xml:"category,omitempty"`
	Summary      *atomContent   `xml:"summary,omitempty"`
	Content      *atomContent   `xml:"content,omitempty"`
	Rights       string         `xml:"rights,omitempty"`
	Links        []atomLink     `xml:"link"`
}

type atomAuthor struct {
	Name string `xml:"name"`
	URI  string `xml:"uri,omitempty"`
}

type atomCategory struct {
	Term   string `xml:"term,attr"`
	Scheme string `xml:"scheme,attr,omitempty"`
	Label  string `xml:"label,attr,omitempty"`
}

type atomContent struct {
	Type string `xml:"type,attr,omitempty"`
	Body string `xml:",chardata"`
}

// atomLink is an Atom link extended with the OPDS acquisition and facet child
// elements/attributes.
type atomLink struct {
	XMLName     xml.Name `xml:"link"`
	Rel         string   `xml:"rel,attr,omitempty"`
	Href        string   `xml:"href,attr"`
	Type        string   `xml:"type,attr,omitempty"`
	Title       string   `xml:"title,attr,omitempty"`
	FacetGroup  string   `xml:"opds:facetGroup,attr,omitempty"`
	ActiveFacet string   `xml:"opds:activeFacet,attr,omitempty"`
	Count       *int     `xml:"thr:count,attr,omitempty"`

	// OPDS-PSE stream link attributes. PSECount is a pointer so the required
	// pse:count is emitted even when zero, but omitted from non-PSE links.
	PSECount        *int   `xml:"pse:count,attr,omitempty"`
	PSELastRead     *int   `xml:"pse:lastRead,attr,omitempty"`
	PSELastReadDate string `xml:"pse:lastReadDate,attr,omitempty"`

	Prices       []atomPrice       `xml:"opds:price,omitempty"`
	Indirect     []atomIndirect    `xml:"opds:indirectAcquisition,omitempty"`
	Availability *atomAvailability `xml:"opds:availability,omitempty"`
	Holds        *atomHolds        `xml:"opds:holds,omitempty"`
	Copies       *atomCopies       `xml:"opds:copies,omitempty"`
}

type atomPrice struct {
	Currency string `xml:"currencycode,attr"`
	Value    string `xml:",chardata"`
}

type atomIndirect struct {
	XMLName xml.Name       `xml:"opds:indirectAcquisition"`
	Type    string         `xml:"type,attr"`
	Child   []atomIndirect `xml:"opds:indirectAcquisition,omitempty"`
}

type atomAvailability struct {
	Status string `xml:"status,attr"`
	Since  string `xml:"since,attr,omitempty"`
	Until  string `xml:"until,attr,omitempty"`
}

type atomHolds struct {
	Total    *int `xml:"total,attr,omitempty"`
	Position *int `xml:"position,attr,omitempty"`
}

type atomCopies struct {
	Total     *int `xml:"total,attr,omitempty"`
	Available *int `xml:"available,attr,omitempty"`
}
