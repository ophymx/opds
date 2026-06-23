package opds2

import "encoding/json"

// JSON document shapes for OPDS 2.0. Field order is cosmetic (JSON is
// unordered); omitempty keeps optional members out of the output. Counts that
// are meaningfully zero (copies.available) deliberately omit omitempty.

type jsonFeed struct {
	Metadata     jsonMetadata      `json:"metadata"`
	Links        []jsonLink        `json:"links"`
	Navigation   []jsonLink        `json:"navigation,omitempty"`
	Publications []jsonPublication `json:"publications,omitempty"`
	Groups       []jsonGroup       `json:"groups,omitempty"`
	Facets       []jsonFacetGroup  `json:"facets,omitempty"`
}

type jsonMetadata struct {
	Type          string `json:"@type,omitempty"`
	Title         string `json:"title"`
	Subtitle      string `json:"subtitle,omitempty"`
	Identifier    string `json:"identifier,omitempty"`
	Modified      string `json:"modified,omitempty"`
	NumberOfItems int    `json:"numberOfItems,omitempty"`
	ItemsPerPage  int    `json:"itemsPerPage,omitempty"`
	CurrentPage   int    `json:"currentPage,omitempty"`
}

type jsonGroup struct {
	Metadata     jsonMetadata      `json:"metadata"`
	Links        []jsonLink        `json:"links,omitempty"`
	Navigation   []jsonLink        `json:"navigation,omitempty"`
	Publications []jsonPublication `json:"publications,omitempty"`
}

type jsonFacetGroup struct {
	Metadata jsonMetadata `json:"metadata"`
	Links    []jsonLink   `json:"links"`
}

type jsonLink struct {
	Rel        any             `json:"rel,omitempty"` // string or []string
	Href       string          `json:"href"`
	Type       string          `json:"type,omitempty"`
	Title      string          `json:"title,omitempty"`
	Templated  bool            `json:"templated,omitempty"`
	Height     int             `json:"height,omitempty"`
	Width      int             `json:"width,omitempty"`
	Properties *jsonProperties `json:"properties,omitempty"`
}

type jsonProperties struct {
	NumberOfItems int               `json:"numberOfItems,omitempty"`
	Price         *jsonPrice        `json:"price,omitempty"`
	Indirect      []jsonIndirect    `json:"indirectAcquisition,omitempty"`
	Availability  *jsonAvailability `json:"availability,omitempty"`
	Holds         *jsonHolds        `json:"holds,omitempty"`
	Copies        *jsonCopies       `json:"copies,omitempty"`
}

func (p *jsonProperties) empty() bool {
	return p.NumberOfItems == 0 && p.Price == nil && len(p.Indirect) == 0 &&
		p.Availability == nil && p.Holds == nil && p.Copies == nil
}

type jsonPrice struct {
	Currency string  `json:"currency"`
	Value    float64 `json:"value"`
}

type jsonIndirect struct {
	Type  string         `json:"type"`
	Child []jsonIndirect `json:"child,omitempty"`
}

type jsonAvailability struct {
	State string `json:"state"`
	Since string `json:"since,omitempty"`
	Until string `json:"until,omitempty"`
}

type jsonHolds struct {
	Total    int  `json:"total"`
	Position *int `json:"position,omitempty"`
}

type jsonCopies struct {
	Total     int `json:"total"`
	Available int `json:"available"`
}

type jsonPublication struct {
	Metadata jsonPubMetadata `json:"metadata"`
	Links    []jsonLink      `json:"links"`
	Images   []jsonLink      `json:"images,omitempty"`
}

type jsonPubMetadata struct {
	Type        string        `json:"@type,omitempty"`
	Title       string        `json:"title"`
	SortAs      string        `json:"sortAs,omitempty"`
	Identifier  string        `json:"identifier,omitempty"`
	Author      []contributor `json:"author,omitempty"`
	Contributor []contributor `json:"contributor,omitempty"`
	Publisher   string        `json:"publisher,omitempty"`
	Language    []string      `json:"language,omitempty"`
	Subject     []subject     `json:"subject,omitempty"`
	Modified    string        `json:"modified,omitempty"`
	Published   string        `json:"published,omitempty"`
	Description string        `json:"description,omitempty"`
	BelongsTo   *belongsTo    `json:"belongsTo,omitempty"`
}

type belongsTo struct {
	Series *series `json:"series,omitempty"`
}

type series struct {
	Name     string  `json:"name"`
	Position float64 `json:"position,omitempty"`
}

// contributor marshals as a bare string when only Name is set, otherwise as an
// object — both are valid per the Readium/OPDS 2.0 contributor model.
type contributor struct {
	Name string
	URI  string
	Sort string
}

func (c contributor) MarshalJSON() ([]byte, error) {
	if c.URI == "" && c.Sort == "" {
		return json.Marshal(c.Name)
	}
	obj := struct {
		Name   string     `json:"name"`
		SortAs string     `json:"sortAs,omitempty"`
		Links  []jsonLink `json:"links,omitempty"`
	}{Name: c.Name, SortAs: c.Sort}
	if c.URI != "" {
		obj.Links = []jsonLink{{Href: c.URI}}
	}
	return json.Marshal(obj)
}

// subject marshals as a bare string when only Name is set, otherwise an object.
type subject struct {
	Name   string
	Code   string
	Scheme string
}

func (s subject) MarshalJSON() ([]byte, error) {
	if s.Code == "" && s.Scheme == "" {
		return json.Marshal(s.Name)
	}
	return json.Marshal(struct {
		Name   string `json:"name"`
		Code   string `json:"code,omitempty"`
		Scheme string `json:"scheme,omitempty"`
	}{s.Name, s.Code, s.Scheme})
}
