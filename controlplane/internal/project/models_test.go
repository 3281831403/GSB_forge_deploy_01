package project_test

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
)

// TestCloudProjectIDIsUniquePerCounterpart pins the one invariant this field
// carries (ADR-005 D6): at most one Forge project may claim a given Cloud
// counterpart. Two projects sharing a cloud_project_id would drive the same
// Cloud runtime from two authoring projects, so a deploy of one silently
// overwrites the other and the audit trail cannot attribute a Cloud deployment
// to a single Forge project. The nullable unique index is what forbids it.
func TestCloudProjectIDIsUniquePerCounterpart(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)

	cloudID := "cloud-abc"
	first := &project.Project{OrganizationID: 1, Name: "A", Slug: "a", CloudProjectID: &cloudID, CreatedBy: 7}
	if err := db.Create(first).Error; err != nil {
		t.Fatalf("first link: %v", err)
	}

	// A different Forge project (distinct slug so the collision is on the Cloud
	// id, not the org+slug index) must not claim the same counterpart.
	second := &project.Project{OrganizationID: 1, Name: "B", Slug: "b", CloudProjectID: &cloudID, CreatedBy: 7}
	if err := db.Create(second).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("second project claiming the same cloud_project_id: got %v, want gorm.ErrDuplicatedKey", err)
	}
}

// TestUnlinkedProjectsAreUnconstrained is the other half of the nullable unique
// index: unlimited projects may sit unlinked (CloudProjectID nil), because
// Postgres treats NULLs as distinct. Without this, "nil until linked" and the
// uniqueness rule would be in tension.
func TestUnlinkedProjectsAreUnconstrained(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)

	for _, slug := range []string{"x", "y", "z"} {
		p := &project.Project{OrganizationID: 1, Name: slug, Slug: slug, CreatedBy: 7}
		if err := db.Create(p).Error; err != nil {
			t.Fatalf("unlinked project %q: %v", slug, err)
		}
	}
}
