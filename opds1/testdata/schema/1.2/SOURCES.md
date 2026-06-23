# OPDS 1.2 conformance schema (RELAX NG)

Used by `conformance_test.go` (via Jing) to validate the encoder's OPDS 1.2
Atom output.

## Provenance

| File | Source | Retrieved |
|---|---|---|
| `opds.rnc` | [`opds-community/specs`](https://github.com/opds-community/specs/blob/master/schema/1.2/opds.rnc) `schema/1.2/opds.rnc` | 2026-06-22 |
| `atom.rnc` | [`opds-community/opds-validator`](https://github.com/opds-community/opds-validator/blob/master/res/atom.rnc) `res/atom.rnc` | 2026-06-22 |

`opds.rnc` `include`s `atom.rnc`; the OPDS specs repo does not bundle the Atom
schema, so `atom.rnc` (the RFC 4287 RELAX NG grammar) is taken from the official
OPDS validator, which is the file `opds.rnc` is designed to include.

## Local modification

`opds.rnc` line 78 redefines `atomUri` to exclude characters invalid in an IRI.
The upstream pattern is:

```
atomUri = xsd:anyURI - xsd:string {pattern = '.*[ <>{}|^`"\nrt].*'}
```

The `\nrt` is a typo for `\n\r\t` (newline, carriage return, tab): without the
backslashes it excludes the literal letters **`r`** and **`t`**, so the upstream
schema wrongly rejects any URI containing `r` or `t` (e.g. `urn:...`,
`https://...`). We correct it to:

```
atomUri = xsd:anyURI - xsd:string {pattern = '.*[ <>{}|^`"\n\r\t].*'}
```

This is the only change, and it restores the schema's documented intent.

## What core 1.2 does NOT cover

The official schema's `atom:link` content model allows only `opds:price`,
`opds:indirectAcquisition`, foreign-namespace elements, or text. The
library-lending elements `opds:availability`, `opds:holds` and `opds:copies`
are therefore **not** part of core OPDS 1.2 — they are standard in OPDS 2.0 and
a de-facto 1.x extension. `TestConformanceLendingIsExtension` documents this.
