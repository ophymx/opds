package opdshttp

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/internal/wire"
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
		body, err = wire.MarshalReadiumProgression(p)
		ct = opds.MediaTypeProgressionReadium
	} else {
		body, err = wire.MarshalProgression(p)
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
		p, err = wire.ParseReadiumProgression(body)
	} else {
		p, err = parseProgression(body)
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
	out, err := wire.MarshalProgression(p)
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

// The progression documents themselves are encoded by internal/wire, shared
// with opdsclient so the two sides of the protocol cannot drift. Parsing is
// split from validation there: a server validates what it accepts, a client
// stays lenient about what it reads.

// parseProgression parses and validates a submitted Progression Document.
func parseProgression(body []byte) (*opds.Progression, error) {
	p, err := wire.ParseProgression(body)
	if err != nil {
		return nil, err
	}
	if err := wire.ValidateProgression(p); err != nil {
		return nil, err
	}
	return p, nil
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
