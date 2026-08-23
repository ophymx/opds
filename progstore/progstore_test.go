package progstore_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opdshttp"
	"github.com/ophymx/opds/progstore"
)

// The store must satisfy the interface it exists to implement.
var _ opdshttp.ProgressionStore = (*progstore.Store)(nil)

func newStore(t *testing.T) *progstore.Store {
	t.Helper()
	s, err := progstore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sample(modified time.Time) *opds.Progression {
	return &opds.Progression{
		Progression: 0.42,
		Modified:    modified,
		Device:      opds.Device{ID: "urn:uuid:1", Name: "Kobo Elipsa"},
		Title:       "Chapter 3",
		References:  []string{"chapter3.html#frag"},
	}
}

func TestProgressionRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	ts := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	if _, err := s.Progression(ctx, "jane", "urn:b1"); !errors.Is(err, opds.ErrNotFound) {
		t.Fatalf("empty store: err = %v, want ErrNotFound", err)
	}
	if err := s.SetProgression(ctx, "jane", "urn:b1", sample(ts)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Progression(ctx, "jane", "urn:b1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Progression != 0.42 || !got.Modified.Equal(ts) || got.Device.Name != "Kobo Elipsa" ||
		got.Title != "Chapter 3" || len(got.References) != 1 {
		t.Errorf("round trip = %+v", got)
	}

	// Per-user isolation.
	if _, err := s.Progression(ctx, "bob", "urn:b1"); !errors.Is(err, opds.ErrNotFound) {
		t.Errorf("bob sees jane's progression: err = %v", err)
	}
}

func TestPersistsAcrossInstances(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	ts := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	s1, err := progstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.SetProgression(ctx, "jane", "urn:b1", sample(ts)); err != nil {
		t.Fatal(err)
	}
	if err := s1.SetLastRead(ctx, "jane", "urn:b1", 12, ts); err != nil {
		t.Fatal(err)
	}

	s2, err := progstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s2.Progression(ctx, "jane", "urn:b1"); err != nil || got.Progression != 0.42 {
		t.Errorf("progression after reopen = %+v, %v", got, err)
	}
	if page, date, err := s2.LastRead(ctx, "jane", "urn:b1"); err != nil || page != 12 || !date.Equal(ts) {
		t.Errorf("lastRead after reopen = %d, %v, %v", page, date, err)
	}
}

// Progression and lastRead share a record without clobbering each other.
func TestProgressionAndLastReadCoexist(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	ts := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	if err := s.SetLastRead(ctx, "jane", "urn:b1", 7, ts); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProgression(ctx, "jane", "urn:b1", sample(ts)); err != nil {
		t.Fatal(err)
	}
	if page, _, err := s.LastRead(ctx, "jane", "urn:b1"); err != nil || page != 7 {
		t.Errorf("SetProgression clobbered lastRead: %d, %v", page, err)
	}
	if err := s.SetLastRead(ctx, "jane", "urn:b1", 9, ts.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Progression(ctx, "jane", "urn:b1"); err != nil || got.Progression != 0.42 {
		t.Errorf("SetLastRead clobbered progression: %+v, %v", got, err)
	}
	// Page 0 clears.
	if err := s.SetLastRead(ctx, "jane", "urn:b1", 0, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LastRead(ctx, "jane", "urn:b1"); !errors.Is(err, opds.ErrNotFound) {
		t.Errorf("cleared lastRead: err = %v, want ErrNotFound", err)
	}
}

// Keys are opaque bytes: traversal sequences, separators, and control bytes
// must neither escape the store directory nor collide with each other.
func TestHostileKeysAreConfined(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := progstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	keys := []struct{ user, pub string }{
		{"../../etc", "../passwd"},
		{"a/b", "c\\d"},
		{"user", "urn:b1"},
		{"user", "urn:b1/../urn:b2"},
		{".", ".."},
		{"nul\x00byte", "co\nn"},
	}
	for i, k := range keys {
		p := sample(ts)
		p.Progression = float64(i) / 10
		if err := s.SetProgression(ctx, k.user, k.pub, p); err != nil {
			t.Fatalf("SetProgression(%q, %q): %v", k.user, k.pub, err)
		}
	}
	for i, k := range keys {
		got, err := s.Progression(ctx, k.user, k.pub)
		if err != nil || got.Progression != float64(i)/10 {
			t.Errorf("Progression(%q, %q) = %+v, %v", k.user, k.pub, got, err)
		}
	}

	// Nothing may exist outside the store directory, and everything inside it
	// must be within depth 2 (user dir / record file).
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel != "." && strings.Count(rel, string(filepath.Separator)) > 1 {
			t.Errorf("unexpected depth: %s", rel)
		}
		if strings.Contains(rel, "..") {
			t.Errorf("path escaped encoding: %s", rel)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "passwd.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("record escaped the store directory")
	}
}

func TestConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	ts := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := sample(ts.Add(time.Duration(i) * time.Second))
			if err := s.SetProgression(ctx, "jane", "urn:b1", p); err != nil {
				t.Error(err)
			}
			if err := s.SetLastRead(ctx, "jane", "urn:b1", i+1, ts); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got, err := s.Progression(ctx, "jane", "urn:b1"); err != nil || got.Progression != 0.42 {
		t.Errorf("after concurrent writes: %+v, %v", got, err)
	}
	if page, _, err := s.LastRead(ctx, "jane", "urn:b1"); err != nil || page < 1 || page > 20 {
		t.Errorf("lastRead after concurrent writes: %d, %v", page, err)
	}
}

// catalogStub is the minimal Source the handler test needs.
type catalogStub struct{}

func (catalogStub) Root(context.Context, opds.FeedRequest) (*opds.Feed, error) {
	return opds.NewFeed("urn:root", "Root"), nil
}
func (catalogStub) Feed(context.Context, opds.FeedRequest) (*opds.Feed, error) {
	return nil, opds.ErrNotFound
}
func (catalogStub) Publication(context.Context, string) (*opds.Publication, error) {
	return nil, opds.ErrNotFound
}

func request(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.SetBasicAuth("jane", "pw")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// The handler wired to a Store behaves identically to the in-memory store.
func TestServesThroughHandler(t *testing.T) {
	s := newStore(t)
	h := opdshttp.New(catalogStub{},
		opdshttp.WithPrefix("/opds"),
		opdshttp.WithAuth(opdshttp.StaticUsers(map[string]string{"jane": "pw"}), opdshttp.AuthDocument{Title: "T"}),
		opdshttp.WithProgression(s),
	)
	put := request(t, h, "PUT", "/opds/progression/urn:b1",
		`{"modified":"2026-08-01T10:00:00Z","device":{"id":"urn:d","name":"D"},"progression":0.5}`)
	if put.Code != 201 {
		t.Fatalf("PUT code = %d: %s", put.Code, put.Body.String())
	}
	get := request(t, h, "GET", "/opds/progression/urn:b1", "")
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"progression":0.5`) {
		t.Errorf("GET = %d %s", get.Code, get.Body.String())
	}
}
