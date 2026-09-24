package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// projectWithResource creates a project and one resource, returning the service,
// project id and the resource's stable ID.
func projectWithResource(t *testing.T) (*project.Service, uint, spec.ID) {
	t.Helper()
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()
	p, err := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddResource(ctx, p.ID, "Order", "Orders", 7); err != nil {
		t.Fatalf("add resource: %v", err)
	}
	return svc, p.ID, headSpec(t, svc, p.ID).Resources[0].ID
}

func TestAddFieldCommitsAndMintsSymbol(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	ctx := context.Background()

	res, err := svc.AddField(ctx, projectID, resID, project.FieldInput{Label: "Total", Type: spec.TypeDecimal, Required: true}, 7)
	if err != nil {
		t.Fatalf("add field: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted {
		t.Fatalf("add field outcome = %s, want committed", res.Outcome)
	}
	f := headSpec(t, svc, projectID).Resources[0].Fields[0]
	if f.Label != "Total" || f.Type != spec.TypeDecimal || !f.Required {
		t.Errorf("field = %+v, want Total/decimal/required", f)
	}
	if f.CodeName != "Total" || f.StorageName != "total" {
		t.Errorf("minted code=%q storage=%q, want Total/total", f.CodeName, f.StorageName)
	}
}

func TestAddFieldMintsUniqueSymbols(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	ctx := context.Background()

	if _, err := svc.AddField(ctx, projectID, resID, project.FieldInput{Label: "Note", Type: spec.TypeString}, 7); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := svc.AddField(ctx, projectID, resID, project.FieldInput{Label: "Note", Type: spec.TypeString}, 7); err != nil {
		t.Fatalf("second: %v", err)
	}
	fields := headSpec(t, svc, projectID).Resources[0].Fields
	if len(fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(fields))
	}
	if fields[0].CodeName == fields[1].CodeName || fields[0].StorageName == fields[1].StorageName {
		t.Errorf("field symbols/storage must be unique; got %+v", fields)
	}
}

func TestAddFieldRejectsBelongsTo(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	_, err := svc.AddField(context.Background(), projectID, resID, project.FieldInput{Label: "Customer", Type: spec.TypeBelongsTo}, 7)
	if !errors.Is(err, project.ErrInvalidFieldEdit) {
		t.Errorf("belongs_to via field editor = %v, want ErrInvalidFieldEdit", err)
	}
}

func TestUpdateFieldLabelIsNeutral(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	ctx := context.Background()

	if _, err := svc.AddField(ctx, projectID, resID, project.FieldInput{Label: "Total", Type: spec.TypeDecimal}, 7); err != nil {
		t.Fatalf("add: %v", err)
	}
	fieldID := headSpec(t, svc, projectID).Resources[0].Fields[0].ID

	res, err := svc.UpdateField(ctx, projectID, resID, fieldID, project.FieldInput{Label: "Amount", Type: spec.TypeDecimal, Required: true}, 7)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted || res.Class != "neutral" {
		t.Fatalf("relabel+require outcome=%s class=%s, want committed/neutral", res.Outcome, res.Class)
	}
	f := headSpec(t, svc, projectID).Resources[0].Fields[0]
	if f.Label != "Amount" || !f.Required {
		t.Errorf("field not updated: %+v", f)
	}
	if f.CodeName != "Total" {
		t.Errorf("relabel must not move the code symbol; got %q", f.CodeName)
	}
}

// TestUpdateFieldTypeIsBreaking is the §56/§93 acceptance criterion: an
// extension-visible type change is flagged breaking and not committed without
// validation.
func TestUpdateFieldTypeIsBreaking(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	ctx := context.Background()

	if _, err := svc.AddField(ctx, projectID, resID, project.FieldInput{Label: "Qty", Type: spec.TypeInteger}, 7); err != nil {
		t.Fatalf("add: %v", err)
	}
	head := headSpec(t, svc, projectID)
	fieldID := head.Resources[0].Fields[0].ID
	headBefore, _, _ := svc.Head(ctx, projectID)

	res, err := svc.UpdateField(ctx, projectID, resID, fieldID, project.FieldInput{Label: "Qty", Type: spec.TypeString}, 7)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.Outcome != project.OutcomeBreaking {
		t.Fatalf("a field type change must be breaking; got %s", res.Outcome)
	}
	// Not committed: head unchanged.
	headAfter, _, _ := svc.Head(ctx, projectID)
	if headAfter.ID != headBefore.ID {
		t.Error("a breaking type change must not advance head")
	}
}

// TestDeleteFieldIsBreaking: removing a field drops its accessor, so it is
// breaking and returned for validation, not committed.
func TestDeleteFieldIsBreaking(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	ctx := context.Background()

	if _, err := svc.AddField(ctx, projectID, resID, project.FieldInput{Label: "A", Type: spec.TypeString}, 7); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if _, err := svc.AddField(ctx, projectID, resID, project.FieldInput{Label: "B", Type: spec.TypeString}, 7); err != nil {
		t.Fatalf("add b: %v", err)
	}
	fieldID := headSpec(t, svc, projectID).Resources[0].Fields[0].ID

	res, err := svc.DeleteField(ctx, projectID, resID, fieldID, 7)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.Outcome != project.OutcomeBreaking {
		t.Errorf("a field delete must be breaking; got %s", res.Outcome)
	}
	// Not committed: both fields remain.
	if len(headSpec(t, svc, projectID).Resources[0].Fields) != 2 {
		t.Error("a breaking field delete must not commit")
	}
}

// TestUpdateFieldRejectsBelongsTo: updating an existing relationship field is
// deferred to the relationship editor (#46) with a clear rejection, not a
// confusing spec-validation failure.
func TestUpdateFieldRejectsBelongsTo(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	// Seed a spec with a belongs_to field via the raw candidate path.
	customer := spec.MustNewID(spec.KindResource)
	invoice := spec.MustNewID(spec.KindResource)
	rel := spec.MustNewID(spec.KindField)
	base := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: spec.MustNewID(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: customer, Label: "Customer", CodeName: "Customer", StorageName: "customers",
				Fields: []*spec.Field{{ID: spec.MustNewID(spec.KindField), Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"}}},
			{ID: invoice, Label: "Invoice", CodeName: "Invoice", StorageName: "invoices",
				Fields: []*spec.Field{{ID: rel, Label: "Customer", Type: spec.TypeBelongsTo, CodeName: "Customer", StorageName: "customer_id", Required: true, Target: customer}}},
		},
	}
	p, _ := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if r, err := svc.SubmitCandidate(ctx, p.ID, base, 7); err != nil || r.Outcome != project.OutcomeCommitted {
		t.Fatalf("seed: outcome=%v err=%v", r.Outcome, err)
	}

	_, err := svc.UpdateField(ctx, p.ID, invoice, rel, project.FieldInput{Label: "Buyer", Type: spec.TypeString}, 7)
	if !errors.Is(err, project.ErrInvalidFieldEdit) {
		t.Errorf("updating a belongs_to field = %v, want ErrInvalidFieldEdit", err)
	}
}

func TestFieldEditErrors(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	ctx := context.Background()

	if _, err := svc.AddField(ctx, projectID, resID, project.FieldInput{Label: "  ", Type: spec.TypeString}, 7); !errors.Is(err, project.ErrInvalidFieldEdit) {
		t.Errorf("empty label = %v, want ErrInvalidFieldEdit", err)
	}
	if _, err := svc.UpdateField(ctx, projectID, resID, spec.MustNewID(spec.KindField), project.FieldInput{Label: "X", Type: spec.TypeString}, 7); !errors.Is(err, project.ErrFieldNotFound) {
		t.Errorf("unknown field = %v, want ErrFieldNotFound", err)
	}
	if _, err := svc.AddField(ctx, projectID, spec.MustNewID(spec.KindResource), project.FieldInput{Label: "X", Type: spec.TypeString}, 7); !errors.Is(err, project.ErrResourceNotFound) {
		t.Errorf("unknown resource = %v, want ErrResourceNotFound", err)
	}
}
