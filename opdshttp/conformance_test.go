package opdshttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opdshttp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// These tests validate the handler's emitted Authentication Documents and
// Progression Documents against the official JSON Schemas published with the
// specs, vendored under testdata/schema (see testdata/schema/SOURCES.md for
// provenance and the pinned draft revision), and lock the exact serializations
// with golden files.

const (
	authSchemaID        = "https://drafts.opds.io/schema/authentication.schema.json"
	progressionSchemaID = "https://drafts.opds.io/schema/progression.schema.json"
	feedSchemaID        = "https://specs.opds.io/schema/feed.schema.json"
)

// loadSchema compiles the schema with the given $id, registering every
// vendored schema from this package and from opds2 (whose Readium webpub tree
// the authentication schema $refs) so resolution is fully offline.
func loadSchema(t *testing.T, id string) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	// The library's built-in "uri-template" check rejects RFC 6570 operator
	// expressions such as the canonical OPDS 2.0 search link's {?q}. Register
	// the same corrected check opds2's conformance tests use, so the feed
	// schema's format assertion does not produce a false negative.
	c.RegisterFormat(&jsonschema.Format{Name: "uri-template", Validate: validateURITemplate})
	for _, root := range []string{
		filepath.Join("testdata", "schema"),
		filepath.Join("..", "opds2", "testdata", "schema"),
	} {
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
			sid, _ := doc.(map[string]any)["$id"].(string)
			if sid == "" {
				t.Fatalf("%s: missing $id", path)
			}
			return c.AddResource(sid, doc)
		})
		if err != nil {
			t.Fatalf("loading schemas from %s: %v", root, err)
		}
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatalf("compiling %s: %v", id, err)
	}
	return sch
}

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

// checkGolden locks bytes against testdata/<name>. Regenerate after an
// intentional encoding change with:
//
//	OPDS_UPDATE_GOLDEN=1 go test ./opdshttp
func checkGolden(t *testing.T, name string, b []byte) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if os.Getenv("OPDS_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(want) {
		t.Errorf("output differs from %s\n--- got ---\n%s\n--- want ---\n%s", golden, b, want)
	}
}

func newAuthServerWithDoc(doc opdshttp.AuthDocument) http.Handler {
	return opdshttp.New(memSource{prefix: "/opds"},
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, doc),
	)
}

func TestConformanceAuthDocument(t *testing.T) {
	sch := loadSchema(t, authSchemaID)
	h := newAuthServer()

	// The document served at the auth route and the 401 body must both conform.
	if w := get(t, h, "/opds/auth", ""); w.Code != 200 {
		t.Fatalf("auth route code = %d", w.Code)
	} else {
		validate(t, sch, w.Body.Bytes())
	}
	if w := get(t, h, "/opds/", ""); w.Code != 401 {
		t.Fatalf("challenge code = %d", w.Code)
	} else {
		validate(t, sch, w.Body.Bytes())
	}
}

func TestConformanceProgressionDocument(t *testing.T) {
	sch := loadSchema(t, progressionSchemaID)
	h := newProgressionServer()

	// Both the PUT echo and the subsequent GET must conform.
	w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", validProgression)
	if w.Code != 201 {
		t.Fatalf("PUT code = %d", w.Code)
	}
	validate(t, sch, w.Body.Bytes())

	w = do(t, h, http.MethodGet, "/opds/progression/urn:b1", "")
	if w.Code != 200 {
		t.Fatalf("GET code = %d", w.Code)
	}
	validate(t, sch, w.Body.Bytes())
}

func TestGoldenAuthDocument(t *testing.T) {
	// A configured ID keeps the document independent of the request host.
	doc := testAuthDoc()
	doc.ID = "https://books.example.com/opds/auth"
	h := newAuthServerWithDoc(doc)
	w := get(t, h, "/opds/auth", "")
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	checkGolden(t, "auth_document.json", w.Body.Bytes())
}

func TestGoldenProgressionDocuments(t *testing.T) {
	h := newProgressionServer()
	if w := do(t, h, http.MethodPut, "/opds/progression/urn:b1", validProgression); w.Code != 201 {
		t.Fatalf("PUT code = %d", w.Code)
	}
	checkGolden(t, "progression.json", do(t, h, http.MethodGet, "/opds/progression/urn:b1", "").Body.Bytes())
	checkGolden(t, "progression_readium.json", doReadium(t, h, http.MethodGet, "/opds/progression/urn:b1", "").Body.Bytes())
}

// The injected progression links — including the draft's authenticate hint,
// which lands in link properties — must keep the 2.0 feed schema-valid.
func TestConformanceProgressionLinksInFeed(t *testing.T) {
	sch := loadSchema(t, feedSchemaID)
	w := do(t, newProgressionServer(), http.MethodGet, "/opds/feed/new?version=2", "")
	if w.Code != 200 {
		t.Fatalf("feed code = %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"authenticate"`)) {
		t.Fatalf("feed carries no authenticate hint:\n%s", w.Body.String())
	}
	validate(t, sch, w.Body.Bytes())
}

// validateURITemplate performs a lightweight RFC 6570 check: every expression
// is brace-balanced and, if it carries an operator, the operator is one of the
// defined characters. This accepts level 1-4 templates (including {?query}).
// It mirrors the check in opds2's conformance tests, which cannot be shared
// across the two test packages.
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

// The handler must never serve a Progression Document that fails the official
// schema, whatever reached the store: through the lenient Cantook alias, or
// written directly by an application whose own data is looser than the draft.
func TestConformanceProgressionOutputFromLooseStore(t *testing.T) {
	sch := loadSchema(t, progressionSchemaID)
	for _, tc := range []struct {
		name   string
		device opds.Device
		refs   []string
	}{
		{"no device at all", opds.Device{}, nil},
		{"bare uuid id", opds.Device{ID: "550e8400-e29b-41d4-a716-446655440000", Name: "Komga"}, nil},
		{"opaque id", opds.Device{ID: "device-123", Name: "Aldiko"}, []string{"c1.html#p1"}},
		{"id needing escaping", opds.Device{ID: "a b/c?d", Name: "Odd"}, nil},
		{"named but unidentified", opds.Device{Name: "Anonymous"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := opdshttp.NewMemProgressionStore()
			if err := store.SetProgression(context.Background(), "user-1", "urn:b1", &opds.Progression{
				Progression: 0.5,
				Modified:    time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
				Device:      tc.device,
				References:  tc.refs,
			}); err != nil {
				t.Fatal(err)
			}
			h := opdshttp.New(memSource{prefix: "/opds"},
				opdshttp.WithPrefix("/opds"),
				opdshttp.WithAuth(staticAuth{"jane", "secret", "user-1"}, testAuthDoc()),
				opdshttp.WithProgression(store),
			)
			w := do(t, h, http.MethodGet, "/opds/progression/urn:b1", "")
			if w.Code != 200 {
				t.Fatalf("code = %d", w.Code)
			}
			validate(t, sch, w.Body.Bytes())
		})
	}
}

// The same invariant reached through the wire: a Cantook PUT that omits the
// device (and whose locator href already carries a fragment) must still leave
// the record servable as a valid Progression Document.
func TestConformanceProgressionAfterCantookPut(t *testing.T) {
	sch := loadSchema(t, progressionSchemaID)
	h := newProgressionServer()
	body := `{"modified":"2026-08-01T10:00:00Z","locator":{"href":"c1.html#x","locations":{"totalProgression":0.5,"fragments":["p1"]}}}`
	if w := doReadium(t, h, http.MethodPut, "/opds/progression/urn:b1", body); w.Code != http.StatusNoContent {
		t.Fatalf("readium PUT code = %d: %s", w.Code, w.Body.String())
	}
	w := do(t, h, http.MethodGet, "/opds/progression/urn:b1", "")
	validate(t, sch, w.Body.Bytes())

	// The href's own fragment must not have been concatenated with the
	// locator's, which would yield "c1.html#x#p1".
	var doc struct{ References []string }
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.References) != 1 || doc.References[0] != "c1.html#p1" {
		t.Errorf("references = %v, want [c1.html#p1]", doc.References)
	}
}
