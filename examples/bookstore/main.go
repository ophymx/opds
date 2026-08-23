// Command bookstore is a runnable example OPDS catalog backed by an in-memory
// list of books. It serves both OPDS 1.2 (Atom) and OPDS 2.0 (JSON) from the
// same data, chosen by content negotiation.
//
// Run it:
//
//	go run ./examples/bookstore
//
// Then browse with an OPDS reader, or:
//
//	curl localhost:8080/opds/                              # 1.2 (default)
//	curl -H 'Accept: application/opds+json' localhost:8080/opds/   # 2.0
//	curl 'localhost:8080/opds/?version=2'                  # 2.0 via query
//	curl 'localhost:8080/opds/search?q=go'
//
// With --auth the catalog requires HTTP Basic credentials (demo/demo), serves
// the OPDS Authentication Document, and syncs per-user reading progression —
// the live interop target for clients like Thorium and Cantook:
//
//	go run ./examples/bookstore --auth
//	curl localhost:8080/opds/                              # 401 + auth document
//	curl -u demo:demo localhost:8080/opds/
//	curl -u demo:demo localhost:8080/opds/progression/urn:isbn:9781503280786
//	curl -u demo:demo -X PUT -d '{"modified":"2026-08-22T10:00:00Z",
//	  "device":{"id":"urn:uuid:1","name":"curl"},"progression":0.5}' \
//	  localhost:8080/opds/progression/urn:isbn:9781503280786
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opdshttp"
)

const prefix = "/opds"

type book struct {
	id, title, author, lang, isbn, summary string
	subject                                string
	published                              time.Time
}

var books = []book{
	{"gopl", "The Go Programming Language", "Alan Donovan", "en", "9780134190440",
		"The authoritative resource for the Go language.", "Computers",
		time.Date(2015, 11, 16, 0, 0, 0, 0, time.UTC)},
	{"tcpl", "The C Programming Language", "Brian Kernighan", "en", "9780131103627",
		"The classic introduction to C.", "Computers",
		time.Date(1988, 4, 1, 0, 0, 0, 0, time.UTC)},
	{"moby", "Moby-Dick", "Herman Melville", "en", "9781503280786",
		"The voyage of the whaling ship Pequod.", "Fiction",
		time.Date(1851, 10, 18, 0, 0, 0, 0, time.UTC)},
}

// catalog implements opds.Source and opds.Searcher.
type catalog struct{}

func (c catalog) Root(_ context.Context, _ opds.FeedRequest) (*opds.Feed, error) {
	return opds.NewFeed("urn:bookstore:root", "Example Bookstore").
		By("OPDS Example").
		AddNav("All Books", opdshttp.FeedPath(prefix, "all"), opds.MediaTypeAcquisition, opds.RelSortNew).
		AddNav("Fiction", opdshttp.FeedPath(prefix, "subject/Fiction"), opds.MediaTypeAcquisition, "").
		AddNav("Computers", opdshttp.FeedPath(prefix, "subject/Computers"), opds.MediaTypeAcquisition, ""), nil
}

func (c catalog) Feed(_ context.Context, req opds.FeedRequest) (*opds.Feed, error) {
	switch {
	case req.ID == "all":
		return buildFeed("urn:bookstore:all", "All Books", books), nil
	case strings.HasPrefix(req.ID, "subject/"):
		subject := strings.TrimPrefix(req.ID, "subject/")
		var matched []book
		for _, b := range books {
			if b.subject == subject {
				matched = append(matched, b)
			}
		}
		if matched == nil {
			return nil, opds.ErrNotFound
		}
		return buildFeed("urn:bookstore:subject:"+subject, subject, matched), nil
	}
	return nil, opds.ErrNotFound
}

func (c catalog) Publication(_ context.Context, id string) (*opds.Publication, error) {
	for _, b := range books {
		if b.id == id {
			p := toPublication(b)
			return &p, nil
		}
	}
	return nil, opds.ErrNotFound
}

func (c catalog) Search(_ context.Context, req opds.SearchRequest) (*opds.Feed, error) {
	q := strings.ToLower(req.Terms)
	var matched []book
	for _, b := range books {
		if q == "" || strings.Contains(strings.ToLower(b.title), q) ||
			strings.Contains(strings.ToLower(b.author), q) {
			matched = append(matched, b)
		}
	}
	return buildFeed("urn:bookstore:search", "Search: "+req.Terms, matched), nil
}

func (c catalog) SearchDescription() opds.SearchDescription {
	return opds.SearchDescription{
		ShortName:   "Bookstore",
		Description: "Search the example bookstore by title or author",
	}
}

func buildFeed(id, title string, list []book) *opds.Feed {
	// No self link: the handler injects one derived from the request URL.
	f := opds.NewFeed(id, title)
	for _, b := range list {
		f.Add(toPublication(b))
	}
	f.Page(len(list), 50, 1)
	return f
}

func toPublication(b book) opds.Publication {
	p := opds.NewPublication("urn:isbn:"+b.isbn, b.title).
		By(b.author).In(b.lang).ISBN(b.isbn).
		PublishedAt(b.published).About(b.subject).
		Summarize(b.summary).
		Cover("https://placehold.co/400x600?text="+b.id, "image/png").
		OpenAccess("https://www.gutenberg.org/ebooks/"+b.id+".epub", "application/epub+zip").
		Link(opds.RelAlternate, opdshttp.PublicationPath(prefix, b.id), opds.MediaTypeEntry)
	return *p
}

func main() {
	auth := flag.Bool("auth", false, "require Basic authentication (demo/demo) and enable progression sync")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	opts := []opdshttp.Option{
		opdshttp.WithPrefix(prefix),
		opdshttp.WithDefaultVersion(opds.Version1),
	}
	if *auth {
		opts = append(opts,
			opdshttp.WithAuth(opdshttp.StaticUsers(map[string]string{"demo": "demo"}), opdshttp.AuthDocument{
				Title:       "Example Bookstore",
				Description: `Sign in with the demo account: user "demo", password "demo".`,
				Links: []opds.Link{
					{Rel: "help", Href: "https://github.com/ophymx/opds"},
				},
			}),
			opdshttp.WithProgression(opdshttp.NewMemProgressionStore()),
		)
		log.Printf(`authentication enabled: user "demo", password "demo"`)
	}
	h := opdshttp.New(catalog{}, opts...)
	mux := http.NewServeMux()
	mux.Handle(prefix+"/", h)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, prefix+"/", http.StatusFound)
	})

	log.Printf("OPDS bookstore listening on http://localhost%s%s/", *addr, prefix)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
