package opensearch

import (
	"encoding/xml"
	"errors"
	"net/url"
	"strings"

	"github.com/ophymx/opds"
)

// Unmarshal decodes an OpenSearch description document. A document may
// advertise several result formats; the one whose type names an OPDS feed is
// chosen, falling back to the first, so the returned Template is the one an
// OPDS client should use.
func Unmarshal(b []byte) (Description, error) {
	var doc osDescription
	if err := xml.Unmarshal(b, &doc); err != nil {
		return Description{}, err
	}
	if len(doc.URLs) == 0 {
		return Description{}, errors.New("opensearch: description document declares no Url")
	}
	chosen := doc.URLs[0]
	for _, u := range doc.URLs {
		if strings.Contains(u.Type, "opds") {
			chosen = u
			break
		}
	}
	return Description{
		ShortName:   doc.ShortName,
		Description: doc.Description,
		Template:    chosen.Template,
		ResultType:  chosen.Type,
	}, nil
}

// Search returns the URL that searches for terms, expanding the description's
// template. It is shorthand for Expand with only {searchTerms} supplied.
func (d Description) Search(terms string) string {
	return Expand(d.Template, map[string]string{"searchTerms": terms})
}

// Expand substitutes template parameters, covering both shapes an OPDS
// catalog advertises: the OpenSearch {name} placeholders of a 1.x description
// document, and the RFC 6570 form-style expansions of a 2.0 search link
// ("/search{?q}" and its "{&page}" continuation form).
//
// Parameter names are matched on their local part, so the {atom:author} an
// OPDS 1.x catalog advertises is supplied as "author"; a trailing "?" marks a
// parameter optional and is ignored when matching. Values are query-escaped.
// A placeholder with no value supplied expands to nothing, which is what
// leaves an optional parameter out of the request — and a form-style
// expansion whose variables are all unsupplied contributes no "?" either.
func Expand(template string, params map[string]string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(template, '{')
		if i < 0 {
			b.WriteString(template)
			return b.String()
		}
		j := strings.IndexByte(template[i:], '}')
		if j < 0 {
			b.WriteString(template)
			return b.String()
		}
		b.WriteString(template[:i])
		expr := template[i+1 : i+j]
		template = template[i+j+1:]
		expandExpr(&b, expr, params)
	}
}

// expandExpr writes one {...} expression. The leading operator, when there is
// one, decides whether the supplied variables are written bare or as a query
// string.
func expandExpr(b *strings.Builder, expr string, params map[string]string) {
	if expr == "" {
		return
	}
	op := expr[0]
	if op != '?' && op != '&' {
		if v, ok := params[paramName(expr)]; ok {
			b.WriteString(url.QueryEscape(v))
		}
		return
	}
	sep := op
	for name := range strings.SplitSeq(expr[1:], ",") {
		key := paramName(name)
		v, ok := params[key]
		if !ok {
			continue
		}
		b.WriteByte(sep)
		sep = '&'
		b.WriteString(url.QueryEscape(key))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(v))
	}
}

// paramName reduces a template parameter to the name callers supply: without
// its namespace prefix and without the optional marker.
func paramName(s string) string {
	s = strings.TrimSuffix(s, "?")
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// IsDescription reports whether a media type names an OpenSearch description
// document, which is how an OPDS 1.x search link is told apart from a link
// straight to a result feed.
func IsDescription(mediaType string) bool {
	return strings.Contains(mediaType, "opensearchdescription") ||
		strings.HasPrefix(mediaType, opds.MediaTypeOpenSearch)
}
