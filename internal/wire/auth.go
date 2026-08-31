// Package wire holds the JSON encodings shared by the server (opdshttp) and
// the client (opdsclient): the OPDS Authentication Document and the two
// progression document shapes. It is internal because the documents are
// protocol details — callers work with the version-neutral opds types on
// either side of them.
package wire

import (
	"encoding/json"
	"errors"

	"github.com/ophymx/opds"
)

// JSON shape of an Authentication Document
// (https://drafts.opds.io/authentication-for-opds-1.0.html). Field order is
// the serialization order; both directions share these types so the two sides
// of the protocol cannot drift.
type (
	authDocJSON struct {
		ID             string         `json:"id"`
		Title          string         `json:"title"`
		Description    string         `json:"description,omitempty"`
		Links          []authLinkJSON `json:"links,omitempty"`
		Authentication []authFlowJSON `json:"authentication"`
	}
	authFlowJSON struct {
		Type   string          `json:"type"`
		Labels *authLabelsJSON `json:"labels,omitempty"`
		Links  []authLinkJSON  `json:"links,omitempty"`
	}
	authLabelsJSON struct {
		Login    string `json:"login,omitempty"`
		Password string `json:"password,omitempty"`
	}
	authLinkJSON struct {
		Rel   relJSON `json:"rel,omitempty"`
		Href  string  `json:"href"`
		Type  string  `json:"type,omitempty"`
		Title string  `json:"title,omitempty"`
	}
)

// relJSON is a link relation that serializes as a single string but accepts
// either a string or an array on the way in, which the format permits and
// deployed catalogs use.
type relJSON string

func (r *relJSON) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*r = relJSON(s)
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	if len(many) > 0 {
		*r = relJSON(many[0])
	}
	return nil
}

// MarshalAuthDocument renders d, filling in defaultID when d.ID is empty. A
// document with no explicit flows declares HTTP Basic built from the
// document-level labels, which is the shape a catalog offering nothing else
// wants and the only flow this library can satisfy on its own.
func MarshalAuthDocument(d opds.AuthDocument, defaultID string) ([]byte, error) {
	id := d.ID
	if id == "" {
		id = defaultID
	}
	flows := d.Authentication
	if len(flows) == 0 {
		flows = []opds.AuthFlow{{
			Type:          opds.AuthFlowBasic,
			LoginLabel:    d.LoginLabel,
			PasswordLabel: d.PasswordLabel,
		}}
	}
	doc := authDocJSON{
		ID:             id,
		Title:          d.Title,
		Description:    d.Description,
		Links:          buildAuthLinks(d.Links),
		Authentication: make([]authFlowJSON, 0, len(flows)),
	}
	for _, f := range flows {
		jf := authFlowJSON{Type: f.Type, Links: buildAuthLinks(f.Links)}
		if f.LoginLabel != "" || f.PasswordLabel != "" {
			jf.Labels = &authLabelsJSON{Login: f.LoginLabel, Password: f.PasswordLabel}
		}
		doc.Authentication = append(doc.Authentication, jf)
	}
	return json.Marshal(doc)
}

// ParseAuthDocument decodes an Authentication Document. The document-level
// label shorthand is filled from the Basic flow when it declares one, so a
// caller that only speaks Basic never has to walk the flow list.
func ParseAuthDocument(b []byte) (*opds.AuthDocument, error) {
	var doc authDocJSON
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if len(doc.Authentication) == 0 {
		return nil, errors.New("opds: authentication document declares no flows")
	}
	out := &opds.AuthDocument{
		ID:          doc.ID,
		Title:       doc.Title,
		Description: doc.Description,
		Links:       parseAuthLinks(doc.Links),
	}
	for _, f := range doc.Authentication {
		flow := opds.AuthFlow{Type: f.Type, Links: parseAuthLinks(f.Links)}
		if f.Labels != nil {
			flow.LoginLabel, flow.PasswordLabel = f.Labels.Login, f.Labels.Password
		}
		out.Authentication = append(out.Authentication, flow)
	}
	if basic, ok := out.Flow(opds.AuthFlowBasic); ok {
		out.LoginLabel, out.PasswordLabel = basic.LoginLabel, basic.PasswordLabel
	}
	return out, nil
}

func buildAuthLinks(links []opds.Link) []authLinkJSON {
	if len(links) == 0 {
		return nil
	}
	out := make([]authLinkJSON, 0, len(links))
	for _, l := range links {
		out = append(out, authLinkJSON{Rel: relJSON(l.Rel), Href: l.Href, Type: l.Type, Title: l.Title})
	}
	return out
}

func parseAuthLinks(links []authLinkJSON) []opds.Link {
	if len(links) == 0 {
		return nil
	}
	out := make([]opds.Link, 0, len(links))
	for _, l := range links {
		out = append(out, opds.Link{Rel: string(l.Rel), Href: l.Href, Type: l.Type, Title: l.Title})
	}
	return out
}
