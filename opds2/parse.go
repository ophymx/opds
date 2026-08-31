package opds2

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ophymx/opds"
)

// Unmarshal decodes an OPDS 2.0 feed into the version-neutral model. It is the
// inverse of Marshal, and reuses the same wire types, so the two directions
// cannot drift apart.
//
// A few members of the model have no 2.0 representation and so never come back
// from a decode: Publication.PageStream (OPDS-PSE is a 1.x extension),
// Publication.Summary (2.0 has one description, which lands in Description),
// Feed.Icon and Feed.Authors, Facet.Active, and every Publication.Identifiers
// entry after the first, since 2.0 metadata carries a single identifier.
func Unmarshal(b []byte) (*opds.Feed, error) {
	var doc jsonFeed
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	kind, err := documentKind(b)
	if err != nil {
		return nil, err
	}
	if kind == kindPublication {
		return nil, errors.New("opds2: document is an OPDS 2.0 publication, not a feed")
	}
	f := &opds.Feed{
		ID:           doc.Metadata.Identifier,
		Title:        string(doc.Metadata.Title),
		Subtitle:     string(doc.Metadata.Subtitle),
		Updated:      parseTime(doc.Metadata.Modified),
		Links:        parseLinks(doc.Links),
		TotalResults: doc.Metadata.NumberOfItems,
		ItemsPerPage: doc.Metadata.ItemsPerPage,
		CurrentPage:  doc.Metadata.CurrentPage,
	}
	for _, n := range doc.Navigation {
		f.Navigation = append(f.Navigation, parseNavLink(n))
	}
	for _, p := range doc.Publications {
		f.Publications = append(f.Publications, parsePublication(p))
	}
	for _, g := range doc.Groups {
		f.Groups = append(f.Groups, parseGroup(g))
	}
	f.Facets = parseFacets(doc.Facets)
	return f, nil
}

// UnmarshalPublication decodes a standalone OPDS 2.0 publication document
// (media type opds.MediaTypePublication). It is the inverse of
// MarshalPublication, with the same lossy members noted on Unmarshal.
func UnmarshalPublication(b []byte) (*opds.Publication, error) {
	var doc jsonPublication
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	kind, err := documentKind(b)
	if err != nil {
		return nil, err
	}
	if kind == kindFeed {
		return nil, errors.New("opds2: document is an OPDS 2.0 feed, not a publication")
	}
	p := parsePublication(doc)
	return &p, nil
}

func parseGroup(g jsonGroup) opds.Group {
	out := opds.Group{Title: string(g.Metadata.Title)}
	if len(g.Links) > 0 {
		l := g.Links[0]
		out.Href, out.Type, out.Rel = l.Href, l.Type, firstRel(l.Rel)
		if out.Title == "" {
			out.Title = l.Title
		}
	}
	for _, n := range g.Navigation {
		out.Navigation = append(out.Navigation, parseNavLink(n))
	}
	for _, p := range g.Publications {
		out.Publications = append(out.Publications, parsePublication(p))
	}
	return out
}

// parseFacets flattens the 2.0 facets structure — links grouped under a group
// title — back into the flat []Facet the model uses.
func parseFacets(groups []jsonFacetGroup) []opds.Facet {
	var out []opds.Facet
	for _, g := range groups {
		for _, l := range g.Links {
			ft := opds.Facet{Group: string(g.Metadata.Title), Title: l.Title, Href: l.Href, Type: l.Type}
			if l.Properties != nil {
				ft.Count = l.Properties.NumberOfItems
			}
			out = append(out, ft)
		}
	}
	return out
}

func parseNavLink(l jsonLink) opds.NavEntry {
	return opds.NavEntry{
		Title: l.Title,
		Href:  l.Href,
		Type:  l.Type,
		Rel:   firstRel(l.Rel),
	}
}

func parsePublication(jp jsonPublication) opds.Publication {
	m := jp.Metadata
	p := opds.Publication{
		ID:           m.Identifier,
		Title:        string(m.Title),
		SortAs:       string(m.SortAs),
		Updated:      parseTime(m.Modified),
		Published:    parseTime(m.Published),
		Languages:    m.Language,
		Publisher:    string(m.Publisher),
		Authors:      parseContributors(m.Author),
		Contributors: parseContributors(m.Contributor),
		Subjects:     parseSubjects(m.Subject),
		Description:  string(m.Description),
	}
	if m.BelongsTo != nil && m.BelongsTo.Series != nil {
		p.Series = &opds.Series{Name: m.BelongsTo.Series.Name, Position: m.BelongsTo.Series.Position}
	}
	for _, l := range jp.Links {
		switch rel := firstRel(l.Rel); {
		case isAcquisitionRel(l.Rel):
			p.Acquisitions = append(p.Acquisitions, parseAcquisition(l, rel))
		case rel == opds.RelImage || rel == opds.RelThumbnail:
			p.Images = append(p.Images, parseImage(l, rel == opds.RelThumbnail))
		default:
			p.Links = append(p.Links, parseLink(l))
		}
	}
	// The images collection carries no rel; the first is the cover by
	// convention, matching how Marshal emits them.
	for _, l := range jp.Images {
		p.Images = append(p.Images, parseImage(l, false))
	}
	return p
}

func parseImage(l jsonLink, thumbnail bool) opds.Image {
	return opds.Image{
		Href: l.Href, Type: l.Type, Width: l.Width, Height: l.Height, Thumbnail: thumbnail,
	}
}

func parseAcquisition(l jsonLink, rel string) opds.Acquisition {
	a := opds.Acquisition{
		Rel:   opds.AcquisitionRel(rel),
		Href:  l.Href,
		Type:  l.Type,
		Title: l.Title,
	}
	props := l.Properties
	if props == nil {
		return a
	}
	if pr := props.Price; pr != nil {
		a.Prices = []opds.Price{{Currency: pr.Currency, Value: pr.Value}}
	}
	a.Indirect = parseIndirect(props.Indirect)
	if av := props.Availability; av != nil {
		a.Availability = &opds.Availability{
			State: av.State,
			Since: parseTime(av.Since),
			Until: parseTime(av.Until),
		}
	}
	if h := props.Holds; h != nil {
		a.Holds = &opds.Holds{Total: h.Total, Position: h.Position}
	}
	if c := props.Copies; c != nil {
		a.Copies = &opds.Copies{Total: c.Total, Available: c.Available}
	}
	return a
}

func parseIndirect(in []jsonIndirect) []opds.IndirectAcquisition {
	if len(in) == 0 {
		return nil
	}
	out := make([]opds.IndirectAcquisition, 0, len(in))
	for _, i := range in {
		out = append(out, opds.IndirectAcquisition{Type: i.Type, Child: parseIndirect(i.Child)})
	}
	return out
}

func parseLinks(links []jsonLink) []opds.Link {
	if len(links) == 0 {
		return nil
	}
	out := make([]opds.Link, 0, len(links))
	for _, l := range links {
		out = append(out, parseLink(l))
	}
	return out
}

func parseLink(l jsonLink) opds.Link {
	out := opds.Link{
		Rel:       firstRel(l.Rel),
		Href:      l.Href,
		Type:      l.Type,
		Title:     l.Title,
		Templated: l.Templated,
	}
	if l.Properties != nil && l.Properties.Authenticate != nil {
		a := l.Properties.Authenticate
		out.Authenticate = &opds.AuthenticateHint{Href: a.Href, Type: a.Type}
	}
	return out
}

func parseContributors(cs contributors) []opds.Author {
	if len(cs) == 0 {
		return nil
	}
	out := make([]opds.Author, 0, len(cs))
	for _, c := range cs {
		out = append(out, opds.Author{Name: c.Name, URI: c.URI, SortAs: c.Sort})
	}
	return out
}

func parseSubjects(ss subjects) []opds.Subject {
	if len(ss) == 0 {
		return nil
	}
	out := make([]opds.Subject, 0, len(ss))
	for _, s := range ss {
		out = append(out, opds.Subject{Name: s.Name, Code: s.Code, Scheme: s.Scheme})
	}
	return out
}

// isAcquisitionRel reports whether any of a link's relations is an OPDS
// acquisition relation. A link may carry several rels; one acquisition
// relation makes it an acquisition link.
func isAcquisitionRel(rel any) bool {
	for _, r := range rels(rel) {
		if strings.HasPrefix(r, string(opds.AcquireGeneric)) {
			return true
		}
	}
	return false
}

// firstRel returns the relation the neutral model keeps. OPDS 2.0 permits a
// rel to be a string or an array; the model has room for one, so a link
// carrying several keeps the first, which is the one servers put the primary
// relation in.
func firstRel(rel any) string {
	if r := rels(rel); len(r) > 0 {
		return r[0]
	}
	return ""
}

// rels normalizes the string-or-array rel member to a slice.
func rels(rel any) []string {
	switch v := rel.(type) {
	case nil:
		return nil
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// parseTime accepts the RFC 3339 timestamps OPDS 2.0 specifies and the
// date-only form catalogs commonly use for metadata.published. An
// unparseable or absent value yields the zero time rather than an error: a
// bad date is not a reason to reject a whole catalog.
func parseTime(s string) time.Time {
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

// UnmarshalJSON accepts a contributor as a bare string, an object, or (through
// the slice form below) either inside an array — all three shapes appear in
// deployed 2.0 catalogs.
func (c *contributor) UnmarshalJSON(b []byte) error {
	var name string
	if json.Unmarshal(b, &name) == nil {
		*c = contributor{Name: name}
		return nil
	}
	var obj struct {
		Name   any        `json:"name"`
		SortAs string     `json:"sortAs"`
		Links  []jsonLink `json:"links"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return fmt.Errorf("opds2: contributor is neither a string nor an object: %w", err)
	}
	*c = contributor{Name: localizedString(obj.Name), Sort: obj.SortAs}
	if len(obj.Links) > 0 {
		c.URI = obj.Links[0].Href
	}
	return nil
}

// UnmarshalJSON accepts a subject as a bare string or an object.
func (s *subject) UnmarshalJSON(b []byte) error {
	var name string
	if json.Unmarshal(b, &name) == nil {
		*s = subject{Name: name}
		return nil
	}
	var obj struct {
		Name   any    `json:"name"`
		Code   string `json:"code"`
		Scheme string `json:"scheme"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return fmt.Errorf("opds2: subject is neither a string nor an object: %w", err)
	}
	*s = subject{Name: localizedString(obj.Name), Code: obj.Code, Scheme: obj.Scheme}
	return nil
}

// localizedString flattens the Readium "localized string" shape — either a
// plain string or a language-tagged object — that 2.0 permits wherever a
// human-readable name appears. The model is not language-aware, so a tagged
// object contributes its "und" (undetermined) entry when present and any
// entry otherwise.
func localizedString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case map[string]any:
		if und, ok := s["und"].(string); ok {
			return und
		}
		for _, lang := range sortedKeys(s) {
			if str, ok := s[lang].(string); ok {
				return str
			}
		}
	}
	return ""
}

// sortedKeys keeps the choice among equally-eligible language entries
// deterministic.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// The 2.0 metadata members below are all "string or something richer" on the
// wire: Readium permits a localized-string object wherever a human-readable
// string appears, and a single value wherever a list is allowed. Each type
// marshals exactly as the plain form the encoder emits and accepts every
// shape on the way in, so leniency costs nothing in the output.

// localized is a human-readable string that may arrive as a language-tagged
// object.
type localized string

func (l *localized) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*l = localized(localizedString(v))
	return nil
}

// contributors is a contributor list that may arrive as a single contributor.
type contributors []contributor

func (c *contributors) UnmarshalJSON(b []byte) error {
	var many []contributor
	if json.Unmarshal(b, &many) == nil {
		*c = many
		return nil
	}
	var one contributor
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*c = contributors{one}
	return nil
}

// subjects is a subject list that may arrive as a single subject.
type subjects []subject

func (s *subjects) UnmarshalJSON(b []byte) error {
	var many []subject
	if json.Unmarshal(b, &many) == nil {
		*s = many
		return nil
	}
	var one subject
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*s = subjects{one}
	return nil
}

// languages is a language list that may arrive as a single code.
type languages []string

func (l *languages) UnmarshalJSON(b []byte) error {
	var many []string
	if json.Unmarshal(b, &many) == nil {
		*l = many
		return nil
	}
	var one string
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*l = languages{one}
	return nil
}

// publisher is a publisher name that may arrive as a full contributor object,
// or a list of them, since Readium types it as a contributor.
type publisher string

func (p *publisher) UnmarshalJSON(b []byte) error {
	var cs contributors
	if err := cs.UnmarshalJSON(b); err != nil {
		return err
	}
	if len(cs) > 0 {
		*p = publisher(cs[0].Name)
	}
	return nil
}

// A feed and a publication have the same two required members — "metadata" and
// "links" — so neither decoder can tell from its own struct what it was
// handed: a publication decoded as a feed yields a feed titled after the book
// with no publications in it, which is worse than an error. What separates
// them is everything else. A feed is the only one with the collections; a
// publication is the only one with images or an acquisition link. A document
// with none of those markers is an empty feed, which is legitimate, and is
// decoded as whichever the caller asked for.
type docKind int

const (
	kindAmbiguous docKind = iota
	kindFeed
	kindPublication
)

func documentKind(b []byte) (docKind, error) {
	var probe struct {
		Metadata     json.RawMessage `json:"metadata"`
		Navigation   json.RawMessage `json:"navigation"`
		Publications json.RawMessage `json:"publications"`
		Groups       json.RawMessage `json:"groups"`
		Facets       json.RawMessage `json:"facets"`
		Images       json.RawMessage `json:"images"`
		Links        []jsonLink      `json:"links"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return kindAmbiguous, err
	}
	if probe.Metadata == nil {
		return kindAmbiguous, errors.New("opds2: not an OPDS 2.0 document: no metadata")
	}
	if probe.Navigation != nil || probe.Publications != nil || probe.Groups != nil || probe.Facets != nil {
		return kindFeed, nil
	}
	if probe.Images != nil {
		return kindPublication, nil
	}
	for _, l := range probe.Links {
		if isAcquisitionRel(l.Rel) {
			return kindPublication, nil
		}
	}
	return kindAmbiguous, nil
}
