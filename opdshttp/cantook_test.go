package opdshttp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ophymx/opds"
)

// The Cantook/Readium progression alias: the pre-spec document shape served by
// Komga and Stump, translated onto the same ProgressionStore.

// doReadium performs an authenticated request against the alias endpoint with
// the Readium media type set the way real clients set it.
func doReadium(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.SetBasicAuth("jane", "secret")
	if method == http.MethodPut {
		r.Header.Set("Content-Type", opds.MediaTypeProgressionReadium)
	} else {
		r.Header.Set("Accept", opds.MediaTypeProgressionReadium)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// validReadium mirrors Stump's own test fixture: a page-based (manga-style)
// locator with totalProgression.
const validReadium = `{
	"modified": "2026-08-01T10:00:00.986000-07:00",
	"device": {"id": "device-123", "name": "Aldiko Next"},
	"locator": {
		"href": "/books/1/pages/5",
		"type": "image/jpeg",
		"title": "Page 5",
		"locations": {"position": 5, "progression": 0.25, "totalProgression": 0.25}
	}
}`

type readiumDoc struct {
	Modified string
	Device   struct{ ID, Name string }
	Locator  struct {
		Href      string
		Title     string
		Locations struct {
			Fragments        []string
			TotalProgression float64
		}
	}
}

func TestCantookLinkInjected(t *testing.T) {
	w := do(t, newProgressionServer(), http.MethodGet, "/opds/feed/new?version=2", "")
	var feed struct {
		Publications []struct {
			Links []struct{ Rel, Href, Type string } `json:"links"`
		} `json:"publications"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range feed.Publications[0].Links {
		if l.Rel == opds.RelProgressionCantook {
			found = true
			if l.Href != "/opds/progression/urn:b1?format=readium" || l.Type != opds.MediaTypeProgressionReadium {
				t.Errorf("cantook link = %+v", l)
			}
		}
	}
	if !found {
		t.Errorf("no cantook progression link:\n%s", w.Body.String())
	}
}

func TestCantookGetEmptyIs204(t *testing.T) {
	// Komga parity: no stored progression answers 204, not the draft's 200.
	w := doReadium(t, newProgressionServer(), http.MethodGet, "/opds/progression/urn:b1", "")
	if w.Code != http.StatusNoContent {
		t.Errorf("code = %d, want 204", w.Code)
	}
}

func TestCantookPutTranslatesOntoStore(t *testing.T) {
	h := newProgressionServer()
	w := doReadium(t, h, http.MethodPut, "/opds/progression/urn:b1", validReadium)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT code = %d, want 204 (Komga parity): %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("PUT body = %q, want empty", w.Body.String())
	}

	// The draft endpoint sees the translated document.
	w = do(t, h, http.MethodGet, "/opds/progression/urn:b1", "")
	var doc struct {
		Progression float64
		Title       string
		Device      struct{ ID, Name string }
		References  []string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("draft GET: %v\n%s", err, w.Body.String())
	}
	// The alias accepts Komga's opaque device id as sent, but the draft
	// document served for the same record must carry a URI, so the id is
	// wrapped on the way out rather than emitted as a bare token the draft's
	// schema would reject.
	if doc.Progression != 0.25 || doc.Title != "Page 5" || doc.Device.ID != "urn:opds:device:device-123" {
		t.Errorf("translated doc = %+v", doc)
	}
	if len(doc.References) != 1 || doc.References[0] != "/books/1/pages/5" {
		t.Errorf("references = %v, want the locator href", doc.References)
	}

	// And the alias endpoint round-trips its own shape.
	w = doReadium(t, h, http.MethodGet, "/opds/progression/urn:b1", "")
	if w.Code != 200 {
		t.Fatalf("readium GET code = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != opds.MediaTypeProgressionReadium {
		t.Errorf("content-type = %q", ct)
	}
	var rdoc readiumDoc
	if err := json.Unmarshal(w.Body.Bytes(), &rdoc); err != nil {
		t.Fatal(err)
	}
	if rdoc.Locator.Locations.TotalProgression != 0.25 || rdoc.Locator.Href != "/books/1/pages/5" {
		t.Errorf("round-trip = %+v", rdoc.Locator)
	}
	// The alias round-trips the id it was given: a Cantook client must still
	// recognize its own device.
	if rdoc.Device.ID != "device-123" || rdoc.Device.Name != "Aldiko Next" || rdoc.Locator.Title != "Page 5" {
		t.Errorf("device/title = %+v / %q", rdoc.Device, rdoc.Locator.Title)
	}
}

func TestCantookFragmentsRoundTrip(t *testing.T) {
	h := newProgressionServer()
	body := `{
		"modified": "2026-08-01T10:00:00Z",
		"device": {"id": "d", "name": "D"},
		"locator": {
			"href": "chapter3.html",
			"type": "application/xhtml+xml",
			"locations": {"fragments": [":~:text=meanwhile"], "totalProgression": 0.42}
		}
	}`
	if w := doReadium(t, h, http.MethodPut, "/opds/progression/urn:b1", body); w.Code != 204 {
		t.Fatalf("PUT code = %d", w.Code)
	}

	// Draft view: href#fragment as a media-fragment URI reference.
	w := do(t, h, http.MethodGet, "/opds/progression/urn:b1", "")
	if !strings.Contains(w.Body.String(), `"chapter3.html#:~:text=meanwhile"`) {
		t.Errorf("draft references missing media-fragment URI:\n%s", w.Body.String())
	}

	// Readium view: factored back into href + fragments.
	var rdoc readiumDoc
	if err := json.Unmarshal(doReadium(t, h, http.MethodGet, "/opds/progression/urn:b1", "").Body.Bytes(), &rdoc); err != nil {
		t.Fatal(err)
	}
	if rdoc.Locator.Href != "chapter3.html" ||
		len(rdoc.Locator.Locations.Fragments) != 1 ||
		rdoc.Locator.Locations.Fragments[0] != ":~:text=meanwhile" {
		t.Errorf("locator = %+v", rdoc.Locator)
	}
}

func TestCantookStaleRejected(t *testing.T) {
	h := newProgressionServer()
	doReadium(t, h, http.MethodPut, "/opds/progression/urn:b1", validReadium)
	older := strings.Replace(validReadium, "2026-08-01T10:00:00.986000-07:00", "2026-07-01T10:00:00Z", 1)
	if w := doReadium(t, h, http.MethodPut, "/opds/progression/urn:b1", older); w.Code != http.StatusConflict {
		t.Errorf("stale PUT code = %d, want 409 (Komga parity)", w.Code)
	}
}

func TestCantookPutInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"malformed JSON":           `{`,
		"totalProgression missing": `{"modified": "2026-08-01T10:00:00Z", "device": {"id": "d", "name": "D"}, "locator": {"href": "x", "type": "image/jpeg"}}`,
		"totalProgression > 1":     `{"modified": "2026-08-01T10:00:00Z", "device": {"id": "d", "name": "D"}, "locator": {"locations": {"totalProgression": 1.5}}}`,
		"modified missing":         `{"device": {"id": "d", "name": "D"}, "locator": {"locations": {"totalProgression": 0.5}}}`,
	} {
		w := doReadium(t, newProgressionServer(), http.MethodPut, "/opds/progression/urn:b1", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400", name, w.Code)
		}
	}
}

// The alias must not be stricter than the deployed servers: Komga accepts an
// empty device, so a device-less client still syncs.
func TestCantookDeviceOptional(t *testing.T) {
	body := `{
		"modified": "2026-08-01T10:00:00Z",
		"device": {"id": "", "name": ""},
		"locator": {"locations": {"totalProgression": 0.5}}
	}`
	if w := doReadium(t, newProgressionServer(), http.MethodPut, "/opds/progression/urn:b1", body); w.Code != 204 {
		t.Errorf("code = %d, want 204 with empty device", w.Code)
	}
}

// The ?format=readium query parameter alone selects the alias, for clients
// that follow the advertised href without setting negotiation headers.
func TestCantookSelectedByQueryParam(t *testing.T) {
	h := newProgressionServer()
	doReadium(t, h, http.MethodPut, "/opds/progression/urn:b1", validReadium)
	w := do(t, h, http.MethodGet, "/opds/progression/urn:b1?format=readium", "")
	if ct := w.Header().Get("Content-Type"); ct != opds.MediaTypeProgressionReadium {
		t.Errorf("content-type = %q, want readium via query param", ct)
	}
	if !strings.Contains(w.Body.String(), "totalProgression") {
		t.Errorf("body is not the readium shape:\n%s", w.Body.String())
	}
}
