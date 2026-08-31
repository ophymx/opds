// Package opdsclient consumes OPDS catalogs: it fetches and decodes feeds in
// either wire version, authenticates with them, and reads and writes per-user
// reading progression.
//
// It is the other side of opdshttp. Both work in the version-neutral opds
// model, so a caller writes one traversal and it runs against an OPDS 1.2
// Atom catalog and an OPDS 2.0 JSON one alike — the client negotiates the
// version, decodes whichever it is handed, and resolves every href in the
// result to an absolute URL so the caller can follow it without tracking base
// URLs itself.
//
// A Client is built for one catalog, given the URL of its root:
//
//	c, err := opdsclient.New("https://example.com/opds/",
//		opdsclient.WithBasicAuth("jane", "secret"))
//	feed, err := c.Root(ctx)
//
// Credentials are sent only to the catalog's own host, so following a link to
// a cover on a CDN or an acquisition on a partner site does not leak them.
//
// A Client is safe for concurrent use by multiple goroutines.
package opdsclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opds1"
	"github.com/ophymx/opds/opds2"
)

// maxDocumentSize bounds a decoded document. Feeds are text; a catalog serving
// more than this in one response is not one this client can usefully page
// through.
const maxDocumentSize = 32 << 20

// maxSmallDocument bounds the documents that are small by construction:
// Authentication Documents and progression documents. Anything larger is not
// one of them.
const maxSmallDocument = 64 << 10

// Client fetches from one OPDS catalog. Construct it with New.
type Client struct {
	base   *url.URL
	http   *http.Client
	user   string
	pass   string
	agent  string
	prefer opds.Version
	device opds.Device
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the underlying HTTP client, for callers that need their
// own transport, timeout, cookie jar or proxy. It defaults to a client with a
// 30-second timeout.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithBasicAuth sends HTTP Basic credentials, the flow OPDS authentication
// defines and every OPDS client implements. They are sent preemptively, which
// saves a challenge round-trip on every request, but only to the catalog's own
// host: a request to any other host is made anonymously.
//
// Discover whether a catalog wants credentials, and what to call them, from
// the Authentication Document — either by fetching it (AuthDocument) or from
// the Auth field of the *Error a 401 returns.
func WithBasicAuth(user, password string) Option {
	return func(c *Client) { c.user, c.pass = user, password }
}

// WithUserAgent sets the User-Agent header. Catalogs use it to tell clients
// apart in their logs; naming the application is good manners.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.agent = ua }
}

// WithVersion sets which wire version to ask for. The default is
// opds.Version2, whose Accept header still lists the 1.x types below it, so a
// 1.2-only catalog answers in Atom and is decoded just the same.
//
// Asking for opds.Version1 sends only the Atom types. That asymmetry is
// deliberate: enough deployed catalogs negotiate by looking for a substring in
// Accept that mentioning JSON at all is taken as a preference for it.
func WithVersion(v opds.Version) Option {
	return func(c *Client) { c.prefer = v }
}

// WithDevice names the device progression updates are recorded from, filling
// in SetProgression's Device when the caller leaves it empty. The id should be
// a URI, and a bare UUID is turned into one; see opds.Device.
func WithDevice(d opds.Device) Option {
	return func(c *Client) { c.device = d }
}

// New returns a Client for the catalog rooted at baseURL, which must be an
// absolute URL. Every href the client is given is resolved against it, so
// relative links taken from a decoded feed can be passed straight back in.
func New(baseURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("opdsclient: base URL: %w", err)
	}
	if !u.IsAbs() || u.Host == "" {
		return nil, fmt.Errorf("opdsclient: base URL %q is not absolute", baseURL)
	}
	c := &Client{
		base:   u,
		http:   &http.Client{Timeout: 30 * time.Second},
		prefer: opds.Version2,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// BaseURL returns the catalog root the client was built for.
func (c *Client) BaseURL() string { return c.base.String() }

// Root fetches the catalog's root feed.
func (c *Client) Root(ctx context.Context) (*opds.Feed, error) {
	return c.Feed(ctx, c.base.String())
}

// Feed fetches and decodes the feed at href, which may be relative to the
// catalog root. Every href in the result is absolute.
//
// A publication document served where a feed was expected — a catalog answering
// an entry URL, say — is decoded into a feed holding that one publication, so
// a caller need not know which it asked for.
func (c *Client) Feed(ctx context.Context, href string) (*opds.Feed, error) {
	body, u, version, err := c.fetchDocument(ctx, href, c.feedAccept())
	if err != nil {
		return nil, err
	}
	f, err := decodeFeed(body, version)
	if err != nil {
		return nil, fmt.Errorf("opdsclient: %s: %w", u, err)
	}
	resolveFeed(f, u)
	return f, nil
}

// Publication fetches and decodes a standalone publication document (an OPDS
// 1.2 entry document or an OPDS 2.0 publication document). Every href in the
// result is absolute.
func (c *Client) Publication(ctx context.Context, href string) (*opds.Publication, error) {
	body, u, version, err := c.fetchDocument(ctx, href, c.publicationAccept())
	if err != nil {
		return nil, err
	}
	p, err := decodePublication(body, version)
	if err != nil {
		return nil, fmt.Errorf("opdsclient: %s: %w", u, err)
	}
	resolvePublication(p, u)
	return p, nil
}

// Resource is an opened non-catalog resource: an acquisition download, a cover
// image, a streamed page. The caller must close Body.
type Resource struct {
	// Body is the resource's content.
	Body io.ReadCloser
	// Type is the response's media type, which may differ from the one the
	// feed advertised.
	Type string
	// Length is the response's Content-Length, or -1 when unknown.
	Length int64
	// URL is the URL the resource was finally read from, after redirects.
	URL string
}

// Open fetches an arbitrary resource — an acquisition link, an image, a page —
// without decoding it. The caller closes Resource.Body.
func (c *Client) Open(ctx context.Context, href string) (*Resource, error) {
	resp, u, err := c.do(ctx, http.MethodGet, href, "*/*", "", nil)
	if err != nil {
		return nil, err
	}
	return &Resource{
		Body:   resp.Body,
		Type:   resp.Header.Get("Content-Type"),
		Length: resp.ContentLength,
		URL:    u.String(),
	}, nil
}

// Page opens one page of a publication advertised with OPDS-PSE page
// streaming. The page number is zero-based, as the extension specifies, and
// maxWidth is the client's maximum desired image width in pixels; pass 0 to
// leave it to the server.
func (c *Client) Page(ctx context.Context, ps *opds.PageStream, page, maxWidth int) (*Resource, error) {
	href, err := PageURL(ps, page, maxWidth)
	if err != nil {
		return nil, err
	}
	return c.Open(ctx, href)
}

// PageURL expands an OPDS-PSE stream template for one page. The page number is
// zero-based; maxWidth is dropped from the template when 0.
func PageURL(ps *opds.PageStream, page, maxWidth int) (string, error) {
	if ps == nil || ps.Href == "" {
		return "", errors.New("opdsclient: publication has no page stream")
	}
	if page < 0 || (ps.PageCount > 0 && page >= ps.PageCount) {
		return "", fmt.Errorf("opdsclient: page %d out of range [0, %d)", page, ps.PageCount)
	}
	href := strings.ReplaceAll(ps.Href, "{pageNumber}", fmt.Sprint(page))
	width := ""
	if maxWidth > 0 {
		width = fmt.Sprint(maxWidth)
	}
	return strings.ReplaceAll(href, "{maxWidth}", width), nil
}

// fetchDocument performs the request and reads the body, reporting which OPDS
// version the response turned out to be.
func (c *Client) fetchDocument(ctx context.Context, href, accept string) ([]byte, *url.URL, opds.Version, error) {
	resp, u, err := c.do(ctx, http.MethodGet, href, accept, "", nil)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	body, err := readBody(resp.Body, maxDocumentSize)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("opdsclient: %s: %w", u, err)
	}
	version, err := detectVersion(resp.Header.Get("Content-Type"), body)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("opdsclient: %s: %w", u, err)
	}
	return body, u, version, nil
}

// do issues one request, returning the response only when it succeeded; a
// failing status is read, classified and returned as an *Error with the
// response already closed.
func (c *Client) do(ctx context.Context, method, href, accept, contentType string, body []byte) (*http.Response, *url.URL, error) {
	u, err := c.resolve(href)
	if err != nil {
		return nil, nil, err
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return nil, nil, fmt.Errorf("opdsclient: %s %s: %w", method, u, err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.agent != "" {
		req.Header.Set("User-Agent", c.agent)
	}
	if c.sendCredentials(u) {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("opdsclient: %s %s: %w", method, u, err)
	}
	// Redirects may have moved the response; hrefs resolve against where it
	// actually came from.
	final := u
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		errBody, _ := readBody(resp.Body, maxErrorBody)
		return nil, final, newError(method, final.String(), resp, errBody)
	}
	return resp, final, nil
}

// resolve turns an href from a feed — or from the caller — into an absolute
// URL against the catalog root. A URI template is resolved up to its first
// placeholder, since percent-escaping the braces would destroy it.
func (c *Client) resolve(href string) (*url.URL, error) {
	if href == "" {
		return nil, errors.New("opdsclient: empty URL")
	}
	if found := strings.Contains(href, "{"); found {
		return nil, fmt.Errorf("opdsclient: %q is a URI template; expand it before requesting it", href)
	}
	ref, err := url.Parse(href)
	if err != nil {
		return nil, fmt.Errorf("opdsclient: %q: %w", href, err)
	}
	return c.base.ResolveReference(ref), nil
}

// sendCredentials reports whether the configured credentials may go to u: only
// to the catalog's own host, and never downgraded off a TLS-protected base.
func (c *Client) sendCredentials(u *url.URL) bool {
	if c.user == "" {
		return false
	}
	if !strings.EqualFold(u.Host, c.base.Host) {
		return false
	}
	return c.base.Scheme != "https" || u.Scheme == "https"
}

// feedAccept is the Accept header for a feed request. See WithVersion for why
// the two preferences are not symmetric.
func (c *Client) feedAccept() string {
	if c.prefer == opds.Version1 {
		return strings.Join([]string{
			opds.MediaTypeNavigation,
			opds.MediaTypeAcquisition,
			"application/atom+xml;q=0.9",
			"*/*;q=0.1",
		}, ", ")
	}
	return strings.Join([]string{
		opds.MediaTypeFeed,
		opds.MediaTypeNavigation + ";q=0.9",
		opds.MediaTypeAcquisition + ";q=0.9",
		"application/atom+xml;q=0.8",
		"*/*;q=0.1",
	}, ", ")
}

func (c *Client) publicationAccept() string {
	if c.prefer == opds.Version1 {
		return opds.MediaTypeEntry + ", application/atom+xml;q=0.9, */*;q=0.1"
	}
	return opds.MediaTypePublication + ", " + opds.MediaTypeEntry + ";q=0.9, */*;q=0.1"
}

// detectVersion decides which decoder a response needs. The media type is
// authoritative when it says anything useful; when it does not — plenty of
// catalogs serve feeds as text/xml or application/json — the first meaningful
// byte does, since the two formats cannot be confused.
func detectVersion(contentType string, body []byte) (opds.Version, error) {
	switch {
	case strings.Contains(contentType, "opds+json"),
		strings.Contains(contentType, "opds-publication+json"):
		return opds.Version2, nil
	case strings.Contains(contentType, "atom+xml"), strings.Contains(contentType, "opds-catalog"):
		return opds.Version1, nil
	}
	switch firstByte(body) {
	case '{':
		return opds.Version2, nil
	case '<':
		return opds.Version1, nil
	}
	return 0, fmt.Errorf("%w (content type %q)", ErrUnrecognizedFormat, contentType)
}

func firstByte(b []byte) byte {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF}) // a UTF-8 BOM, which some catalogs emit
	for _, ch := range b {
		switch ch {
		case ' ', '\t', '\r', '\n':
		default:
			return ch
		}
	}
	return 0
}

// decodeFeed decodes a feed, accepting a lone publication document in its
// place and wrapping it as a one-publication feed.
func decodeFeed(body []byte, version opds.Version) (*opds.Feed, error) {
	var (
		f   *opds.Feed
		err error
	)
	if version == opds.Version2 {
		f, err = opds2.Unmarshal(body)
	} else {
		f, err = opds1.Unmarshal(body)
	}
	if err == nil {
		return f, nil
	}
	p, perr := decodePublication(body, version)
	if perr != nil {
		return nil, err
	}
	return &opds.Feed{ID: p.ID, Title: p.Title, Updated: p.Updated, Publications: []opds.Publication{*p}}, nil
}

func decodePublication(body []byte, version opds.Version) (*opds.Publication, error) {
	if version == opds.Version2 {
		return opds2.UnmarshalPublication(body)
	}
	return opds1.UnmarshalEntry(body)
}

// readBody reads at most limit bytes, reporting an error rather than silently
// truncating a document into something that would fail to parse for the wrong
// reason.
func readBody(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("response larger than %d bytes", limit)
	}
	return b, nil
}
