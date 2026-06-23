package opds2

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ophymx/opds"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// These tests validate the encoder's output against the official OPDS 2.0 JSON
// Schemas published by the OPDS community (and the Readium Web Publication
// Manifest schemas they reference), vendored under testdata/schema. See
// testdata/schema/SOURCES.md for provenance and how to refresh them.

const (
	feedSchemaID = "https://specs.opds.io/schema/feed.schema.json"
	pubSchemaID  = "https://specs.opds.io/schema/publication.schema.json"
)

// loadSchemas builds a compiler with every vendored schema registered under its
// own $id, so $ref resolution is fully offline.
func loadSchemas(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	c := jsonschema.NewCompiler()
	// The library's built-in "uri-template" format check does not implement
	// RFC 6570 operator expressions (e.g. {?query}), which are valid and are
	// the canonical OPDS 2.0 search-link form. Register a correct check so the
	// schema's format assertion does not produce false negatives.
	c.RegisterFormat(&jsonschema.Format{Name: "uri-template", Validate: validateURITemplate})
	root := filepath.Join("testdata", "schema")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err != nil {
			return err
		}
		id, _ := doc.(map[string]any)["$id"].(string)
		if id == "" {
			t.Fatalf("%s: missing $id", path)
		}
		return c.AddResource(id, doc)
	})
	if err != nil {
		t.Fatalf("loading schemas: %v", err)
	}
	return c
}

func compile(t *testing.T, c *jsonschema.Compiler, id string) *jsonschema.Schema {
	t.Helper()
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatalf("compiling %s: %v", id, err)
	}
	return sch
}

// validate parses the marshaled bytes and validates them, reporting a readable
// error on failure.
func validate(t *testing.T, sch *jsonschema.Schema, b []byte) {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("instance is not valid JSON: %v", err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("does not conform to schema:\n%v\n---\n%s", err, b)
	}
}

// validateURITemplate performs a lightweight RFC 6570 check: every expression
// is brace-balanced and, if it carries an operator, the operator is one of the
// defined characters. This accepts level 1-4 templates (including {?query}).
func validateURITemplate(v any) error {
	s, ok := v.(string)
	if !ok {
		return nil // format applies to strings only
	}
	const operators = "+#./;?&=,!@|"
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '}':
			return fmt.Errorf("unexpected '}' at %d", i)
		case '{':
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return fmt.Errorf("no matching closing brace")
			}
			expr := s[i+1 : i+end]
			if expr == "" {
				return fmt.Errorf("empty expression")
			}
			if strings.IndexByte(operators, expr[0]) >= 0 {
				expr = expr[1:]
			}
			if expr == "" {
				return fmt.Errorf("operator without variable")
			}
			i += end
		}
	}
	return nil
}

func ts() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

func TestConformanceNavigationFeed(t *testing.T) {
	feedSchema := compile(t, loadSchemas(t), feedSchemaID)
	f := opds.NewFeed("urn:cat:root", "Catalog Root").At(ts()).
		Self("https://example.com/opds/", opds.MediaTypeFeed).
		AddNav("New", "https://example.com/opds/feed/new", opds.MediaTypeFeed, opds.RelSortNew).
		AddNav("Popular", "https://example.com/opds/feed/pop", opds.MediaTypeFeed, opds.RelSortPopular)
	b, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	validate(t, feedSchema, b)
}

func TestConformanceAcquisitionFeed(t *testing.T) {
	feedSchema := compile(t, loadSchemas(t), feedSchemaID)
	b, err := Marshal(richFeed())
	if err != nil {
		t.Fatal(err)
	}
	validate(t, feedSchema, b)
}

func TestConformancePublication(t *testing.T) {
	pubSchema := compile(t, loadSchemas(t), pubSchemaID)
	b, err := MarshalPublication(richPublication())
	if err != nil {
		t.Fatal(err)
	}
	validate(t, pubSchema, b)
}

func richFeed() *opds.Feed {
	f := opds.NewFeed("urn:cat:new", "New Releases").At(ts()).
		Self("https://example.com/opds/feed/new", opds.MediaTypeFeed).
		Page(120, 50, 1)
	f.Next("https://example.com/opds/feed/new?page=2", opds.MediaTypeFeed)
	f.SearchLink("https://example.com/opds/search{?query}", opds.MediaTypeFeed, true)
	f.AddFacet("Language", "English", "https://example.com/opds/feed/new?lang=en", opds.MediaTypeFeed, 80, false)
	f.AddFacet("Language", "French", "https://example.com/opds/feed/new?lang=fr", opds.MediaTypeFeed, 40, false)
	f.Add(*richPublication())
	return f
}

func richPublication() *opds.Publication {
	pos := 3
	p := opds.NewPublication("urn:isbn:9780134190440", "The Go Programming Language").
		Author(opds.Author{Name: "Alan Donovan", URI: "https://example.com/authors/donovan", SortAs: "Donovan, Alan"}).
		By("Brian Kernighan").
		In("en").UpdatedAt(ts()).PublishedAt(ts()).From("Addison-Wesley").
		ISBN("9780134190440").
		Categorize("Computers", "COM051000", "https://bisg.org").
		PartOf("Professional Computing", 1).
		Describe("The authoritative resource for the Go language.").
		Cover("https://example.com/covers/gopl.jpg", "image/jpeg")
	p.Images[0].Width, p.Images[0].Height = 800, 1200
	p.Buy("https://example.com/buy/gopl", "application/epub+zip", "USD", 39.99)
	p.Acquire(opds.Acquisition{
		Rel: opds.AcquireBorrow, Href: "https://example.com/borrow/gopl", Type: "application/epub+zip",
		Availability: &opds.Availability{State: opds.StateUnavailable, Since: ts(), Until: ts()},
		Holds:        &opds.Holds{Total: 12, Position: &pos},
		Copies:       &opds.Copies{Total: 3, Available: 0},
		Indirect: []opds.IndirectAcquisition{{
			Type:  "application/vnd.readium.lcp.license.v1.0+json",
			Child: []opds.IndirectAcquisition{{Type: "application/epub+zip"}},
		}},
	})
	p.Link(opds.RelSelf, "https://example.com/opds/publication/gopl", opds.MediaTypePublication)
	return p
}
