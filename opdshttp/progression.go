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
	if r.Method == http.MethodPut {
		h.putProgression(w, r, user, id)
		return
	}
	p, err := h.progression.Progression(r.Context(), user, id)
	if err != nil && !errors.Is(err, opds.ErrNotFound) {
		h.handleError(w, r, err)
		return
	}
	if p == nil {
		// The draft prescribes 200 with an empty payload over 404 when no
		// progression has been communicated yet.
		w.WriteHeader(http.StatusOK)
		return
	}
	body, err := marshalProgression(p)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	write(w, r, opds.MediaTypeProgression, body)
}

func (h *Handler) putProgression(w http.ResponseWriter, r *http.Request, user, id string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxProgressionBody))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, problemProgressionInvalid)
		return
	}
	p, err := unmarshalProgression(body)
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
