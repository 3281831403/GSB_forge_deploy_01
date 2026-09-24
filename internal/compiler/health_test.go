package compiler

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// TestHealthReportsThreeFacetsSeparately is acceptance criterion 1: the model
// exposes exactly the three §71 states, each named and independently statused.
func TestHealthReportsThreeFacetsSeparately(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].Label = "Clients" // neutral

	h, err := Evaluate(context.Background(), HealthRequest{
		Current: base, Candidate: cand, Workspace: seedWorkspace(t, base),
	}, &fakeToolchain{available: true, t: t})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	facets := h.Facets()
	wantNames := []string{"Spec", "Extension ABI", "Runtime Build"}
	if len(facets) != len(wantNames) {
		t.Fatalf("Facets() = %d entries, want %d", len(facets), len(wantNames))
	}
	for i, name := range wantNames {
		if facets[i].Name != name {
			t.Errorf("facet %d name = %q, want %q", i, facets[i].Name, name)
		}
	}
}

// TestHealthDistinguishesBrokenBuildFromInvalidSpec is acceptance criterion 2
// and the §43 guarantee: a broken current build must not present as an invalid
// candidate spec, and vice versa.
func TestHealthDistinguishesBrokenBuildFromInvalidSpec(t *testing.T) {
	base := sampleSpec(t)

	t.Run("broken build, valid candidate", func(t *testing.T) {
		cand := cloneSpec(t, base)
		cand.Resources[0].Label = "Clients" // neutral, valid
		tc := &fakeToolchain{available: true, typecheckErr: fmt.Errorf("internal/extensions/customer/hooks.go:42: syntax error"), t: t}

		h, err := Evaluate(context.Background(), HealthRequest{
			Current: base, Candidate: cand, Workspace: seedWorkspace(t, base),
		}, tc)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		// The edit itself is fine...
		if h.Spec.Status != StatusOK {
			t.Errorf("spec must stay OK when only the build is broken; got %s", h.Spec.Status)
		}
		if h.ABI.Status != StatusOK {
			t.Errorf("ABI must stay OK when only the build is broken; got %s", h.ABI.Status)
		}
		// ...only the runtime build is broken, and it carries the location.
		if h.Build.Status != StatusFailed {
			t.Fatalf("build must be failed; got %s", h.Build.Status)
		}
		if !strings.Contains(h.Build.Summary(), "hooks.go:42") {
			t.Errorf("build summary must carry the compiler detail; got %q", h.Build.Summary())
		}
	})

	t.Run("invalid candidate, healthy build", func(t *testing.T) {
		cand := cloneSpec(t, base)
		cand.Resources[0].StorageName = "1_not_an_identifier" // invalid storage name
		if spec.Validate(cand) == nil {
			t.Fatal("fixture drift: candidate expected to be invalid")
		}
		tc := &fakeToolchain{available: true, t: t} // build is fine

		h, err := Evaluate(context.Background(), HealthRequest{
			Current: base, Candidate: cand, Workspace: seedWorkspace(t, base),
		}, tc)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if h.Spec.Status != StatusFailed {
			t.Errorf("invalid candidate spec must fail the spec facet; got %s", h.Spec.Status)
		}
		if len(h.Spec.Diagnostics) == 0 {
			t.Error("a failed spec facet must carry diagnostics")
		}
		// An invalid spec has no defined ABI to classify.
		if h.ABI.Status != StatusUnknown {
			t.Errorf("ABI over an invalid spec must be Unknown, not Failed; got %s", h.ABI.Status)
		}
		// The build is wholly independent and still healthy.
		if h.Build.Status != StatusOK {
			t.Errorf("a spec error must not break the build facet; got %s (%s)", h.Build.Status, h.Build.Detail)
		}
	})
}

// TestHealthAllGreen: valid candidate, neutral transition, building workspace.
func TestHealthAllGreen(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].Label = "Clients"

	h, err := Evaluate(context.Background(), HealthRequest{
		Current: base, Candidate: cand, Workspace: seedWorkspace(t, base),
	}, &fakeToolchain{available: true, t: t})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	for _, f := range h.Facets() {
		if f.Status != StatusOK {
			t.Errorf("facet %s = %s (%s), want ok", f.Name, f.Status, f.Summary)
		}
	}
}

// TestHealthBreakingEditFailsOnlyABI: a source rename is breaking; the spec is
// still valid and the build unaffected.
func TestHealthBreakingEditFailsOnlyABI(t *testing.T) {
	base, cand := breakingCandidate(t)

	h, err := Evaluate(context.Background(), HealthRequest{
		Current: base, Candidate: cand, Workspace: seedWorkspace(t, base),
	}, &fakeToolchain{available: true, t: t})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if h.Spec.Status != StatusOK {
		t.Errorf("a breaking-but-valid edit keeps the spec OK; got %s", h.Spec.Status)
	}
	if h.ABI.Status != StatusFailed {
		t.Fatalf("a breaking edit must fail the ABI facet; got %s", h.ABI.Status)
	}
	if h.ABI.Class != gen.ClassBreaking {
		t.Errorf("ABI class = %s, want breaking", h.ABI.Class)
	}
	if len(h.ABI.Reasons) == 0 {
		t.Error("a failed ABI facet must carry reasons")
	}
	if h.Build.Status != StatusOK {
		t.Errorf("build unaffected by an ABI break; got %s", h.Build.Status)
	}
}

// TestHealthBuildUnknownWithoutToolchain: no toolchain (or no workspace) leaves
// the build facet Unknown — explicitly not Failed.
func TestHealthBuildUnknownWithoutToolchain(t *testing.T) {
	base := sampleSpec(t)

	t.Run("no toolchain", func(t *testing.T) {
		h, err := Evaluate(context.Background(), HealthRequest{
			Current: base, Workspace: seedWorkspace(t, base),
		}, nil)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if h.Build.Status != StatusUnknown {
			t.Errorf("no toolchain must leave build Unknown, not %s", h.Build.Status)
		}
	})

	t.Run("unavailable toolchain", func(t *testing.T) {
		h, err := Evaluate(context.Background(), HealthRequest{
			Current: base, Workspace: seedWorkspace(t, base),
		}, &fakeToolchain{available: false, typecheckErr: fmt.Errorf("must not run"), t: t})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if h.Build.Status != StatusUnknown {
			t.Errorf("unavailable toolchain must leave build Unknown; got %s", h.Build.Status)
		}
	})

	t.Run("no workspace", func(t *testing.T) {
		h, err := Evaluate(context.Background(), HealthRequest{
			Current: base,
		}, &fakeToolchain{available: true, t: t})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if h.Build.Status != StatusUnknown {
			t.Errorf("no workspace must leave build Unknown; got %s", h.Build.Status)
		}
	})
}

// TestHealthNoCandidateAssessesCurrent: with no candidate, the spec facet
// reflects Current and the ABI is unchanged (there is no edit to break it).
func TestHealthNoCandidateAssessesCurrent(t *testing.T) {
	base := sampleSpec(t)
	h, err := Evaluate(context.Background(), HealthRequest{
		Current: base, Workspace: seedWorkspace(t, base),
	}, &fakeToolchain{available: true, t: t})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if h.Spec.Status != StatusOK {
		t.Errorf("current spec is valid; got %s", h.Spec.Status)
	}
	if h.ABI.Status != StatusOK || h.ABI.Class != gen.ClassNeutral {
		t.Errorf("no edit means an unchanged ABI; got %s/%s", h.ABI.Status, h.ABI.Class)
	}
}

// TestHealthABIUnknownWhenCurrentInvalid: a valid candidate against an invalid
// current spec cannot be classified, so ABI is Unknown, not Failed.
func TestHealthABIUnknownWhenCurrentInvalid(t *testing.T) {
	current := sampleSpec(t)
	current.Resources[0].StorageName = "1_bad" // make current invalid
	if spec.Validate(current) == nil {
		t.Fatal("fixture drift: current expected invalid")
	}
	cand := sampleSpec(t) // valid, but shares no identity with current

	h, err := Evaluate(context.Background(), HealthRequest{
		Current: current, Candidate: cand,
	}, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if h.Spec.Status != StatusOK {
		t.Errorf("the candidate is valid, so the spec facet is OK; got %s", h.Spec.Status)
	}
	if h.ABI.Status != StatusUnknown {
		t.Errorf("ABI over an unclassifiable pair must be Unknown; got %s", h.ABI.Status)
	}
}

func TestHealthNeedsASpec(t *testing.T) {
	if _, err := Evaluate(context.Background(), HealthRequest{}, nil); err == nil {
		t.Error("Evaluate with no spec must error")
	}
}
