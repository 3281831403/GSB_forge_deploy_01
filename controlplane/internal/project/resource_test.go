package project_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// headSpec loads a project's current spec for assertions.
func headSpec(t *testing.T, svc *project.Service, projectID uint) *spec.ProjectSpec {
	t.Helper()
	rev, ok, err := svc.Head(context.Background(), projectID)
	if err != nil || !ok {
		t.Fatalf("head: ok=%v err=%v", ok, err)
	}
	s, err := spec.Unmarshal([]byte(rev.SpecJSON))
	if err != nil {
		t.Fatalf("decode head spec: %v", err)
	}
	return s
}

// TestAddResourceBootstrapsEmptyProject: adding the first resource to a project
// with no revisions bootstraps an initial spec, mints the code symbol and
// derives the storage name from the label.
func TestAddResourceBootstrapsEmptyProject(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	p, err := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	res, err := svc.AddResource(ctx, p.ID, "Order", "Orders", 7)
	if err != nil {
		t.Fatalf("add resource: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted {
		t.Fatalf("add resource outcome = %s, want committed", res.Outcome)
	}

	s := headSpec(t, svc, p.ID)
	if len(s.Resources) != 1 {
		t.Fatalf("want 1 resource, got %d", len(s.Resources))
	}
	r := s.Resources[0]
	if r.Label != "Order" || r.LabelPlural != "Orders" {
		t.Errorf("labels = %q/%q, want Order/Orders", r.Label, r.LabelPlural)
	}
	if r.CodeName != "Order" {
		t.Errorf("code_name = %q, want the minted Order", r.CodeName)
	}
	if r.StorageName != "orders" {
		t.Errorf("storage_name = %q, want orders", r.StorageName)
	}
}

// TestAddResourceMintsUniqueSymbols: two resources with the same label get
// distinct, collision-free code symbols and storage names — the backend mints,
// the editor does not.
func TestAddResourceMintsUniqueSymbols(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if _, err := svc.AddResource(ctx, p.ID, "Order", "Orders", 7); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := svc.AddResource(ctx, p.ID, "Order", "Orders", 7); err != nil {
		t.Fatalf("second add: %v", err)
	}

	s := headSpec(t, svc, p.ID)
	if len(s.Resources) != 2 {
		t.Fatalf("want 2 resources, got %d", len(s.Resources))
	}
	if s.Resources[0].CodeName == s.Resources[1].CodeName {
		t.Errorf("code symbols must be unique; both are %q", s.Resources[0].CodeName)
	}
	if s.Resources[0].StorageName == s.Resources[1].StorageName {
		t.Errorf("storage names must be unique; both are %q", s.Resources[0].StorageName)
	}
	// The candidate remains valid (unique symbols is exactly what validation checks).
	if d := spec.Validate(s); d != nil {
		t.Errorf("spec after two adds invalid:\n%s", d.Error())
	}
}

// TestRenameResourceIsNeutral: renaming edits labels only, so the transition is
// ABI-neutral and the frozen code symbol is unchanged.
func TestRenameResourceIsNeutral(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if _, err := svc.AddResource(ctx, p.ID, "Order", "Orders", 7); err != nil {
		t.Fatalf("add: %v", err)
	}
	id := headSpec(t, svc, p.ID).Resources[0].ID

	res, err := svc.RenameResource(ctx, p.ID, id, "Purchase", "Purchases", 7)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted || res.Class != "neutral" {
		t.Fatalf("rename outcome=%s class=%s, want committed/neutral", res.Outcome, res.Class)
	}
	r := headSpec(t, svc, p.ID).Resources[0]
	if r.Label != "Purchase" {
		t.Errorf("label = %q, want Purchase", r.Label)
	}
	if r.CodeName != "Order" {
		t.Errorf("a rename must not move the code symbol; got %q", r.CodeName)
	}
}

// TestDeleteResourceCommits: deleting an unreferenced resource commits.
func TestDeleteResourceCommits(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if _, err := svc.AddResource(ctx, p.ID, "Order", "Orders", 7); err != nil {
		t.Fatalf("add order: %v", err)
	}
	if _, err := svc.AddResource(ctx, p.ID, "Customer", "Customers", 7); err != nil {
		t.Fatalf("add customer: %v", err)
	}
	orderID := headSpec(t, svc, p.ID).Resources[0].ID

	del, err := svc.DeleteResource(ctx, p.ID, orderID, 7)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !del.Committed {
		t.Fatalf("unreferenced delete must commit; %+v", del)
	}
	s := headSpec(t, svc, p.ID)
	if len(s.Resources) != 1 || s.Resources[0].Label != "Customer" {
		t.Errorf("after delete want only Customer; got %d resources", len(s.Resources))
	}
}

// TestDeleteResourceBlockedByRelationship: deleting a resource a relationship
// still references is blocked before generation, with the concrete blocker.
func TestDeleteResourceBlockedByRelationship(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	// Two resources where Invoice.Customer belongs_to Customer, committed via the
	// raw candidate path (the resource ops don't add relationships yet).
	customer := spec.MustNewID(spec.KindResource)
	invoice := spec.MustNewID(spec.KindResource)
	base := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: spec.MustNewID(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: customer, Label: "Customer", CodeName: "Customer", StorageName: "customers",
				Fields: []*spec.Field{{ID: spec.MustNewID(spec.KindField), Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"}}},
			{ID: invoice, Label: "Invoice", CodeName: "Invoice", StorageName: "invoices",
				Fields: []*spec.Field{{ID: spec.MustNewID(spec.KindField), Label: "Customer", Type: spec.TypeBelongsTo, CodeName: "Customer", StorageName: "customer_id", Required: true, Target: customer}}},
		},
	}
	if d := spec.Validate(base); d != nil {
		t.Fatalf("fixture invalid:\n%s", d.Error())
	}
	p, _ := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if r, err := svc.SubmitCandidate(ctx, p.ID, base, 7); err != nil || r.Outcome != project.OutcomeCommitted {
		t.Fatalf("seed candidate: outcome=%v err=%v", r.Outcome, err)
	}

	del, err := svc.DeleteResource(ctx, p.ID, customer, 7)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if del.Committed {
		t.Fatal("deleting a referenced resource must be blocked, not committed")
	}
	if !del.Blocked || len(del.Blockers) == 0 {
		t.Fatalf("want a blocked delete with blockers; got %+v", del)
	}
	if del.Blockers[0].Kind != "relationship" {
		t.Errorf("blocker kind = %q, want relationship", del.Blockers[0].Kind)
	}
	// Nothing committed: head is still the seed revision (both resources present).
	if len(headSpec(t, svc, p.ID).Resources) != 2 {
		t.Error("a blocked delete must not change the spec")
	}
}

// TestConcurrentAddsNeverLose is the concurrency guard the review asked for: N
// resources added at once must all survive. Each op builds its candidate from
// the head read inside the project lock, so concurrent adds serialize and each
// sees the others' commits — none is lost, and no revision forges a parent it
// did not descend from. Against the old read-modify-write (candidate built from a
// head read outside the lock) the stale candidates read as removing each other's
// resources and most would be rejected, leaving fewer than N.
func TestConcurrentAddsNeverLose(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	// Seed a base resource so the concurrent adds edit a non-empty spec.
	if _, err := svc.AddResource(ctx, p.ID, "Base", "Bases", 7); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const n = 5
	var wg sync.WaitGroup
	var mu sync.Mutex
	committed := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.AddResource(ctx, p.ID, "Item", "Items", 7)
			if err == nil && res.Outcome == project.OutcomeCommitted {
				mu.Lock()
				committed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if committed != n {
		t.Errorf("committed %d of %d concurrent adds; some were lost or rejected", committed, n)
	}
	// The final spec holds base + all n adds, and remains valid (unique symbols).
	s := headSpec(t, svc, p.ID)
	if len(s.Resources) != n+1 {
		t.Errorf("final spec has %d resources, want %d (base + %d)", len(s.Resources), n+1, n)
	}
	if d := spec.Validate(s); d != nil {
		t.Errorf("final spec invalid:\n%s", d.Error())
	}
}

func TestResourceEditErrors(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	p, _ := svc.CreateProject(ctx, 1, "Acme", "acme", 7)

	if _, err := svc.AddResource(ctx, p.ID, "  ", "", 7); !errors.Is(err, project.ErrInvalidResourceEdit) {
		t.Errorf("empty label add = %v, want ErrInvalidResourceEdit", err)
	}
	// Rename/delete need an existing spec.
	if _, err := svc.RenameResource(ctx, p.ID, spec.MustNewID(spec.KindResource), "X", "", 7); !errors.Is(err, project.ErrNoSpec) {
		t.Errorf("rename with no spec = %v, want ErrNoSpec", err)
	}
	// After a spec exists, an unknown resource id is not found.
	if _, err := svc.AddResource(ctx, p.ID, "Order", "Orders", 7); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.RenameResource(ctx, p.ID, spec.MustNewID(spec.KindResource), "X", "", 7); !errors.Is(err, project.ErrResourceNotFound) {
		t.Errorf("rename unknown = %v, want ErrResourceNotFound", err)
	}
}
