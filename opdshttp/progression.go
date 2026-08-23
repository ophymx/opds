package opdshttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
	SetProgression(ctx context.Context, user, publicationID string, p *opds.Progression) error
}

// WithProgression enables the OPDS Progression 1.0 endpoint
// (https://drafts.opds.io/opds-progression-1.0.html, as retrieved 2026-08-22)
// backed by the given store. Publications in feeds and publication documents
// are advertised with an injected opds.RelProgression link, and the handler
// serves GET and PUT on {prefix}/progression/{id}: GET returns the last-known
// Progression Document (200 with an empty payload when none is stored), PUT
// validates and stores one (201 on first store, 200 on update, 400 for an
// invalid document, 409 when the stored progression is more recent — errors
// carry an RFC 7807 problem body).
//
// The same endpoint also serves the pre-spec Cantook alias
// (opds.RelProgressionCantook, advertised as a second injected link): the
// Readium-locator-shaped document that Komga and Stump serve and the
// Cantook/Aldiko client family consumes, translated onto the same store with
// the deployed servers' status semantics (GET 204 when nothing is stored,
// PUT 204 on success). Publication-level progression, title, device, modified
// and the locator href/fragments translate both ways; resource-level
// progression and position have no draft equivalent and are dropped.
//
// Progression is per-user by definition, so WithProgression requires WithAuth:
// New panics when the store is configured without an Authenticator.
func WithProgression(store ProgressionStore) Option {
	return func(h *Handler) { h.progression = store }
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
)

// maxProgressionBody bounds a PUT body; real Progression Documents are tiny.
const maxProgressionBody = 64 << 10

func (h *Handler) serveProgression(w http.ResponseWriter, r *http.Request, id string) {
	if h.progression == nil {
		http.NotFound(w, r)
		return
	}
	user, _ := User(r.Context()) // authenticated: New requires WithAuth
	readium := readiumRequest(r)
	if r.Method == http.MethodPut {
		h.putProgression(w, r, user, id, readium)
		return
	}
	p, err := h.progression.Progression(r.Context(), user, id)
	if err != nil && !errors.Is(err, opds.ErrNotFound) {
		h.handleError(w, r, err)
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
		h.handleError(w, r, err)
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
	existing, err := h.progression.Progression(r.Context(), user, id)
	if err != nil && !errors.Is(err, opds.ErrNotFound) {
		h.handleError(w, r, err)
		return
	}
	if existing != nil && p.Modified.Before(existing.Modified) {
		writeProblem(w, http.StatusConflict, problemProgressionDate)
		return
	}
	if err := h.progression.SetProgression(r.Context(), user, id, p); err != nil {
		h.handleError(w, r, err)
		return
	}
	if readium {
		// Komga parity: the deployed endpoint answers 204 with no body.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	out, err := marshalProgression(p)
	if err != nil {
		h.handleError(w, r, err)
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

// writeProblem writes an RFC 7807 problem response with the registry title for
// the given problem type.
func writeProblem(w http.ResponseWriter, code int, typeURI string) {
	titles := map[string]string{
		problemProgressionInvalid: "Progression could not be updated due to an invalid payload.",
		problemProgressionDate:    "A more recent progression point is already available.",
	}
	body, _ := json.Marshal(struct {
		Type  string `json:"type"`
		Title string `json:"title"`
	}{typeURI, titles[typeURI]})
	w.Header().Set("Content-Type", mediaTypeProblem)
	w.WriteHeader(code)
	w.Write(body)
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
	return json.Marshal(progressionJSON{
		Title:       p.Title,
		Modified:    p.Modified.UTC().Format(time.RFC3339),
		Device:      deviceJSON{ID: p.Device.ID, Name: p.Device.Name},
		Progression: &v,
		References:  p.References,
	})
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
	var refs []string
	if fragments := doc.Locator.Locations.Fragments; len(fragments) > 0 {
		for _, f := range fragments {
			refs = append(refs, doc.Locator.Href+"#"+f)
		}
	} else if doc.Locator.Href != "" {
		refs = []string{doc.Locator.Href}
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
