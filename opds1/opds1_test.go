package opds1

import (
	"encoding/xml"
	"os"
	"path/filepath"
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

func TestPageStream(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	p := opds.NewPublication("urn:comic:1", "Comic #1").By("Artist").UpdatedAt(ts).
		OpenAccess("/dl/1.cbz", "application/vnd.comicbook+zip").
		Stream("/opds/page/1?page={pageNumber}&width={maxWidth}", "image/jpeg", 35).
		LastRead(10, ts)
	f := opds.NewFeed("urn:feed:comics", "Comics").At(ts).Add(*p)
	b, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	wants := []string{
		`xmlns:pse="http://vaemendis.net/opds-pse/ns"`,
		`rel="http://vaemendis.net/opds-pse/stream"`,
		`href="/opds/page/1?page={pageNumber}&amp;width={maxWidth}"`,
		`type="image/jpeg"`,
		`pse:count="35"`,
		`pse:lastRead="10"`,
		`pse:lastReadDate="2026-01-02T03:04:05Z"`,
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func TestPageStreamCountEmittedWhenZero(t *testing.T) {
	// pse:count is required by the spec (and by KOReader, which otherwise
	// renders only the first page), so even a zero count must appear.
	p := opds.NewPublication("urn:comic:1", "Comic").Stream("/p?page={pageNumber}", "image/jpeg", 0)
	f := opds.NewFeed("urn:feed:comics", "Comics").Add(*p)
	b, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `pse:count="0"`) {
		t.Errorf("pse:count missing for zero page count:\n%s", b)
	}
	if strings.Contains(string(b), "pse:lastRead") {
		t.Errorf("lastRead attributes should be omitted when unset:\n%s", b)
	}
}

func TestPSENamespaceOnlyWhenUsed(t *testing.T) {
	b, err := Marshal(sampleFeed())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "xmlns:pse") {
		t.Errorf("pse namespace declared on a feed without stream links:\n%s", b)
	}

	e, err := MarshalEntry(opds.NewPublication("urn:b1", "Book"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(e), "xmlns:pse") {
		t.Errorf("pse namespace declared on an entry without a stream link:\n%s", e)
	}
}

func TestPageStreamEntryDocument(t *testing.T) {
	p := opds.NewPublication("urn:comic:1", "Comic").
		Stream("/opds/page/1?page={pageNumber}", "image/jpeg", 12)
	b, err := MarshalEntry(p)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, w := range []string{
		`xmlns:pse="http://vaemendis.net/opds-pse/ns"`,
		`pse:count="12"`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("entry document missing %q\n---\n%s", w, out)
		}
	}
}

func TestPSENamespaceForGroupedPublications(t *testing.T) {
	p := opds.NewPublication("urn:comic:1", "Comic").
		Stream("/opds/page/1?page={pageNumber}", "image/jpeg", 12)
	f := opds.NewFeed("urn:feed:home", "Home")
	f.AddGroup(opds.Group{Title: "Comics", Publications: []opds.Publication{*p}})
	b, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `xmlns:pse=`) {
		t.Errorf("pse namespace missing for stream link inside a group:\n%s", b)
	}
}

// TestGoldenPageStreamFeed locks the exact serialization of a PSE-bearing
// acquisition feed against testdata/pse_feed.xml. Regenerate the golden file
// after an intentional encoding change with:
//
//	OPDS_UPDATE_GOLDEN=1 go test ./opds1 -run TestGoldenPageStreamFeed
func TestGoldenPageStreamFeed(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	comic := opds.NewPublication("urn:comic:vol1", "Example Comic, Vol. 1").
		By("Example Artist").In("en").UpdatedAt(ts).
		Summarize("An example comic.").
		Cover("/covers/vol1.jpg", "image/jpeg").
		Thumbnail("/covers/vol1-t.jpg", "image/jpeg").
		OpenAccess("/dl/vol1.cbz", "application/vnd.comicbook+zip").
		Stream("/opds/page/vol1?page={pageNumber}&width={maxWidth}", "image/jpeg", 35).
		LastRead(10, ts)
	unread := opds.NewPublication("urn:comic:vol2", "Example Comic, Vol. 2").
		By("Example Artist").In("en").UpdatedAt(ts).
		OpenAccess("/dl/vol2.cbz", "application/vnd.comicbook+zip").
		Stream("/opds/page/vol2?page={pageNumber}&width={maxWidth}", "image/jpeg", 42)
	f := opds.NewFeed("urn:feed:comics", "Comics").At(ts).
		Self("/opds/feed/comics", opds.MediaTypeAcquisition).Start("/opds/").
		Add(*comic, *unread)

	b, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "pse_feed.xml")
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
