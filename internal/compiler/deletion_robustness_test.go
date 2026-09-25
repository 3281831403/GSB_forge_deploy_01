package compiler

import (
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// TestAnalyzeDeletionsToleratesNilEntries: a spec decoded from hand-edited JSON
// can carry nil slice entries ("resources":[null], "pages":[null], a resource
// with "fields":[null]). The deletion gate runs before validation, so it must
// analyze such a spec — reporting the real deletions and blockers — rather than
// panic on a nil dereference.
func TestAnalyzeDeletionsToleratesNilEntries(t *testing.T) {
	base := sampleSpec(t)
	customerID := base.Resources[0].ID

	cand := cloneSpec(t, base)
	// Delete Customer, keep Invoice + its belongs_to — and lace every slice the
	// analysis walks with a nil entry.
	cand.Resources = []*spec.Resource{nil, cand.Resources[1]}
	cand.Resources[1].Fields = append([]*spec.Field{nil}, cand.Resources[1].Fields...)
	cand.Pages = []*spec.Page{nil}

	var deletions []DeletedResource
	// The nil entries must not panic — and the analysis must still see the real
	// entries around them.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("AnalyzeDeletions panicked on nil entries: %v", r)
			}
		}()
		deletions = AnalyzeDeletions(base, cand)
	}()

	del := findDeletion(t, deletions, customerID)
	if !del.Blocked() {
		t.Fatalf("the surviving belongs_to must still block the deletion; blockers=%v", del.Blockers)
	}
	if len(del.Blockers) != 1 || del.Blockers[0].Kind != "relationship" {
		t.Fatalf("want the one real relationship blocker past the nil entries, got %+v", del.Blockers)
	}
}

// TestAnalyzeDeletionsNilEntryInCurrentSpec: nil entries in the current spec
// are skipped too — they have no identity to delete — while the real resources
// are still analyzed.
func TestAnalyzeDeletionsNilEntryInCurrentSpec(t *testing.T) {
	base := sampleSpec(t)
	base.Resources = append([]*spec.Resource{nil}, base.Resources...)

	cand := cloneSpec(t, base)
	cand.Resources = nil // delete everything real

	var deletions []DeletedResource
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("AnalyzeDeletions panicked on a nil current entry: %v", r)
			}
		}()
		deletions = AnalyzeDeletions(base, cand)
	}()
	if len(deletions) != 2 {
		t.Fatalf("want the two real resources reported deleted, got %d", len(deletions))
	}
}

// TestDeletionBlockedByAggregateCard: a dashboard aggregate card (sum/avg/…
// over a resource's field) binds its resource exactly like a count card does,
// so deleting the resource while the card survives must report a dashboard_card
// blocker — not fall through to a bare dangling-reference diagnostic.
func TestDeletionBlockedByAggregateCard(t *testing.T) {
	orderID := spec.MustNewID(spec.KindResource)
	totalID := spec.MustNewID(spec.KindField)
	dashID := spec.MustNewID(spec.KindPage)

	current := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: spec.MustNewID(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{
				ID: orderID, Label: "Order", CodeName: "Order", StorageName: "orders",
				Behavior: spec.ResourceBehavior{AdminVisible: true, AggregatableFields: []spec.ID{totalID}},
				Fields: []*spec.Field{
					{ID: totalID, Label: "Total", Type: spec.TypeInteger, CodeName: "Total", StorageName: "total"},
				},
			},
		},
		Pages: []*spec.Page{
			{ID: dashID, Slug: "home", Label: "Home", Type: spec.PageDashboard, Dashboard: &spec.DashboardConfig{
				AggregateCards: []spec.AggregateCard{
					{Label: "Revenue", Resource: orderID, Field: totalID, Op: spec.AggregateSum},
				},
			}},
		},
	}

	// Delete Order but leave the dashboard — and its aggregate card — behind.
	cand := cloneSpec(t, current)
	cand.Resources = nil

	del := findDeletion(t, AnalyzeDeletions(current, cand), orderID)
	if !del.Blocked() {
		t.Fatalf("an aggregate card still referencing the resource must block its deletion; blockers=%v", del.Blockers)
	}
	if len(del.Blockers) != 1 || del.Blockers[0].Kind != "dashboard_card" {
		t.Fatalf("want one dashboard_card blocker for the aggregate card, got %+v", del.Blockers)
	}
	if del.Blockers[0].Entity != dashID {
		t.Errorf("blocker entity = %s, want the dashboard page %s", del.Blockers[0].Entity, dashID)
	}
	if len(BlockedDeletions(AnalyzeDeletions(current, cand))) != 1 {
		t.Error("BlockedDeletions must surface the aggregate-card-blocked deletion")
	}
}
