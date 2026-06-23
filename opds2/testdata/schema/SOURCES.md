# OPDS 2.0 conformance schemas

These are the official JSON Schemas used by `conformance_test.go` to validate
the encoder's OPDS 2.0 output. They are vendored so the tests run offline.

## Provenance

| Files | Source | Retrieved |
|---|---|---|
| `*.schema.json` | [`opds-community/specs`](https://github.com/opds-community/specs/tree/master/schema) `schema/` | 2026-06-22 |
| `webpub/**` | [`readium/webpub-manifest`](https://github.com/readium/webpub-manifest/tree/master/schema) `schema/` | 2026-06-22 |

The OPDS feed/publication schemas `$ref` the Readium Web Publication Manifest
schemas, which is why both sets are vendored. Each file is registered with the
compiler under its own `$id`, so `$ref` resolution never touches the network.

## Refreshing

Re-download from the same paths, preserving the `webpub/` subdirectory layout
(including `webpub/extensions/**`). No local modifications are made to these
files.
