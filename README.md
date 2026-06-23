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

Zero dependencies — only the Go standard library.

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
| `opds/opdshttp` | Embeddable `http.Handler`: routing, content negotiation, pagination, search. |

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
- Pagination via `Feed.Page(total, perPage, startIndex)`.

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

A note on **library lending in 1.2**: `opds:availability`/`holds`/`copies` are
standard in OPDS 2.0 but are *not* part of the official OPDS 1.2 RELAX NG schema
— they are a de-facto 1.x extension (Library Simplified/Palace). The library
emits them in both versions because real library clients rely on them; just be
aware that a 1.2 feed using them intentionally goes beyond the core 1.2 schema.

Not yet included: the OPDS Authentication document flow (the model leaves room
for it). Contributions welcome.

## References

- [OPDS 1.2 specification](https://specs.opds.io/opds-1.2)
- [OPDS 2.0 specification](https://drafts.opds.io/opds-2.0)
- [Readium Web Publication Manifest](https://readium.org/webpub-manifest/)
- [OpenSearch 1.1](https://github.com/dewitt/opensearch)
