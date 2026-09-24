package project_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/gombit-dev/gombit/auth"
	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/audit"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/org"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
)

// seedRefs inserts the organization and user a project references so these
// tests hold whether or not the #101 foreign keys are present in the schema
// under test. On a schema without them the rows are harmless; with them they
// make the project's organization_id/created_by valid.
func seedRefs(t *testing.T, db *gorm.DB, orgID, userID uint) {
	t.Helper()
	if err := db.Create(&auth.User{ID: userID, Email: fmt.Sprintf("u%d@example.test", userID), PasswordHash: "x"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.Create(&org.Organization{ID: orgID, Name: "Acme", Slug: fmt.Sprintf("acme-%d", orgID)}).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
}

// TestCreateProjectRecordsAudit: creating a project records project.created,
// attributed to the actor and the org, referencing the project by id — and
// carrying only that reference.
func TestCreateProjectRecordsAudit(t *testing.T) {
	db := dbtest.DB(t)
	seedRefs(t, db, 1, 7)
	ctx := context.Background()

	p, err := project.NewService(db).CreateProject(ctx, 1, "Acme CRM", "acme-crm", 7)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	events, err := audit.List(ctx, db, audit.Filter{OrganizationID: 1})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Action != audit.ActionProjectCreated {
		t.Errorf("action = %q, want project.created", e.Action)
	}
	if e.ActorUserID == nil || *e.ActorUserID != 7 {
		t.Errorf("actor = %v, want 7", e.ActorUserID)
	}
	if e.TargetType != "project" || e.TargetID != fmt.Sprint(p.ID) {
		t.Errorf("target = (%s,%s), want (project,%d)", e.TargetType, e.TargetID, p.ID)
	}
}

// TestCreateRevisionRecordsAudit: a revision records spec.revision.created in
// addition to CreateProject's project.created, both scoped to the org.
func TestCreateRevisionRecordsAudit(t *testing.T) {
	db := dbtest.DB(t)
	seedRefs(t, db, 1, 7)
	ctx := context.Background()
	svc := project.NewService(db)

	p, err := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	rev, err := svc.CreateRevision(ctx, p.ID, validSpec(t, "V1", "v1"), 7)
	if err != nil {
		t.Fatalf("create revision: %v", err)
	}

	revised, err := audit.List(ctx, db, audit.Filter{OrganizationID: 1, Action: audit.ActionSpecRevised})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(revised) != 1 {
		t.Fatalf("spec.revision.created events = %d, want 1", len(revised))
	}
	if revised[0].TargetType != "revision" || revised[0].TargetID != fmt.Sprint(rev.ID) {
		t.Errorf("target = (%s,%s), want (revision,%d)", revised[0].TargetType, revised[0].TargetID, rev.ID)
	}

	all, err := audit.List(ctx, db, audit.Filter{OrganizationID: 1})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("total org events = %d, want 2 (project.created + spec.revision.created)", len(all))
	}
}
