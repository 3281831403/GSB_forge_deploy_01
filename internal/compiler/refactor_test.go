package compiler

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// ledgerFor records every resource and field code symbol of a spec as live — the
// accepted symbol ledger a refactor starts from.
func ledgerFor(t *testing.T, s *spec.ProjectSpec) *spec.Ledger {
	t.Helper()
	l := spec.NewLedger()
	for _, r := range s.Resources {
		if err := l.Record(spec.NamespaceResource, r.CodeName, r.ID); err != nil {
			t.Fatalf("record %s: %v", r.CodeName, err)
		}
		for _, f := range r.Fields {
			if err := l.Record(spec.FieldNamespace(r.ID), f.CodeName, f.ID); err != nil {
				t.Fatalf("record %s.%s: %v", r.CodeName, f.CodeName, err)
			}
		}
	}
	return l
}

// TestRelabelIsNeutralRefactorIsBreaking is the §55/§92 pair, end to end: a
// relabel (label only) is ABI-neutral so CustomerView is preserved, while an
// explicit code-symbol refactor of the same resource is classified breaking.
func TestRelabelIsNeutralRefactorIsBreaking(t *testing.T) {
	base := sampleSpec(t)
	customerID := base.Resources[0].ID

	// Relabel: change only the label. ABI-neutral.
	relabel := cloneSpec(t, base)
	relabel.Resources[0].Label = "Client"
	if tr := classify(t, base, relabel); tr.Class != gen.ClassNeutral {
		t.Errorf("relabel must be ABI-neutral (CustomerView preserved); got %s: %v", tr.Class, tr.Reasons)
	}

	// Explicit source refactor Customer -> Client via the F0 #32 operation.
	result, err := spec.RefactorCodeName(base, ledgerFor(t, base), customerID, "Client")
	if err != nil {
		t.Fatalf("RefactorCodeName: %v", err)
	}
	if tr := classify(t, base, result.Spec); tr.Class != gen.ClassBreaking {
		t.Errorf("an explicit code refactor must be breaking; got %s: %v", tr.Class, tr.Reasons)
	}
}

// TestRefactorBlockedUntilCompatible: a breaking refactor requires candidate
// validation (§13 step 5-6, §92). With incompatible user code it is rejected;
// with no toolchain it surfaces the toolchain-unavailable outcome — never a
// silent accept.
func TestRefactorBlockedUntilCompatible(t *testing.T) {
	base := sampleSpec(t)
	result, err := spec.RefactorCodeName(base, ledgerFor(t, base), base.Resources[0].ID, "Client")
	if err != nil {
		t.Fatalf("RefactorCodeName: %v", err)
	}
	ws := seedWorkspace(t, base)

	rejected, err := ValidateCandidate(context.Background(), CandidateRequest{
		Workspace: ws, Module: testModule, Current: base, Candidate: result.Spec,
	}, &fakeToolchain{available: true, typecheckErr: fmt.Errorf("undefined: CustomerView"), t: t})
	if err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	if rejected.Outcome != OutcomeRejected {
		t.Errorf("a refactor with incompatible user code must be rejected; got %s", rejected.Outcome)
	}

	unavailable, err := ValidateCandidate(context.Background(), CandidateRequest{
		Workspace: ws, Module: testModule, Current: base, Candidate: result.Spec,
	}, &fakeToolchain{available: false, t: t})
	if err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	if unavailable.Outcome != OutcomeToolchainUnavailable {
		t.Errorf("a breaking refactor without a toolchain must not silently commit; got %s", unavailable.Outcome)
	}
}

// TestGenerationNeverNormalizes is the §14 guarantee from the generation side:
// a resource whose label has drifted from its frozen symbol still generates
// against the symbol, never the label. Compilation must never quietly normalize
// identifiers — normalization is a manual, explicit operation only.
func TestGenerationNeverNormalizes(t *testing.T) {
	s := sampleSpec(t)
	// Label drifts to "Client"; the frozen code symbol stays "Customer".
	s.Resources[0].Label = "Client"
	if d := spec.Validate(s); d != nil {
		t.Fatalf("spec invalid:\n%s", d.Error())
	}

	files, err := Compile(s, testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	var sawCustomerPkg, sawClientPkg bool
	for _, f := range files {
		if strings.Contains(f.Path, "forge_generated/customer/") {
			sawCustomerPkg = true
		}
		if strings.Contains(f.Path, "forge_generated/client/") {
			sawClientPkg = true
		}
	}
	if !sawCustomerPkg {
		t.Error("generation must use the frozen symbol (customer package), regardless of the label")
	}
	if sawClientPkg {
		t.Error("generation must never normalize the label into the symbol (no client package)")
	}
}
