package opdshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ophymx/opds"
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

// AuthDocument configures the OPDS Authentication Document the handler serves:
// as the body of every 401 response, and at {prefix}/auth (see AuthPath), which
// is the one route that never requires credentials. The document declares the
// HTTP Basic flow, which clients such as Thorium and Cantook render as a native
// login dialog. See https://drafts.opds.io/authentication-for-opds-1.0.html.
type AuthDocument struct {
	// ID is the document's canonical URL. Optional: it defaults to the
	// handler's auth document URL, made absolute from the request.
	ID string
	// Title names the catalog access is being requested for. Required; it is
	// also used as the Basic realm in the WWW-Authenticate challenge.
	Title string
	// Description optionally tells the user how to authenticate
	// (e.g. "Enter your library card number and PIN.").
	Description string
	// LoginLabel and PasswordLabel are optional alternate labels for the
	// credential fields (e.g. "Library card" and "PIN"). Empty means the
	// client shows its defaults.
	LoginLabel    string
	PasswordLabel string
	// Links are optional associated resources: rel "logo" (an image type),
	// "help" (a page or mailto: URL), and "register".
	Links []opds.Link
}

// Wire shapes of the Authentication Document and its members.
type (
	authDocJSON struct {
		ID             string         `json:"id"`
		Title          string         `json:"title"`
		Description    string         `json:"description,omitempty"`
		Links          []authLinkJSON `json:"links,omitempty"`
		Authentication []authFlowJSON `json:"authentication"`
	}
	authFlowJSON struct {
		Type   string          `json:"type"`
		Labels *authLabelsJSON `json:"labels,omitempty"`
	}
	authLabelsJSON struct {
		Login    string `json:"login,omitempty"`
		Password string `json:"password,omitempty"`
	}
	authLinkJSON struct {
		Rel   string `json:"rel,omitempty"`
		Href  string `json:"href"`
		Type  string `json:"type,omitempty"`
		Title string `json:"title,omitempty"`
	}
)

// marshal renders the document with the given id filled in when the configured
// ID is empty.
func (d AuthDocument) marshal(id string) ([]byte, error) {
	if d.ID != "" {
		id = d.ID
	}
	flow := authFlowJSON{Type: opds.AuthFlowBasic}
	if d.LoginLabel != "" || d.PasswordLabel != "" {
		flow.Labels = &authLabelsJSON{Login: d.LoginLabel, Password: d.PasswordLabel}
	}
	var links []authLinkJSON
	for _, l := range d.Links {
		links = append(links, authLinkJSON{Rel: l.Rel, Href: l.Href, Type: l.Type, Title: l.Title})
	}
	return json.Marshal(authDocJSON{
		ID:             id,
		Title:          d.Title,
		Description:    d.Description,
		Links:          links,
		Authentication: []authFlowJSON{flow},
	})
}

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
	body, err := h.authDoc.marshal(docURL)
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
