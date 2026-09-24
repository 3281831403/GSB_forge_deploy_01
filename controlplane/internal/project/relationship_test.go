package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// twoResourceProject creates a project with Customer and Invoice resources and
// returns their stable IDs.
func twoResourceProject(t *testing.T) (*project.Service, uint, spec.ID, spec.ID) {
	t.Helper()
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()
	p, err := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := svc.AddResource(ctx, p.ID, "Customer", "Customers", 7); err != nil {
		t.Fatalf("add customer: %v", err)
	}
	if _, err := svc.AddResource(ctx, p.ID, "Invoice", "Invoices", 7); err != nil {
		t.Fatalf("add invoice: %v", err)
	}
	s := headSpec(t, svc, p.ID)
	return svc, p.ID, s.Resources[0].ID, s.Resources[1].ID
}

func TestAddRelationshipCommits(t *testing.T) {
	svc, projectID, customer, invoice := twoResourceProject(t)

	res, err := svc.AddRelationship(context.Background(), projectID, invoice, project.RelationshipInput{
		Label: "Customer", Target: customer, InverseLabel: "Invoices", Required: true,
	}, 7)
	if err != nil {
		t.Fatalf("add relationship: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted {
		t.Fatalf("add relationship outcome = %s, want committed", res.Outcome)
	}
	inv := findResource(t, headSpec(t, svc, projectID), invoice)
	f := inv.Fields[len(inv.Fields)-1]
	if f.Type != spec.TypeBelongsTo || f.Target != customer || f.InverseLabel != "Invoices" {
		t.Errorf("belongs_to field wrong: %+v", f)
	}
	if f.StorageName != "customer_id" || !f.Required {
		t.Errorf("fk column = %q required=%v, want customer_id/true", f.StorageName, f.Required)
	}
}

// TestAddRelationshipDerivesHasMany is acceptance criterion 2: the has_many side
// falls out of the compiler graph on the target resource.
func TestAddRelationshipDerivesHasMany(t *testing.T) {
	svc, projectID, customer, invoice := twoResourceProject(t)
	if _, err := svc.AddRelationship(context.Background(), projectID, invoice, project.RelationshipInput{
		Label: "Customer", Target: customer, InverseLabel: "Invoices",
	}, 7); err != nil {
		t.Fatalf("add relationship: %v", err)
	}

	g, err := graph.Build(headSpec(t, svc, projectID))
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	var target *graph.Resource
	for _, r := range g.Resources {
		if r.Spec.ID == customer {
			target = r
		}
	}
	if target == nil {
		t.Fatal("customer resource missing from graph")
	}
	if len(target.HasMany) != 1 {
		t.Fatalf("customer must have one derived has_many; got %d", len(target.HasMany))
	}
	if target.HasMany[0].From.Spec.ID != invoice || target.HasMany[0].To.Spec.ID != customer {
		t.Errorf("derived has_many points the wrong way: %+v", target.HasMany[0])
	}
}

func TestAddRelationshipTargetNotFound(t *testing.T) {
	svc, projectID, _, invoice := twoResourceProject(t)
	_, err := svc.AddRelationship(context.Background(), projectID, invoice, project.RelationshipInput{
		Label: "Ghost", Target: spec.MustNewID(spec.KindResource),
	}, 7)
	if !errors.Is(err, project.ErrRelationshipTarget) {
		t.Errorf("missing target = %v, want ErrRelationshipTarget", err)
	}
}

// TestDeleteRelationshipParticipatesInValidation is acceptance criterion 3:
// deleting a relationship goes through the candidate pipeline — it drops the
// accessor, so it is breaking and returned for validation, not silently committed.
func TestDeleteRelationshipParticipatesInValidation(t *testing.T) {
	svc, projectID, customer, invoice := twoResourceProject(t)
	ctx := context.Background()
	if _, err := svc.AddRelationship(ctx, projectID, invoice, project.RelationshipInput{
		Label: "Customer", Target: customer, InverseLabel: "Invoices",
	}, 7); err != nil {
		t.Fatalf("add relationship: %v", err)
	}
	inv := findResource(t, headSpec(t, svc, projectID), invoice)
	relID := inv.Fields[len(inv.Fields)-1].ID

	res, err := svc.DeleteField(ctx, projectID, invoice, relID, 7)
	if err != nil {
		t.Fatalf("delete relationship: %v", err)
	}
	if res.Outcome != project.OutcomeBreaking {
		t.Errorf("deleting a relationship must be breaking (validated); got %s", res.Outcome)
	}
}

// TestAddRelationshipRejectsGeneratedFieldCollision: a belongs_to "Customer"
// derives the key CustomerID, which collides with an existing scalar field whose
// code symbol is CustomerID. That collision is a spec-validity error now, so the
// relationship is rejected at edit time — not committed as a revision that would
// not build.
func TestAddRelationshipRejectsGeneratedFieldCollision(t *testing.T) {
	svc, projectID, customer, invoice := twoResourceProject(t)
	ctx := context.Background()

	// A scalar field on Invoice whose code symbol folds to CustomerID.
	if _, err := svc.AddField(ctx, projectID, invoice, project.FieldInput{Label: "Customer ID", Type: spec.TypeString}, 7); err != nil {
		t.Fatalf("add scalar: %v", err)
	}
	res, err := svc.AddRelationship(ctx, projectID, invoice, project.RelationshipInput{Label: "Customer", Target: customer}, 7)
	if err != nil {
		t.Fatalf("add relationship: %v", err)
	}
	if res.Outcome != project.OutcomeInvalidSpec {
		t.Fatalf("a relationship colliding with a scalar's generated field must be rejected; got %s", res.Outcome)
	}
	if len(res.Diagnostics) == 0 {
		t.Error("the rejection must carry diagnostics")
	}
	// Not committed: Invoice still has only the scalar.
	if len(findResource(t, headSpec(t, svc, projectID), invoice).Fields) != 1 {
		t.Error("the colliding relationship must not have committed")
	}
}

func findResource(t *testing.T, s *spec.ProjectSpec, id spec.ID) *spec.Resource {
	t.Helper()
	r := s.FindResource(id)
	if r == nil {
		t.Fatalf("resource %s not in spec", id)
	}
	return r
}
