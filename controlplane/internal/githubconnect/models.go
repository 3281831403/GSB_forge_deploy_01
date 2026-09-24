// Package githubconnect is the control-plane side of the optional GitHub export
// (M7 #85): the OAuth connect flow that links a Forge user to a GitHub account
// and stores the resulting access token, which the export-to-GitHub endpoint
// (a follow-up) uses to create and push a repository via internal/githubexport.
//
// Identity is Gombit's (D12): a connection belongs to an auth.User the cookie
// session establishes; this package stores no credentials of its own beyond the
// GitHub OAuth token it is granted.
package githubconnect

import "time"

// Connection is a user's stored GitHub OAuth access token, granting Forge the
// ability to create and push repositories on their behalf. At most one
// connection per user (UserID is unique).
//
// The token is stored as granted. Encrypting it at rest is a deployment concern
// worth adding before this ships to real users (the same class of secret Gombit
// Cloud manages for runtime, ADR-005); it is called out here so the follow-up
// wiring decides it deliberately rather than by omission.
type Connection struct {
	ID          uint   `gorm:"primaryKey"`
	UserID      uint   `gorm:"uniqueIndex;not null"`
	AccessToken string `gorm:"not null"`
	Login       string `gorm:"size:120"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// OAuthState is a one-time, per-user anti-CSRF token for the connect flow.
// StartConnect mints a cryptographically-random state bound to the initiating
// user and CompleteConnect consumes it exactly once, so an attacker cannot graft
// their GitHub authorization onto a victim's Forge session (or vice versa) — the
// single most important property of an OAuth connect flow.
type OAuthState struct {
	ID        uint      `gorm:"primaryKey"`
	State     string    `gorm:"uniqueIndex;size:64;not null"`
	UserID    uint      `gorm:"index;not null"`
	ExpiresAt time.Time `gorm:"index;not null"`
	CreatedAt time.Time
}
