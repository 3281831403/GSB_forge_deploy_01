// Package project is the control plane's project and revision store (DESIGN.md
// §8). A Project belongs to an organization and accumulates an append-only
// chain of immutable ProjectRevisions; each revision pins the exact canonical
// ProjectSpec and its hash, and points at the revision it descended from. That
// chain is what makes deterministic rebuilds, rollback, diff and semantic
// lineage possible (DESIGN.md §8, ADR-001 §60).
//
// The canonical encoding and hash come from the compiler's internal/spec — the
// single source of truth for how a spec serializes (ADR-001 §70) — so a
// revision's stored bytes and the compiler's are the same bytes.
package project

import (
	"errors"
	"time"

	"github.com/gombit-dev/gombit/auth"
	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/org"
)

// Project is a Forge project: a named, org-scoped container for a spec that
// evolves through revisions. HeadRevisionID is the current revision, nil until
// the first is created.
type Project struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null;uniqueIndex:uidx_org_project_slug,priority:1"`
	Name           string `gorm:"size:120;not null"`
	Slug           string `gorm:"size:120;not null;uniqueIndex:uidx_org_project_slug,priority:2"`
	// HeadRevisionID is the project's current revision. It is a plain uint FK
	// with no database constraint yet; like the org models, the FK decision is
	// deferred to #101's initial migration (ON DELETE for organization_id is
	// CASCADE; head_revision_id references project_revisions and must be
	// nullable + ON DELETE SET NULL so a rollback that prunes revisions cannot
	// orphan the project).
	HeadRevisionID *uint `gorm:"index"`
	// CloudProjectID links this Forge project to its Gombit Cloud counterpart
	// (ADR-005 D6). Runtime state — builds, deployments, environments, the
	// database, secrets, domains — lives in Cloud, not the Forge control plane;
	// this opaque identifier is the whole of Forge's knowledge of it. Stored as
	// a string because it is Cloud's ID space, not a Forge FK — there is no
	// table here to reference.
	//
	// Nil until the project is linked to Cloud, which happens at its first
	// preview: under ADR-005 D5 preview is a Cloud primitive, so Cloud must know
	// the project before it can provision one. A project can be authored and
	// revised before it is linked; it cannot be previewed or deployed. Every
	// Cloud-facing operation must treat nil as "not linked yet" and refuse
	// rather than dereferencing.
	//
	// uniqueIndex, not a plain index: one Forge project per Cloud counterpart. A
	// nullable unique index permits unlimited unlinked (NULL) rows while
	// forbidding two projects from claiming the same cloud_project_id, so "nil
	// until linked" and "at most one project per counterpart" do not conflict.
	// #101 materializes this from the tag, the way head_revision_id's ON DELETE
	// SET NULL is recorded above.
	CloudProjectID *string `gorm:"size:64;uniqueIndex"`
	CreatedBy      uint    `gorm:"not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// Foreign keys (#101). Pointer associations carry only the DDL; the create
	// paths set the *ID columns, never these. Organization CASCADEs — dropping
	// an org removes its projects. Head references project_revisions ON DELETE
	// SET NULL so pruning a revision cannot orphan the project (it matches
	// HeadRevisionID's nullability). Creator RESTRICTs — a user who owns
	// projects cannot be deleted out from under them.
	Organization *org.Organization `gorm:"foreignKey:OrganizationID;constraint:OnDelete:CASCADE"`
	Head         *Revision         `gorm:"foreignKey:HeadRevisionID;constraint:OnDelete:SET NULL"`
	Creator      *auth.User        `gorm:"foreignKey:CreatedBy;constraint:OnDelete:RESTRICT"`
}

// Revision is one immutable snapshot of a project's spec (DESIGN.md §8). It is
// append-only: once written it is never updated. The BeforeUpdate hook below
// enforces that against GORM's model API — the Updates/Save paths a human
// actually takes — rather than leaving it to the service's call sites; a raw
// Exec or an explicit Session{SkipHooks: true} still bypasses it, and closing
// those is the job of the database-level rule #101 adds. Every promise this
// package makes (deterministic rebuild, rollback, diff, lineage) reduces to
// "these bytes are the bytes that were accepted", and a silent UPDATE that
// rewrote spec_json and spec_hash together would break all of them while the
// stored hash still matched. The absence of an UpdatedAt is a signal of this,
// but only the hook makes it a property.
//
// Append-only, not write-once-forever: deletion stays possible (there is no
// BeforeDelete guard), because a rollback that prunes revisions is a legitimate
// operation the HeadRevisionID comment above anticipates.
type Revision struct {
	ID          uint `gorm:"primaryKey"`
	ProjectID   uint `gorm:"not null;index"`
	SpecVersion int  `gorm:"not null"`
	// SpecJSON is the exact canonical encoding (spec.Marshal). It is stored as
	// text, not jsonb: jsonb reorders keys and drops whitespace, which would
	// make the stored bytes differ from the bytes SpecHash was taken over and
	// break byte-for-byte lineage (ADR-001 §60/§70).
	SpecJSON string `gorm:"type:text;not null"`
	// SpecHash is spec.Hash — the SHA-256 of SpecJSON — recorded so lineage can
	// be checked without re-canonicalizing (ADR-001 §60).
	SpecHash string `gorm:"size:64;not null;index"`
	// ParentRevisionID is the revision this one descended from — the project's
	// head at creation time. Nil only for a project's first revision.
	ParentRevisionID *uint `gorm:"index"`
	CreatedBy        uint  `gorm:"not null"`
	CreatedAt        time.Time
	// Foreign keys (#101). Project CASCADEs — a project's revisions die with it.
	// Parent CASCADEs too: pruning a revision prunes its descendants (a revision
	// whose parent is gone is not a lineage). It is CASCADE, not SET NULL,
	// because SET NULL is implemented as an UPDATE of the child row, which the
	// project_revisions append-only trigger rejects — a chain of DELETEs does
	// not trip it, so pruning stays possible. Creator RESTRICTs. Pointer
	// associations carry only the DDL.
	Project *Project   `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	Parent  *Revision  `gorm:"foreignKey:ParentRevisionID;constraint:OnDelete:CASCADE"`
	Creator *auth.User `gorm:"foreignKey:CreatedBy;constraint:OnDelete:RESTRICT"`
}

// TableName pins the table so renaming the Go type never migrates the data.
func (Revision) TableName() string { return "project_revisions" }

// BeforeUpdate makes immutability real: GORM would otherwise happily UPDATE any
// column on this table. Insert and delete remain allowed (append-only); only
// in-place mutation is refused. When #101 authors the migration this can be
// reinforced with a database-level rule, but the hook is what enforces the AC
// today.
func (Revision) BeforeUpdate(*gorm.DB) error {
	return errors.New("project: revisions are immutable")
}
