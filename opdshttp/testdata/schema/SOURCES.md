# Authentication and Progression conformance schemas

These are the official JSON Schemas used by `conformance_test.go` to validate
the Authentication Documents and Progression Documents the handler emits. They
are vendored so the tests run offline.

## Provenance

| Files | Source | Retrieved |
|---|---|---|
| `authentication.schema.json` | https://drafts.opds.io/schema/authentication.schema.json ([Authentication for OPDS 1.0](https://drafts.opds.io/authentication-for-opds-1.0.html)) | 2026-08-22 |
| `progression.schema.json` | https://drafts.opds.io/schema/progression.schema.json ([OPDS Progression 1.0 draft](https://drafts.opds.io/opds-progression-1.0.html)) | 2026-08-22 |

## Pinned revision

The Progression spec is a young draft (created 2026-01-27, revised four times
through 2026-02-23). The implementation and this schema were pinned against
the draft **as published on 2026-08-22** and re-diffed against it on
**2026-08-30** (schema byte-identical, prose unchanged); re-diff the draft
against the latter date before tagging a release.

## Refs

`authentication.schema.json` `$ref`s the Readium Web Publication Manifest
`link.schema.json`, which is already vendored (with its transitive refs) under
`../../../opds2/testdata/schema/`. Rather than duplicate that tree,
`conformance_test.go` registers the schemas from both directories with the
compiler under their `$id`s, so `$ref` resolution never touches the network.

## Refreshing

Re-download from the same URLs. No local modifications are made to these
files.
