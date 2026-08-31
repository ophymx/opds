package opdshttp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/internal/wire"
)

// ErrInvalidCredentials is the sentinel an Authenticator returns when the
// presented credentials are wrong. The handler maps it — and absent
// credentials — to a 401 response carrying the Authentication Document. Any
// other error from an Authenticator is treated as an internal failure.
var ErrInvalidCredentials = errors.New("opdshttp: invalid credentials")

// Authenticator validates HTTP Basic credentials for a catalog. Implementations
// are supplied by the application (a password file, a database, an upstream
// service); the library never stores credentials or users itself.
type Authenticator interface {
	// Authenticate validates the credentials and returns a stable, opaque
	// identity for the authenticated user (the username itself, a database
	// key, ...). Success requires a non-empty user and a nil error. Failed
	// authentication returns ErrInvalidCredentials (possibly wrapped); any
	// other error reports an internal failure and is not treated as a
	// credential rejection.
	Authenticate(username, password string) (user string, err error)
}

// StaticUsers returns an Authenticator that checks credentials against a
// fixed username → password map. The authenticated identity is the username.
//
// Passwords are digested at construction and only the SHA-256 digests are
// retained, so the per-request work — hashing the presented password and a
// constant-time compare of fixed-length digests — is independent of the
// stored credentials: timing reveals neither stored-password lengths nor
// whether a username exists. This is a deliberate fit for small deployments
// with a handful of users in a config file; for hashed-at-rest credentials
// (bcrypt, htpasswd), implement Authenticator over your hash scheme
// (returning ErrInvalidCredentials on mismatch) — the library keeps that
// dependency out of your build. Rate limiting and lockout, if needed, also
// belong in a wrapping Authenticator.
func StaticUsers(users map[string]string) Authenticator {
	m := make(staticUsers, len(users))
	for name, pw := range users {
		m[name] = sha256.Sum256([]byte(pw))
	}
	return m
}

type staticUsers map[string][sha256.Size]byte

// dummyDigest keeps the unknown-user path doing the same comparison work as
// the known-user path.
var dummyDigest = sha256.Sum256(nil)

func (u staticUsers) Authenticate(username, password string) (string, error) {
	want, ok := u[username]
	if !ok {
		want = dummyDigest
	}
	got := sha256.Sum256([]byte(password))
	if subtle.ConstantTimeCompare(want[:], got[:]) == 1 && ok {
		return username, nil
	}
	return "", ErrInvalidCredentials
}

// AuthDocument configures the OPDS Authentication Document the handler serves:
// as the body of every 401 response, and at {prefix}/auth (see AuthPath), which
// is the one route that never requires credentials. A document declaring
// nothing else declares the HTTP Basic flow, which clients such as Thorium and
// Cantook render as a native login dialog.
//
// It is an alias for the version-neutral opds.AuthDocument, which opdsclient
// parses on the other side of the protocol.
type AuthDocument = opds.AuthDocument

// WithAuth requires HTTP Basic authentication on every route except the
// Authentication Document itself. Requests with missing or invalid credentials
// receive 401 with a WWW-Authenticate Basic challenge (which satisfies clients
// such as KOReader and Foliate that speak only Basic) and the Authentication
// Document as the body (which lets clients such as Thorium and Cantook show a
// native login dialog). The authenticated identity is available to Source
// implementations via User. Feeds additionally advertise the document through
// an injected opds.RelAuthDocument link.
//
// Without this option the handler's behavior is unchanged: no authentication,
// no auth route, no injected link.
func WithAuth(a Authenticator, doc AuthDocument) Option {
	return func(h *Handler) {
		h.auth = a
		h.authDoc = doc
	}
}

// userKey is the context key the handler stores the authenticated identity under.
type userKey struct{}

// User returns the identity authenticated by the handler's Authenticator, if
// any. Source implementations that serve per-user content read it from the
// request context; in a handler without WithAuth it reports false.
func User(ctx context.Context) (string, bool) {
	u, ok := ctx.Value(userKey{}).(string)
	return u, ok && u != ""
}

// AuthPath returns the request path for the Authentication Document.
func AuthPath(prefix string) string {
	return strings.TrimRight(prefix, "/") + "/auth"
}

// AuthURL returns the Authentication Document path under this handler's prefix.
func (h *Handler) AuthURL() string { return AuthPath(h.prefix) }

// authenticate validates the request's Basic credentials, writing the 401 (or
// internal error) response itself when it reports false.
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (string, bool) {
	username, password, ok := r.BasicAuth()
	if !ok {
		h.writeUnauthorized(w, r)
		return "", false
	}
	user, err := h.auth.Authenticate(username, password)
	if errors.Is(err, ErrInvalidCredentials) {
		h.writeUnauthorized(w, r)
		return "", false
	}
	if err == nil && user == "" {
		err = errors.New("opdshttp: Authenticator returned empty user without error")
	}
	if err != nil {
		h.handleError(w, r, err)
		return "", false
	}
	return user, true
}

func (h *Handler) writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	realm := h.authDoc.Title
	if realm == "" {
		realm = "OPDS"
	}
	w.Header().Set("WWW-Authenticate", "Basic realm="+strconv.Quote(realm))
	h.writeAuthDocument(w, r, http.StatusUnauthorized)
}

func (h *Handler) writeAuthDocument(w http.ResponseWriter, r *http.Request, code int) {
	docURL := h.baseURL(r) + h.AuthURL()
	body, err := wire.MarshalAuthDocument(h.authDoc, docURL)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	w.Header().Set("Link", "<"+docURL+`>; rel="`+opds.RelAuthDocument+`"; type="`+opds.MediaTypeAuthDocument+`"`)
	w.Header().Set("Content-Type", opds.MediaTypeAuthDocument)
	w.WriteHeader(code)
	if r.Method == http.MethodHead {
		return
	}
	w.Write(body)
}
