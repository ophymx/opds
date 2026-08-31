package opds1

import "encoding/xml"

// XML shapes for decoding. They are separate from the marshalling shapes in
// xml.go because encoding/xml is asymmetric about namespaces: the encoder
// emits prefixed names as literal attributes on the root ("opds:price"), while
// the decoder resolves prefixes to namespace URIs. These tags deliberately
// carry no namespace, which makes encoding/xml match on the local name alone —
// tolerating the prefix a catalog happens to use, and the dc/dcterms variants
// deployed feeds mix, without any of the names colliding across the OPDS
// vocabulary.

type xFeed struct {
	XMLName      xml.Name  `xml:"feed"`
	ID           string    `xml:"id"`
	Title        string    `xml:"title"`
	Subtitle     string    `xml:"subtitle"`
	Updated      string    `xml:"updated"`
	Icon         string    `xml:"icon"`
	Authors      []xAuthor `xml:"author"`
	Links        []xLink   `xml:"link"`
	TotalResults int       `xml:"totalResults"`
	ItemsPerPage int       `xml:"itemsPerPage"`
	StartIndex   int       `xml:"startIndex"`
	Entries      []xEntry  `xml:"entry"`
}

type xEntry struct {
	XMLName      xml.Name    `xml:"entry"`
	ID           string      `xml:"id"`
	Title        string      `xml:"title"`
	Updated      string      `xml:"updated"`
	Authors      []xAuthor   `xml:"author"`
	Contributors []xAuthor   `xml:"contributor"`
	Languages    []string    `xml:"language"`
	Issued       string      `xml:"issued"`
	Publisher    string      `xml:"publisher"`
	Identifiers  []string    `xml:"identifier"`
	Categories   []xCategory `xml:"category"`
	Summary      *xContent   `xml:"summary"`
	Content      *xContent   `xml:"content"`
	Rights       string      `xml:"rights"`
	Links        []xLink     `xml:"link"`
}

type xAuthor struct {
	Name string `xml:"name"`
	URI  string `xml:"uri"`
}

type xCategory struct {
	Term   string `xml:"term,attr"`
	Scheme string `xml:"scheme,attr"`
	Label  string `xml:"label,attr"`
}

type xContent struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

// xLink collects every attribute rather than naming them, because the OPDS
// extensions put attributes in namespaces (opds:facetGroup, thr:count,
// pse:count) and Go's handling of prefixed attributes is the least predictable
// part of encoding/xml. Reading them by local name is both simpler and more
// tolerant; no two OPDS link attributes share a local name on the same link.
type xLink struct {
	Attrs        []xml.Attr     `xml:",any,attr"`
	Prices       []xPrice       `xml:"price"`
	Indirect     []xIndirect    `xml:"indirectAcquisition"`
	Availability *xAvailability `xml:"availability"`
	Holds        *xHolds        `xml:"holds"`
	Copies       *xCopies       `xml:"copies"`
}

type xPrice struct {
	Currency string `xml:"currencycode,attr"`
	Value    string `xml:",chardata"`
}

type xIndirect struct {
	Type  string      `xml:"type,attr"`
	Child []xIndirect `xml:"indirectAcquisition"`
}

type xAvailability struct {
	Status string `xml:"status,attr"`
	Since  string `xml:"since,attr"`
	Until  string `xml:"until,attr"`
}

type xHolds struct {
	Total    *int `xml:"total,attr"`
	Position *int `xml:"position,attr"`
}

type xCopies struct {
	Total     *int `xml:"total,attr"`
	Available *int `xml:"available,attr"`
}
