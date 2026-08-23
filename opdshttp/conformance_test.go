package opdshttp_test

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
)

// loadSchema compiles the schema with the given $id, registering every
// vendored schema from this package and from opds2 (whose Readium webpub tree
// the authentication schema $refs) so resolution is fully offline.
func loadSchema(t *testing.T, id string) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
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
