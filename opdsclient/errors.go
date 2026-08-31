package opdsclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/internal/wire"
)

// The conditions a catalog reports that a caller is likely to branch on. Every
// failing request returns an *Error wrapping one of these (or the transport's
// own error), so they are matched with errors.Is:
//
//	if errors.Is(err, opdsclient.ErrUnauthorized) { ... }
//
// A 404 wraps opds.ErrNotFound, the sentinel the rest of the library already
// uses for a missing feed or publication.
var (
	// ErrUnauthorized reports a 401: credentials are missing or wrong. The
	// *Error carries the catalog's Authentication Document in Auth when the
	// response supplied one, which is how a client learns what to ask its
	// user for.
	ErrUnauthorized = errors.New("opdsclient: authentication required")
	// ErrForbidden reports a 403 the progression vocabulary does not explain.
	ErrForbidden = errors.New("opdsclient: forbidden")
	// ErrUnrecognizedFormat reports a response that is neither an OPDS 1.2
	// feed nor an OPDS 2.0 one.
	ErrUnrecognizedFormat = errors.New("opdsclient: unrecognized document format")

	// The four failures the OPDS Progression draft defines, distinguished by
	// the problem type the server reports alongside the status.
	//
	// ErrProgressionStale is the one worth handling: the position on the
	// server is newer than the one submitted, so the local position should be
	// replaced by a fresh GET rather than retried.
	ErrProgressionStale         = errors.New("opdsclient: a more recent progression is already stored")
	ErrProgressionInvalid       = errors.New("opdsclient: progression rejected as an invalid payload")
	ErrProgressionIncorrectUser = errors.New("opdsclient: progression belongs to another user")
	ErrProgressionLocked        = errors.New("opdsclient: progression can no longer be updated")
)

// Error is the failure of a single request to a catalog. It carries what the
// response said — the status, the RFC 7807 problem body when there was one,
// the Authentication Document when the catalog challenged — and unwraps to the
// sentinel that classifies it.
type Error struct {
	// Op is the HTTP method attempted.
	Op string
	// URL is the request URL.
	URL string
	// StatusCode and Status are the response's.
	StatusCode int
	Status     string
	// Problem is the parsed problem body, when the response carried one.
	Problem *Problem
	// Auth is the Authentication Document a 401 carried, when it did.
	Auth *opds.AuthDocument
	// Body is the first part of an unparsed error body, for diagnosis.
	Body string

	// authLink is the Authentication Document URL a challenge advertised in
	// its Link header, the other place OPDS allows it to appear.
	authLink string
	kind     error
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "opdsclient: %s %s: %s", e.Op, e.URL, e.Status)
	switch {
	case e.Problem != nil && e.Problem.Detail != "":
		fmt.Fprintf(&b, ": %s", e.Problem.Detail)
	case e.Problem != nil && e.Problem.Title != "":
		fmt.Fprintf(&b, ": %s", e.Problem.Title)
	case e.Body != "":
		fmt.Fprintf(&b, ": %s", e.Body)
	}
	return b.String()
}

// Unwrap returns the sentinel classifying the failure, so errors.Is matches it.
func (e *Error) Unwrap() error { return e.kind }

// Problem is an RFC 7807 problem detail, the body the Progression draft
// requires on every error it defines.
type Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	Status   int    `json:"status,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// Registry error types from the OPDS Progression draft.
const (
	problemProgressionInvalid = "https://registry.opds.io/error#progression-invalid-payload"
	problemProgressionDate    = "https://registry.opds.io/error#progression-date"
	problemProgressionUser    = "https://registry.opds.io/error#progression-incorrect-user"
	problemProgressionLocked  = "https://registry.opds.io/error#progression-locked"
)

// maxErrorBody bounds how much of an error response is read: problem details
// and Authentication Documents are small, and an error page need not be kept.
const maxErrorBody = 64 << 10

// newError builds the Error for a failing response, parsing whatever the body
// turns out to be. body has already been read and truncated by the caller.
func newError(op, url string, resp *http.Response, body []byte) *Error {
	e := &Error{
		Op:         op,
		URL:        url,
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
	}
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(ct, "problem+json"):
		var p Problem
		if json.Unmarshal(body, &p) == nil {
			e.Problem = &p
		}
	case strings.Contains(ct, "opds-authentication+json"):
		if doc, err := wire.ParseAuthDocument(body); err == nil {
			e.Auth = doc
		}
	}
	if e.Problem == nil && e.Auth == nil {
		e.Body = strings.TrimSpace(string(body))
		if len(e.Body) > 200 {
			e.Body = e.Body[:200] + "…"
		}
	}
	if resp.StatusCode == http.StatusUnauthorized {
		e.authLink = parseLinkHeader(resp.Header.Get("Link"), opds.RelAuthDocument)
	}
	e.kind = classify(resp.StatusCode, e.Problem)
	return e
}

// classify maps a status and problem type onto the sentinel a caller matches.
// The problem type is what separates the progression failures from each other:
// two of them share 403, and 400 and 409 mean something specific only when the
// registry type says so.
func classify(status int, p *Problem) error {
	typ := ""
	if p != nil {
		typ = p.Type
	}
	switch typ {
	case problemProgressionInvalid:
		return ErrProgressionInvalid
	case problemProgressionDate:
		return ErrProgressionStale
	case problemProgressionUser:
		return ErrProgressionIncorrectUser
	case problemProgressionLocked:
		return ErrProgressionLocked
	}
	switch status {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusNotFound, http.StatusGone:
		return opds.ErrNotFound
	}
	return nil
}
