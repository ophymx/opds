package opdsclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ophymx/opds"
	"github.com/ophymx/opds/internal/wire"
)

// ProgressionLink returns the progression endpoint a publication advertises.
// The OPDS Progression draft's own relation is preferred; the pre-spec Cantook
// relation that Komga and Stump serve is accepted as a fallback, so a client
// syncs with either kind of catalog without knowing which it is talking to.
// It reports false when the publication advertises neither.
func ProgressionLink(p *opds.Publication) (opds.Link, bool) {
	var cantook opds.Link
	for _, l := range p.Links {
		switch l.Rel {
		case opds.RelProgression:
			return l, true
		case opds.RelProgressionCantook:
			cantook = l
		}
	}
	return cantook, cantook.Href != ""
}

// Progression reads the reading position stored for the authenticated user at
// the given progression link, which ProgressionLink finds in a publication.
//
// A publication the user has not opened yet has no position: both the draft
// (200 with an empty payload) and the Cantook alias (204) say so without an
// error, and Progression reports it as a nil progression and a nil error.
func (c *Client) Progression(ctx context.Context, l opds.Link) (*opds.Progression, error) {
	readium := isReadiumProgression(l)
	accept := opds.MediaTypeProgression
	if readium {
		accept = opds.MediaTypeProgressionReadium
	}
	resp, u, err := c.do(ctx, http.MethodGet, l.Href, accept+", application/json;q=0.9", "", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	body, err := readBody(resp.Body, maxSmallDocument)
	if err != nil {
		return nil, fmt.Errorf("opdsclient: %s: %w", u, err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, nil
	}
	// The response's own media type is more reliable than the link's, which
	// only says what the catalog advertised.
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "readium.progression") {
		readium = true
	}
	p, err := parseProgression(body, readium)
	if err != nil {
		return nil, fmt.Errorf("opdsclient: %s: %w", u, err)
	}
	return p, nil
}

// SetProgression stores a reading position at the given progression link.
//
// The document is completed before it is sent: an empty Device is filled from
// WithDevice, and a zero Modified is stamped with the current time, since the
// timestamp is what orders updates and a zero one would lose to everything.
// The wire shape follows the link — the draft's document for the draft's
// relation, the Readium locator for the Cantook alias.
//
// A position older than the one already stored is refused by the catalog;
// the error matches ErrProgressionStale, and the right response is to take the
// server's position rather than retry.
func (c *Client) SetProgression(ctx context.Context, l opds.Link, p *opds.Progression) error {
	if p == nil {
		return errors.New("opdsclient: nil progression")
	}
	if p.Progression < 0 || p.Progression > 1 {
		return fmt.Errorf("opdsclient: progression %v is outside [0, 1]", p.Progression)
	}
	doc := *p
	if doc.Device.ID == "" && doc.Device.Name == "" {
		doc.Device = c.device
	}
	if doc.Modified.IsZero() {
		doc.Modified = time.Now()
	}
	readium := isReadiumProgression(l)
	var (
		body []byte
		ct   string
		err  error
	)
	if readium {
		body, err = wire.MarshalReadiumProgression(&doc)
		ct = opds.MediaTypeProgressionReadium
	} else {
		// The draft requires a device named by a URI and a non-empty name;
		// normalizing here is what lets a caller configure a bare UUID, the id
		// the deployed servers hand out, and still send a valid document.
		doc.Device.ID, doc.Device.Name = wire.NormalizeDevice(doc.Device)
		body, err = wire.MarshalProgression(&doc)
		ct = opds.MediaTypeProgression
	}
	if err != nil {
		return fmt.Errorf("opdsclient: encoding progression: %w", err)
	}
	resp, _, err := c.do(ctx, http.MethodPut, l.Href, ct+", "+mediaTypeProblem, ct, body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// mediaTypeProblem is the RFC 7807 media type the draft's errors use; naming
// it in Accept tells a catalog we can read its problem details.
const mediaTypeProblem = "application/problem+json"

// isReadiumProgression reports whether a link addresses the pre-spec Cantook
// alias rather than the draft endpoint, by its relation, its media type, or
// the format marker opdshttp puts in the href it advertises.
func isReadiumProgression(l opds.Link) bool {
	return l.Rel == opds.RelProgressionCantook ||
		strings.Contains(l.Type, "readium.progression") ||
		strings.Contains(l.Href, "format=readium")
}

func parseProgression(body []byte, readium bool) (*opds.Progression, error) {
	if readium {
		return wire.ParseReadiumProgression(body)
	}
	return wire.ParseProgression(body)
}
