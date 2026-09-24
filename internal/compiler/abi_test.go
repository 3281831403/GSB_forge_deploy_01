package compiler

import (
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// cloneSpec deep-copies a spec through its canonical encoding, preserving every
// stable ID — so a candidate built from a clone shares identity with the
// current spec, which is what lets the classifier tell a rename from a
// delete-plus-add.
func cloneSpec(t *testing.T, s *spec.ProjectSpec) *spec.ProjectSpec {
	t.Helper()
	data, err := spec.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	clone, err := spec.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return clone
}

func classify(t *testing.T, current, candidate *spec.ProjectSpec) gen.Transition {
	t.Helper()
	tr, err := ClassifyEdit(current, candidate)
	if err != nil {
		t.Fatalf("ClassifyEdit: %v", err)
	}
	return tr
}

func TestFingerprintIsStableDigest(t *testing.T) {
	s := sampleSpec(t)
	sum, err := Fingerprint(s)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if len(sum) != 64 {
		t.Errorf("fingerprint must be a 64-hex-char sha256; got %d chars: %q", len(sum), sum)
	}
	again, _ := Fingerprint(cloneSpec(t, s))
	if again != sum {
		t.Errorf("fingerprint not deterministic across an identical clone: %q vs %q", sum, again)
	}
}

// TestNeutralAcrossRelabelAndStorageRename is scenario A/§87: a relabel plus a
// storage rename with frozen code symbols leaves the extension ABI unchanged.
func TestNeutralAcrossRelabelAndStorageRename(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	for _, r := range cand.Resources {
		r.Label = "Renamed " + r.Label
		r.StorageName += "_v2"
		for i, f := range r.Fields {
			f.Label = "Renamed field"
			if f.Type == spec.TypeBelongsTo {
				f.StorageName = "renamed_ref_id"
			} else {
				f.StorageName = "renamed_col_" + string(rune('a'+i))
			}
		}
	}
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("renamed candidate invalid:\n%s", d.Error())
	}
	if tr := classify(t, base, cand); tr.Class != gen.ClassNeutral {
		t.Errorf("relabel + storage rename must be neutral; got %s: %v", tr.Class, tr.Reasons)
	}
}

// TestNeutralAcrossResourceReorder confirms the ABI is order-independent (§38
// "page ordering" is neutral): the surface is a set of contracts.
func TestNeutralAcrossResourceReorder(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0], cand.Resources[1] = cand.Resources[1], cand.Resources[0]
	if tr := classify(t, base, cand); tr.Class != gen.ClassNeutral {
		t.Errorf("resource reorder must be neutral; got %s: %v", tr.Class, tr.Reasons)
	}
}

// TestBreakingOnResourceSourceRename is scenario G/§55: a source-symbol refactor
// changes the generated type names, so existing code fails to bind.
func TestBreakingOnResourceSourceRename(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].CodeName = "Client"
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("renamed candidate invalid:\n%s", d.Error())
	}
	if tr := classify(t, base, cand); tr.Class != gen.ClassBreaking {
		t.Errorf("resource source rename must be breaking; got %s: %v", tr.Class, tr.Reasons)
	}
}

// TestBreakingOnFieldSourceRename: renaming a field's code symbol removes its
// accessor and adds a new one — existing hooks calling the old accessor break.
func TestBreakingOnFieldSourceRename(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].Fields[0].CodeName = "ContactEmail" // was Email
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("candidate invalid:\n%s", d.Error())
	}
	if tr := classify(t, base, cand); tr.Class != gen.ClassBreaking {
		t.Errorf("field source rename must be breaking; got %s: %v", tr.Class, tr.Reasons)
	}
}

// TestBreakingOnFieldTypeChange is scenario H/§56: an extension-visible type
// change (here decimal -> string) cannot preserve source compatibility.
func TestBreakingOnFieldTypeChange(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	// Invoice.Total is a decimal; changing it to string changes Total() decimal
	// to Total() string on the view/draft surface.
	total := cand.Resources[1].Fields[1]
	if total.Type != spec.TypeDecimal {
		t.Fatalf("fixture drift: expected Total to be decimal, got %s", total.Type)
	}
	total.Type = spec.TypeString
	// The fixture declares Total aggregatable; a non-numeric field cannot be, so
	// the same edit drops it from the aggregate set to keep the candidate valid.
	cand.Resources[1].Behavior.AggregatableFields = nil
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("candidate invalid:\n%s", d.Error())
	}
	if tr := classify(t, base, cand); tr.Class != gen.ClassBreaking {
		t.Errorf("field type change must be breaking; got %s: %v", tr.Class, tr.Reasons)
	}
}

func TestBreakingOnFieldRemoval(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	// Drop customer.Active (not referenced by behavior lists in the fixture).
	cand.Resources[0].Fields = cand.Resources[0].Fields[:1]
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("candidate invalid:\n%s", d.Error())
	}
	if tr := classify(t, base, cand); tr.Class != gen.ClassBreaking {
		t.Errorf("field removal must be breaking; got %s: %v", tr.Class, tr.Reasons)
	}
}

func TestBreakingOnResourceDeletion(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources = cand.Resources[:1] // drop invoice
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("candidate invalid:\n%s", d.Error())
	}
	if tr := classify(t, base, cand); tr.Class != gen.ClassBreaking {
		t.Errorf("resource deletion must be breaking; got %s: %v", tr.Class, tr.Reasons)
	}
}

func TestAdditiveOnNewField(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].Fields = append(cand.Resources[0].Fields, &spec.Field{
		ID: spec.MustNewID(spec.KindField), Label: "Nickname", Type: spec.TypeString,
		CodeName: "Nickname", StorageName: "nickname",
	})
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("candidate invalid:\n%s", d.Error())
	}
	if tr := classify(t, base, cand); tr.Class != gen.ClassAdditive {
		t.Errorf("new field must be additive; got %s: %v", tr.Class, tr.Reasons)
	}
}

func TestAdditiveOnNewResource(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources = append(cand.Resources, &spec.Resource{
		ID: spec.MustNewID(spec.KindResource), Label: "Note", CodeName: "Note", StorageName: "notes",
		Fields: []*spec.Field{
			{ID: spec.MustNewID(spec.KindField), Label: "Body", Type: spec.TypeText, CodeName: "Body", StorageName: "body"},
		},
	})
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("candidate invalid:\n%s", d.Error())
	}
	if tr := classify(t, base, cand); tr.Class != gen.ClassAdditive {
		t.Errorf("new independent resource must be additive; got %s: %v", tr.Class, tr.Reasons)
	}
}

// TestAdditiveOnFirstHook is scenario C/§51: enabling the first lifecycle hook
// on a resource introduces a brand-new contract with no prior implementation.
func TestAdditiveOnFirstHook(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].Hooks = []*spec.Hook{
		{ID: spec.MustNewID(spec.KindHook), Event: spec.HookAfterCreate},
	}
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("candidate invalid:\n%s", d.Error())
	}
	if tr := classify(t, base, cand); tr.Class != gen.ClassAdditive {
		t.Errorf("first hook on a resource must be additive; got %s: %v", tr.Class, tr.Reasons)
	}
}

// TestBreakingOnHookAddedToAlreadyHooked: a second hook adds a required method
// to an interface the user's Hooks type already implements (§40 caveat).
func TestBreakingOnHookAddedToAlreadyHooked(t *testing.T) {
	base := sampleSpec(t)
	base.Resources[0].Hooks = []*spec.Hook{
		{ID: spec.MustNewID(spec.KindHook), Event: spec.HookAfterCreate},
	}
	if d := spec.Validate(base); d != nil {
		t.Fatalf("base invalid:\n%s", d.Error())
	}
	cand := cloneSpec(t, base)
	cand.Resources[0].Hooks = append(cand.Resources[0].Hooks, &spec.Hook{
		ID: spec.MustNewID(spec.KindHook), Event: spec.HookBeforeUpdate,
	})
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("candidate invalid:\n%s", d.Error())
	}
	if tr := classify(t, base, cand); tr.Class != gen.ClassBreaking {
		t.Errorf("adding a hook to an already-hooked resource must be breaking; got %s: %v", tr.Class, tr.Reasons)
	}
}

// TestBreakingOnHookRemoval: removing a hook contract is breaking (§41).
func TestBreakingOnHookRemoval(t *testing.T) {
	base := sampleSpec(t)
	base.Resources[0].Hooks = []*spec.Hook{
		{ID: spec.MustNewID(spec.KindHook), Event: spec.HookAfterCreate},
	}
	if d := spec.Validate(base); d != nil {
		t.Fatalf("base invalid:\n%s", d.Error())
	}
	cand := cloneSpec(t, base)
	cand.Resources[0].Hooks = nil
	if tr := classify(t, base, cand); tr.Class != gen.ClassBreaking {
		t.Errorf("hook removal must be breaking; got %s: %v", tr.Class, tr.Reasons)
	}
}

// TestNeutralEditDecidedWithoutUserCode is the §86 mandatory proof: a purely
// presentational change must classify as neutral so it can be accepted even
// while the user's extension code is broken. The guarantee is structural —
// ClassifyEdit takes only specs and never a project directory, so neutrality is
// decided from the ABI fingerprint alone and cannot depend on user code
// compiling. Here a project-level rename (not part of the extension ABI at all)
// is neutral.
func TestNeutralEditDecidedWithoutUserCode(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Project.Name = "Renamed Co"
	cand.Resources[0].Label = "Clients" // relabel, code symbol frozen
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("candidate invalid:\n%s", d.Error())
	}
	if tr := classify(t, base, cand); tr.Class != gen.ClassNeutral {
		t.Errorf("a presentation-only edit must be neutral regardless of user-code health; got %s: %v", tr.Class, tr.Reasons)
	}
}

// TestClassifyReportsReasons: a breaking verdict carries at least one concrete
// reason, so the editor can explain why a transition is gated.
func TestClassifyReportsReasons(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].CodeName = "Client"
	tr := classify(t, base, cand)
	if len(tr.Reasons) == 0 {
		t.Error("a breaking transition must report reasons")
	}
	if tr.Fingerprint == "" {
		t.Error("a transition must carry the candidate fingerprint")
	}
}
