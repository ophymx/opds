package opds1

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/ophymx/opds"
)

func sampleFeed() *opds.Feed {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	f := opds.NewFeed("urn:feed:root", "Library").At(ts).By("Library Inc").
		Self("/opds/feed/new", opds.MediaTypeAcquisition).
		Start("/opds/")
	f.Page(120, 50, 51)
	f.Next("/opds/feed/new?page=2", opds.MediaTypeAcquisition)
	f.Prev("/opds/feed/new?page=1", opds.MediaTypeAcquisition)
	f.AddFacet("Language", "English", "/opds/feed/new?lang=en", opds.MediaTypeAcquisition, 80, true)

	pos := 3
	p := opds.NewPublication("urn:book:1", "The Go Programming Language").
		By("Alan Donovan").By("Brian Kernighan").
		In("en").PublishedAt(ts).UpdatedAt(ts).From("Addison-Wesley").
		ISBN("9780134190440").
		Categorize("Computers", "COM051000", "https://bisg.org").
		Summarize("The authoritative guide.").
		Describe("<p>The authoritative guide.</p>").
		Cover("/covers/1.jpg", "image/jpeg").Thumbnail("/covers/1t.jpg", "image/jpeg").
		Buy("/buy/1", "application/epub+zip", "USD", 39.99)
	p.Acquire(opds.Acquisition{
		Rel: opds.AcquireBorrow, Href: "/borrow/1", Type: "application/epub+zip",
		Availability: &opds.Availability{State: opds.StateUnavailable, Until: ts},
		Holds:        &opds.Holds{Total: 12, Position: &pos},
		Copies:       &opds.Copies{Total: 3, Available: 0},
		Indirect: []opds.IndirectAcquisition{{
			Type:  "application/vnd.readium.lcp.license.v1.0+json",
			Child: []opds.IndirectAcquisition{{Type: "application/epub+zip"}},
		}},
	})
	return f.Add(*p)
}

func TestMarshalWellFormed(t *testing.T) {
	b, err := Marshal(sampleFeed())
	if err != nil {
		t.Fatal(err)
	}
	// Must be parseable XML.
	var v any
	if err := xml.Unmarshal(b, &v); err != nil {
		t.Fatalf("not well-formed XML: %v", err)
	}
	out := string(b)
	wants := []string{
		xml.Header,
		`xmlns="http://www.w3.org/2005/Atom"`,
		`xmlns:opds="http://opds-spec.org/2010/catalog"`,
		`xmlns:dcterms="http://purl.org/dc/terms/"`,
		`<opensearch:totalResults>120</opensearch:totalResults>`,
		`<opensearch:startIndex>51</opensearch:startIndex>`,
		`<dcterms:language>en</dcterms:language>`,
		`<dcterms:issued>2026-01-02</dcterms:issued>`,
		`<dcterms:identifier>urn:isbn:9780134190440</dcterms:identifier>`,
		`<category term="COM051000" scheme="https://bisg.org" label="Computers">`,
		`rel="http://opds-spec.org/image/thumbnail"`,
		`<opds:price currencycode="USD">39.99</opds:price>`,
		`opds:facetGroup="Language"`,
		`opds:activeFacet="true"`,
		`thr:count="80"`,
		`<opds:availability status="unavailable" until="2026-01-02T03:04:05Z">`,
		`<opds:holds total="12" position="3">`,
		`<opds:copies total="3" available="0">`,
		`<opds:indirectAcquisition type="application/vnd.readium.lcp.license.v1.0+json">`,
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func TestMarshalEntry(t *testing.T) {
	p := opds.NewPublication("urn:book:1", "Title").By("Author").OpenAccess("/d.epub", "application/epub+zip")
	b, err := MarshalEntry(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(b, new(any)); err != nil {
		t.Fatalf("not well-formed: %v", err)
	}
	if !strings.Contains(string(b), `rel="http://opds-spec.org/acquisition/open-access"`) {
		t.Errorf("entry missing acquisition link:\n%s", b)
	}
}

func TestNavigationFeed(t *testing.T) {
	f := opds.NewFeed("urn:root", "Root").
		AddNav("New Books", "/opds/feed/new", opds.MediaTypeAcquisition, opds.RelSortNew)
	if f.IsAcquisition() {
		t.Error("navigation feed misclassified as acquisition")
	}
	b, _ := Marshal(f)
	if !strings.Contains(string(b), `rel="http://opds-spec.org/sort/new"`) {
		t.Errorf("nav link missing:\n%s", b)
	}
}
