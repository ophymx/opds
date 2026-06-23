package opds1

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ophymx/opds"
)

// These tests validate the encoder's output against the official OPDS 1.2
// RELAX NG schema (schema/1.2/opds.rnc, which includes atom.rnc) using Jing,
// the reference RELAX NG validator and the engine behind the official OPDS
// validator. See testdata/schema/1.2/SOURCES.md for provenance.
//
// Jing requires a JRE. The tests skip when java or the Jing jar is unavailable,
// so `go test ./...` still passes in minimal environments. Run
// scripts/conformance.sh to fetch Jing and exercise these tests.

const rncPath = "testdata/schema/1.2/opds.rnc"

// findJing locates the Jing jar via OPDS_JING_JAR or the tools/ directory.
func findJing(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("java"); err != nil {
		t.Skip("java not found; skipping RELAX NG conformance (run scripts/conformance.sh)")
	}
	if p := os.Getenv("OPDS_JING_JAR"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		t.Fatalf("OPDS_JING_JAR=%q does not exist", p)
	}
	for _, pat := range []string{"../tools/jing.jar", "../tools/*/bin/jing.jar"} {
		if m, _ := filepath.Glob(pat); len(m) > 0 {
			return m[0]
		}
	}
	t.Skip("Jing jar not found; set OPDS_JING_JAR or run scripts/conformance.sh")
	return ""
}

// jingValidate runs Jing against doc using the compact-syntax OPDS schema.
// It returns Jing's diagnostic output (empty on success) and whether the
// document is valid.
func jingValidate(t *testing.T, jar string, doc []byte) (string, bool) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "opds-*.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(doc); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := exec.Command("java", "-jar", jar, "-c", rncPath, f.Name()).CombinedOutput()
	return string(out), err == nil
}

func mustValid(t *testing.T, doc []byte) {
	t.Helper()
	jar := findJing(t)
	if out, ok := jingValidate(t, jar, doc); !ok {
		t.Errorf("document failed OPDS 1.2 RELAX NG validation:\n%s\n---\n%s", out, doc)
	}
}

func at() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

func TestConformanceNavigationFeed(t *testing.T) {
	f := opds.NewFeed("urn:cat:root", "Catalog Root").At(at()).By("Library Inc").
		Self("/opds/", opds.MediaTypeNavigation).Start("/opds/").
		AddNav("New", "/opds/feed/new", opds.MediaTypeAcquisition, opds.RelSortNew).
		AddNav("Popular", "/opds/feed/pop", opds.MediaTypeAcquisition, opds.RelSortPopular)
	b, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	mustValid(t, b)
}

func TestConformanceAcquisitionFeed(t *testing.T) {
	b, err := Marshal(coreFeed())
	if err != nil {
		t.Fatal(err)
	}
	mustValid(t, b)
}

func TestConformanceEntryDocument(t *testing.T) {
	b, err := MarshalEntry(corePublication())
	if err != nil {
		t.Fatal(err)
	}
	mustValid(t, b)
}

// TestConformanceLendingIsExtension documents the boundary of the official OPDS
// 1.2 schema: library-lending elements (opds:availability/holds/copies) are NOT
// part of core 1.2 (they are standard only in OPDS 2.0, and a de-facto 1.x
// extension used by Library Simplified/Palace). This test confirms a lending
// feed fails strict validation, and that those three elements are the ONLY
// reason — i.e. everything else the encoder emits for lending is well-formed.
func TestConformanceLendingIsExtension(t *testing.T) {
	jar := findJing(t)
	b, _ := Marshal(lendingFeed())
	out, ok := jingValidate(t, jar, b)
	if ok {
		t.Skip("schema now accepts lending elements; revisit this test")
	}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if !strings.Contains(line, "error:") {
			continue
		}
		if !strings.Contains(line, "opds:availability") &&
			!strings.Contains(line, "opds:holds") &&
			!strings.Contains(line, "opds:copies") {
			t.Errorf("unexpected non-lending validation error:\n%s\n---\n%s", line, b)
		}
	}
}

// TestConformanceCatchesInvalid proves the harness has teeth: a buy link with
// no opds:price violates the OPDS schema and Jing must reject it.
func TestConformanceCatchesInvalid(t *testing.T) {
	jar := findJing(t)
	p := opds.NewPublication("urn:book:bad", "No Price").By("Author").
		Acquire(opds.Acquisition{Rel: opds.AcquireBuy, Href: "/buy", Type: "application/epub+zip"})
	f := opds.NewFeed("urn:cat:bad", "Bad").At(at()).Self("/opds/bad", opds.MediaTypeAcquisition).Add(*p)
	b, _ := Marshal(f)
	if _, ok := jingValidate(t, jar, b); ok {
		t.Errorf("expected validation failure for buy link without price, but it passed:\n%s", b)
	}
}

// coreFeed exercises every feature the official 1.2 schema defines: pagination,
// search, faceted navigation, categories, cover + thumbnail, summary, content,
// a buy link with price and nested indirect acquisition, and open access.
func coreFeed() *opds.Feed {
	f := opds.NewFeed("urn:cat:new", "New Releases").At(at()).By("Library Inc").
		Self("/opds/feed/new", opds.MediaTypeAcquisition).Start("/opds/").Page(120, 50, 1)
	f.Next("/opds/feed/new?page=2", opds.MediaTypeAcquisition)
	f.SearchLink("/opds/opensearch.xml", opds.MediaTypeOpenSearch, false)
	f.AddFacet("Language", "English", "/opds/feed/new?lang=en", opds.MediaTypeAcquisition, 80, true)
	f.AddFacet("Language", "French", "/opds/feed/new?lang=fr", opds.MediaTypeAcquisition, 40, false)
	return f.Add(*corePublication())
}

func corePublication() *opds.Publication {
	p := opds.NewPublication("urn:isbn:9780134190440", "The Go Programming Language").
		By("Alan Donovan").By("Brian Kernighan").
		In("en").UpdatedAt(at()).PublishedAt(at()).From("Addison-Wesley").
		ISBN("9780134190440").
		Categorize("Computers", "COM051000", "https://bisg.org").
		Summarize("The authoritative resource.").
		Describe("<p>The authoritative resource for the Go language.</p>").
		Cover("/covers/gopl.jpg", "image/jpeg").Thumbnail("/covers/gopl-t.jpg", "image/jpeg")
	p.Acquire(opds.Acquisition{
		Rel: opds.AcquireBuy, Href: "/buy/gopl", Type: "text/html",
		Prices: []opds.Price{{Currency: "USD", Value: 39.99}},
		Indirect: []opds.IndirectAcquisition{{
			Type:  "application/vnd.readium.lcp.license.v1.0+json",
			Child: []opds.IndirectAcquisition{{Type: "application/epub+zip"}},
		}},
	})
	p.OpenAccess("/dl/gopl.epub", "application/epub+zip")
	p.Link(opds.RelAlternate, "/opds/publication/gopl", opds.MediaTypeEntry)
	return p
}

// lendingFeed is coreFeed plus a borrow link carrying the lending extension
// (availability/holds/copies), used to document the schema boundary.
func lendingFeed() *opds.Feed {
	f := coreFeed()
	pos := 3
	f.Publications[0].Acquire(opds.Acquisition{
		Rel: opds.AcquireBorrow, Href: "/borrow/gopl", Type: "application/epub+zip",
		Availability: &opds.Availability{State: opds.StateUnavailable, Until: at()},
		Holds:        &opds.Holds{Total: 12, Position: &pos},
		Copies:       &opds.Copies{Total: 3, Available: 0},
	})
	return f
}
