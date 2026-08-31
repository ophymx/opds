package wire

import (
	"reflect"
	"testing"
	"time"

	"github.com/ophymx/opds"
)

// The two sides of the protocol share these encodings, so the tests that
// matter most are the round trips: what a server writes, a client reads back
// unchanged. The shapes a server never writes but a client meets in the wild
// are covered separately.

func TestAuthDocumentRoundTrip(t *testing.T) {
	want := opds.AuthDocument{
		ID:          "https://example.com/opds/auth",
		Title:       "Example Library",
		Description: "Enter your card number.",
		Authentication: []opds.AuthFlow{
			{Type: opds.AuthFlowBasic, LoginLabel: "Card", PasswordLabel: "PIN"},
			{Type: "http://opds-spec.org/auth/oauth/password", Links: []opds.Link{
				{Rel: "authenticate", Href: "/token", Type: "application/json"},
			}},
		},
		Links: []opds.Link{{Rel: "logo", Href: "/logo.png", Type: "image/png"}},
	}
	// The document-level labels are the parsed shorthand for the Basic flow's.
	want.LoginLabel, want.PasswordLabel = "Card", "PIN"

	b, err := MarshalAuthDocument(want, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseAuthDocument(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("round trip differs\n got %+v\nwant %+v", *got, want)
	}
}

// TestAuthDocumentShorthandBecomesABasicFlow covers the common case: a server
// naming only labels declares Basic, and a client reads those labels back
// without walking the flow list.
func TestAuthDocumentShorthandBecomesABasicFlow(t *testing.T) {
	b, err := MarshalAuthDocument(opds.AuthDocument{
		Title: "Library", LoginLabel: "Card", PasswordLabel: "PIN",
	}, "https://example.com/opds/auth")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseAuthDocument(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "https://example.com/opds/auth" {
		t.Errorf("id = %q, want the default filled in", got.ID)
	}
	if len(got.Authentication) != 1 || got.Authentication[0].Type != opds.AuthFlowBasic {
		t.Fatalf("authentication = %+v", got.Authentication)
	}
	if got.LoginLabel != "Card" || got.PasswordLabel != "PIN" {
		t.Errorf("labels = %q / %q", got.LoginLabel, got.PasswordLabel)
	}
	if !got.SupportsBasic() {
		t.Error("SupportsBasic() = false")
	}
}

func TestParseAuthDocumentAcceptsArrayRel(t *testing.T) {
	got, err := ParseAuthDocument([]byte(`{
		"id": "/auth", "title": "Library",
		"links": [{"rel": ["logo", "icon"], "href": "/logo.png"}],
		"authentication": [{"type": "http://opds-spec.org/auth/basic"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Links[0].Rel != "logo" {
		t.Errorf("rel = %q, want the first of the array", got.Links[0].Rel)
	}
}

func TestParseAuthDocumentRequiresAFlow(t *testing.T) {
	if _, err := ParseAuthDocument([]byte(`{"id":"/a","title":"T"}`)); err == nil {
		t.Error("want an error for a document declaring no flows")
	}
}

func TestProgressionRoundTrip(t *testing.T) {
	want := &opds.Progression{
		Progression: 0.42,
		Modified:    time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
		Device:      opds.Device{ID: "urn:uuid:6ba7b810-9dad-11d1-80b4-00c04fd430c8", Name: "Kobo"},
		Title:       "Chapter 4",
		References:  []string{"/c4.html#p12"},
	}
	b, err := MarshalProgression(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseProgression(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip differs\n got %+v\nwant %+v", got, want)
	}
	if err := ValidateProgression(got); err != nil {
		t.Errorf("ValidateProgression: %v", err)
	}
}

func TestReadiumProgressionRoundTrip(t *testing.T) {
	want := &opds.Progression{
		Progression: 0.42,
		Modified:    time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
		Device:      opds.Device{ID: "6ba7b810-9dad-11d1-80b4-00c04fd430c8", Name: "Kobo"},
		Title:       "Chapter 4",
		References:  []string{"/c4.html#p12", "/c4.html#p13"},
	}
	b, err := MarshalReadiumProgression(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseReadiumProgression(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip differs\n got %+v\nwant %+v", got, want)
	}
	// The alias keeps the bare UUID a deployed server hands out, where the
	// draft document normalizes it.
	if err := ValidateProgression(got); err == nil {
		t.Error("ValidateProgression accepted a bare-UUID device id")
	}
}

func TestParseProgressionIsLenientWhereValidateIsNot(t *testing.T) {
	// A device the draft's schema would reject still parses: a client reading
	// a lenient server's output should not be stopped by it.
	body := []byte(`{"modified":"2026-08-30T12:00:00Z","device":{"id":"kobo-1","name":""},"progression":0.5}`)
	p, err := ParseProgression(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateProgression(p); err == nil {
		t.Error("ValidateProgression accepted an unnamed, non-URI device")
	}
	// Serving it back is still schema-valid, because marshalling normalizes.
	out, err := MarshalProgression(p)
	if err != nil {
		t.Fatal(err)
	}
	round, err := ParseProgression(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateProgression(round); err != nil {
		t.Errorf("normalized document still invalid: %v", err)
	}
}

func TestParseProgressionRejectsMeaninglessDocuments(t *testing.T) {
	for name, body := range map[string]string{
		"no progression": `{"modified":"2026-08-30T12:00:00Z","device":{"id":"urn:a","name":"n"}}`,
		"out of range":   `{"modified":"2026-08-30T12:00:00Z","device":{"id":"urn:a","name":"n"},"progression":2}`,
		"bad modified":   `{"modified":"yesterday","device":{"id":"urn:a","name":"n"},"progression":0.5}`,
		"not json":       `nope`,
	} {
		if _, err := ParseProgression([]byte(body)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestNormalizeDevice(t *testing.T) {
	for name, tc := range map[string]struct {
		in       opds.Device
		id, name string
	}{
		"absolute uri kept": {opds.Device{ID: "https://a/1", Name: "A"}, "https://a/1", "A"},
		"bare uuid":         {opds.Device{ID: "6ba7b810-9dad-11d1-80b4-00c04fd430c8", Name: "A"}, "urn:uuid:6ba7b810-9dad-11d1-80b4-00c04fd430c8", "A"},
		"opaque":            {opds.Device{ID: "kobo 1", Name: "A"}, "urn:opds:device:kobo%201", "A"},
		"empty":             {opds.Device{}, "urn:opds:device:unknown", "Unknown device"},
	} {
		t.Run(name, func(t *testing.T) {
			id, n := NormalizeDevice(tc.in)
			if id != tc.id || n != tc.name {
				t.Errorf("got %q / %q, want %q / %q", id, n, tc.id, tc.name)
			}
		})
	}
}
