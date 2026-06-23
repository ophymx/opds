// Package opds1 encodes the version-neutral opds model to OPDS 1.2 feeds and
// entry documents, serialized as Atom (XML) with the OPDS extension namespaces.
package opds1

import (
	"encoding/xml"
	"io"
	"strconv"
	"time"

	"github.com/ophymx/opds"
)

// Header is the XML declaration emitted before every document.
const Header = xml.Header

// Marshal returns the OPDS 1.2 Atom encoding of the feed, including the XML
// declaration.
func Marshal(f *opds.Feed) ([]byte, error) {
	doc := buildFeed(f)
	return marshal(doc)
}

// Encode writes the OPDS 1.2 Atom encoding of the feed to w.
func Encode(w io.Writer, f *opds.Feed) error {
	b, err := Marshal(f)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// MarshalEntry returns a standalone OPDS 1.2 entry document for a single
// publication (media type application/atom+xml;type=entry;profile=opds-catalog).
func MarshalEntry(p *opds.Publication) ([]byte, error) {
	e := buildEntry(*p, time.Now())
	doc := entryDoc{
		atomEntry:    e,
		Xmlns:        opds.NSAtom,
		XmlnsOPDS:    opds.NSOPDS,
		XmlnsDCTerms: opds.NSDCTerms,
	}
	return marshal(doc)
}

func marshal(doc any) ([]byte, error) {
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(Header), body...), nil
}

func buildFeed(f *opds.Feed) atomFeed {
	updated := f.Updated
	if updated.IsZero() {
		updated = time.Now()
	}
	doc := atomFeed{
		Xmlns:        opds.NSAtom,
		XmlnsOPDS:    opds.NSOPDS,
		XmlnsDCTerms: opds.NSDCTerms,
		XmlnsOS:      opds.NSOpenSearch,
		XmlnsThr:     opds.NSThreading,
		ID:           f.ID,
		Title:        f.Title,
		Subtitle:     f.Subtitle,
		Updated:      formatTime(updated),
		Icon:         f.Icon,
		Authors:      buildAuthors(f.Authors),
	}

	for _, l := range f.Links {
		doc.Links = append(doc.Links, buildLink(l))
	}
	for _, ft := range f.Facets {
		doc.Links = append(doc.Links, buildFacet(ft))
	}

	if f.TotalResults > 0 {
		doc.TotalResults = new(f.TotalResults)
	}
	if f.ItemsPerPage > 0 {
		doc.ItemsPerPage = new(f.ItemsPerPage)
	}
	if f.StartIndex > 0 {
		doc.StartIndex = new(f.StartIndex)
	}

	for _, n := range f.Navigation {
		doc.Entries = append(doc.Entries, buildNav(n, updated))
	}
	for _, p := range f.Publications {
		doc.Entries = append(doc.Entries, buildEntry(p, updated))
	}
	// Groups are not a first-class concept in 1.2; flatten their publications
	// into the feed, tagging each entry with a collection link to the group.
	for _, g := range f.Groups {
		for _, n := range g.Navigation {
			doc.Entries = append(doc.Entries, buildNav(n, updated))
		}
		for _, p := range g.Publications {
			e := buildEntry(p, updated)
			if g.Href != "" {
				e.Links = append(e.Links, atomLink{
					Rel: opds.RelCollection, Href: g.Href, Type: g.Type, Title: g.Title,
				})
			}
			doc.Entries = append(doc.Entries, e)
		}
	}
	return doc
}

func buildNav(n opds.NavEntry, feedUpdated time.Time) atomEntry {
	id := n.ID
	if id == "" {
		id = n.Href
	}
	rel := n.Rel
	if rel == "" {
		rel = opds.RelSubsection
	}
	e := atomEntry{
		ID:      id,
		Title:   n.Title,
		Updated: formatTime(when(n.Updated, feedUpdated)),
	}
	if n.Content != "" {
		e.Content = &atomContent{Type: "text", Body: n.Content}
	}
	e.Links = append(e.Links, atomLink{Rel: rel, Href: n.Href, Type: n.Type})
	for _, img := range n.Images {
		e.Links = append(e.Links, buildImage(img))
	}
	return e
}

func buildEntry(p opds.Publication, feedUpdated time.Time) atomEntry {
	e := atomEntry{
		ID:           p.ID,
		Title:        p.Title,
		Updated:      formatTime(when(p.Updated, feedUpdated)),
		Authors:      buildAuthors(p.Authors),
		Contributors: buildContributors(p.Contributors),
		Languages:    p.Languages,
		Publisher:    p.Publisher,
		Identifiers:  p.Identifiers,
		Rights:       p.Rights,
	}
	if !p.Published.IsZero() {
		e.Issued = formatDate(p.Published)
	}
	for _, s := range p.Subjects {
		term := s.Code
		if term == "" {
			term = s.Name
		}
		e.Categories = append(e.Categories, atomCategory{Term: term, Scheme: s.Scheme, Label: s.Name})
	}
	if p.Summary != "" {
		e.Summary = &atomContent{Type: "text", Body: p.Summary}
	}
	if p.Description != "" {
		e.Content = &atomContent{Type: "html", Body: p.Description}
	}
	for _, l := range p.Links {
		e.Links = append(e.Links, buildLink(l))
	}
	for _, img := range p.Images {
		e.Links = append(e.Links, buildImage(img))
	}
	for _, a := range p.Acquisitions {
		e.Links = append(e.Links, buildAcquisition(a))
	}
	return e
}

func buildImage(img opds.Image) atomLink {
	rel := opds.RelImage
	if img.Thumbnail {
		rel = opds.RelThumbnail
	}
	return atomLink{Rel: rel, Href: img.Href, Type: img.Type}
}

func buildAcquisition(a opds.Acquisition) atomLink {
	rel := string(a.Rel)
	if rel == "" {
		rel = string(opds.AcquireGeneric)
	}
	l := atomLink{Rel: rel, Href: a.Href, Type: a.Type, Title: a.Title}
	for _, pr := range a.Prices {
		l.Prices = append(l.Prices, atomPrice{
			Currency: pr.Currency,
			Value:    strconv.FormatFloat(pr.Value, 'f', -1, 64),
		})
	}
	l.Indirect = buildIndirect(a.Indirect)
	if av := a.Availability; av != nil {
		l.Availability = &atomAvailability{
			Status: av.State,
			Since:  formatTimeOrEmpty(av.Since),
			Until:  formatTimeOrEmpty(av.Until),
		}
	}
	if h := a.Holds; h != nil {
		l.Holds = &atomHolds{Total: new(h.Total), Position: h.Position}
	}
	if c := a.Copies; c != nil {
		l.Copies = &atomCopies{Total: new(c.Total), Available: new(c.Available)}
	}
	return l
}

func buildIndirect(in []opds.IndirectAcquisition) []atomIndirect {
	if len(in) == 0 {
		return nil
	}
	out := make([]atomIndirect, 0, len(in))
	for _, i := range in {
		out = append(out, atomIndirect{Type: i.Type, Child: buildIndirect(i.Child)})
	}
	return out
}

func buildFacet(f opds.Facet) atomLink {
	l := atomLink{
		Rel:        opds.RelFacet,
		Href:       f.Href,
		Type:       f.Type,
		Title:      f.Title,
		FacetGroup: f.Group,
	}
	if f.Active {
		l.ActiveFacet = "true"
	}
	if f.Count > 0 {
		l.Count = new(f.Count)
	}
	return l
}

func buildLink(l opds.Link) atomLink {
	return atomLink{Rel: l.Rel, Href: l.Href, Type: l.Type, Title: l.Title}
}

func buildAuthors(authors []opds.Author) []atomAuthor {
	if len(authors) == 0 {
		return nil
	}
	out := make([]atomAuthor, 0, len(authors))
	for _, a := range authors {
		out = append(out, atomAuthor{Name: a.Name, URI: a.URI})
	}
	return out
}

func buildContributors(authors []opds.Author) []atomAuthor {
	return buildAuthors(authors)
}

func when(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func formatTimeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return formatTime(t)
}

func formatDate(t time.Time) string { return t.UTC().Format("2006-01-02") }
