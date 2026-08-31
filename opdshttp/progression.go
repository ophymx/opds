package opdshttp

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ophymx/opds"
)

// ProgressionStore is the per-user reading-position backend an application
// supplies (the composition boundary, like Authenticator): the library never
// stores progressions itself. The publicationID is the opds.Publication.ID of
// the publication the progression belongs to — the same key a PageSource
// implementation would use to populate PageStream.LastRead/LastReadDate from
// the store, so one store can back both.
type ProgressionStore interface {
	// Progression returns the stored progression for the user and publication.
	// It returns opds.ErrNotFound (or nil, nil) when none has been stored yet.
	Progression(ctx context.Context, user, publicationID string) (*opds.Progression, error)

	// SetProgression stores p, replacing any existing progression. Staleness
	// and validity are enforced by the handler before this is called.
	//
	// Returning ErrProgressionIncorrectUser or ErrProgressionLocked (possibly
	// wrapped) makes the handler answer 403 with the draft's matching problem
	// type, which is how a store refuses a write the handler cannot judge for
	// itself: a publication the user may read but not track, or one whose
	// progression has been frozen (a returned loan, an archived title).
	SetProgression(ctx context.Context, user, publicationID string, p *opds.Progression) error
}

// Errors a ProgressionStore returns to refuse an operation, mapped to the two
// 403 responses the draft defines. Any other non-ErrNotFound error is an
// internal failure.
var (
	// ErrProgressionIncorrectUser reports that the progression does not belong
	// to the authenticated user (403, error#progression-incorrect-user).
	ErrProgressionIncorrectUser = errors.New("opdshttp: progression belongs to another user")

	// ErrProgressionLocked reports that the publication's progression can no
	// longer be updated (403, error#progression-locked).
	ErrProgressionLocked = errors.New("opdshttp: progression is locked")
)

// WithProgression enables the OPDS Progression 1.0 endpoint
// (https://drafts.opds.io/opds-progression-1.0.html, as retrieved 2026-08-22)
// backed by the given store. Publications in feeds and publication documents
// are advertised with an injected opds.RelProgression link, and the handler
// serves GET and PUT on {prefix}/progression/{id}: GET returns the last-known
// Progression Document (200 with an empty payload when none is stored), PUT
// validates and stores one (201 on first store, 200 on update, 400 for an
// invalid document — including one timestamped implausibly far ahead of the
// server, see WithProgressionSkew — 409 when the stored progression is more
// recent, 403 when the store returns ErrProgressionIncorrectUser or
// ErrProgressionLocked).
// Every error but the 401 challenge — which carries the Authentication
// Document — carries an RFC 7807 problem body with the draft's registry type
// and title. The injected links carry the draft's authenticate hint in 2.0
// feeds (link properties.authenticate), pointing at the Authentication
// Document so a client can skip the unauthenticated round-trip; OPDS 1.x
// links have no properties and carry the plain link.
//
// The same endpoint also serves the pre-spec Cantook alias
// (opds.RelProgressionCantook, advertised as a second injected link): the
// Readium-locator-shaped document that Komga and Stump serve and the
// Cantook/Aldiko client family consumes, translated onto the same store with
// the deployed servers' status semantics (GET 204 when nothing is stored,
// PUT 204 on success). Publication-level progression, title, device, modified
// and the locator href/fragments translate both ways; resource-level
// progression and position have no draft equivalent and are dropped. The
// alias takes the device as sent — Komga hands out bare UUIDs where the draft
// requires a URI — and round-trips it unchanged, so a Cantook client still
// recognizes its own device; it is the draft document served for the same
// record that carries the id in URI form.
//
// Progression is per-user by definition, so WithProgression requires WithAuth:
// New panics when the store is configured without an Authenticator.
func WithProgression(store ProgressionStore) Option {
	return func(h *Handler) { h.progression = store }
}

// DefaultProgressionSkew is how far ahead of the server a submitted modified
// timestamp may be before the handler rejects it. It is generous enough to
// absorb any timezone-as-UTC bug (at most 14 hours) with room to spare.
const DefaultProgressionSkew = 24 * time.Hour

// WithProgressionSkew sets how far into the future a submitted modified
// timestamp may be, overriding DefaultProgressionSkew. A zero or negative
// duration disables the check.
//
// The check exists because the draft orders updates by a client-supplied
// timestamp: one device whose clock is years fast would store a progression
// no honest later update could ever beat, locking the publication behind a
// permanent 409. E-ink readers lose their clocks on a flat battery often
// enough that this is a practical failure, not a theoretical one, so a PUT
// too far ahead is refused as an invalid payload — and a stored timestamp
// already beyond the allowance is treated as not-newer, which lets the next
// honest update heal a record poisoned before this check existed.
func WithProgressionSkew(d time.Duration) Option {
	return func(h *Handler) { h.progSkew = d }
}

// ProgressionPath returns the request path for the progression endpoint of the
// publication with the given id, which must already be path-escaped if it
// contains characters not valid in a path segment.
func ProgressionPath(prefix, id string) string {
	return strings.TrimRight(prefix, "/") + "/progression/" + id
}

// ProgressionURL returns the progression endpoint path for a publication id
// under this handler's prefix.
func (h *Handler) ProgressionURL(id string) string { return ProgressionPath(h.prefix, id) }

// Problem type URIs from the OPDS error registry, and the RFC 7807 media type
// the progression endpoint reports errors with.
const (
	mediaTypeProblem          = "application/problem+json"
	problemProgressionInvalid = "https://registry.opds.io/error#progression-invalid-payload"
	problemProgressionDate    = "https://registry.opds.io/error#progression-date"
	problemProgressionUser    = "https://registry.opds.io/error#progression-incorrect-user"
	problemProgressionLocked  = "https://registry.opds.io/error#progression-locked"
)

// maxProgressionBody bounds a PUT body; real Progression Documents are tiny.
const maxProgressionBody = 64 << 10

// fnv32 hashes a (user, publication) pair for lock striping.
func fnv32(user, id string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(user))
	h.Write([]byte{0})
	h.Write([]byte(id))
	return h.Sum32()
}

func (h *Handler) serveProgression(w http.ResponseWriter, r *http.Request, id string) {
	if h.progression == nil {
		http.NotFound(w, r)
		return
	}
	// Per-user and mutable: RFC 9111 already bars shared caches from storing
	// an authenticated response, but a client- or proxy-side copy of someone's
	// reading position is worth refusing outright.
	w.Header().Set("Cache-Control", "no-store")
	user, _ := User(r.Context()) // authenticated: New requires WithAuth
	readium := readiumRequest(r)
	if r.Method == http.MethodPut {
		h.putProgression(w, r, user, id, readium)
		return
	}
	p, err := h.progression.Progression(r.Context(), user, id)
	if err != nil && !errors.Is(err, opds.ErrNotFound) {
		h.handleProgressionError(w, r, err)
		return
	}
	if p == nil {
		// The draft prescribes 200 with an empty payload over 404 when no
		// progression has been communicated yet; the Readium alias mirrors
		// Komga's 204.
		if readium {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		return
	}
	var (
		body []byte
		ct   = opds.MediaTypeProgression
	)
	if readium {
		body, err = marshalReadiumProgression(p)
		ct = opds.MediaTypeProgressionReadium
	} else {
		body, err = marshalProgression(p)
	}
	if err != nil {
		h.handleProgressionError(w, r, err)
		return
	}
	write(w, r, ct, body)
}

// readiumRequest reports whether the request addresses the Cantook/Readium
// alias rather than the draft document: the injected RelProgressionCantook
// link carries ?format=readium, and the media type in Content-Type (PUT) or
// Accept (GET) is honored for clients that construct their own requests.
func readiumRequest(r *http.Request) bool {
	if r.URL.Query().Get("format") == "readium" {
		return true
	}
	hdr := r.Header.Get("Accept")
	if r.Method == http.MethodPut {
		hdr = r.Header.Get("Content-Type")
	}
	return strings.Contains(hdr, "vnd.readium.progression")
}

func (h *Handler) putProgression(w http.ResponseWriter, r *http.Request, user, id string, readium bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxProgressionBody))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, problemProgressionInvalid)
		return
	}
	var p *opds.Progression
	if readium {
		p, err = unmarshalReadiumProgression(body)
	} else {
		p, err = unmarshalProgression(body)
	}
	if err != nil {
		writeProblem(w, http.StatusBadRequest, problemProgressionInvalid)
		return
	}
	if h.implausible(p.Modified) {
		writeProblem(w, http.StatusBadRequest, problemProgressionInvalid)
		return
	}
	// The staleness check and the store write must be atomic per (user,
	// publication), or a concurrent older PUT could land after a newer one —
	// the regression the 409 exists to prevent. This serializes them within
	// this process; a store shared by several processes needs its own
	// cross-process story (progstore, for one, documents a single writing
	// process).
	mu := &h.progLocks[fnv32(user, id)%uint32(len(h.progLocks))]
	mu.Lock()
	defer mu.Unlock()
	existing, err := h.progression.Progression(r.Context(), user, id)
	if err != nil && !errors.Is(err, opds.ErrNotFound) {
		h.handleProgressionError(w, r, err)
		return
	}
	// A stored timestamp beyond the skew allowance cannot be honest, so it
	// does not get to win the comparison: that is what lets an honest update
	// heal a record poisoned by a device with a broken clock.
	if existing != nil && p.Modified.Before(existing.Modified) && !h.implausible(existing.Modified) {
		writeProblem(w, http.StatusConflict, problemProgressionDate)
		return
	}
	if err := h.progression.SetProgression(r.Context(), user, id, p); err != nil {
		h.handleProgressionError(w, r, err)
		return
	}
	if readium {
		// Komga parity: the deployed endpoint answers 204 with no body.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	out, err := marshalProgression(p)
	if err != nil {
		h.handleProgressionError(w, r, err)
		return
	}
	code := http.StatusOK
	if existing == nil {
		code = http.StatusCreated
	}
	w.Header().Set("Content-Type", opds.MediaTypeProgression)
	w.WriteHeader(code)
	w.Write(out)
}

// problemTitles are the titles the draft pairs with each registry error type.
var problemTitles = map[string]string{
	problemProgressionInvalid: "Progression could not be updated due to an invalid payload.",
	problemProgressionDate:    "A more recent progression point is already available.",
	problemProgressionUser:    "Progression could not be updated for the current user.",
	problemProgressionLocked:  "Progression can no longer be updated for this publication.",
}

// implausible reports whether t is further ahead of the server's clock than
// the configured skew allowance permits. See WithProgressionSkew.
func (h *Handler) implausible(t time.Time) bool {
	return h.progSkew > 0 && t.After(time.Now().Add(h.progSkew))
}

// writeProblem writes an RFC 7807 problem response with the registry title for
// the given problem type.
func writeProblem(w http.ResponseWriter, code int, typeURI string) {
	writeProblemDetail(w, code, typeURI, problemTitles[typeURI])
}

// writeProblemDetail writes an RFC 7807 problem response. An empty typeURI is
// reported as "about:blank", RFC 7807's default for errors with no type of
// their own — the draft requires both members to be present.
func writeProblemDetail(w http.ResponseWriter, code int, typeURI, title string) {
	if typeURI == "" {
		typeURI = "about:blank"
	}
	body, _ := json.Marshal(struct {
		Type  string `json:"type"`
		Title string `json:"title"`
	}{typeURI, title})
	w.Header().Set("Content-Type", mediaTypeProblem)
	w.WriteHeader(code)
	w.Write(body)
}

// handleProgressionError reports a store failure on the progression endpoint.
// It maps the store's two refusal sentinels to the draft's 403 problem types
// and everything else to a 500, in both cases with the Problem Details payload
// the draft requires for errors other than 401.
func (h *Handler) handleProgressionError(w http.ResponseWriter, r *http.Request, err error) {
	// The two sentinels are protocol vocabulary, not failures: they are the
	// store's way of choosing a response the draft defines, so they are
	// answered before a WithErrorHandler hook, which owns unclassified
	// failures only.
	switch {
	case errors.Is(err, ErrProgressionIncorrectUser):
		writeProblem(w, http.StatusForbidden, problemProgressionUser)
		return
	case errors.Is(err, ErrProgressionLocked):
		writeProblem(w, http.StatusForbidden, problemProgressionLocked)
		return
	}
	if h.errorHandler != nil {
		h.errorHandler(w, r, err)
		return
	}
	writeProblemDetail(w, http.StatusInternalServerError, "", "Progression could not be retrieved or updated.")
}

// Wire shape of a Progression Document. Progression is a pointer so a missing
// field is distinguishable from a legitimate 0.
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

func marshalProgression(p *opds.Progression) ([]byte, error) {
	v := p.Progression
	id, name := normalizeDevice(p.Device)
	return json.Marshal(progressionJSON{
		Title:       p.Title,
		Modified:    p.Modified.UTC().Format(time.RFC3339),
		Device:      deviceJSON{ID: id, Name: name},
		Progression: &v,
		References:  p.References,
	})
}

// Placeholders for a device a stored progression cannot name. The URN is
// under the "opds" informal namespace and is only ever emitted, never
// interpreted.
const (
	unknownDeviceID   = "urn:opds:device:unknown"
	unknownDeviceName = "Unknown device"
)

// normalizeDevice coerces a device into the shape the draft's schema requires
// of a served document: an absolute-URI id and a non-empty name. A draft-path
// PUT is validated up front and never needs this; it exists for records that
// reached the store some other way — through the lenient Cantook alias, or
// from an application's own writes and migrations — so that no such record can
// make the handler serve a document that fails the published schema. A bare
// UUID (the id Komga and Stump hand out) becomes the urn:uuid: form the draft
// itself uses in every example; anything else opaque is wrapped in a URN whose
// escaped tail preserves the original bytes.
func normalizeDevice(d opds.Device) (id, name string) {
	id, name = d.ID, d.Name
	if name == "" {
		name = unknownDeviceName
	}
	switch {
	case id == "":
		id = unknownDeviceID
	case absoluteURI(id):
	case isUUID(id):
		id = "urn:uuid:" + id
	default:
		id = "urn:opds:device:" + url.PathEscape(id)
	}
	return id, name
}

// absoluteURI reports whether s is a URI with a scheme, which is what the
// draft's schema means by "format": "uri" for a device id.
func absoluteURI(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme != "" && u.IsAbs()
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
// which the draft's schema requires of the documents this handler serves. An
// empty entry refines nothing and is treated as a mistake.
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

// unmarshalProgression parses and validates a Progression Document, enforcing
// the required fields and progression ∈ [0, 1].
func unmarshalProgression(body []byte) (*opds.Progression, error) {
	var doc progressionJSON
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	if doc.Progression == nil || *doc.Progression < 0 || *doc.Progression > 1 {
		return nil, errors.New("opdshttp: progression missing or outside [0, 1]")
	}
	modified, err := time.Parse(time.RFC3339, doc.Modified)
	if err != nil {
		return nil, errors.New("opdshttp: progression modified missing or not RFC 3339")
	}
	if doc.Device.ID == "" || doc.Device.Name == "" {
		return nil, errors.New("opdshttp: progression device id and name are required")
	}
	// The schema types device.id as a URI. Enforcing it on the way in is what
	// keeps every document this handler later serves for the record valid,
	// and it costs a client nothing: the draft's own examples are all urn:uuid
	// or https.
	if !absoluteURI(doc.Device.ID) {
		return nil, errors.New("opdshttp: progression device id must be an absolute URI")
	}
	if !validReferences(doc.References) {
		return nil, errors.New("opdshttp: progression references must be URI references")
	}
	return &opds.Progression{
		Progression: *doc.Progression,
		Modified:    modified,
		Device:      opds.Device{ID: doc.Device.ID, Name: doc.Device.Name},
		Title:       doc.Title,
		References:  doc.References,
	}, nil
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

// marshalReadiumProgression translates a stored Progression into the Cantook
// document. Modified, device, title and the publication-level progression
// (locations.totalProgression) map directly; the locator href and fragments
// are reconstructed from References when they share a single resource (the
// inverse of the mapping unmarshalReadiumProgression applies).
func marshalReadiumProgression(p *opds.Progression) ([]byte, error) {
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

// unmarshalReadiumProgression parses and validates a Cantook document,
// translating it to the draft model: locations.totalProgression (required)
// becomes Progression, and the locator href/fragments become References as
// media-fragment URIs ("href#fragment"). Resource-level progression and
// position have no draft equivalent and are dropped. Device is not validated,
// matching the deployed servers this alias exists to be compatible with.
func unmarshalReadiumProgression(body []byte) (*opds.Progression, error) {
	var doc readiumProgressionJSON
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	modified, err := time.Parse(time.RFC3339, doc.Modified)
	if err != nil {
		return nil, errors.New("opdshttp: progression modified missing or not RFC 3339")
	}
	if doc.Locator.Locations == nil || doc.Locator.Locations.TotalProgression == nil {
		return nil, errors.New("opdshttp: locator.locations.totalProgression is required")
	}
	tp := *doc.Locator.Locations.TotalProgression
	if tp < 0 || tp > 1 {
		return nil, errors.New("opdshttp: totalProgression outside [0, 1]")
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
		return nil, errors.New("opdshttp: locator does not yield valid URI references")
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

// MemProgressionStore is an in-memory ProgressionStore for tests and examples.
// It is safe for concurrent use. The zero value is not usable; construct with
// NewMemProgressionStore.
type MemProgressionStore struct {
	mu sync.RWMutex
	m  map[memProgressionKey]opds.Progression
}

type memProgressionKey struct{ user, publication string }

// NewMemProgressionStore returns an empty in-memory progression store.
func NewMemProgressionStore() *MemProgressionStore {
	return &MemProgressionStore{m: map[memProgressionKey]opds.Progression{}}
}

// Progression implements ProgressionStore.
func (s *MemProgressionStore) Progression(_ context.Context, user, publicationID string) (*opds.Progression, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.m[memProgressionKey{user, publicationID}]
	if !ok {
		return nil, opds.ErrNotFound
	}
	return &p, nil
}

// SetProgression implements ProgressionStore.
func (s *MemProgressionStore) SetProgression(_ context.Context, user, publicationID string, p *opds.Progression) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := *p
	stored.References = append([]string(nil), p.References...)
	s.m[memProgressionKey{user, publicationID}] = stored
	return nil
}
