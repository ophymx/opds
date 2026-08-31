// Package opds provides a version-neutral domain model and helpers for
// building OPDS (Open Publication Distribution System) catalog services.
//
// OPDS is a syndication format for electronic publications. Two wire formats
// are in wide use:
//
//   - OPDS 1.2, an Atom (XML) based format. Universally supported by readers
//     such as Calibre, KOReader, Thorium, FBReader and many others.
//   - OPDS 2.0, a JSON format built on the Readium Web Publication Manifest.
//
// Both express the same conceptual model, so this library models a catalog
// once, with version-neutral types, and lets pluggable codecs serialize to and
// from either format:
//
//   - Package opds             the domain model, constants and builders (this package).
//   - Package opds/opds1       encodes and decodes OPDS 1.2 (Atom XML).
//   - Package opds/opds2       encodes and decodes OPDS 2.0 (JSON).
//   - Package opds/opensearch  OpenSearch description documents (1.x search).
//   - Package opds/opdshttp    an embeddable http.Handler serving a catalog.
//   - Package opds/opdsclient  an HTTP client consuming one.
//   - Package opds/progstore   a durable store for reading progression.
//
// To expose a catalog you implement the Source interface (and, optionally,
// Searcher) and hand it to opds/opdshttp, or drive the encoders directly. To
// consume one, point opds/opdsclient at its root: it negotiates the version,
// decodes whichever it is served, and hands back the same neutral types, so a
// caller writes one traversal for both formats.
package opds
