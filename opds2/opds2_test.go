package opds2

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ophymx/opds"
)

func sampleFeed() *opds.Feed {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	f := opds.NewFeed("urn:feed:root", "Library").At(ts).
		Self("/opds/feed/new", opds.MediaTypeFeed).Page(120, 50, 51)
	f.AddFacet("Language", "English", "/opds/feed/new?lang=en", opds.MediaTypeFeed, 80, false)
	pos := 3
	p := opds.NewPublication("urn:book:1", "The Go Programming Language").
		Author(opds.Author{Name: "Alan Donovan", URI: "/authors/donovan", SortAs: "Donovan, Alan"}).
		In("en").PublishedAt(ts).UpdatedAt(ts).From("Addison-Wesley").
		ISBN("9780134190440").Categorize("Computers", "COM051000", "https://bisg.org").
		PartOf("Go Books", 1).
		Describe("Guide.").Cover("/covers/1.jpg", "image/jpeg")
	p.Images[0].Width, p.Images[0].Height = 800, 1200
	p.Buy("/buy/1", "application/epub+zip", "USD", 39.99)
	p.Acquire(opds.Acquisition{
		Rel: opds.AcquireBorrow, Href: "/borrow/1", Type: "application/epub+zip",
		Availability: &opds.Availability{State: opds.StateUnavailable, Until: ts},
		Holds:        &opds.Holds{Total: 12, Position: &pos},
		Copies:       &opds.Copies{Total: 3, Available: 0},
	})
	return f.Add(*p)
}

func TestMarshalValidJSON(t *testing.T) {
	b, err := Marshal(sampleFeed())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	meta := doc["metadata"].(map[string]any)
	if meta["title"] != "Library" {
		t.Errorf("title = %v", meta["title"])
	}
	if meta["numberOfItems"].(float64) != 120 {
		t.Errorf("numberOfItems = %v", meta["numberOfItems"])
	}
	if meta["currentPage"].(float64) != 2 {
		t.Errorf("currentPage = %v, want 2", meta["currentPage"])
	}

	pubs := doc["publications"].([]any)
	if len(pubs) != 1 {
		t.Fatalf("got %d publications", len(pubs))
	}
	pub := pubs[0].(map[string]any)
	pmeta := pub["metadata"].(map[string]any)
	if pmeta["@type"] != "http://schema.org/Book" {
		t.Errorf("@type = %v", pmeta["@type"])
	}
	// author with extra fields marshals as an object.
	author := pmeta["author"].([]any)[0].(map[string]any)
	if author["name"] != "Alan Donovan" || author["sortAs"] != "Donovan, Alan" {
		t.Errorf("author = %v", author)
	}
	if pmeta["belongsTo"].(map[string]any)["series"].(map[string]any)["name"] != "Go Books" {
		t.Errorf("series missing: %v", pmeta["belongsTo"])
	}

	links := pub["links"].([]any)
	var borrow map[string]any
	for _, l := range links {
		lm := l.(map[string]any)
		if lm["rel"] == string(opds.AcquireBorrow) {
			borrow = lm
		}
	}
	if borrow == nil {
		t.Fatal("borrow link missing")
	}
	props := borrow["properties"].(map[string]any)
	if props["availability"].(map[string]any)["state"] != "unavailable" {
		t.Errorf("availability = %v", props["availability"])
	}
	copies := props["copies"].(map[string]any)
	if copies["available"].(float64) != 0 {
		t.Errorf("copies.available should be present and 0, got %v", copies["available"])
	}

	img := pub["images"].([]any)[0].(map[string]any)
	if img["width"].(float64) != 800 {
		t.Errorf("image width = %v", img["width"])
	}

	facets := doc["facets"].([]any)
	if len(facets) != 1 {
		t.Fatalf("got %d facet groups", len(facets))
	}
	fg := facets[0].(map[string]any)
	if fg["metadata"].(map[string]any)["title"] != "Language" {
		t.Errorf("facet group title = %v", fg["metadata"])
	}
}

func TestSubjectStringForm(t *testing.T) {
	p := opds.NewPublication("id", "t").About("Fiction")
	b, _ := MarshalPublication(p)
	var doc map[string]any
	json.Unmarshal(b, &doc)
	subj := doc["metadata"].(map[string]any)["subject"].([]any)
	if subj[0] != "Fiction" {
		t.Errorf("bare subject should marshal as string, got %T %v", subj[0], subj[0])
	}
}
