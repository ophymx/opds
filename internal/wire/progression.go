package wire

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/ophymx/opds"
)

// Wire shape of a Progression Document
// (https://drafts.opds.io/opds-progression-1.0.html). Progression is a pointer
// so a missing field is distinguishable from a legitimate 0.
type progressionJSON struct {
	Title       string     `json:"title,omitempty"`
	Modified    string     `json:"modified"`
	Device      deviceJSON `json:"device"`
	Progression *float64   `json:"progression"`
	References  []string   `json:"references,omitempty"`
}

type deviceJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MarshalProgression renders a Progression Document. The device is normalized
// on the way out (see NormalizeDevice) so the result satisfies the published
// schema whatever the stored record holds.
func MarshalProgression(p *opds.Progression) ([]byte, error) {
	v := p.Progression
	id, name := NormalizeDevice(p.Device)
	return json.Marshal(progressionJSON{
		Title:       p.Title,
		Modified:    p.Modified.UTC().Format(time.RFC3339),
		Device:      deviceJSON{ID: id, Name: name},
		Progression: &v,
		References:  p.References,
	})
}

// ParseProgression decodes a Progression Document, enforcing what the model
// needs to be meaningful: a progression in [0, 1] and an RFC 3339 modified.
// The schema's stricter requirements are ValidateProgression's, so a client
// reading a lenient server's output is not stopped by them.
func ParseProgression(b []byte) (*opds.Progression, error) {
	var doc progressionJSON
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if doc.Progression == nil || *doc.Progression < 0 || *doc.Progression > 1 {
		return nil, errors.New("opds: progression missing or outside [0, 1]")
	}
	modified, err := time.Parse(time.RFC3339, doc.Modified)
	if err != nil {
		return nil, errors.New("opds: progression modified missing or not RFC 3339")
	}
	return &opds.Progression{
		Progression: *doc.Progression,
		Modified:    modified,
		Device:      opds.Device{ID: doc.Device.ID, Name: doc.Device.Name},
		Title:       doc.Title,
		References:  doc.References,
	}, nil
}

// ValidateProgression applies the schema constraints on a submitted document:
// a device named by an absolute URI, and references that parse. Enforcing them
// on the way in is what keeps every document served for the record valid, and
// it costs a client nothing — the draft's own examples are all urn:uuid or
// https.
func ValidateProgression(p *opds.Progression) error {
	if p.Device.ID == "" || p.Device.Name == "" {
		return errors.New("opds: progression device id and name are required")
	}
	if !AbsoluteURI(p.Device.ID) {
		return errors.New("opds: progression device id must be an absolute URI")
	}
	if !validReferences(p.References) {
		return errors.New("opds: progression references must be URI references")
	}
	return nil
}

// Placeholders for a device a stored progression cannot name. The URN is
// under the "opds" informal namespace and is only ever emitted, never
// interpreted.
const (
	unknownDeviceID   = "urn:opds:device:unknown"
	unknownDeviceName = "Unknown device"
)

// NormalizeDevice coerces a device into the shape the draft's schema requires
// of a served document: an absolute-URI id and a non-empty name. A validated
// document never needs it; it exists for records that reached a store some
// other way — through the lenient Cantook alias, or from an application's own
// writes and migrations — so that no such record can produce a document that
// fails the published schema. A bare UUID (the id Komga and Stump hand out)
// becomes the urn:uuid: form the draft itself uses in every example; anything
// else opaque is wrapped in a URN whose escaped tail preserves the original
// bytes.
func NormalizeDevice(d opds.Device) (id, name string) {
	id, name = d.ID, d.Name
	if name == "" {
		name = unknownDeviceName
	}
	switch {
	case id == "":
		id = unknownDeviceID
	case AbsoluteURI(id):
	case isUUID(id):
		id = "urn:uuid:" + id
	default:
		id = "urn:opds:device:" + url.PathEscape(id)
	}
	return id, name
}

// AbsoluteURI reports whether s is a URI with a scheme, which is what the
// draft's schema means by "format": "uri" for a device id.
func AbsoluteURI(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.IsAbs()
}

// isUUID reports whether s is a plain 8-4-4-4-12 hex UUID.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
				return false
			}
		}
	}
	return true
}

// validReferences reports whether every reference parses as a URI reference,
// which the draft's schema requires of a served document. An empty entry
// refines nothing and is treated as a mistake.
func validReferences(refs []string) bool {
	for _, ref := range refs {
		if ref == "" {
			return false
		}
		if _, err := url.Parse(ref); err != nil {
			return false
		}
	}
	return true
}

// Wire shape of the pre-spec Cantook/Readium progression document, as served
// by Komga (R2Progression) and Stump and consumed by Cantook/Aldiko: a Readium
// Locator (https://readium.org/architecture/schema/locator.schema.json) under
// "locator" instead of the draft's flat fields.
type (
	readiumProgressionJSON struct {
		Modified string             `json:"modified"`
		Device   deviceJSON         `json:"device"`
		Locator  readiumLocatorJSON `json:"locator"`
	}
	readiumLocatorJSON struct {
		Href      string                `json:"href,omitempty"`
		Type      string                `json:"type,omitempty"`
		Title     string                `json:"title,omitempty"`
		Locations *readiumLocationsJSON `json:"locations,omitempty"`
	}
	readiumLocationsJSON struct {
		Fragments        []string `json:"fragments,omitempty"`
		Position         *int     `json:"position,omitempty"`
		Progression      *float64 `json:"progression,omitempty"`
		TotalProgression *float64 `json:"totalProgression,omitempty"`
	}
)

// MarshalReadiumProgression translates a Progression into the Cantook
// document. Modified, device, title and the publication-level progression
// (locations.totalProgression) map directly; the locator href and fragments
// are reconstructed from References when they share a single resource (the
// inverse of the mapping ParseReadiumProgression applies). The device is taken
// as held — Komga hands out bare UUIDs where the draft requires a URI, and a
// Cantook client has to recognize its own device.
func MarshalReadiumProgression(p *opds.Progression) ([]byte, error) {
	tp := p.Progression
	loc := readiumLocatorJSON{
		Title:     p.Title,
		Locations: &readiumLocationsJSON{TotalProgression: &tp},
	}
	if href, fragments, ok := splitReferences(p.References); ok {
		loc.Href = href
		loc.Locations.Fragments = fragments
	}
	return json.Marshal(readiumProgressionJSON{
		Modified: p.Modified.UTC().Format(time.RFC3339),
		Device:   deviceJSON{ID: p.Device.ID, Name: p.Device.Name},
		Locator:  loc,
	})
}

// ParseReadiumProgression parses a Cantook document, translating it to the
// draft model: locations.totalProgression (required) becomes Progression, and
// the locator href/fragments become References as media-fragment URIs
// ("href#fragment"). Resource-level progression and position have no draft
// equivalent and are dropped. Device is not validated, matching the deployed
// servers this shape exists to be compatible with.
func ParseReadiumProgression(b []byte) (*opds.Progression, error) {
	var doc readiumProgressionJSON
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	modified, err := time.Parse(time.RFC3339, doc.Modified)
	if err != nil {
		return nil, errors.New("opds: progression modified missing or not RFC 3339")
	}
	if doc.Locator.Locations == nil || doc.Locator.Locations.TotalProgression == nil {
		return nil, errors.New("opds: locator.locations.totalProgression is required")
	}
	tp := *doc.Locator.Locations.TotalProgression
	if tp < 0 || tp > 1 {
		return nil, errors.New("opds: totalProgression outside [0, 1]")
	}
	// The locator href may already carry a fragment. Split it off before
	// joining, so an href like "c1.html#x" cannot produce a second "#" — and
	// keep that fragment as the reference when locations carries none of its
	// own.
	href, hrefFragment, _ := strings.Cut(doc.Locator.Href, "#")
	fragments := doc.Locator.Locations.Fragments
	if len(fragments) == 0 && hrefFragment != "" {
		fragments = []string{hrefFragment}
	}
	var refs []string
	if len(fragments) > 0 {
		for _, f := range fragments {
			refs = append(refs, href+"#"+f)
		}
	} else if href != "" {
		refs = []string{href}
	}
	if !validReferences(refs) {
		return nil, errors.New("opds: locator does not yield valid URI references")
	}
	return &opds.Progression{
		Progression: tp,
		Modified:    modified,
		Device:      opds.Device{ID: doc.Device.ID, Name: doc.Device.Name},
		Title:       doc.Locator.Title,
		References:  refs,
	}, nil
}

// splitReferences factors media-fragment URI references into a common resource
// href and its fragments. It reports false when the references do not share a
// single resource, in which case they cannot be represented as one locator.
func splitReferences(refs []string) (href string, fragments []string, ok bool) {
	for i, ref := range refs {
		h, frag, _ := strings.Cut(ref, "#")
		if i == 0 {
			href = h
		} else if h != href {
			return "", nil, false
		}
		if frag != "" {
			fragments = append(fragments, frag)
		}
	}
	return href, fragments, len(refs) > 0
}
