// Package org is the control plane's Forge-tenancy feature: organizations, the
// members that belong to them, and the invitation flow that adds new members
// (DESIGN.md §22).
//
// Identity is Gombit's. A member is a Gombit auth.User (the cookie session
// establishes who the caller is); this package never stores credentials or a
// second user table. What it adds is the tenancy that Gombit has no opinion
// about — which user holds which Forge-level role in which organization — plus
// the invitation flow that grants it. Authorization over those roles is a thin
// capability matrix (see roles.go), not a parallel permission engine (D12).
package org

import (
	"time"

	"github.com/gombit-dev/gombit/auth"
)

// Organization is a Forge tenant. It owns projects (added in #37) and has
// members with Forge-level roles.
type Organization struct {
	ID        uint      `gorm:"primaryKey"`
	Name      string    `gorm:"size:120;not null"`
	Slug      string    `gorm:"uniqueIndex;size:120;not null"`
	CreatedAt time.Time `gorm:"index"`
	UpdatedAt time.Time
}

// Member links a Gombit user to an organization with a Forge-level role. The
// (organization, user) pair is unique: a user holds exactly one role per org.
//
// OrganizationID and UserID are foreign keys. The ON DELETE rules are declared
// through the Organization and User association fields below (#101): CASCADE on
// the organization side (dropping an org removes its members and invitations)
// and RESTRICT on the user side (a Gombit user cannot be deleted out from under
// a live membership). Declaring them on the model rather than only in the
// migration keeps `gombit db makemigrations` from drifting — the desired schema
// it diffs against includes the constraints.
type Member struct {
	ID             uint `gorm:"primaryKey"`
	OrganizationID uint `gorm:"not null;uniqueIndex:uidx_org_member,priority:1"`
	UserID         uint `gorm:"not null;uniqueIndex:uidx_org_member,priority:2"`
	Role           Role `gorm:"size:20;not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// Association fields exist only to carry the foreign-key DDL; they are
	// pointers so a nil (the create paths never set them) is skipped rather
	// than upserting a zero row. Reads use the *ID columns, not these.
	Organization *Organization `gorm:"foreignKey:OrganizationID;constraint:OnDelete:CASCADE"`
	User         *auth.User    `gorm:"foreignKey:UserID;constraint:OnDelete:RESTRICT"`
}

// TableName pins the table so renaming the Go type never migrates the data.
func (Member) TableName() string { return "organization_members" }

// Invitation is a pending grant of membership to an email address. The raw
// token is shown to the inviter once (to put in the invite link) and never
// stored; only its hash is persisted, the same discipline Gombit uses for
// refresh tokens. Accepting a still-valid, unaccepted, unexpired invitation
// creates the Member.
//
// Email is stored already normalized (see org.normalizeEmail), so it is the
// same relation as users.email. The partial unique index makes at most one
// non-accepted invitation exist per (organization, email); accepted rows are
// exempt, so history is preserved. Re-inviting an address is a reissue:
// InviteMember supersedes the existing non-accepted row inside the same
// transaction, so this index never blocks a legitimate re-invite (an expired
// invitation does not lock the address) — it only stops two live tokens for
// one address from a concurrent race.
type Invitation struct {
	ID              uint       `gorm:"primaryKey"`
	OrganizationID  uint       `gorm:"not null;uniqueIndex:uidx_pending_invite,priority:1,where:accepted_at IS NULL"`
	Email           string     `gorm:"size:255;not null;index;uniqueIndex:uidx_pending_invite,priority:2,where:accepted_at IS NULL"`
	Role            Role       `gorm:"size:20;not null"`
	TokenHash       string     `gorm:"size:64;not null;uniqueIndex"`
	InvitedByUserID uint       `gorm:"not null"`
	ExpiresAt       time.Time  `gorm:"not null"`
	AcceptedAt      *time.Time `gorm:"index"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// Foreign keys (see Member): the organization CASCADEs, the inviting user
	// RESTRICTs. Pointer associations carry only the DDL.
	Organization *Organization `gorm:"foreignKey:OrganizationID;constraint:OnDelete:CASCADE"`
	InvitedBy    *auth.User    `gorm:"foreignKey:InvitedByUserID;constraint:OnDelete:RESTRICT"`
}
