package compiler

// F0 mandatory-proof acceptance suite (ADR-001 §97).
//
// These are the named proofs that gate the F0 exit (§98). Each maps to one or
// more of the 17 exit-gate points, noted in its comment. The behaviors are
// exercised elsewhere too; this file is the single, discoverable place that
// demonstrates F0 is complete, using only exported APIs (internal/spec and the
// compiler), so the suite reads as one contract rather than scattered unit tests.
//
// Exit-gate coverage (§98):
//   1  stable IDs                       — the whole suite keys off stable IDs
//   2  deterministic minting            — TestSymbolMintIsUnique
//   3  collisions resolved before commit— TestSymbolMintIsUnique, TestFrozenSymbolsRemainReserved
//   4  deleted symbols tombstoned       — TestDeletedSymbolsRemainTombstoned
//   5  reserved symbols cannot collide  — TestReservedGeneratedNamesCannotCollide
//   6  renames preserve ABI             — TestRenamePreservesExtensionABI
//   7  neutral edits while code broken  — TestNeutralEditAllowedWhileUserCodeBroken
//   8  BeforeCreate mutate + reject      — TestBeforeCreateCanReadAndMutate, TestBeforeCreateCanRejectField
//   9  BeforeUpdate presence semantics   — TestBeforeUpdateDistinguishesAbsentFromZero, TestBeforeUpdateCanMutateChangedValue
//   10 regeneration never rewrites code — TestRegenerationNeverRewritesUserExtension
//   11 archival outside build graph     — TestDeletedResourceArchivesExtensionOutsideBuildPath
//   12 dependency-breaking delete fails — TestDeleteReferencedResourceRejected
//   13 explicit source rename breaks    — TestExplicitCodeRenameBreaksAndBlocks
//   14 extension-visible type change    — TestFieldTypeChangeBreaksAndBlocks
//   15 discontinuity disables inference — TestIdentityDiscontinuityBlocksRenameInference
//   16 app builds as ordinary Gombit    — proven by TestM0EndToEnd in this package
//   17 versioned Gombit boundary        — proven by internal/gombit (the CLI boundary tests)
//
// TestABIAdditiveCompatibilityClassification is a supporting §40 proof, not one
// of the 15 numbered non-integration gates — the additive class rounds out the
// neutral/additive/breaking classification the gate points above rely on.
//
// (16) and (17) are integration properties proven by TestM0EndToEnd and the
// gombit boundary package, referenced here rather than duplicated. Several of the
// generated-surface proofs (§98.8-9) assert that the generated code carries the
// right shape; that the shape behaves is proven end to end by TestM0EndToEnd,
// which compiles, migrates, boots and serves a real generated app.

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// fileWithSuffix returns the content of the first compiled file whose path ends
// with suffix, or fails.
func fileWithSuffix(t *testing.T, files []gen.File, suffix string) string {
	t.Helper()
	for _, f := range files {
		if strings.HasSuffix(f.Path, suffix) {
			return string(f.Content)
		}
	}
	t.Fatalf("no compiled file ending in %q", suffix)
	return ""
}

// --- Identity & symbol allocation (§98.2-5) ---------------------------------

// TestSymbolMintIsUnique (§98.2, §98.3): minting the same label for two entities
// in one namespace resolves the collision deterministically before either is
// frozen — Email, then Email2.
func TestSymbolMintIsUnique(t *testing.T) {
	l := spec.NewLedger()
	res := spec.MustNewID(spec.KindResource)
	ns := spec.FieldNamespace(res)
	a := spec.MustNewID(spec.KindField)
	b := spec.MustNewID(spec.KindField)

	first, err := spec.Mint(l, ns, "Email", a, spec.IsReservedCodeName)
	if err != nil {
		t.Fatalf("mint a: %v", err)
	}
	second, err := spec.Mint(l, ns, "Email", b, spec.IsReservedCodeName)
	if err != nil {
		t.Fatalf("mint b: %v", err)
	}
	if first != "Email" {
		t.Errorf("first mint = %q, want Email", first)
	}
	// Deterministic disambiguation (gate 2), not mere inequality: the second
	// entity gets Email2.
	if second != "Email2" {
		t.Fatalf("second mint = %q, want the deterministic Email2", second)
	}
}

// TestFrozenSymbolsRemainReserved (§98.3): once minted, a symbol is recorded and
// cannot be handed to a different entity — it is frozen for its owner's life.
func TestFrozenSymbolsRemainReserved(t *testing.T) {
	l := spec.NewLedger()
	res := spec.MustNewID(spec.KindResource)
	ns := spec.FieldNamespace(res)
	owner := spec.MustNewID(spec.KindField)

	sym, err := spec.Mint(l, ns, "Email", owner, spec.IsReservedCodeName)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if l.IsFree(ns, sym) {
		t.Errorf("a minted symbol must not read as free")
	}
	if !l.IsLive(ns, sym) {
		t.Errorf("a minted symbol must be live")
	}
	// Re-minting for the owner is idempotent (same symbol); a different entity is
	// pushed to a fresh one.
	if again, _ := spec.Mint(l, ns, "Email", owner, spec.IsReservedCodeName); again != sym {
		t.Errorf("re-mint for the owner must be idempotent; got %q want %q", again, sym)
	}
	other, _ := spec.Mint(l, ns, "Email", spec.MustNewID(spec.KindField), spec.IsReservedCodeName)
	if other == sym {
		t.Errorf("a frozen symbol must stay reserved against a new entity")
	}
}

// TestDeletedSymbolsRemainTombstoned (§98.4): deleting an entity tombstones its
// symbol, which is never reused — a later mint of the same label skips it.
func TestDeletedSymbolsRemainTombstoned(t *testing.T) {
	l := spec.NewLedger()
	res := spec.MustNewID(spec.KindResource)
	ns := spec.FieldNamespace(res)
	gone := spec.MustNewID(spec.KindField)

	sym, _ := spec.Mint(l, ns, "Email", gone, spec.IsReservedCodeName)
	l.TombstoneEntity(gone)
	if !l.IsTombstoned(ns, sym) {
		t.Fatalf("deleted entity's symbol %q must be tombstoned", sym)
	}
	reused, _ := spec.Mint(l, ns, "Email", spec.MustNewID(spec.KindField), spec.IsReservedCodeName)
	if reused == sym {
		t.Errorf("a tombstoned symbol must never be reused; got %q", reused)
	}
	if l.IsFree(ns, sym) {
		t.Errorf("a tombstoned symbol must never read as free again")
	}
}

// TestReservedGeneratedNamesCannotCollide (§98.5): a field cannot take a symbol
// reserved by the generated model (gorm.Model's ID/CreatedAt/…) — validation
// rejects it, and minting skips it.
func TestReservedGeneratedNamesCannotCollide(t *testing.T) {
	base := sampleSpec(t)
	bad := cloneSpec(t, base)
	bad.Resources[0].Fields[0].CodeName = "ID" // collides with gorm.Model.ID
	diags := spec.Validate(bad)
	if diags == nil || !diags.Has(spec.CodeReservedName) {
		t.Errorf("a field code_name colliding with a reserved model symbol must be rejected; got %v", diags)
	}

	// Minting also refuses to hand out the reserved symbol.
	l := spec.NewLedger()
	ns := spec.FieldNamespace(spec.MustNewID(spec.KindResource))
	sym, err := spec.Mint(l, ns, "ID", spec.MustNewID(spec.KindField), spec.IsReservedCodeName)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if sym == "ID" {
		t.Errorf("minting must not hand out a reserved symbol; got %q", sym)
	}
}

// --- ABI classification (§98.6-7, §98.13-14) --------------------------------

// TestNeutralEditAllowedWhileUserCodeBroken (§98.7, §86): a relabel classifies
// neutral. The reason it can commit even while user code is broken is structural
// — ClassifyEdit takes only specs, never a workspace or toolchain, so neutrality
// cannot depend on user code compiling. This asserts the neutral verdict; the
// "while user code is broken" half is guaranteed by that signature (and pinned
// directly in candidate_test's TestNeutralEditDecidedWithoutUserCode).
func TestNeutralEditAllowedWhileUserCodeBroken(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].Label = "Clients"
	if tr := classify(t, base, cand); tr.Class != gen.ClassNeutral {
		t.Errorf("a relabel must be neutral regardless of user-code health; got %s", tr.Class)
	}
}

// TestRenamePreservesExtensionABI (§98.6): a relabel leaves the extension ABI
// fingerprint unchanged, so CustomerView and every other frozen symbol survive.
func TestRenamePreservesExtensionABI(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].Label = "Clients"
	cand.Resources[0].StorageName = "clients"

	before, err := Fingerprint(base)
	if err != nil {
		t.Fatalf("fingerprint base: %v", err)
	}
	after, err := Fingerprint(cand)
	if err != nil {
		t.Fatalf("fingerprint cand: %v", err)
	}
	if before != after {
		t.Errorf("a relabel/storage rename must preserve the extension ABI; %s != %s", before, after)
	}
}

// TestABIAdditiveCompatibilityClassification (§98): a new field is a
// backward-compatible additive transition.
func TestABIAdditiveCompatibilityClassification(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].Fields = append(cand.Resources[0].Fields, &spec.Field{
		ID: spec.MustNewID(spec.KindField), Label: "Nickname", Type: spec.TypeString,
		CodeName: "Nickname", StorageName: "nickname",
	})
	if tr := classify(t, base, cand); tr.Class != gen.ClassAdditive {
		t.Errorf("a new field must be additive; got %s", tr.Class)
	}
}

// --- Generated extension surface (§98.8-9) ----------------------------------

// TestBeforeCreateCanReadAndMutate (§98.8): the generated before-create surface
// exposes a read accessor and a mutator per field.
func TestBeforeCreateCanReadAndMutate(t *testing.T) {
	files, err := Compile(sampleSpec(t), testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	mutation := fileWithSuffix(t, files, "forge_generated/customer/mutation.go")
	if !strings.Contains(mutation, "CustomerCreateDraft") {
		t.Fatal("missing CustomerCreateDraft")
	}
	if !strings.Contains(mutation, "Email() string") {
		t.Error("before-create surface must expose a read accessor Email() string")
	}
	if !strings.Contains(mutation, "SetEmail(v string)") {
		t.Error("before-create surface must expose a mutator SetEmail(v string)")
	}
}

// TestBeforeCreateCanRejectField (§98.8): the generated extension API exposes a
// structured field rejection a before-hook returns.
func TestBeforeCreateCanRejectField(t *testing.T) {
	files, err := Compile(sampleSpec(t), testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ext := fileWithSuffix(t, files, "forge_generated/extension/extension.go")
	if !strings.Contains(ext, "FieldError") {
		t.Error("rejection API must expose FieldError")
	}
	if !strings.Contains(ext, "func InvalidField(") {
		t.Error("rejection API must expose InvalidField to build a field rejection")
	}
}

// TestBeforeUpdateDistinguishesAbsentFromZero (§98.9): the before-update change
// set exposes a presence-carrying accessor — Email() returns (value, changed) —
// so an absent field stays distinct from one set to its zero value. This proves
// the generated surface carries presence; that the surface behaves is proven end
// to end by TestM0EndToEnd.
func TestBeforeUpdateDistinguishesAbsentFromZero(t *testing.T) {
	files, err := Compile(sampleSpec(t), testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	mutation := fileWithSuffix(t, files, "forge_generated/customer/mutation.go")
	if !strings.Contains(mutation, "CustomerUpdateChanges") {
		t.Fatal("missing CustomerUpdateChanges")
	}
	// The full presence signature, not just the "(string, bool)" fragment.
	if !strings.Contains(mutation, "func (c *CustomerUpdateChanges) Email() (string, bool)") {
		t.Error("before-update accessor must return (value, changed) to carry presence")
	}
}

// TestBeforeUpdateCanMutateChangedValue (§98.9): a before-update mutator sets the
// value and marks the field changed. Asserts the generated mutator shape; the
// runtime behavior rides on TestM0EndToEnd.
func TestBeforeUpdateCanMutateChangedValue(t *testing.T) {
	files, err := Compile(sampleSpec(t), testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	mutation := fileWithSuffix(t, files, "forge_generated/customer/mutation.go")
	// The mutator sets the value and flips the presence bit in one method.
	if !strings.Contains(mutation, "func (c *CustomerUpdateChanges) SetEmail(v string)") {
		t.Fatal("missing UpdateChanges mutator SetEmail")
	}
	if !strings.Contains(mutation, "Changed = true") {
		t.Error("a before-update mutator must mark the field changed")
	}
}

// --- Ownership & regeneration (§98.10-11) -----------------------------------

// TestRegenerationNeverRewritesUserExtension (§98.10, §95): repeated
// materialization of the generated tree never touches user extension code.
func TestRegenerationNeverRewritesUserExtension(t *testing.T) {
	dir := t.TempDir()
	base := sampleSpec(t)
	files, err := Compile(base, testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// A user-owned extension file with hand-written content.
	extRel := "internal/extensions/customer/hooks.go"
	const userCode = "package customer\n\n// hand-written by a developer; Forge must never touch this\nfunc Custom() {}\n"
	writeFile(t, dir, extRel, userCode)
	want := sha256.Sum256([]byte(userCode))

	// Multiple regenerations.
	for i := 0; i < 3; i++ {
		if err := Materialize(dir, files); err != nil {
			t.Fatalf("materialize %d: %v", i, err)
		}
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(extRel)))
		if err != nil {
			t.Fatalf("read extension after materialize %d: %v", i, err)
		}
		if sha256.Sum256(got) != want {
			t.Fatalf("materialize %d rewrote user extension code", i)
		}
	}
}

// TestDeletedResourceArchivesExtensionOutsideBuildPath (§98.11, §96): deleting a
// resource's extension archives it under the dot-prefixed archive root, out of
// the active build tree, and removes it from internal/extensions.
func TestDeletedResourceArchivesExtensionOutsideBuildPath(t *testing.T) {
	dir := t.TempDir()
	src := gen.ExtensionPackageDirForCodeName("Customer")
	writeFile(t, dir, src+"/hooks.go", "package customer\n")

	archived, err := ArchiveExtensions(dir, "rev-1", []DeletedResource{{ID: spec.MustNewID(spec.KindResource), CodeName: "Customer"}})
	if err != nil {
		t.Fatalf("ArchiveExtensions: %v", err)
	}
	if len(archived) != 1 {
		t.Fatalf("want one archived extension, got %d", len(archived))
	}
	if !strings.HasPrefix(archived[0].ArchivedPath, ".") {
		t.Errorf("archive must live under a dot-prefixed (build-excluded) root; got %q", archived[0].ArchivedPath)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(src))); !os.IsNotExist(err) {
		t.Errorf("extension must be gone from the build tree; stat err = %v", err)
	}
}

// --- Deletion, refactor, type change, discontinuity (§98.12-15) -------------

// TestDeleteReferencedResourceRejected (§98.12, §91): deleting a resource still
// referenced by a relationship is blocked before generation.
func TestDeleteReferencedResourceRejected(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources = cand.Resources[1:] // delete Customer, keep Invoice's belongs_to

	blocked := BlockedDeletions(AnalyzeDeletions(base, cand))
	if len(blocked) == 0 {
		t.Error("deleting a referenced resource must be blocked")
	}
}

// TestExplicitCodeRenameBreaksAndBlocks (§98.13, §92): an explicit code-symbol
// refactor is breaking and stays uncommitted until user code is proven
// compatible.
func TestExplicitCodeRenameBreaksAndBlocks(t *testing.T) {
	base := sampleSpec(t)
	result, err := spec.RefactorCodeName(base, ledgerFor(t, base), base.Resources[0].ID, "Client")
	if err != nil {
		t.Fatalf("RefactorCodeName: %v", err)
	}
	if tr := classify(t, base, result.Spec); tr.Class != gen.ClassBreaking {
		t.Fatalf("an explicit code rename must be breaking; got %s", tr.Class)
	}
	v, err := ValidateCandidate(context.Background(), CandidateRequest{
		Workspace: seedWorkspace(t, base), Module: testModule, Current: base, Candidate: result.Spec,
	}, &fakeToolchain{available: false, t: t})
	if err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	if v.Outcome == OutcomeAccepted {
		t.Error("a breaking refactor must not be accepted without a compatibility proof")
	}
}

// TestFieldTypeChangeBreaksAndBlocks (§98.14, §93): an extension-visible field
// type change is breaking even though stable identity is unchanged.
func TestFieldTypeChangeBreaksAndBlocks(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	total := cand.Resources[1].Fields[1] // Invoice.Total, a decimal
	if total.Type != spec.TypeDecimal {
		t.Fatalf("fixture drift: expected Total decimal, got %s", total.Type)
	}
	total.Type = spec.TypeString
	// Total is declared aggregatable in the fixture; a non-numeric field cannot
	// be, so the same edit drops it from the aggregate set to keep the candidate
	// valid (the classifier still sees the breaking decimal→string change).
	cand.Resources[1].Behavior.AggregatableFields = nil

	if tr := classify(t, base, cand); tr.Class != gen.ClassBreaking {
		t.Fatalf("an extension-visible type change must be breaking; got %s", tr.Class)
	}
	v, err := ValidateCandidate(context.Background(), CandidateRequest{
		Workspace: seedWorkspace(t, base), Module: testModule, Current: base, Candidate: cand,
	}, &fakeToolchain{available: true, typecheckErr: errStub("undefined method"), t: t})
	if err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	if v.Outcome != OutcomeRejected {
		t.Errorf("a breaking type change with incompatible code must be rejected; got %s", v.Outcome)
	}
}

// TestIdentityDiscontinuityBlocksRenameInference (§98.15, §94): an external ID
// rewrite (fld_A→fld_B) is flagged discontinuous, so Forge refuses to infer a
// rename and fails closed.
func TestIdentityDiscontinuityBlocksRenameInference(t *testing.T) {
	base := sampleSpec(t)
	prior := cloneSpec(t, base)
	current := cloneSpec(t, base)
	// Rewrite the identity of the first field (email) — same role, new stable ID.
	current.Resources[0].Fields[0].ID = spec.MustNewID(spec.KindField)

	l := spec.CheckLineage(prior, current)
	if !l.Discontinuous() {
		t.Fatalf("an external ID rewrite must be discontinuous; removed=%v added=%v", l.Removed, l.Added)
	}
}

// errStub is a tiny error value for a scripted toolchain failure.
type errStub string

func (e errStub) Error() string { return string(e) }
