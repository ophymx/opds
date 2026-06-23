// Package opensearch generates OpenSearch description documents, the mechanism
// OPDS 1.x catalogs use to advertise their search interface.
package opensearch

import (
	"encoding/xml"
	"strings"

	"github.com/ophymx/opds"
)

// Description is the data needed to render an OpenSearch description document.
type Description struct {
	// ShortName is a brief name for the search engine.
	ShortName string
	// Description is a human-readable description of the search.
	Description string
	// Template is the search URL template, e.g.
	// "https://example.com/opds/search?q={searchTerms}". It must contain at
	// least the {searchTerms} parameter.
	Template string
	// ResultType is the media type of search results. Defaults to the OPDS 1.x
	// acquisition feed media type.
	ResultType string
}

// Marshal renders the OpenSearch description document, including the XML
// declaration.
func Marshal(d Description) ([]byte, error) {
	resultType := d.ResultType
	if resultType == "" {
		resultType = opds.MediaTypeAcquisition
	}
	short := d.ShortName
	if short == "" {
		short = "Search"
	}
	doc := osDescription{
		Xmlns:       opds.NSOpenSearch,
		ShortName:   short,
		Description: strings.TrimSpace(d.Description),
		URLs: []osURL{{
			Type:     resultType,
			Template: d.Template,
		}},
	}
	if doc.Description == "" {
		doc.Description = short
	}
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}

type osDescription struct {
	XMLName     xml.Name `xml:"OpenSearchDescription"`
	Xmlns       string   `xml:"xmlns,attr"`
	ShortName   string   `xml:"ShortName"`
	Description string   `xml:"Description"`
	URLs        []osURL  `xml:"Url"`
}

type osURL struct {
	Type     string `xml:"type,attr"`
	Template string `xml:"template,attr"`
}
