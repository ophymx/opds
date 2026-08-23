package opds

import "time"

// Progression is a user's last-known reading position in a publication, per
// the OPDS Progression 1.0 draft
// (https://drafts.opds.io/opds-progression-1.0.html, as retrieved 2026-08-22).
// Progression is peer wire vocabulary like PageStream: the model lives here,
// and the opdshttp handler serves it per-user behind authentication when a
// ProgressionStore is configured.
type Progression struct {
	// Progression is the total progression through the publication, as a
	// fraction in [0, 1]. Required.
	Progression float64
	// Modified is when the progression was recorded. Required; the server
	// rejects updates older than the stored progression.
	Modified time.Time
	// Device identifies where the progression was recorded. Required.
	Device Device
	// Title optionally contextualizes the position for display (e.g. the
	// current chapter heading).
	Title string
	// References optionally refine the position as media-fragment URIs. The
	// library passes them through opaquely and never interprets them.
	References []string
}

// Device identifies the device a Progression was recorded on.
type Device struct {
	// ID is a URI identifying the device (e.g. a urn:uuid:). Required.
	ID string
	// Name is the user-facing device name. Required.
	Name string
}
