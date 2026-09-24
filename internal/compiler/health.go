package compiler

import (
	"context"
	"fmt"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// FacetStatus is the health of one of the three independent facets (ADR-001
// §36). The three facets are never collapsed into a single pass/fail — a project
// can hold a valid spec and a compatible ABI while its runtime build is broken,
// and stay fully editable (§43).
type FacetStatus int

const (
	// StatusOK means the facet is healthy: the spec is valid, the ABI is
	// compatible, or the project builds.
	StatusOK FacetStatus = iota
	// StatusFailed means the facet is unhealthy: the spec is invalid, the ABI is
	// breaking, or the project does not build.
	StatusFailed
	// StatusUnknown means the facet was not evaluated — the ABI cannot be
	// classified over an invalid spec, and build health needs a workspace and a
	// toolchain. Unknown is deliberately distinct from Failed: "we did not check"
	// is not "it is broken".
	StatusUnknown
)

func (s FacetStatus) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusFailed:
		return "failed"
	case StatusUnknown:
		return "unknown"
	default:
		return fmt.Sprintf("FacetStatus(%d)", int(s))
	}
}

// SpecHealth is the spec-validity facet: does the semantic application model make
// sense (ADR-001 §36)? It describes the candidate edit being assessed, so a
// failure here is "this candidate spec change itself is invalid" (§43) — not a
// statement about the currently running build.
type SpecHealth struct {
	Status      FacetStatus
	Diagnostics spec.Diagnostics
}

// ABIHealth is the extension-compatibility facet: does the candidate's generated
// extension contract remain compatible with the accepted ABI (ADR-001 §36)? It
// is Unknown when there is no candidate edit's spec to classify or the spec is
// invalid, OK for a neutral or additive transition, and Failed for a breaking
// one.
type ABIHealth struct {
	Status  FacetStatus
	Class   gen.Class
	Reasons []string
}

// BuildHealth is the runtime-build facet: does all current generated and user
// code compile (ADR-001 §36)? It describes the currently accepted project on
// disk, so a failure here is "Current Runtime Build is broken" (§43) — the state
// that must not block visual editing. It is Unknown when no toolchain or
// workspace is available to check, which is not the same as broken.
type BuildHealth struct {
	Status FacetStatus
	// Detail carries the compiler output for a broken build (e.g. a
	// hooks.go:42 syntax error), or the reason the facet is Unknown.
	Detail string
}

// Health is the three-state health model (ADR-001 §36, §71): spec validity, ABI
// compatibility and runtime build health, tracked and reported separately. The
// separation is the point — it is what makes it clear why visual editing may
// continue while Runtime Preview cannot (§71), and why a broken build never
// globally freezes editing (§43).
type Health struct {
	Spec  SpecHealth
	ABI   ABIHealth
	Build BuildHealth
}

// HealthRequest describes what to assess. Current is the accepted revision's
// spec; Candidate is the proposed edit (nil to assess Current alone). Workspace
// is the current project on disk, typechecked for build health; Module is only
// needed by callers that also drive candidate validation and is unused here.
type HealthRequest struct {
	Current   *spec.ProjectSpec
	Candidate *spec.ProjectSpec
	Workspace string
}

// FacetReport is one facet rendered uniformly for an API or UI: its §71 name,
// its status and a one-line human summary. Facets returns the three in a fixed
// order.
type FacetReport struct {
	Name    string
	Status  FacetStatus
	Summary string
}

// Evaluate computes the three-state health for an edit (ADR-001 §36, §71).
//
// The three facets come from independent inputs, which is what keeps them from
// collapsing:
//
//   - Spec validity is spec.Validate of the candidate (or Current when there is
//     no candidate) — a property of the edit.
//   - ABI compatibility is the candidate-vs-current transition class — also a
//     property of the edit, and Unknown when the spec is invalid (an invalid
//     spec has no defined ABI).
//   - Build health is a typecheck of the current workspace on disk — a property
//     of the accepted project, wholly independent of the candidate, so a broken
//     build reports here without touching the Spec or ABI facets (§43).
//
// The workspace is typechecked in place: build health is a read-only question
// about the real current project (go build writes nothing), unlike candidate
// validation, which must assemble a throwaway workspace.
func Evaluate(ctx context.Context, req HealthRequest, tc Toolchain) (Health, error) {
	target := req.Candidate
	if target == nil {
		target = req.Current
	}
	if target == nil {
		return Health{}, fmt.Errorf("compiler: Evaluate needs at least a current or candidate spec")
	}

	var health Health

	// Spec facet.
	diags := spec.Validate(target)
	health.Spec = SpecHealth{Status: statusFor(len(diags) == 0), Diagnostics: diags}

	// ABI facet: meaningful only for a candidate edit over a valid, classifiable
	// pair of specs.
	health.ABI = evaluateABI(req, health.Spec.Status)

	// Build facet: the current project, independent of the candidate.
	health.Build = evaluateBuild(ctx, req.Workspace, tc)

	return health, nil
}

func evaluateABI(req HealthRequest, specStatus FacetStatus) ABIHealth {
	// No edit to compare, or nothing to compare against: the ABI is unchanged.
	if req.Candidate == nil || req.Current == nil {
		return ABIHealth{Status: StatusOK, Class: gen.ClassNeutral}
	}
	// An invalid candidate spec has no defined ABI to classify — keep the facet
	// Unknown rather than letting a spec failure masquerade as an ABI verdict.
	if specStatus == StatusFailed {
		return ABIHealth{Status: StatusUnknown}
	}
	transition, err := ClassifyEdit(req.Current, req.Candidate)
	if err != nil {
		// Classification builds both graphs; a failure here (e.g. an invalid
		// current spec) leaves compatibility genuinely undetermined.
		return ABIHealth{Status: StatusUnknown}
	}
	return ABIHealth{
		Status:  statusFor(transition.Class != gen.ClassBreaking),
		Class:   transition.Class,
		Reasons: transition.Reasons,
	}
}

func evaluateBuild(ctx context.Context, workspace string, tc Toolchain) BuildHealth {
	if tc == nil || !tc.Available(ctx) {
		return BuildHealth{Status: StatusUnknown, Detail: "no toolchain available to check build health"}
	}
	if workspace == "" {
		return BuildHealth{Status: StatusUnknown, Detail: "no workspace to check build health"}
	}
	if err := tc.Typecheck(ctx, workspace); err != nil {
		return BuildHealth{Status: StatusFailed, Detail: err.Error()}
	}
	return BuildHealth{Status: StatusOK}
}

func statusFor(ok bool) FacetStatus {
	if ok {
		return StatusOK
	}
	return StatusFailed
}

// Facets renders the three states as an ordered, uniform list for an API or UI
// (ADR-001 §71). The order — Spec, Extension ABI, Runtime Build — is fixed.
func (h Health) Facets() []FacetReport {
	return []FacetReport{
		{Name: "Spec", Status: h.Spec.Status, Summary: h.Spec.Summary()},
		{Name: "Extension ABI", Status: h.ABI.Status, Summary: h.ABI.Summary()},
		{Name: "Runtime Build", Status: h.Build.Status, Summary: h.Build.Summary()},
	}
}

// Summary is a one-line description of the spec facet.
func (s SpecHealth) Summary() string {
	if s.Status == StatusOK {
		return "Valid"
	}
	if n := len(s.Diagnostics); n > 0 {
		return fmt.Sprintf("Invalid (%d issue(s))", n)
	}
	return "Invalid"
}

// Summary is a one-line description of the ABI facet.
func (a ABIHealth) Summary() string {
	switch a.Status {
	case StatusOK:
		return "Compatible"
	case StatusUnknown:
		return "Not evaluated"
	default:
		return fmt.Sprintf("Incompatible (%s)", a.Class)
	}
}

// Summary is a one-line description of the build facet, carrying the compiler
// detail when broken (the §71 "hooks.go:42 syntax error" line).
func (b BuildHealth) Summary() string {
	switch b.Status {
	case StatusOK:
		return "Builds"
	case StatusUnknown:
		if b.Detail != "" {
			return b.Detail
		}
		return "Not evaluated"
	default:
		if b.Detail != "" {
			return b.Detail
		}
		return "Broken"
	}
}
