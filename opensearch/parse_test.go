package opensearch

import "testing"

func TestUnmarshalRoundTrip(t *testing.T) {
	want := Description{
		ShortName:   "Library",
		Description: "Search the library",
		Template:    "https://example.com/opds/search?q={searchTerms}",
		ResultType:  "application/atom+xml;profile=opds-catalog;kind=acquisition",
	}
	b, err := Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestUnmarshalPrefersOPDSResultType(t *testing.T) {
	const body = `<?xml version="1.0"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>Library</ShortName><Description>Search</Description>
  <Url type="text/html" template="/html?q={searchTerms}"/>
  <Url type="application/atom+xml;profile=opds-catalog" template="/opds?q={searchTerms}"/>
</OpenSearchDescription>`
	d, err := Unmarshal([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if d.Template != "/opds?q={searchTerms}" {
		t.Errorf("template = %q, want the OPDS one", d.Template)
	}
}

func TestExpand(t *testing.T) {
	for _, tc := range []struct {
		name     string
		template string
		params   map[string]string
		want     string
	}{
		{"terms", "/s?q={searchTerms}", map[string]string{"searchTerms": "go book"}, "/s?q=go+book"},
		{"namespaced", "/s?a={atom:author}", map[string]string{"author": "Donovan"}, "/s?a=Donovan"},
		{"optional dropped", "/s?q={searchTerms}&p={startPage?}", map[string]string{"searchTerms": "x"}, "/s?q=x&p="},
		{"unclosed left alone", "/s?q={searchTerms", nil, "/s?q={searchTerms"},
		{"no params", "/s", nil, "/s"},
		{"form style", "/s{?q}", map[string]string{"q": "go book"}, "/s?q=go+book"},
		{"form style multi", "/s{?q,author}", map[string]string{"q": "go", "author": "Donovan"}, "/s?q=go&author=Donovan"},
		{"form style partial", "/s{?q,author}", map[string]string{"author": "D"}, "/s?author=D"},
		{"form style empty", "/s{?q}", nil, "/s"},
		{"continuation", "/s?a=1{&page}", map[string]string{"page": "2"}, "/s?a=1&page=2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Expand(tc.template, tc.params); got != tc.want {
				t.Errorf("Expand() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	d := Description{Template: "/s?q={searchTerms}"}
	if got := d.Search("a b"); got != "/s?q=a+b" {
		t.Errorf("Search() = %q", got)
	}
}
