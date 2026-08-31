# opds

A Go library for building, implementing, and embedding [OPDS](https://opds.io)
(Open Publication Distribution System) catalog services — the standard ebook
readers and library systems use to browse, search, and acquire publications.

You model your catalog **once**, with version-neutral types, and the library
serializes it to either supported wire format, chosen by content negotiation:

| Version | Format | Status | Clients |
|---|---|---|---|
| **OPDS 1.2** | Atom (XML) | stable, universal | Calibre, KOReader, Thorium, FBReader, Foliate, Aldiko, Moon+, … |
| **OPDS 2.0** | JSON (Readium Web Publication Manifest) | current draft | Thorium, Readium toolkits, Palace/SimplyE, newer apps |

No runtime dependencies — the library itself uses only the Go standard
library. (A single test-only dependency,
[`santhosh-tekuri/jsonschema`](https://github.com/santhosh-tekuri/jsonschema),
powers the JSON conformance tests; it never appears in your builds.)

## Install

```sh
go get github.com/ophymx/opds
```

## Packages

| Package | Purpose |
|---|---|
| `opds` | Version-neutral domain model, constants, and fluent builders. |
| `opds/opds1` | Encodes the model to OPDS 1.2 (Atom XML). |
| `opds/opds2` | Encodes the model to OPDS 2.0 (JSON). |
| `opds/opensearch` | Generates OpenSearch description documents (1.x search). |
| `opds/opdshttp` | Embeddable `http.Handler`: routing, content negotiation, pagination, search, Basic authentication, progression sync. |
| `opds/progstore` | Durable file-backed `ProgressionStore` (plus OPDS-PSE last-read storage). |

## Quick start

Implement the `opds.Source` interface (and optionally `opds.Searcher`), hand it
to `opdshttp`, and you have a catalog that speaks both OPDS versions.

```go
package main

import (
	"context"
	"net/http"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opdshttp"
)

type catalog struct{}

func (catalog) Root(_ context.Context, _ opds.FeedRequest) (*opds.Feed, error) {
	return opds.NewFeed("urn:cat:root", "My Library").
		AddNav("All Books", "/opds/feed/all", opds.MediaTypeAcquisition, opds.RelSortNew), nil
}

func (catalog) Feed(_ context.Context, req opds.FeedRequest) (*opds.Feed, error) {
	if req.ID != "all" {
		return nil, opds.ErrNotFound
	}
	book := opds.NewPublication("urn:isbn:9780134190440", "The Go Programming Language").
		By("Alan Donovan").By("Brian Kernighan").
		In("en").ISBN("9780134190440").About("Computers").
		Cover("/covers/gopl.jpg", "image/jpeg").
		OpenAccess("/download/gopl.epub", "application/epub+zip")
	return opds.NewFeed("urn:cat:all", "All Books").Add(*book), nil
}

func (catalog) Publication(_ context.Context, id string) (*opds.Publication, error) {
	return nil, opds.ErrNotFound // serve standalone entry documents if you like
}

func main() {
	h := opdshttp.New(catalog{}, opdshttp.WithPrefix("/opds"))
	http.Handle("/opds/", h)
	http.ListenAndServe(":8080", nil)
}
```

```sh
curl localhost:8080/opds/                                    # OPDS 1.2 (default)
curl -H 'Accept: application/opds+json' localhost:8080/opds/ # OPDS 2.0
curl 'localhost:8080/opds/?version=2'                        # OPDS 2.0 via query
```

A complete runnable catalog with multiple feeds, subjects, and search lives in
[`examples/bookstore`](examples/bookstore): `go run ./examples/bookstore`.

## The domain model

Both OPDS versions express the same concepts, so you build them once:

- **`Feed`** — a navigation feed (entries link to sub-feeds via `AddNav`) or an
  acquisition feed (entries are publications via `Add`).
- **`Publication`** — bibliographic metadata, cover images, and acquisition links.
- **`Acquisition`** — how to obtain a publication: open-access, buy, borrow,
  sample, or subscribe, with prices, indirect acquisition (e.g. LCP → EPUB),
  and library-lending `availability` / `holds` / `copies`.
- **`Facet`** / **`Group`** — filtered/sorted views and labelled sections.
- Pagination via `Feed.Page(total, perPage, startIndex)` for the counters and
  `Feed.Paged(baseHref, page, hasNext)` for the prev/next links (build
  `baseHref` with `opdshttp.FeedPagePath` / `opdshttp.SearchPagePath`).

Fluent builders keep construction terse:

```go
p := opds.NewPublication("urn:isbn:9781503280786", "Moby-Dick").
	By("Herman Melville").In("en").ISBN("9781503280786").
	PublishedAt(pubDate).About("Fiction").
	Summarize("The voyage of the Pequod.").
	Cover("/covers/moby.jpg", "image/jpeg").
	Buy("/buy/moby", "application/epub+zip", "USD", 4.99)

// Library lending with availability, a hold queue, and copy counts:
pos := 5
p.Acquire(opds.Acquisition{
	Rel:          opds.AcquireBorrow,
	Href:         "/borrow/moby",
	Type:         "application/epub+zip",
	Availability: &opds.Availability{State: opds.StateUnavailable, Until: dueDate},
	Holds:        &opds.Holds{Total: 12, Position: &pos},
	Copies:       &opds.Copies{Total: 3, Available: 0},
})
```

## Search

If your `Source` also implements `opds.Searcher`, the handler automatically:

- serves search results at `{prefix}/search?q=...`,
- serves an OpenSearch description at `{prefix}/opensearch.xml` (for 1.x clients),
- advertises a `search` link in every feed (OpenSearch for 1.2, a templated
  link for 2.0).

```go
func (catalog) Search(_ context.Context, req opds.SearchRequest) (*opds.Feed, error) {
	results := db.Query(req.Terms) // also: req.Author, req.Title, req.Page
	f := opds.NewFeed("urn:cat:search", "Results")
	for _, b := range results {
		f.Add(toPublication(b))
	}
	return f, nil
}

func (catalog) SearchDescription() opds.SearchDescription {
	return opds.SearchDescription{ShortName: "My Library", Description: "Search the catalog"}
}
```

## Page streaming (OPDS-PSE)

For comics and manga, the [OPDS Page Streaming
Extension](https://anansi-project.github.io/docs/opds-pse/intro) lets clients
such as KOReader fetch one page image at a time instead of downloading a whole
CBZ. Advertise a stream on a publication with `Stream` (and optionally
`LastRead` for server-side resume, PSE 1.2):

```go
comic := opds.NewPublication("urn:comic:vol1", "Vol. 1").
	OpenAccess("/dl/vol1.cbz", "application/vnd.comicbook+zip").
	Stream(h.PageStreamURL("vol1"), "image/jpeg", 35). // 35 pages
	LastRead(10, lastReadAt)                           // resume at page 10
```

If your `Source` also implements `opds.PageSource`, the handler serves the
page images behind the template that `PageStreamURL` / `PageStreamPath`
builds (`{prefix}/page/{id}?page={pageNumber}&width={maxWidth}`):

```go
func (catalog) Page(_ context.Context, req opds.PageRequest) (*opds.PageImage, error) {
	img, err := openPage(req.ID, req.Number) // req.Number is zero-based
	if err != nil {
		return nil, opds.ErrNotFound
	}
	return &opds.PageImage{Type: "image/jpeg", Content: img}, nil
}
```

`req.MaxWidth` carries the client's desired maximum width; implementations may
ignore it and serve full-size images. PSE is an OPDS **1.x** extension with no
2.0 mapping: the `opds2` encoder omits it and 2.0 clients fall back to the
acquisition links.

## Authentication and progression sync

`WithAuth` puts the catalog behind HTTP Basic authentication. You supply the
credential check (an `Authenticator` — a password file, a database, an
upstream service); the library answers unauthenticated requests with both
things real clients need:

- a `WWW-Authenticate: Basic` challenge, which is all that header-only clients
  such as KOReader and Foliate require, and
- an [OPDS Authentication Document](https://drafts.opds.io/authentication-for-opds-1.0.html)
  as the 401 body (and at `{prefix}/auth`), which clients such as Thorium and
  Cantook render as a native login dialog.

```go
h := opdshttp.New(catalog{},
	opdshttp.WithPrefix("/opds"),
	opdshttp.WithAuth(myAuth{}, opdshttp.AuthDocument{
		Title:       "My Library", // also used as the Basic realm
		Description: "Sign in with your library account.",
	}),
	opdshttp.WithProgression(store), // requires WithAuth
)
```

The authenticated identity reaches your `Source` through the request context:
`user, ok := opdshttp.User(ctx)`. For small deployments,
`opdshttp.StaticUsers(map[string]string{...})` is a ready-made `Authenticator`
with constant-time comparison; anything hashed-at-rest (bcrypt, htpasswd) or
rate-limited is a small wrapper you write, keeping those dependencies out of
the library.

`WithProgression` adds per-user reading-position sync per the
[OPDS Progression 1.0 draft](https://drafts.opds.io/opds-progression-1.0.html):
every publication is advertised with a progression link, and the handler
serves GET/PUT at `{prefix}/progression/{id}` through the `ProgressionStore`
interface you supply, keyed by user and `Publication.ID`
(`NewMemProgressionStore` covers tests and examples; `progstore.New(dir)` is
a durable file-backed store that also persists OPDS-PSE last-read pages, so
one store carries all per-user reading state). The same endpoint and
store also answer the pre-spec Cantook rel
(`http://www.cantook.com/api/progression`, the Readium-locator shape) that
Komga and Stump serve and the Cantook/Aldiko client family consumes.

In 2.0 feeds the injected links carry the draft's `authenticate` hint
(`properties.authenticate`, pointing at the Authentication Document) so a
client can present credentials without first spending a 401. A `modified`
timestamp implausibly far ahead of the server is refused (see
`WithProgressionSkew`), so a reader whose clock is years fast cannot store a
position that no honest later update could beat. Errors follow
the draft's registry: `400` for an invalid payload, `409` when the stored
progression is more recent, and `403` when your store returns
`opdshttp.ErrProgressionIncorrectUser` or `opdshttp.ErrProgressionLocked` —
each with an RFC 7807 problem body, except the `401` challenge, which carries
the Authentication Document.

Try it live: `go run ./examples/bookstore --auth` (user `demo`, password
`demo`).

## Deployment notes

- **Serve authenticated catalogs over HTTPS.** Basic authentication sends
  credentials in cleartext, and reader clients will happily do so over plain
  HTTP.
- **Set `WithBaseURL` unless a trusted proxy fronts the handler.** Absolute
  URLs (the OpenSearch template, the Authentication Document `id`) are
  otherwise derived from the request's `Host`, `X-Forwarded-Proto`, and
  `X-Forwarded-Host` headers, which are client-controlled: fine behind a
  reverse proxy that overwrites them (the usual multi-user deployment), but a
  directly exposed handler should pin its canonical base with
  `opdshttp.WithBaseURL("https://books.example.com")`.
- Rate limiting and brute-force lockout are the `Authenticator`
  implementation's responsibility; the library only defines the boundary.

## Content negotiation

The handler picks the version per request, in priority order:

1. `?version=1` / `?version=2` (or `?f=atom` / `?f=json`) query parameter.
2. The `Accept` header (`application/opds+json` → 2.0; `atom+xml` → 1.2).
3. The configured default (`WithDefaultVersion`, defaulting to 1.2 for maximum
   client compatibility).

## URL layout

`opdshttp.Handler` serves this layout relative to its mount prefix. Use the
`FeedPath`, `PublicationPath`, and `SearchPath` helpers (or the handler's
`FeedURL` etc. methods) to build matching hrefs in your `Source`:

```
{prefix}/                  root feed
{prefix}/feed/{id}         a feed by id
{prefix}/publication/{id}  a single publication document
{prefix}/search            search results
{prefix}/opensearch.xml    OpenSearch description
{prefix}/page/{id}         a page image (OPDS-PSE, if the Source is a PageSource)
{prefix}/auth              the Authentication Document (with WithAuth)
{prefix}/progression/{id}  per-user reading progression, GET/PUT (with WithProgression)
```

## Using the encoders directly

You don't have to use `opdshttp`. The encoders turn a `*opds.Feed` into bytes,
so they drop into any framework or static-generation pipeline:

```go
xmlBytes, _ := opds1.Marshal(feed)  // OPDS 1.2 Atom
jsonBytes, _ := opds2.Marshal(feed) // OPDS 2.0 JSON
```

## Conformance

The encoders are tested against the **official** OPDS schemas, not just
hand-written expectations:

- **OPDS 2.0** output is validated against the official JSON Schemas from
  [`opds-community/specs`](https://github.com/opds-community/specs) and the
  Readium Web Publication Manifest schemas they reference. These tests are pure
  Go (using a vendored copy of the schemas) and run under `go test ./...`.
- **OPDS 1.2** output is validated against the official RELAX NG schema
  (`opds.rnc`) with [Jing](https://github.com/relaxng/jing-trang) — the
  reference validator behind the official OPDS validator. These tests need a JRE
  and skip automatically when Java/Jing are absent, so `go test ./...` still
  passes everywhere.
- **Authentication Documents and Progression Documents** emitted by `opdshttp`
  are validated against the official schemas from
  [drafts.opds.io](https://drafts.opds.io) (vendored, with the pinned draft
  revision recorded in `opdshttp/testdata/schema/SOURCES.md`), and their exact
  serializations are locked with golden files.

To run the 1.2 RELAX NG tests, which fetch Jing into `tools/` on first run:

```sh
scripts/conformance.sh
```

Vendored schema provenance (and the one documented upstream-typo fix in
`opds.rnc`) is recorded in the `testdata/schema/*/SOURCES.md` files.

## Status / scope

- OPDS **1.2** and **2.0** feeds, navigation and acquisition — validated against
  the official schemas (see [Conformance](#conformance)).
- Acquisition model including prices, nested indirect acquisition, and library
  lending (availability/holds/copies).
- Facets, groups, pagination, OpenSearch.
- HTTP Basic authentication via
  [Authentication for OPDS 1.0](https://drafts.opds.io/authentication-for-opds-1.0.html)
  (`opdshttp.WithAuth`): 401 responses carry both a `WWW-Authenticate: Basic`
  challenge (for clients like KOReader and Foliate) and an Authentication
  Document body (for clients like Thorium and Cantook), feeds advertise the
  document link, and the authenticated identity reaches your `Source` via
  `opdshttp.User`.
- Per-user reading-progression sync via the
  [OPDS Progression 1.0 draft](https://drafts.opds.io/opds-progression-1.0.html)
  (`opdshttp.WithProgression`, backed by a caller-supplied `ProgressionStore`):
  publications are advertised with a progression link (carrying the draft's
  `authenticate` hint in 2.0), and the handler serves GET/PUT with the draft's
  validation, staleness (409), refusal (403) and problem-details semantics.
  Requires `WithAuth` — progression is per-user by definition. The
  same endpoint and store also serve the pre-spec Cantook alias
  (`http://www.cantook.com/api/progression`, the Readium-locator shape that
  Komga and Stump serve and Cantook/Aldiko consume), translated with the
  deployed servers' status semantics.
- Page streaming for comics/manga via
  [OPDS-PSE](https://anansi-project.github.io/docs/opds-pse/intro) 1.2
  (`pse:count`, `pse:lastRead`, `pse:lastReadDate`) — 1.x feeds only, like the
  extension itself. The stream link's templated href (`{pageNumber}`)
  necessarily goes beyond Atom's strict URI datatype; the conformance tests
  document that this is the extension's only deviation.

A note on **library lending in 1.2**: `opds:availability`/`holds`/`copies` are
standard in OPDS 2.0 but are *not* part of the official OPDS 1.2 RELAX NG schema
— they are a de-facto 1.x extension (Library Simplified/Palace). The library
emits them in both versions because real library clients rely on them; just be
aware that a 1.2 feed using them intentionally goes beyond the core 1.2 schema.

Deliberately out of scope: user management (the `Authenticator` interface is
the boundary), auth flows beyond Basic, KOReader kosync, and annotation sync.
Contributions welcome.

## References

- [OPDS 1.2 specification](https://specs.opds.io/opds-1.2)
- [OPDS 2.0 specification](https://drafts.opds.io/opds-2.0)
- [OPDS-PSE specification](https://anansi-project.github.io/docs/opds-pse/specs/v1.2)
- [Authentication for OPDS 1.0](https://drafts.opds.io/authentication-for-opds-1.0.html)
- [OPDS Progression 1.0 draft](https://drafts.opds.io/opds-progression-1.0.html)
- [Readium Web Publication Manifest](https://readium.org/webpub-manifest/)
- [OpenSearch 1.1](https://github.com/dewitt/opensearch)

## License

[MIT](LICENSE) © 2026 Jeffrey T. Peckham
