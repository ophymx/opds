package opds2

import (
	"reflect"
	"testing"
	"time"

	"github.com/ophymx/opds"
)

// roundTripFeed is a feed built entirely from members OPDS 2.0 can express, so
// Marshal followed by Unmarshal must reproduce it exactly. The members 2.0
// drops (page streaming, summary, feed icon and authors, extra identifiers,
// facet activation, 1.x start index) are covered by TestUnmarshalDropsWhat20CannotCarry.
func roundTripFeed() *opds.Feed {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	pos := 3
	return &opds.Feed{
		ID:       "urn:feed:root",
		Title:    "Library",
		Subtitle: "Everything we have",
		Updated:  t0,
		Links: []opds.Link{
			{Rel: opds.RelSelf, Href: "https://example.com/opds/feed/new", Type: opds.MediaTypeFeed},
			{Rel: opds.RelSearch, Href: "https://example.com/opds/search{?q}", Type: opds.MediaTypeFeed, Templated: true},
			{
				Rel:  opds.RelProgression,
				Href: "https://example.com/opds/progression/1",
				Type: opds.MediaTypeProgression,
				Authenticate: &opds.AuthenticateHint{
					Href: "https://example.com/opds/auth", Type: opds.MediaTypeAuthDocument,
				},
			},
		},
		TotalResults: 120,
		ItemsPerPage: 50,
		CurrentPage:  2,
		Navigation: []opds.NavEntry{
			{Title: "New", Href: "/opds/feed/new", Type: opds.MediaTypeFeed, Rel: opds.RelSortNew},
		},
		Publications: []opds.Publication{{
			ID:           "urn:isbn:9780134190440",
			Title:        "The Go Programming Language",
			SortAs:       "Go Programming Language, The",
			Updated:      t0,
			Published:    t0,
			Languages:    []string{"en"},
			Publisher:    "Addison-Wesley",
			Authors:      []opds.Author{{Name: "Alan Donovan", URI: "/authors/donovan", SortAs: "Donovan, Alan"}},
			Contributors: []opds.Author{{Name: "Brian Kernighan"}},
			Subjects: []opds.Subject{
				{Name: "Computers", Code: "COM051000", Scheme: "https://bisg.org"},
				{Name: "Programming"},
			},
			Description: "Guide.",
			Series:      &opds.Series{Name: "Go Books", Position: 1},
			Images: []opds.Image{
				{Href: "/covers/1.jpg", Type: "image/jpeg", Width: 800, Height: 1200},
			},
			Links: []opds.Link{
				{Rel: opds.RelAlternate, Href: "/opds/publication/1", Type: opds.MediaTypePublication},
			},
			Acquisitions: []opds.Acquisition{
				{
					Rel: opds.AcquireBuy, Href: "/buy/1", Type: "application/epub+zip", Title: "Buy",
					Prices:   []opds.Price{{Currency: "USD", Value: 39.99}},
					Indirect: []opds.IndirectAcquisition{{Type: "application/vnd.adobe.adept+xml", Child: []opds.IndirectAcquisition{{Type: "application/epub+zip"}}}},
				},
				{
					Rel: opds.AcquireBorrow, Href: "/borrow/1", Type: "application/epub+zip",
					Availability: &opds.Availability{State: opds.StateUnavailable, Since: t0, Until: t0},
					Holds:        &opds.Holds{Total: 12, Position: &pos},
					Copies:       &opds.Copies{Total: 3, Available: 0},
				},
			},
		}},
		Groups: []opds.Group{{
			Title: "Staff picks",
			Href:  "/opds/feed/picks",
			Type:  opds.MediaTypeFeed,
			Rel:   opds.RelSubsection,
			Publications: []opds.Publication{{
				ID: "urn:book:2", Title: "Second",
				Acquisitions: []opds.Acquisition{{Rel: opds.AcquireOpenAccess, Href: "/dl/2", Type: "application/epub+zip"}},
			}},
		}},
		Facets: []opds.Facet{
			{Group: "Language", Title: "English", Href: "/opds/feed/new?lang=en", Type: opds.MediaTypeFeed, Count: 80},
			{Group: "Language", Title: "French", Href: "/opds/feed/new?lang=fr", Type: opds.MediaTypeFeed},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	want := roundTripFeed()
	b, err := Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, b)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip differs\n--- got ---\n%#v\n--- want ---\n%#v", got, want)
	}
}

func TestRoundTripPublication(t *testing.T) {
	want := &roundTripFeed().Publications[0]
	b, err := MarshalPublication(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalPublication(b)
	if err != nil {
		t.Fatalf("UnmarshalPublication: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip differs\n--- got ---\n%#v\n--- want ---\n%#v", got, want)
	}
}

// TestUnmarshalDropsWhat20CannotCarry pins the documented lossy members, so a
// caller can rely on the list and a future encoder change that starts carrying
// one of them has to update it.
func TestUnmarshalDropsWhat20CannotCarry(t *testing.T) {
	f := opds.NewFeed("urn:feed:root", "Library").At(time.Now())
	f.Icon = "/icon.png"
	f.Authors = []opds.Author{{Name: "Library staff"}}
	f.StartIndex = 51
	f.AddFacet("Language", "English", "/en", opds.MediaTypeFeed, 3, true)
	p := opds.NewPublication("urn:book:1", "Book").
		Summarize("A summary.").Identifier("urn:isbn:1").Identifier("urn:isbn:2").
		Stream("/page/1/{pageNumber}", "image/jpeg", 42)
	f.Add(*p)

	b, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Icon != "" || got.Authors != nil {
		t.Errorf("feed icon/authors survived: %q %v", got.Icon, got.Authors)
	}
	if got.StartIndex != 0 {
		t.Errorf("StartIndex = %d, want 0 (2.0 paginates by currentPage)", got.StartIndex)
	}
	if len(got.Facets) != 1 || got.Facets[0].Active {
		t.Errorf("facets = %+v, want one inactive", got.Facets)
	}
	pub := got.Publications[0]
	if pub.PageStream != nil {
		t.Errorf("PageStream survived: %+v", pub.PageStream)
	}
	if pub.Summary != "" || pub.Description != "A summary." {
		t.Errorf("summary = %q, description = %q; want the summary folded into description", pub.Summary, pub.Description)
	}
	if pub.ID != "urn:isbn:1" || pub.Identifiers != nil {
		t.Errorf("id = %q, identifiers = %v; want only the first identifier, as the id", pub.ID, pub.Identifiers)
	}
}

func TestUnmarshalAcceptsAlternateShapes(t *testing.T) {
	const body = `{
	  "metadata": {"title": "Catalog"},
	  "links": [{"rel": ["self", "start"], "href": "/opds", "type": "application/opds+json"}],
	  "publications": [{
	    "metadata": {
	      "title": {"und": "Livre", "fr": "Livre"},
	      "author": ["Anon", {"name": "Named", "sortAs": "Named, A", "links": [{"href": "/a/1"}]}],
	      "subject": "Fiction"
	    },
	    "links": [{"rel": "http://opds-spec.org/acquisition", "href": "/dl/1"}],
	    "images": [{"href": "/c/1.jpg", "type": "image/jpeg"}]
	  }]
	}`
	f, err := Unmarshal([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Links[0].Rel; got != opds.RelSelf {
		t.Errorf("rel = %q, want the first of the array", got)
	}
	p := f.Publications[0]
	if p.Title != "Livre" {
		t.Errorf("title = %q, want the localized string flattened", p.Title)
	}
	want := []opds.Author{{Name: "Anon"}, {Name: "Named", SortAs: "Named, A", URI: "/a/1"}}
	if !reflect.DeepEqual(p.Authors, want) {
		t.Errorf("authors = %+v, want %+v", p.Authors, want)
	}
	if len(p.Subjects) != 1 || p.Subjects[0].Name != "Fiction" {
		t.Errorf("subjects = %+v", p.Subjects)
	}
	if len(p.Acquisitions) != 1 || len(p.Images) != 1 {
		t.Errorf("acquisitions = %+v, images = %+v", p.Acquisitions, p.Images)
	}
}

func TestUnmarshalRejectsNonFeed(t *testing.T) {
	if _, err := Unmarshal([]byte(`{"foo": 1}`)); err == nil {
		t.Error("want an error for a JSON document that is not a feed")
	}
	if _, err := Unmarshal([]byte(`not json`)); err == nil {
		t.Error("want an error for non-JSON")
	}
}

// TestUnmarshalDistinguishesFeedFromPublication covers the two documents that
// share a shape: both are {"metadata": …, "links": …}, so decoding one as the
// other has to fail rather than silently produce a feed named after a book.
func TestUnmarshalDistinguishesFeedFromPublication(t *testing.T) {
	pub, err := MarshalPublication(&roundTripFeed().Publications[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Unmarshal(pub); err == nil {
		t.Error("Unmarshal accepted a publication document as a feed")
	}

	feed, err := Marshal(roundTripFeed())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnmarshalPublication(feed); err == nil {
		t.Error("UnmarshalPublication accepted a feed as a publication")
	}

	// A feed with no entries has none of the distinguishing markers, and is
	// still a feed when that is what was asked for.
	empty, err := Marshal(&opds.Feed{ID: "urn:empty", Title: "Empty"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := Unmarshal(empty)
	if err != nil {
		t.Fatalf("Unmarshal rejected an empty feed: %v", err)
	}
	if f.Title != "Empty" {
		t.Errorf("title = %q", f.Title)
	}
}
