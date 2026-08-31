// Package opds2 encodes the version-neutral opds model to OPDS 2.0, the JSON
// format built on the Readium Web Publication Manifest.
package opds2

import (
	"encoding/json"
	"io"
	"time"

	"github.com/ophymx/opds"
)

// Marshal returns the OPDS 2.0 JSON encoding of the feed.
func Marshal(f *opds.Feed) ([]byte, error) {
	return json.MarshalIndent(buildFeed(f), "", "  ")
}

// Encode writes the OPDS 2.0 JSON encoding of the feed to w.
func Encode(w io.Writer, f *opds.Feed) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(buildFeed(f))
}

// MarshalPublication returns a standalone OPDS 2.0 publication document
// (media type application/opds-publication+json).
func MarshalPublication(p *opds.Publication) ([]byte, error) {
	return json.MarshalIndent(buildPublication(*p), "", "  ")
}

func buildFeed(f *opds.Feed) jsonFeed {
	updated := f.Updated
	if updated.IsZero() {
		updated = time.Now()
	}
	doc := jsonFeed{
		Metadata: jsonMetadata{
			Title:         localized(f.Title),
			Subtitle:      localized(f.Subtitle),
			Identifier:    f.ID,
			Modified:      formatTime(updated),
			NumberOfItems: f.TotalResults,
			ItemsPerPage:  f.ItemsPerPage,
			CurrentPage:   f.CurrentPage,
		},
		Links: buildLinks(f.Links),
	}
	if doc.Links == nil {
		doc.Links = []jsonLink{}
	}

	for _, n := range f.Navigation {
		doc.Navigation = append(doc.Navigation, buildNavLink(n))
	}
	for _, p := range f.Publications {
		doc.Publications = append(doc.Publications, buildPublication(p))
	}
	for _, g := range f.Groups {
		doc.Groups = append(doc.Groups, buildGroup(g))
	}
	doc.Facets = buildFacets(f.Facets)
	return doc
}

func buildGroup(g opds.Group) jsonGroup {
	jg := jsonGroup{Metadata: jsonMetadata{Title: localized(g.Title)}}
	if g.Href != "" {
		rel := g.Rel
		jg.Links = []jsonLink{{Rel: relValue(rel), Href: g.Href, Type: g.Type, Title: g.Title}}
	}
	for _, n := range g.Navigation {
		jg.Navigation = append(jg.Navigation, buildNavLink(n))
	}
	for _, p := range g.Publications {
		jg.Publications = append(jg.Publications, buildPublication(p))
	}
	return jg
}

// buildFacets groups Facet values by their Group label, preserving first-seen
// order, into the OPDS 2.0 facets structure.
func buildFacets(facets []opds.Facet) []jsonFacetGroup {
	if len(facets) == 0 {
		return nil
	}
	var order []string
	byGroup := map[string][]opds.Facet{}
	for _, ft := range facets {
		if _, ok := byGroup[ft.Group]; !ok {
			order = append(order, ft.Group)
		}
		byGroup[ft.Group] = append(byGroup[ft.Group], ft)
	}
	out := make([]jsonFacetGroup, 0, len(order))
	for _, name := range order {
		fg := jsonFacetGroup{Metadata: jsonMetadata{Title: localized(name)}}
		for _, ft := range byGroup[name] {
			l := jsonLink{Href: ft.Href, Type: ft.Type, Title: ft.Title}
			if ft.Count > 0 {
				l.Properties = &jsonProperties{NumberOfItems: ft.Count}
			}
			fg.Links = append(fg.Links, l)
		}
		out = append(out, fg)
	}
	return out
}

func buildNavLink(n opds.NavEntry) jsonLink {
	return jsonLink{
		Rel:   relValue(n.Rel),
		Href:  n.Href,
		Type:  n.Type,
		Title: n.Title,
	}
}

func buildPublication(p opds.Publication) jsonPublication {
	jp := jsonPublication{
		Metadata: jsonPubMetadata{
			Type:        "http://schema.org/Book",
			Title:       localized(p.Title),
			SortAs:      localized(p.SortAs),
			Identifier:  firstIdentifier(p),
			Author:      buildContributors(p.Authors),
			Contributor: buildContributors(p.Contributors),
			Publisher:   publisher(p.Publisher),
			Language:    p.Languages,
			Subject:     buildSubjects(p.Subjects),
			Description: localized(p.Description),
		},
	}
	if jp.Metadata.Description == "" {
		jp.Metadata.Description = localized(p.Summary)
	}
	if !p.Updated.IsZero() {
		jp.Metadata.Modified = formatTime(p.Updated)
	}
	if !p.Published.IsZero() {
		jp.Metadata.Published = formatTime(p.Published)
	}
	if p.Series != nil {
		jp.Metadata.BelongsTo = &belongsTo{Series: &series{Name: p.Series.Name, Position: p.Series.Position}}
	}

	jp.Links = buildLinks(p.Links)
	for _, a := range p.Acquisitions {
		jp.Links = append(jp.Links, buildAcquisition(a))
	}
	if jp.Links == nil {
		jp.Links = []jsonLink{}
	}
	for _, img := range p.Images {
		jp.Images = append(jp.Images, jsonLink{
			Rel: nil, Href: img.Href, Type: img.Type, Height: img.Height, Width: img.Width,
		})
	}
	return jp
}

func buildAcquisition(a opds.Acquisition) jsonLink {
	rel := string(a.Rel)
	if rel == "" {
		rel = string(opds.AcquireGeneric)
	}
	l := jsonLink{Rel: rel, Href: a.Href, Type: a.Type, Title: a.Title}
	props := &jsonProperties{}
	if len(a.Prices) > 0 {
		props.Price = &jsonPrice{Currency: a.Prices[0].Currency, Value: a.Prices[0].Value}
	}
	props.Indirect = buildIndirect(a.Indirect)
	if av := a.Availability; av != nil {
		props.Availability = &jsonAvailability{
			State: av.State,
			Since: formatTimeOrEmpty(av.Since),
			Until: formatTimeOrEmpty(av.Until),
		}
	}
	if h := a.Holds; h != nil {
		props.Holds = &jsonHolds{Total: h.Total, Position: h.Position}
	}
	if c := a.Copies; c != nil {
		props.Copies = &jsonCopies{Total: c.Total, Available: c.Available}
	}
	if !props.empty() {
		l.Properties = props
	}
	return l
}

func buildIndirect(in []opds.IndirectAcquisition) []jsonIndirect {
	if len(in) == 0 {
		return nil
	}
	out := make([]jsonIndirect, 0, len(in))
	for _, i := range in {
		out = append(out, jsonIndirect{Type: i.Type, Child: buildIndirect(i.Child)})
	}
	return out
}

func buildLinks(links []opds.Link) []jsonLink {
	if len(links) == 0 {
		return nil
	}
	out := make([]jsonLink, 0, len(links))
	for _, l := range links {
		jl := jsonLink{
			Rel: relValue(l.Rel), Href: l.Href, Type: l.Type, Title: l.Title, Templated: l.Templated,
		}
		if a := l.Authenticate; a != nil && a.Href != "" {
			typ := a.Type
			if typ == "" {
				typ = opds.MediaTypeAuthDocument
			}
			jl.Properties = &jsonProperties{Authenticate: &jsonAuthenticate{Href: a.Href, Type: typ}}
		}
		out = append(out, jl)
	}
	return out
}

func buildContributors(authors []opds.Author) contributors {
	if len(authors) == 0 {
		return nil
	}
	out := make(contributors, 0, len(authors))
	for _, a := range authors {
		out = append(out, contributor{Name: a.Name, URI: a.URI, Sort: a.SortAs})
	}
	return out
}

func buildSubjects(subs []opds.Subject) subjects {
	if len(subs) == 0 {
		return nil
	}
	out := make(subjects, 0, len(subs))
	for _, s := range subs {
		out = append(out, subject{Name: s.Name, Code: s.Code, Scheme: s.Scheme})
	}
	return out
}

func firstIdentifier(p opds.Publication) string {
	if len(p.Identifiers) > 0 {
		return p.Identifiers[0]
	}
	return p.ID
}

// relValue returns nil for an empty rel (so it is omitted) and the string
// otherwise. OPDS 2.0 permits a rel to be a string or an array; a single
// string is emitted.
func relValue(rel string) any {
	if rel == "" {
		return nil
	}
	return rel
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func formatTimeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return formatTime(t)
}
