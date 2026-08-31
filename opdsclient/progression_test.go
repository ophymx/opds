package opdsclient_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/opdsclient"
	"github.com/ophymx/opds/opdshttp"
)

func device() opds.Device {
	return opds.Device{ID: "urn:uuid:6ba7b810-9dad-11d1-80b4-00c04fd430c8", Name: "Kobo Elipsa"}
}

// newSyncClient returns a client for a catalog with progression sync enabled,
// together with the progression link of its one publication.
func newSyncClient(t *testing.T, store opdshttp.ProgressionStore, opts ...opdsclient.Option) (*opdsclient.Client, opds.Link) {
	t.Helper()
	srv := newAuthServer(t, opdshttp.WithProgression(store))
	opts = append([]opdsclient.Option{
		opdsclient.WithBasicAuth("jane", "secret"),
		opdsclient.WithDevice(device()),
	}, opts...)
	c := newClient(t, srv, opts...)
	f, err := c.Feed(context.Background(), "feed/new")
	if err != nil {
		t.Fatal(err)
	}
	l, ok := opdsclient.ProgressionLink(&f.Publications[0])
	if !ok {
		t.Fatalf("publication advertises no progression link: %+v", f.Publications[0].Links)
	}
	return c, l
}

func TestProgressionRoundTrip(t *testing.T) {
	bothVersions(t, func(t *testing.T, _ opds.Version, opts ...opdsclient.Option) {
		ctx := context.Background()
		c, link := newSyncClient(t, opdshttp.NewMemProgressionStore(), opts...)

		// Nothing read yet is not an error, in either wire shape.
		p, err := c.Progression(ctx, link)
		if err != nil {
			t.Fatal(err)
		}
		if p != nil {
			t.Fatalf("progression = %+v, want nil before anything is stored", p)
		}

		at := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
		want := &opds.Progression{
			Progression: 0.42,
			Modified:    at,
			Device:      device(),
			Title:       "Chapter 4",
			References:  []string{"/chapter4.html#p12"},
		}
		if err := c.SetProgression(ctx, link, want); err != nil {
			t.Fatal(err)
		}

		got, err := c.Progression(ctx, link)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatal("progression = nil after storing one")
		}
		if got.Progression != want.Progression || !got.Modified.Equal(want.Modified) {
			t.Errorf("progression = %v at %v, want %v at %v", got.Progression, got.Modified, want.Progression, want.Modified)
		}
		if got.Device != want.Device || got.Title != want.Title {
			t.Errorf("device/title = %+v %q", got.Device, got.Title)
		}
	})
}

func TestProgressionLinkPrefersTheDraftRelation(t *testing.T) {
	p := &opds.Publication{Links: []opds.Link{
		{Rel: opds.RelProgressionCantook, Href: "/p/1?format=readium"},
		{Rel: opds.RelProgression, Href: "/p/1"},
	}}
	l, ok := opdsclient.ProgressionLink(p)
	if !ok || l.Rel != opds.RelProgression {
		t.Errorf("link = %+v, want the draft relation", l)
	}

	cantookOnly := &opds.Publication{Links: []opds.Link{
		{Rel: opds.RelProgressionCantook, Href: "/p/1?format=readium"},
	}}
	if got, ok := opdsclient.ProgressionLink(cantookOnly); !ok || got.Rel != opds.RelProgressionCantook {
		t.Errorf("link = %+v, want the Cantook fallback", got)
	}

	if _, ok := opdsclient.ProgressionLink(&opds.Publication{}); ok {
		t.Error("want no link for a publication advertising none")
	}
}

// TestProgressionOverTheCantookAlias drives the pre-spec relation Komga and
// Stump serve, which carries a Readium locator rather than the draft document.
func TestProgressionOverTheCantookAlias(t *testing.T) {
	ctx := context.Background()
	c, draft := newSyncClient(t, opdshttp.NewMemProgressionStore())
	alias := opds.Link{
		Rel:  opds.RelProgressionCantook,
		Href: draft.Href + "?format=readium",
		Type: opds.MediaTypeProgressionReadium,
	}

	if p, err := c.Progression(ctx, alias); err != nil || p != nil {
		t.Fatalf("progression = %+v, %v; want nil, nil (the alias answers 204)", p, err)
	}
	at := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	err := c.SetProgression(ctx, alias, &opds.Progression{
		Progression: 0.5, Modified: at, Device: device(),
		Title: "Chapter 5", References: []string{"/c5.html#p3"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The same record reads back through either shape.
	for name, l := range map[string]opds.Link{"alias": alias, "draft": draft} {
		got, err := c.Progression(ctx, l)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Progression != 0.5 || got.Title != "Chapter 5" {
			t.Errorf("%s: progression = %+v", name, got)
		}
	}
}

func TestSetProgressionFillsDeviceAndTimestamp(t *testing.T) {
	ctx := context.Background()
	c, link := newSyncClient(t, opdshttp.NewMemProgressionStore())
	before := time.Now().Add(-time.Second)
	if err := c.SetProgression(ctx, link, &opds.Progression{Progression: 0.1}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Progression(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	if got.Device != device() {
		t.Errorf("device = %+v, want the one configured with WithDevice", got.Device)
	}
	if got.Modified.Before(before) {
		t.Errorf("modified = %v, want it stamped with the current time", got.Modified)
	}
}

// TestSetProgressionNormalizesABareUUIDDevice covers the id the deployed
// servers hand out: the draft requires a URI, and the client supplies one
// rather than letting the catalog reject the document.
func TestSetProgressionNormalizesABareUUIDDevice(t *testing.T) {
	ctx := context.Background()
	c, link := newSyncClient(t, opdshttp.NewMemProgressionStore(),
		opdsclient.WithDevice(opds.Device{ID: "6ba7b810-9dad-11d1-80b4-00c04fd430c8", Name: "Kobo"}))
	if err := c.SetProgression(ctx, link, &opds.Progression{Progression: 0.1}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Progression(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	if want := "urn:uuid:6ba7b810-9dad-11d1-80b4-00c04fd430c8"; got.Device.ID != want {
		t.Errorf("device id = %q, want %q", got.Device.ID, want)
	}
}

func TestSetProgressionStaleIsTyped(t *testing.T) {
	ctx := context.Background()
	c, link := newSyncClient(t, opdshttp.NewMemProgressionStore())
	newer := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	if err := c.SetProgression(ctx, link, &opds.Progression{Progression: 0.5, Modified: newer, Device: device()}); err != nil {
		t.Fatal(err)
	}
	older := newer.Add(-time.Hour)
	err := c.SetProgression(ctx, link, &opds.Progression{Progression: 0.2, Modified: older, Device: device()})
	if !errors.Is(err, opdsclient.ErrProgressionStale) {
		t.Fatalf("err = %v, want ErrProgressionStale", err)
	}
	var httpErr *opdsclient.Error
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusConflict {
		t.Errorf("err = %#v, want an *Error carrying 409", err)
	}
	if httpErr.Problem == nil || httpErr.Problem.Title == "" {
		t.Errorf("problem = %+v, want the draft's problem detail", httpErr.Problem)
	}
}

func TestSetProgressionFutureTimestampIsRejected(t *testing.T) {
	ctx := context.Background()
	c, link := newSyncClient(t, opdshttp.NewMemProgressionStore())
	err := c.SetProgression(ctx, link, &opds.Progression{
		Progression: 0.5, Modified: time.Now().Add(72 * time.Hour), Device: device(),
	})
	if !errors.Is(err, opdsclient.ErrProgressionInvalid) {
		t.Errorf("err = %v, want ErrProgressionInvalid", err)
	}
}

// refusingStore refuses every write with the sentinel it was built with, which
// is how a store picks one of the draft's two 403s.
type refusingStore struct{ err error }

func (refusingStore) Progression(context.Context, string, string) (*opds.Progression, error) {
	return nil, opds.ErrNotFound
}

func (s refusingStore) SetProgression(context.Context, string, string, *opds.Progression) error {
	return s.err
}

func TestSetProgressionRefusalsAreTyped(t *testing.T) {
	for name, tc := range map[string]struct {
		store error
		want  error
	}{
		"incorrect user": {opdshttp.ErrProgressionIncorrectUser, opdsclient.ErrProgressionIncorrectUser},
		"locked":         {opdshttp.ErrProgressionLocked, opdsclient.ErrProgressionLocked},
	} {
		t.Run(name, func(t *testing.T) {
			c, link := newSyncClient(t, refusingStore{tc.store})
			err := c.SetProgression(context.Background(), link, &opds.Progression{Progression: 0.5, Device: device()})
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			var httpErr *opdsclient.Error
			if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusForbidden {
				t.Errorf("err = %#v, want an *Error carrying 403", err)
			}
		})
	}
}

func TestSetProgressionValidatesLocally(t *testing.T) {
	c, link := newSyncClient(t, opdshttp.NewMemProgressionStore())
	if err := c.SetProgression(context.Background(), link, nil); err == nil {
		t.Error("want an error for a nil progression")
	}
	err := c.SetProgression(context.Background(), link, &opds.Progression{Progression: 1.5, Device: device()})
	if err == nil {
		t.Error("want an error for a progression outside [0, 1]")
	}
	if _, ok := errors.AsType[*opdsclient.Error](err); ok {
		t.Errorf("err = %#v, want it caught before the request", err)
	}
}

// TestProgressionAgainstAKomgaShapedServer covers a catalog this library does
// not control: Komga answers the Cantook relation with a Readium locator and a
// bare-UUID device, and returns 204 rather than 200 for an empty position.
func TestProgressionAgainstAKomgaShapedServer(t *testing.T) {
	var stored []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			stored, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if stored == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", opds.MediaTypeProgressionReadium)
		w.Write(stored)
	}))
	defer srv.Close()

	c, err := opdsclient.New(srv.URL+"/", opdsclient.WithDevice(opds.Device{
		ID: "b8f4c8de-1f0e-4a2f-9a0e-0f7b6d2c1a33", Name: "Aldiko",
	}))
	if err != nil {
		t.Fatal(err)
	}
	link := opds.Link{
		Rel:  opds.RelProgressionCantook,
		Href: "/api/v1/books/1/progression",
		Type: opds.MediaTypeProgressionReadium,
	}
	ctx := context.Background()
	if p, err := c.Progression(ctx, link); err != nil || p != nil {
		t.Fatalf("progression = %+v, %v; want nil, nil", p, err)
	}
	at := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	err = c.SetProgression(ctx, link, &opds.Progression{
		Progression: 0.75, Modified: at, Title: "Chapter 9",
		References: []string{"/OEBPS/c9.xhtml#p4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Progression(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	if got.Progression != 0.75 || got.Title != "Chapter 9" {
		t.Errorf("progression = %+v", got)
	}
	// The alias round-trips the device as sent, so Aldiko still recognizes it.
	if got.Device.ID != "b8f4c8de-1f0e-4a2f-9a0e-0f7b6d2c1a33" {
		t.Errorf("device id = %q, want the bare UUID unchanged", got.Device.ID)
	}
	if len(got.References) != 1 || got.References[0] != "/OEBPS/c9.xhtml#p4" {
		t.Errorf("references = %v", got.References)
	}
}
