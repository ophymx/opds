package opds1

import (
	"reflect"
	"testing"
	"time"

	"github.com/ophymx/opds"
)

// roundTripFeed is built entirely from members OPDS 1.2 can express, with
// every value the encoder would otherwise fill in supplied explicitly, so
// Marshal followed by Unmarshal must reproduce it exactly. What 1.2 cannot
// carry is covered by TestUnmarshalDropsWhat12CannotCarry.
func roundTripFeed() *opds.Feed {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	pos := 3
	return &opds.Feed{
		ID:       "urn:feed:root",
		Title:    "Library",
		Subtitle: "Everything we have",
		Updated:  t0,
		Icon:     "/icon.png",
		Authors:  []opds.Author{{Name: "Library staff", URI: "/about"}},
		Links: []opds.Link{
			{Rel: opds.RelSelf, Href: "/opds/feed/new", Type: opds.MediaTypeAcquisition},
			{Rel: opds.RelSearch, Href: "/opds/opensearch.xml", Type: opds.MediaTypeOpenSearch},
		},
		TotalResults: 120,
		ItemsPerPage: 50,
		StartIndex:   51,
		Navigation: []opds.NavEntry{{
			ID:      "urn:nav:new",
			Title:   "New",
			Updated: t0,
			Content: "Recently added",
			Href:    "/opds/feed/new",
			Type:    opds.MediaTypeAcquisition,
			Rel:     opds.RelSortNew,
			Images:  []opds.Image{{Href: "/nav/new.png", Type: "image/png", Thumbnail: true}},
		}},
		Publications: []opds.Publication{{
			ID:           "urn:book:1",
			Title:        "The Go Programming Language",
			Updated:      t0,
			Published:    day,
			Languages:    []string{"en"},
			Identifiers:  []string{"urn:isbn:9780134190440", "urn:isbn:0134190440"},
			Publisher:    "Addison-Wesley",
			Authors:      []opds.Author{{Name: "Alan Donovan", URI: "/authors/donovan"}},
			Contributors: []opds.Author{{Name: "Brian Kernighan"}},
			Subjects: []opds.Subject{
				{Name: "Computers", Code: "COM051000", Scheme: "https://bisg.org"},
				{Name: "Programming"},
			},
			Summary:     "A guide.",
			Description: "<p>A longer guide.</p>",
			Rights:      "© 2015 Alan Donovan",
			Links: []opds.Link{
				{Rel: opds.RelAlternate, Href: "/opds/publication/1", Type: opds.MediaTypeEntry, Title: "Details"},
			},
			Images: []opds.Image{
				{Href: "/covers/1.jpg", Type: "image/jpeg"},
				{Href: "/covers/1-thumb.jpg", Type: "image/jpeg", Thumbnail: true},
			},
			Acquisitions: []opds.Acquisition{
				{
					Rel: opds.AcquireBuy, Href: "/buy/1", Type: "application/epub+zip", Title: "Buy",
					Prices: []opds.Price{{Currency: "USD", Value: 39.99}, {Currency: "EUR", Value: 34.5}},
					Indirect: []opds.IndirectAcquisition{
						{Type: "application/vnd.adobe.adept+xml", Child: []opds.IndirectAcquisition{{Type: "application/epub+zip"}}},
					},
				},
				{
					Rel: opds.AcquireBorrow, Href: "/borrow/1", Type: "application/epub+zip",
					Availability: &opds.Availability{State: opds.StateUnavailable, Since: t0, Until: t0},
					Holds:        &opds.Holds{Total: 12, Position: &pos},
					Copies:       &opds.Copies{Total: 3, Available: 0},
				},
			},
			PageStream: &opds.PageStream{
				Href: "/opds/page/1/{pageNumber}?width={maxWidth}", Type: "image/jpeg",
				PageCount: 42, LastRead: 7, LastReadDate: t0,
			},
		}},
		Groups: []opds.Group{{
			Title: "Staff picks",
			Href:  "/opds/feed/picks",
			Type:  opds.MediaTypeAcquisition,
			Publications: []opds.Publication{{
				ID: "urn:book:2", Title: "Second", Updated: t0,
				Acquisitions: []opds.Acquisition{{Rel: opds.AcquireOpenAccess, Href: "/dl/2", Type: "application/epub+zip"}},
			}},
		}},
		Facets: []opds.Facet{
			{Group: "Language", Title: "English", Href: "/new?lang=en", Type: opds.MediaTypeAcquisition, Count: 80, Active: true},
			{Group: "Language", Title: "French", Href: "/new?lang=fr", Type: opds.MediaTypeAcquisition},
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
		t.Errorf("round trip differs\n--- got ---\n%#v\n--- want ---\n%#v\n--- xml ---\n%s", got, want, b)
	}
}

func TestRoundTripEntry(t *testing.T) {
	want := &roundTripFeed().Publications[0]
	b, err := MarshalEntry(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalEntry(b)
	if err != nil {
		t.Fatalf("UnmarshalEntry: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip differs\n--- got ---\n%#v\n--- want ---\n%#v", got, want)
	}
}

// TestUnmarshalDropsWhat12CannotCarry pins the documented lossy members, so a
// caller can rely on the list and a future encoder change that starts carrying
// one of them has to update it.
func TestUnmarshalDropsWhat12CannotCarry(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	f := &opds.Feed{
		ID: "urn:feed:root", Title: "Library", Updated: t0, CurrentPage: 2,
		Links: []opds.Link{{
			Rel: opds.RelProgression, Href: "/p/1", Type: opds.MediaTypeProgression,
			Authenticate: &opds.AuthenticateHint{Href: "/opds/auth"},
		}},
		Publications: []opds.Publication{{
			ID: "urn:book:1", Title: "Book", Updated: t0,
			SortAs: "Book, The", Series: &opds.Series{Name: "Books", Position: 2},
			Acquisitions: []opds.Acquisition{{Rel: opds.AcquireOpenAccess, Href: "/dl/1"}},
		}},
		Groups: []opds.Group{{
			Title: "Picks", Href: "/picks",
			Navigation: []opds.NavEntry{{ID: "urn:nav:a", Title: "A", Href: "/a", Updated: t0}},
		}},
	}
	b, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentPage != 0 {
		t.Errorf("CurrentPage = %d, want 0 (1.x paginates by startIndex)", got.CurrentPage)
	}
	if got.Links[0].Authenticate != nil {
		t.Errorf("authenticate hint survived: %+v", got.Links[0].Authenticate)
	}
	p := got.Publications[0]
	if p.SortAs != "" || p.Series != nil {
		t.Errorf("sortAs = %q, series = %+v; want both dropped", p.SortAs, p.Series)
	}
	if len(got.Groups) != 0 {
		t.Errorf("groups = %+v, want none: the group held only navigation", got.Groups)
	}
	if len(got.Navigation) != 1 || got.Navigation[0].Title != "A" {
		t.Errorf("navigation = %+v, want the group's entry flattened into the feed", got.Navigation)
	}
}

// TestUnmarshalFillsEncoderDefaults documents the values the encoder supplies
// for a caller that left them empty, which therefore come back populated.
func TestUnmarshalFillsEncoderDefaults(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	f := &opds.Feed{ID: "urn:feed:root", Title: "Library", Updated: t0}
	f.AddNav("New", "/opds/feed/new", opds.MediaTypeAcquisition, "")
	f.Add(opds.Publication{
		ID: "urn:book:1", Title: "Book",
		Acquisitions: []opds.Acquisition{{Rel: opds.AcquireOpenAccess, Href: "/dl/1"}},
	})

	b, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	n := got.Navigation[0]
	if n.ID != "/opds/feed/new" {
		t.Errorf("nav id = %q, want the href the encoder synthesized", n.ID)
	}
	if n.Rel != opds.RelSubsection {
		t.Errorf("nav rel = %q, want the default the encoder supplied", n.Rel)
	}
	if !n.Updated.Equal(t0) || !got.Publications[0].Updated.Equal(t0) {
		t.Errorf("entry timestamps = %v / %v, want the feed's", n.Updated, got.Publications[0].Updated)
	}
}

func TestUnmarshalTolerantOfRealWorldShapes(t *testing.T) {
	// A feed using dc: rather than dcterms:, an entry with no acquisition
	// link (navigation), a date-only issued, and a price with surrounding
	// whitespace.
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"
      xmlns:dc="http://purl.org/dc/elements/1.1/"
      xmlns:o="http://opds-spec.org/2010/catalog">
  <id>urn:feed</id><title>Catalog</title><updated>2026-01-02T03:04:05Z</updated>
  <entry>
    <id>urn:nav:1</id><title>Browse</title><updated>2026-01-02T03:04:05Z</updated>
    <link rel="subsection" href="/browse" type="application/atom+xml"/>
  </entry>
  <entry>
    <id>urn:book:1</id><title>Book</title><updated>2026-01-02T03:04:05Z</updated>
    <dc:language>fr</dc:language>
    <dc:issued>1962</dc:issued>
    <link rel="http://opds-spec.org/acquisition/buy" href="/buy/1" type="application/epub+zip">
      <o:price currencycode="EUR"> 9.99 </o:price>
    </link>
  </entry>
</feed>`
	f, err := Unmarshal([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Navigation) != 1 || len(f.Publications) != 1 {
		t.Fatalf("got %d nav and %d publications, want 1 and 1", len(f.Navigation), len(f.Publications))
	}
	p := f.Publications[0]
	if !reflect.DeepEqual(p.Languages, []string{"fr"}) {
		t.Errorf("languages = %v, want the dc: element read", p.Languages)
	}
	if p.Published.Year() != 1962 {
		t.Errorf("published = %v, want the year-only date parsed", p.Published)
	}
	want := []opds.Price{{Currency: "EUR", Value: 9.99}}
	if !reflect.DeepEqual(p.Acquisitions[0].Prices, want) {
		t.Errorf("prices = %+v, want %+v", p.Acquisitions[0].Prices, want)
	}
}

func TestUnmarshalRejectsNonFeed(t *testing.T) {
	if _, err := Unmarshal([]byte(`<html><body/></html>`)); err == nil {
		t.Error("want an error for a document that is not an Atom feed")
	}
	if _, err := Unmarshal([]byte(`{"metadata":{}}`)); err == nil {
		t.Error("want an error for non-XML")
	}
}
