package opds

// AuthDocument describes how a client authenticates with a catalog, per
// Authentication for OPDS 1.0
// (https://drafts.opds.io/authentication-for-opds-1.0.html). It is the body a
// server returns with a 401 and serves at a stable URL, and the document a
// client parses to learn which credentials to ask its user for.
//
// Like Progression, this is version-neutral wire vocabulary: the model lives
// here, opdshttp serves it, and opdsclient consumes it.
type AuthDocument struct {
	// ID is the document's canonical URL.
	ID string
	// Title names the catalog access is being requested for. Required; a
	// server also uses it as the Basic realm in its WWW-Authenticate challenge.
	Title string
	// Description optionally tells the user how to authenticate
	// (e.g. "Enter your library card number and PIN.").
	Description string
	// LoginLabel and PasswordLabel are alternate labels for the credential
	// fields (e.g. "Library card" and "PIN"), a shorthand for the common case
	// of a document declaring nothing but the Basic flow: a server with no
	// explicit Authentication synthesizes that flow from them, and a parsed
	// document copies them back out of it. Empty means the client shows its
	// own defaults.
	LoginLabel    string
	PasswordLabel string
	// Authentication lists the declared flows. A server may leave it empty,
	// which declares HTTP Basic (AuthFlowBasic); a parsed document always has
	// at least one entry, since the format requires it.
	Authentication []AuthFlow
	// Links are associated resources: rel "logo" (an image type), "help" (a
	// page or mailto: URL), and "register".
	Links []Link
}

// AuthFlow is one authentication method a catalog offers.
type AuthFlow struct {
	// Type is the flow's URI, e.g. AuthFlowBasic. Required.
	Type string
	// LoginLabel and PasswordLabel are alternate labels for the flow's
	// credential fields.
	LoginLabel    string
	PasswordLabel string
	// Links are resources specific to this flow (an authenticate endpoint for
	// a token-based flow, for instance). The library passes them through.
	Links []Link
}

// Flow returns the declared flow of the given type. A document with no
// explicit flows is treated as declaring AuthFlowBasic, matching how a server
// renders it.
func (d *AuthDocument) Flow(typ string) (AuthFlow, bool) {
	if len(d.Authentication) == 0 {
		if typ != AuthFlowBasic {
			return AuthFlow{}, false
		}
		return AuthFlow{Type: AuthFlowBasic, LoginLabel: d.LoginLabel, PasswordLabel: d.PasswordLabel}, true
	}
	for _, f := range d.Authentication {
		if f.Type == typ {
			return f, true
		}
	}
	return AuthFlow{}, false
}

// SupportsBasic reports whether the document offers the HTTP Basic flow, the
// one flow this library can satisfy without application help.
func (d *AuthDocument) SupportsBasic() bool {
	_, ok := d.Flow(AuthFlowBasic)
	return ok
}
