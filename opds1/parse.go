package opds1

import (
	"encoding/xml"
	"strconv"
	"strings"
	"time"

	"github.com/ophymx/opds"
)

// Unmarshal decodes an OPDS 1.2 Atom feed into the version-neutral model. It
// is the inverse of Marshal.
//
// A few things do not survive the 1.2 encoding and so never come back from a
// decode: Publication.SortAs and Publication.Series (1.2 has no representation
// for either), Feed.CurrentPage (1.x paginates by opensearch:startIndex), and
// a group's navigation entries, which 1.2 flattens into the feed without the
// collection link that identifies a group's publications. Values the encoder
// fills in are returned as filled in rather than as the zero values they came
// from: an entry with no timestamp carries the feed's, and a navigation entry
// with no id carries its href.
func Unmarshal(b []byte) (*opds.Feed, error) {
	var doc xFeed
	if err := xml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	f := &opds.Feed{
		ID:           doc.ID,
		Title:        doc.Title,
		Subtitle:     doc.Subtitle,
		Updated:      parseTime(doc.Updated),
		Icon:         doc.Icon,
		Authors:      parseAuthors(doc.Authors),
		TotalResults: doc.TotalResults,
		ItemsPerPage: doc.ItemsPerPage,
		StartIndex:   doc.StartIndex,
	}
	for _, l := range doc.Links {
		if attrValue(l.Attrs, "rel") == opds.RelFacet {
			f.Facets = append(f.Facets, parseFacet(l))
			continue
		}
		f.Links = append(f.Links, parseLink(l))
	}

	// 1.2 has no groups: the encoder flattens them, tagging each member with a
	// collection link back to the group. Rebuilding them here keeps the two
	// directions symmetric for the publications that carry the tag.
	groups := map[string]int{}
	for _, e := range doc.Entries {
		if !isPublication(e) {
			f.Navigation = append(f.Navigation, parseNav(e))
			continue
		}
		p, group := parseEntry(e)
		if group == nil {
			f.Publications = append(f.Publications, p)
			continue
		}
		i, ok := groups[group.Href]
		if !ok {
			i = len(f.Groups)
			groups[group.Href] = i
			f.Groups = append(f.Groups, *group)
		}
		f.Groups[i].Publications = append(f.Groups[i].Publications, p)
	}
	return f, nil
}

// UnmarshalEntry decodes a standalone OPDS 1.2 entry document (media type
// opds.MediaTypeEntry). It is the inverse of MarshalEntry, with the same lossy
// members noted on Unmarshal.
func UnmarshalEntry(b []byte) (*opds.Publication, error) {
	var e xEntry
	if err := xml.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	p, _ := parseEntry(e)
	return &p, nil
}

// isPublication distinguishes an acquisition entry from a navigation entry.
// An OPDS 1.2 acquisition entry is defined by carrying at least one
// acquisition link; page streaming counts too, since a catalog may advertise
// a publication as streamable without offering a download.
func isPublication(e xEntry) bool {
	for _, l := range e.Links {
		switch rel := attrValue(l.Attrs, "rel"); {
		case isAcquisitionRel(rel), rel == opds.RelPageStream:
			return true
		}
	}
	return false
}

func parseNav(e xEntry) opds.NavEntry {
	n := opds.NavEntry{ID: e.ID, Title: e.Title, Updated: parseTime(e.Updated)}
	if e.Content != nil {
		n.Content = e.Content.Body
	}
	for _, l := range e.Links {
		rel := attrValue(l.Attrs, "rel")
		if rel == opds.RelImage || rel == opds.RelThumbnail {
			n.Images = append(n.Images, parseImage(l, rel == opds.RelThumbnail))
			continue
		}
		// The target is the entry's first non-image link; anything further is
		// not representable and is dropped, as the encoder emits only one.
		if n.Href == "" {
			n.Href, n.Type, n.Rel = attrValue(l.Attrs, "href"), attrValue(l.Attrs, "type"), rel
		}
	}
	return n
}

// parseEntry decodes an acquisition entry. It also reports the group the entry
// belongs to, if it carries the collection link 1.2 uses to express group
// membership.
func parseEntry(e xEntry) (opds.Publication, *opds.Group) {
	p := opds.Publication{
		ID:           e.ID,
		Title:        e.Title,
		Updated:      parseTime(e.Updated),
		Published:    parseTime(e.Issued),
		Languages:    e.Languages,
		Identifiers:  e.Identifiers,
		Publisher:    e.Publisher,
		Authors:      parseAuthors(e.Authors),
		Contributors: parseAuthors(e.Contributors),
		Rights:       e.Rights,
	}
	for _, c := range e.Categories {
		p.Subjects = append(p.Subjects, parseCategory(c))
	}
	if e.Summary != nil {
		p.Summary = e.Summary.Body
	}
	if e.Content != nil {
		p.Description = e.Content.Body
	}
	var group *opds.Group
	for _, l := range e.Links {
		switch rel := attrValue(l.Attrs, "rel"); {
		case rel == opds.RelImage || rel == opds.RelThumbnail:
			p.Images = append(p.Images, parseImage(l, rel == opds.RelThumbnail))
		case rel == opds.RelPageStream:
			p.PageStream = parsePageStream(l)
		case rel == opds.RelCollection:
			group = &opds.Group{
				Title: attrValue(l.Attrs, "title"),
				Href:  attrValue(l.Attrs, "href"),
				Type:  attrValue(l.Attrs, "type"),
			}
		case isAcquisitionRel(rel):
			p.Acquisitions = append(p.Acquisitions, parseAcquisition(l, rel))
		default:
			p.Links = append(p.Links, parseLink(l))
		}
	}
	return p, group
}

// parseCategory inverts the encoder's mapping of a Subject onto an Atom
// category: the term is the code when there is one and the name otherwise, and
// the label is always the name. A label that repeats the term therefore means
// the subject had no code.
func parseCategory(c xCategory) opds.Subject {
	s := opds.Subject{Name: c.Term, Scheme: c.Scheme}
	if c.Label != "" && c.Label != c.Term {
		s.Name, s.Code = c.Label, c.Term
	}
	return s
}

func parseImage(l xLink, thumbnail bool) opds.Image {
	return opds.Image{
		Href:      attrValue(l.Attrs, "href"),
		Type:      attrValue(l.Attrs, "type"),
		Thumbnail: thumbnail,
	}
}

func parsePageStream(l xLink) *opds.PageStream {
	ps := &opds.PageStream{
		Href:      attrValue(l.Attrs, "href"),
		Type:      attrValue(l.Attrs, "type"),
		PageCount: attrInt(l.Attrs, "count"),
		LastRead:  attrInt(l.Attrs, "lastRead"),
	}
	if ps.LastRead > 0 {
		ps.LastReadDate = parseTime(attrValue(l.Attrs, "lastReadDate"))
	}
	return ps
}

func parseFacet(l xLink) opds.Facet {
	return opds.Facet{
		Group:  attrValue(l.Attrs, "facetGroup"),
		Title:  attrValue(l.Attrs, "title"),
		Href:   attrValue(l.Attrs, "href"),
		Type:   attrValue(l.Attrs, "type"),
		Count:  attrInt(l.Attrs, "count"),
		Active: attrValue(l.Attrs, "activeFacet") == "true",
	}
}

func parseAcquisition(l xLink, rel string) opds.Acquisition {
	a := opds.Acquisition{
		Rel:   opds.AcquisitionRel(rel),
		Href:  attrValue(l.Attrs, "href"),
		Type:  attrValue(l.Attrs, "type"),
		Title: attrValue(l.Attrs, "title"),
	}
	for _, pr := range l.Prices {
		// A price that does not parse as a number is dropped rather than
		// failing the feed: the rest of the acquisition is still usable.
		v, err := strconv.ParseFloat(strings.TrimSpace(pr.Value), 64)
		if err != nil {
			continue
		}
		a.Prices = append(a.Prices, opds.Price{Currency: pr.Currency, Value: v})
	}
	a.Indirect = parseIndirect(l.Indirect)
	if av := l.Availability; av != nil {
		a.Availability = &opds.Availability{
			State: av.Status,
			Since: parseTime(av.Since),
			Until: parseTime(av.Until),
		}
	}
	if h := l.Holds; h != nil {
		a.Holds = &opds.Holds{Position: h.Position}
		if h.Total != nil {
			a.Holds.Total = *h.Total
		}
	}
	if c := l.Copies; c != nil {
		a.Copies = &opds.Copies{}
		if c.Total != nil {
			a.Copies.Total = *c.Total
		}
		if c.Available != nil {
			a.Copies.Available = *c.Available
		}
	}
	return a
}

func parseIndirect(in []xIndirect) []opds.IndirectAcquisition {
	if len(in) == 0 {
		return nil
	}
	out := make([]opds.IndirectAcquisition, 0, len(in))
	for _, i := range in {
		out = append(out, opds.IndirectAcquisition{Type: i.Type, Child: parseIndirect(i.Child)})
	}
	return out
}

func parseLink(l xLink) opds.Link {
	return opds.Link{
		Rel:   attrValue(l.Attrs, "rel"),
		Href:  attrValue(l.Attrs, "href"),
		Type:  attrValue(l.Attrs, "type"),
		Title: attrValue(l.Attrs, "title"),
	}
}

func parseAuthors(authors []xAuthor) []opds.Author {
	if len(authors) == 0 {
		return nil
	}
	out := make([]opds.Author, 0, len(authors))
	for _, a := range authors {
		out = append(out, opds.Author{Name: a.Name, URI: a.URI})
	}
	return out
}

func isAcquisitionRel(rel string) bool {
	return strings.HasPrefix(rel, string(opds.AcquireGeneric))
}

// attrValue returns the attribute with the given local name, ignoring its
// namespace. See the note on xLink for why decoding matches on local names.
func attrValue(attrs []xml.Attr, local string) string {
	for _, a := range attrs {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func attrInt(attrs []xml.Attr, local string) int {
	n, err := strconv.Atoi(strings.TrimSpace(attrValue(attrs, local)))
	if err != nil {
		return 0
	}
	return n
}

// parseTime accepts the RFC 3339 timestamps Atom requires, the date-only form
// dcterms:issued commonly carries, and the truncated dates catalogs use for
// old publications. An unparseable or absent value yields the zero time rather
// than an error: a bad date is not a reason to reject a whole feed.
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
