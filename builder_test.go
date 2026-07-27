package opds

import (
	"testing"
	"time"
)

func TestStreamBuilder(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	p := NewPublication("urn:comic:1", "Comic").
		Stream("/opds/page/1?page={pageNumber}", "image/jpeg", 35).
		LastRead(10, ts)
	want := PageStream{
		Href: "/opds/page/1?page={pageNumber}", Type: "image/jpeg",
		PageCount: 35, LastRead: 10, LastReadDate: ts,
	}
	if p.PageStream == nil || *p.PageStream != want {
		t.Errorf("PageStream = %+v, want %+v", p.PageStream, want)
	}

	// LastRead before Stream must yield the same descriptor.
	p = NewPublication("urn:comic:1", "Comic").
		LastRead(10, ts).
		Stream("/opds/page/1?page={pageNumber}", "image/jpeg", 35)
	if p.PageStream == nil || *p.PageStream != want {
		t.Errorf("PageStream (LastRead first) = %+v, want %+v", p.PageStream, want)
	}
}

func TestPageHref(t *testing.T) {
	cases := []struct {
		href string
		page int
		want string
	}{
		{"/opds/feed/new", 1, "/opds/feed/new"},
		{"/opds/feed/new", 0, "/opds/feed/new"},
		{"/opds/feed/new", 2, "/opds/feed/new?page=2"},
		{"/opds/feed/new?page=5", 3, "/opds/feed/new?page=3"},
		{"/opds/feed/new?page=5", 1, "/opds/feed/new"},
		{"/opds/search?q=dune", 2, "/opds/search?page=2&q=dune"},
		{"https://example.com/opds/feed/new", 2, "https://example.com/opds/feed/new?page=2"},
	}
	for _, c := range cases {
		if got := PageHref(c.href, c.page); got != c.want {
			t.Errorf("PageHref(%q, %d) = %q, want %q", c.href, c.page, got, c.want)
		}
	}
}

func TestPagedMiddlePage(t *testing.T) {
	f := NewFeed("urn:feed:new", "New").Paged("/opds/feed/new", 2, true)
	if f.CurrentPage != 2 {
		t.Errorf("CurrentPage = %d, want 2", f.CurrentPage)
	}
	if href := findRel(f.Links, RelPrevious); href != "/opds/feed/new" {
		t.Errorf("previous = %q, want /opds/feed/new", href)
	}
	if href := findRel(f.Links, RelNext); href != "/opds/feed/new?page=3" {
		t.Errorf("next = %q, want /opds/feed/new?page=3", href)
	}
}

func TestPagedFirstAndLastPage(t *testing.T) {
	first := NewFeed("urn:f", "F").Paged("/opds/feed/new", 1, true)
	if href := findRel(first.Links, RelPrevious); href != "" {
		t.Errorf("page 1 should have no previous link, got %q", href)
	}
	if href := findRel(first.Links, RelNext); href != "/opds/feed/new?page=2" {
		t.Errorf("next = %q, want /opds/feed/new?page=2", href)
	}

	last := NewFeed("urn:f", "F").Paged("/opds/feed/new", 3, false)
	if href := findRel(last.Links, RelNext); href != "" {
		t.Errorf("last page should have no next link, got %q", href)
	}
	if href := findRel(last.Links, RelPrevious); href != "/opds/feed/new?page=2" {
		t.Errorf("previous = %q, want /opds/feed/new?page=2", href)
	}
}

func TestPagedPreservesQuery(t *testing.T) {
	f := NewFeed("urn:search", "Results").Paged("/opds/search?q=dune", 2, true)
	if href := findRel(f.Links, RelNext); href != "/opds/search?page=3&q=dune" {
		t.Errorf("next = %q, want /opds/search?page=3&q=dune", href)
	}
	if href := findRel(f.Links, RelPrevious); href != "/opds/search?q=dune" {
		t.Errorf("previous = %q, want /opds/search?q=dune", href)
	}
}

func TestPagedDerivesStartIndex(t *testing.T) {
	f := NewFeed("urn:f", "F").Page(100, 20, 0).Paged("/opds/feed/new", 3, true)
	if f.StartIndex != 41 {
		t.Errorf("StartIndex = %d, want 41", f.StartIndex)
	}
	// An explicit StartIndex is left alone.
	f = NewFeed("urn:f", "F").Page(100, 20, 7).Paged("/opds/feed/new", 3, true)
	if f.StartIndex != 7 {
		t.Errorf("StartIndex = %d, want 7 (explicit value kept)", f.StartIndex)
	}
}

func findRel(links []Link, rel string) string {
	for _, l := range links {
		if l.Rel == rel {
			return l.Href
		}
	}
	return ""
}
