package spec

import "testing"

// oneFieldSpec builds a spec with a single resource carrying one field, using
// the given stable IDs and storage names — enough to exercise identity lineage.
func oneFieldSpec(resID, fieldID ID, resStorage, fieldStorage string) *ProjectSpec {
	return &ProjectSpec{
		SpecVersion: SpecVersion,
		Project:     Project{ID: fixID(KindProject, "1"), Name: "Acme", Slug: "acme"},
		Database:    Database{Driver: DriverPostgres},
		Auth:        Auth{Mode: AuthCookie},
		Resources: []*Resource{
			{
				ID: resID, Label: "Customer", CodeName: "Customer", StorageName: resStorage,
				Fields: []*Field{
					{ID: fieldID, Label: "Email", Type: TypeString, CodeName: "Email", StorageName: fieldStorage},
				},
			},
		},
	}
}

func ids(refs []IdentityRef) map[ID]IdentityRef {
	m := make(map[ID]IdentityRef, len(refs))
	for _, r := range refs {
		m[r.ID] = r
	}
	return m
}

// TestLineageFieldIdRewriteIsDiscontinuous is the §94 mandatory proof: a trusted
// prior spec with fld_A→email and an externally modified current spec with
// fld_B→contact_email (same resource) must be flagged discontinuous so Forge
// refuses to infer a rename and fails closed.
func TestLineageFieldIdRewriteIsDiscontinuous(t *testing.T) {
	res := fixID(KindResource, "1")
	fldA := fixID(KindField, "11")
	fldB := fixID(KindField, "12")

	prior := oneFieldSpec(res, fldA, "customers", "email")
	current := oneFieldSpec(res, fldB, "customers", "contact_email")

	l := CheckLineage(prior, current)
	if !l.Discontinuous() {
		t.Fatalf("an ID rewrite must be discontinuous; removed=%v added=%v", l.Removed, l.Added)
	}
	// The delta is delete-plus-add, never a rename — the structural fail-closed
	// guarantee. fld_A is removed, fld_B is added, and the resource (unchanged ID)
	// is in neither list.
	rm := ids(l.Removed)
	if _, ok := rm[fldA]; !ok {
		t.Errorf("fld_A must be reported removed; got %v", l.Removed)
	}
	if rm[fldA].Storage != "email" {
		t.Errorf("removed ref must carry the prior storage name for the resolution prompt; got %q", rm[fldA].Storage)
	}
	add := ids(l.Added)
	if _, ok := add[fldB]; !ok {
		t.Errorf("fld_B must be reported added; got %v", l.Added)
	}
	if _, ok := rm[res]; ok {
		t.Errorf("the unchanged resource ID must not appear in the delta")
	}
}

// TestLineageRelabelPreservingIdsIsContinuous is §61's safe external edit: a
// label and storage change that keeps the stable IDs is not a discontinuity.
func TestLineageRelabelPreservingIdsIsContinuous(t *testing.T) {
	res := fixID(KindResource, "1")
	fld := fixID(KindField, "11")

	prior := oneFieldSpec(res, fld, "customers", "email")
	current := oneFieldSpec(res, fld, "clients", "contact_email") // same IDs, renamed storage

	l := CheckLineage(prior, current)
	if l.Discontinuous() {
		t.Errorf("a relabel/storage rename preserving IDs must not be discontinuous; %+v", l)
	}
	if len(l.Removed) != 0 || len(l.Added) != 0 {
		t.Errorf("preserving IDs must yield an empty delta; removed=%v added=%v", l.Removed, l.Added)
	}
}

// TestLineagePureAdditionIsContinuous: adding a field (new ID, nothing removed)
// is a safe evolution, not a discontinuity.
func TestLineagePureAdditionIsContinuous(t *testing.T) {
	res := fixID(KindResource, "1")
	fld := fixID(KindField, "11")
	prior := oneFieldSpec(res, fld, "customers", "email")

	current := oneFieldSpec(res, fld, "customers", "email")
	current.Resources[0].Fields = append(current.Resources[0].Fields, &Field{
		ID: fixID(KindField, "12"), Label: "Phone", Type: TypeString, CodeName: "Phone", StorageName: "phone",
	})

	l := CheckLineage(prior, current)
	if l.Discontinuous() {
		t.Errorf("a pure addition must not be discontinuous; %+v", l)
	}
	if len(l.Added) != 1 || len(l.Removed) != 0 {
		t.Errorf("want one addition, no removals; removed=%v added=%v", l.Removed, l.Added)
	}
}

// TestLineagePureDeletionIsNotDiscontinuous: removing a field with nothing added
// is an unambiguous deletion — there is no rename target to guess — so it is not
// a discontinuity even though an identity went away.
func TestLineagePureDeletionIsNotDiscontinuous(t *testing.T) {
	res := fixID(KindResource, "1")
	fldA := fixID(KindField, "11")
	fldB := fixID(KindField, "12")

	prior := oneFieldSpec(res, fldA, "customers", "email")
	prior.Resources[0].Fields = append(prior.Resources[0].Fields, &Field{
		ID: fldB, Label: "Phone", Type: TypeString, CodeName: "Phone", StorageName: "phone",
	})
	current := oneFieldSpec(res, fldA, "customers", "email") // dropped Phone (fld_B)

	l := CheckLineage(prior, current)
	if l.Discontinuous() {
		t.Errorf("a pure deletion must not be discontinuous; %+v", l)
	}
	if len(l.Removed) != 1 || l.Removed[0].ID != fldB {
		t.Errorf("want fld_B removed; got %v", l.Removed)
	}
}

// TestLineageResourceIdRewriteIsDiscontinuous: identity discontinuity applies at
// the resource level too — a rewritten resource ID takes its fields with it.
func TestLineageResourceIdRewriteIsDiscontinuous(t *testing.T) {
	prior := oneFieldSpec(fixID(KindResource, "1"), fixID(KindField, "11"), "customers", "email")
	current := oneFieldSpec(fixID(KindResource, "2"), fixID(KindField, "12"), "customers", "email")

	l := CheckLineage(prior, current)
	if !l.Discontinuous() {
		t.Fatalf("a resource ID rewrite must be discontinuous; %+v", l)
	}
	// Both the old resource and its field are removed; both new ones added.
	if len(l.Removed) != 2 || len(l.Added) != 2 {
		t.Errorf("want the resource and its field on each side; removed=%v added=%v", l.Removed, l.Added)
	}
}

// TestLineageNilSpecs: a nil prior is a first revision (all additions); a nil
// current is a full teardown (all removals). Neither is a discontinuity.
func TestLineageNilSpecs(t *testing.T) {
	real := oneFieldSpec(fixID(KindResource, "1"), fixID(KindField, "11"), "customers", "email")

	first := CheckLineage(nil, real)
	if first.Discontinuous() || len(first.Removed) != 0 || len(first.Added) != 2 {
		t.Errorf("nil prior must be all additions; %+v", first)
	}
	teardown := CheckLineage(real, nil)
	if teardown.Discontinuous() || len(teardown.Added) != 0 || len(teardown.Removed) != 2 {
		t.Errorf("nil current must be all removals; %+v", teardown)
	}
}

// TestLineageDeltaOrderIsDeterministic: removals follow prior authored order,
// additions follow current authored order, stable across runs.
func TestLineageDeltaOrderIsDeterministic(t *testing.T) {
	res := fixID(KindResource, "1")
	prior := oneFieldSpec(res, fixID(KindField, "11"), "customers", "email")
	prior.Resources[0].Fields = append(prior.Resources[0].Fields,
		&Field{ID: fixID(KindField, "12"), Label: "A", Type: TypeString, CodeName: "A", StorageName: "a"},
		&Field{ID: fixID(KindField, "13"), Label: "B", Type: TypeString, CodeName: "B", StorageName: "b"},
	)
	current := oneFieldSpec(res, fixID(KindField, "11"), "customers", "email") // dropped A and B

	first := CheckLineage(prior, current)
	if len(first.Removed) != 2 {
		t.Fatalf("expected 2 removals; got %v", first.Removed)
	}
	for i := 0; i < 20; i++ {
		next := CheckLineage(prior, current)
		for j := range first.Removed {
			if next.Removed[j].ID != first.Removed[j].ID {
				t.Fatalf("run %d: removal order changed at %d", i, j)
			}
		}
	}
}

// TestLineageDiscontinuityIsSpecGlobal pins the intended over-flagging: a
// removal in one resource and an unrelated addition in another — obviously not a
// rename of each other — still trips discontinuity. This is deliberate. D14 must
// fail closed, so the gate over-flags (asks) rather than risk missing a genuine
// rewrite; it never under-flags. Sharpening it to pair same-kind/same-parent
// changes would be an optimization, not a correctness fix.
func TestLineageDiscontinuityIsSpecGlobal(t *testing.T) {
	resA := fixID(KindResource, "1")
	resB := fixID(KindResource, "2")
	fldX := fixID(KindField, "11")
	fldY := fixID(KindField, "21")

	// twoResources builds A and B; A holds the given fields, B is constant.
	twoResources := func(aFields ...*Field) *ProjectSpec {
		return &ProjectSpec{
			SpecVersion: SpecVersion,
			Project:     Project{ID: fixID(KindProject, "1"), Name: "Acme", Slug: "acme"},
			Database:    Database{Driver: DriverPostgres},
			Auth:        Auth{Mode: AuthCookie},
			Resources: []*Resource{
				{ID: resA, Label: "A", CodeName: "A", StorageName: "a_s", Fields: aFields},
				{ID: resB, Label: "B", CodeName: "B", StorageName: "b_s", Fields: []*Field{
					{ID: fixID(KindField, "22"), Label: "Keep", Type: TypeString, CodeName: "Keep", StorageName: "keep"},
				}},
			},
		}
	}

	// Prior: A has X. Current: A dropped X (removal in A), B gained Y (addition in
	// B) — independent changes in different resources.
	prior := twoResources(&Field{ID: fldX, Label: "X", Type: TypeString, CodeName: "X", StorageName: "x"})
	current := twoResources() // A now has no fields
	current.Resources[1].Fields = append(current.Resources[1].Fields, &Field{
		ID: fldY, Label: "Y", Type: TypeString, CodeName: "Y", StorageName: "y",
	})

	l := CheckLineage(prior, current)
	if !l.Discontinuous() {
		t.Fatalf("a removal in one resource + an addition in another must over-flag as discontinuous; %+v", l)
	}
}

// TestDiscontinuityResolutions pins the §59 choices and their order.
func TestDiscontinuityResolutions(t *testing.T) {
	got := DiscontinuityResolutions()
	want := []LineageResolution{ResolveAsRename, ResolveAsDeleteAndAdd, ResolveRestoreIdentity, ResolveCancel}
	if len(got) != len(want) {
		t.Fatalf("got %d resolutions, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("resolution %d = %q, want %q", i, got[i], want[i])
		}
	}
}
