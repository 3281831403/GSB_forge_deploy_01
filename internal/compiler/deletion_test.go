package compiler

import (
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// find returns the single deletion for id, or fails.
func findDeletion(t *testing.T, deletions []DeletedResource, id spec.ID) DeletedResource {
	t.Helper()
	for _, d := range deletions {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("no deletion reported for %s", id)
	return DeletedResource{}
}

// TestDeleteReferencedResourceIsBlocked is acceptance criterion 2 (§54, §91):
// deleting a resource while a relationship still binds it is a hard dependency
// violation, caught before generation. The same intermediate candidate is also
// spec-invalid, so both gates refuse it — it can never become a committed
// revision (criterion 3).
func TestDeleteReferencedResourceIsBlocked(t *testing.T) {
	base := sampleSpec(t)
	customerID := base.Resources[0].ID

	cand := cloneSpec(t, base)
	cand.Resources = cand.Resources[1:] // delete Customer, keep Invoice + its belongs_to

	deletions := AnalyzeDeletions(base, cand)
	del := findDeletion(t, deletions, customerID)
	if !del.Blocked() {
		t.Fatalf("deleting a referenced resource must be blocked; blockers=%v", del.Blockers)
	}
	if len(del.Blockers) != 1 || del.Blockers[0].Kind != "relationship" {
		t.Fatalf("want one relationship blocker, got %+v", del.Blockers)
	}
	if del.Blockers[0].Owner != base.Resources[1].ID {
		t.Errorf("blocker owner = %s, want the Invoice resource %s", del.Blockers[0].Owner, base.Resources[1].ID)
	}
	if len(BlockedDeletions(deletions)) != 1 {
		t.Errorf("BlockedDeletions must surface the blocked deletion")
	}
	// The intermediate state is independently caught by spec validity.
	if spec.Validate(cand) == nil {
		t.Error("a dangling-reference candidate must also fail spec validation")
	}
}

// TestAtomicDeleteRelationshipAndResource is acceptance criterion 1 (§44):
// batching the relationship deletion with the resource deletion into one
// candidate clears the dependency, so the deletion is unblocked and the whole
// candidate is valid.
func TestAtomicDeleteRelationshipAndResource(t *testing.T) {
	base := sampleSpec(t)
	customerID := base.Resources[0].ID

	cand := cloneSpec(t, base)
	// One atomic transaction: delete the Customer resource AND the relationship
	// that pointed at it (Invoice.Customer), keeping only Invoice.Total.
	cand.Resources = cand.Resources[1:]
	cand.Resources[0].Fields = cand.Resources[0].Fields[1:]

	deletions := AnalyzeDeletions(base, cand)
	del := findDeletion(t, deletions, customerID)
	if del.Blocked() {
		t.Fatalf("an atomic delete-relationship-and-resource must be unblocked; blockers=%v", del.Blockers)
	}
	if len(BlockedDeletions(deletions)) != 0 {
		t.Errorf("no deletion should be blocked in the atomic candidate")
	}
	// The whole candidate is valid, so it may be accepted as one revision.
	if d := spec.Validate(cand); d != nil {
		t.Errorf("the atomic candidate must be valid:\n%s", d.Error())
	}
}

// TestDeletionSurfacesActiveExtension is the §54 archival dimension: a deleted
// resource that carried lifecycle hooks is flagged (HadExtension) even when the
// deletion is otherwise unblocked, so the pipeline knows to archive its source
// (#30). This is not a spec-validity signal, so spec.Validate never reports it.
func TestDeletionSurfacesActiveExtension(t *testing.T) {
	base := sampleSpec(t)
	base.Resources[0].Hooks = []*spec.Hook{
		{ID: spec.MustNewID(spec.KindHook), Event: spec.HookAfterCreate},
	}
	if d := spec.Validate(base); d != nil {
		t.Fatalf("base invalid:\n%s", d.Error())
	}
	customerID := base.Resources[0].ID

	cand := cloneSpec(t, base)
	cand.Resources = cand.Resources[1:]
	cand.Resources[0].Fields = cand.Resources[0].Fields[1:] // atomic: also drop the relationship

	del := findDeletion(t, AnalyzeDeletions(base, cand), customerID)
	if del.Blocked() {
		t.Fatalf("deletion should be unblocked; blockers=%v", del.Blockers)
	}
	if !del.HadExtension {
		t.Error("a deleted resource with hooks must be flagged as having extension source to archive")
	}
}

// TestMultiDeleteClearsOwnDependencies: deleting both a resource and the
// resource that referenced it leaves no surviving blocker — the reference went
// with its owner.
func TestMultiDeleteClearsOwnDependencies(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources = nil // delete Customer AND Invoice together

	deletions := AnalyzeDeletions(base, cand)
	if len(deletions) != 2 {
		t.Fatalf("both resources should be reported deleted; got %d", len(deletions))
	}
	if len(BlockedDeletions(deletions)) != 0 {
		t.Errorf("deleting the referencing resource too must clear the blocker; got %v", BlockedDeletions(deletions))
	}
}

// TestDeletionBlockedByPageAndDashboard covers the page and dashboard-card
// reference kinds, and confirms the analysis runs on a candidate that does not
// have to be valid (the page reference is dangling) — the gate runs before
// generation, not after.
func TestDeletionBlockedByPageAndDashboard(t *testing.T) {
	widgetID := spec.MustNewID(spec.KindResource)
	pageID := spec.MustNewID(spec.KindPage)
	dashID := spec.MustNewID(spec.KindPage)

	current := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: spec.MustNewID(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{
				ID: widgetID, Label: "Widget", CodeName: "Widget", StorageName: "widgets",
				Behavior: spec.ResourceBehavior{AdminVisible: true},
				Fields: []*spec.Field{
					{ID: spec.MustNewID(spec.KindField), Label: "Name", Type: spec.TypeString, CodeName: "Name", StorageName: "name"},
				},
			},
		},
		Pages: []*spec.Page{
			{ID: pageID, Slug: "widgets", Label: "Widgets", Type: spec.PageResourceTable, Resource: widgetID},
			{ID: dashID, Slug: "home", Label: "Home", Type: spec.PageDashboard, Dashboard: &spec.DashboardConfig{
				CountCards: []spec.DashboardCard{{Label: "Widgets", Resource: widgetID}},
			}},
		},
	}

	// Delete Widget but leave both pages behind.
	cand := cloneSpec(t, current)
	cand.Resources = nil

	del := findDeletion(t, AnalyzeDeletions(current, cand), widgetID)
	kinds := map[string]int{}
	for _, b := range del.Blockers {
		kinds[b.Kind]++
	}
	if kinds["page"] != 1 || kinds["dashboard_card"] != 1 {
		t.Fatalf("want one page and one dashboard_card blocker; got %+v", del.Blockers)
	}
}

// TestAnalyzeDeletionsNilCandidateDeletesAll: a nil candidate deletes every
// resource, and with nothing surviving to reference them, none is blocked.
func TestAnalyzeDeletionsNilCandidateDeletesAll(t *testing.T) {
	base := sampleSpec(t)
	deletions := AnalyzeDeletions(base, nil)
	if len(deletions) != len(base.Resources) {
		t.Fatalf("nil candidate must delete all %d resources; got %d", len(base.Resources), len(deletions))
	}
	if len(BlockedDeletions(deletions)) != 0 {
		t.Errorf("nothing survives to block a deletion; got %v", BlockedDeletions(deletions))
	}
}

// TestAnalyzeDeletionsNoDeletions: an edit that deletes nothing reports nothing.
func TestAnalyzeDeletionsNoDeletions(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].Label = "Renamed" // a non-deletion edit
	if got := AnalyzeDeletions(base, cand); len(got) != 0 {
		t.Errorf("no resource deleted, want no deletions; got %v", got)
	}
}

// TestAnalyzeDeletionsIsDeterministic: repeated analysis yields identical order.
func TestAnalyzeDeletionsIsDeterministic(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources = nil

	first := AnalyzeDeletions(base, cand)
	for i := 0; i < 20; i++ {
		next := AnalyzeDeletions(base, cand)
		if len(next) != len(first) {
			t.Fatalf("run %d: deletion count changed", i)
		}
		for j := range first {
			if next[j].ID != first[j].ID {
				t.Fatalf("run %d: deletion order changed at %d", i, j)
			}
		}
	}
}

// TestDeletionBlockerCoverageAndOrder exercises the reference kinds and multi-
// blocker cases the earlier tests leave implicit: two relationships targeting
// the same resource, a RecentLists (not CountCards) dashboard card, and a page —
// all binding one deleted resource — and asserts the blocker list is complete
// and its order is stable across runs.
func TestDeletionBlockerCoverageAndOrder(t *testing.T) {
	targetID := spec.MustNewID(spec.KindResource)
	refID := spec.MustNewID(spec.KindResource)
	f1 := spec.MustNewID(spec.KindField)
	f2 := spec.MustNewID(spec.KindField)

	current := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: spec.MustNewID(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: targetID, Label: "Target", CodeName: "Target", StorageName: "targets"},
			{
				ID: refID, Label: "Ref", CodeName: "Ref", StorageName: "refs",
				// Two distinct belongs_to fields both pointing at Target.
				Fields: []*spec.Field{
					{ID: f1, Label: "Primary", Type: spec.TypeBelongsTo, CodeName: "Primary", StorageName: "primary_id", Target: targetID},
					{ID: f2, Label: "Secondary", Type: spec.TypeBelongsTo, CodeName: "Secondary", StorageName: "secondary_id", Target: targetID},
				},
			},
		},
		Pages: []*spec.Page{
			{ID: spec.MustNewID(spec.KindPage), Slug: "targets", Label: "Targets", Type: spec.PageResourceTable, Resource: targetID},
			{ID: spec.MustNewID(spec.KindPage), Slug: "home", Label: "Home", Type: spec.PageDashboard, Dashboard: &spec.DashboardConfig{
				RecentLists: []spec.DashboardCard{{Label: "Recent Targets", Resource: targetID}},
			}},
		},
	}

	cand := cloneSpec(t, current)
	cand.Resources = cand.Resources[1:] // delete Target, keep Ref + both pages

	del := findDeletion(t, AnalyzeDeletions(current, cand), targetID)
	kinds := map[string]int{}
	for _, b := range del.Blockers {
		kinds[b.Kind]++
	}
	if kinds["relationship"] != 2 {
		t.Errorf("want 2 relationship blockers, got %d (%+v)", kinds["relationship"], del.Blockers)
	}
	if kinds["page"] != 1 {
		t.Errorf("want 1 page blocker, got %d", kinds["page"])
	}
	if kinds["dashboard_card"] != 1 {
		t.Errorf("want 1 dashboard_card blocker (from RecentLists), got %d", kinds["dashboard_card"])
	}

	// Blocker order is stable across runs (relationships in candidate field
	// order, then pages in authored order).
	first := del.Blockers
	for i := 0; i < 20; i++ {
		again := findDeletion(t, AnalyzeDeletions(current, cand), targetID).Blockers
		if len(again) != len(first) {
			t.Fatalf("run %d: blocker count changed %d -> %d", i, len(first), len(again))
		}
		for j := range first {
			if again[j].Kind != first[j].Kind || again[j].Entity != first[j].Entity {
				t.Fatalf("run %d: blocker order changed at %d", i, j)
			}
		}
	}
	// The two relationship blockers come first and in field order.
	if first[0].Entity != f1 || first[1].Entity != f2 {
		t.Errorf("relationship blockers must preserve candidate field order; got %s, %s", first[0].Entity, first[1].Entity)
	}
}
