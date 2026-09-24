package compiler

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// fakeToolchain is a scripted Toolchain that records whether it was consulted,
// so a test can prove a neutral/additive candidate never reaches the compiler.
type fakeToolchain struct {
	available    bool
	typecheckErr error
	// inspect, if set, runs against the workspace dir the validator built,
	// before the scripted result is returned.
	inspect func(t *testing.T, dir string)
	t       *testing.T

	availableCalls int
	typecheckCalls int
	lastDir        string
}

func (f *fakeToolchain) Available(context.Context) bool {
	f.availableCalls++
	return f.available
}

func (f *fakeToolchain) Typecheck(_ context.Context, dir string) error {
	f.typecheckCalls++
	f.lastDir = dir
	if f.inspect != nil {
		f.inspect(f.t, dir)
	}
	return f.typecheckErr
}

// breakingCandidate returns base and a clone with resource[0]'s code symbol
// renamed — a source rename, the canonical breaking transition.
func breakingCandidate(t *testing.T) (base, cand *spec.ProjectSpec) {
	t.Helper()
	base = sampleSpec(t)
	cand = cloneSpec(t, base)
	cand.Resources[0].CodeName = "Client"
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("candidate invalid:\n%s", d.Error())
	}
	if tr := classify(t, base, cand); tr.Class != gen.ClassBreaking {
		t.Fatalf("fixture drift: expected breaking, got %s", tr.Class)
	}
	return base, cand
}

// TestValidateAcceptsNeutralWithoutToolchain is the §43 guarantee: a
// presentation-only edit is accepted without ever compiling user code, so it
// commits even when the toolchain would fail. The fake would reject every
// typecheck and reports itself unavailable; neither must be consulted.
func TestValidateAcceptsNeutralWithoutToolchain(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].Label = "Clients" // relabel, code symbol frozen

	tc := &fakeToolchain{available: false, typecheckErr: fmt.Errorf("should never run"), t: t}
	got, err := ValidateCandidate(context.Background(), CandidateRequest{
		Module: testModule, Current: base, Candidate: cand,
	}, tc)
	if err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	if got.Outcome != OutcomeAccepted {
		t.Errorf("neutral edit must be accepted; got %s (%s)", got.Outcome, got.Detail)
	}
	if tc.typecheckCalls != 0 || tc.availableCalls != 0 {
		t.Errorf("neutral edit must not consult the toolchain; available=%d typecheck=%d",
			tc.availableCalls, tc.typecheckCalls)
	}
}

// TestValidateAcceptsAdditiveWithoutToolchain: a new field is additive, proven
// compatible without compiling (§40).
func TestValidateAcceptsAdditiveWithoutToolchain(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].Fields = append(cand.Resources[0].Fields, &spec.Field{
		ID: spec.MustNewID(spec.KindField), Label: "Nickname", Type: spec.TypeString,
		CodeName: "Nickname", StorageName: "nickname",
	})
	if d := spec.Validate(cand); d != nil {
		t.Fatalf("candidate invalid:\n%s", d.Error())
	}

	tc := &fakeToolchain{available: true, t: t}
	got, err := ValidateCandidate(context.Background(), CandidateRequest{
		Module: testModule, Current: base, Candidate: cand,
	}, tc)
	if err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	if got.Outcome != OutcomeAccepted {
		t.Errorf("additive edit must be accepted; got %s", got.Outcome)
	}
	if tc.typecheckCalls != 0 {
		t.Errorf("additive edit must not compile user code; typecheck ran %d times", tc.typecheckCalls)
	}
}

// TestValidateRejectsBreakingWhenIncompatible is acceptance criterion 1: a
// breaking transition whose workspace fails to typecheck is rejected, and the
// caller's workspace is left untouched (the current revision stands).
func TestValidateRejectsBreakingWhenIncompatible(t *testing.T) {
	base, cand := breakingCandidate(t)
	ws := seedWorkspace(t, base)
	before := snapshotTree(t, ws)

	tc := &fakeToolchain{available: true, typecheckErr: fmt.Errorf("undefined: CustomerView"), t: t}
	got, err := ValidateCandidate(context.Background(), CandidateRequest{
		Workspace: ws, Module: testModule, Current: base, Candidate: cand,
	}, tc)
	if err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	if got.Outcome != OutcomeRejected {
		t.Fatalf("incompatible breaking transition must be rejected; got %s", got.Outcome)
	}
	if got.Detail == "" {
		t.Error("a rejection must carry the typecheck detail")
	}
	if tc.typecheckCalls != 1 {
		t.Errorf("breaking transition must run exactly one typecheck; ran %d", tc.typecheckCalls)
	}
	if after := snapshotTree(t, ws); !equalTrees(before, after) {
		t.Error("validation must not mutate the caller's workspace")
	}
}

// TestValidateAcceptsBreakingWhenCompatible: the same breaking transition, but
// the copied user code still compiles against the new contracts -> accepted.
func TestValidateAcceptsBreakingWhenCompatible(t *testing.T) {
	base, cand := breakingCandidate(t)
	ws := seedWorkspace(t, base)

	tc := &fakeToolchain{available: true, t: t}
	got, err := ValidateCandidate(context.Background(), CandidateRequest{
		Workspace: ws, Module: testModule, Current: base, Candidate: cand,
	}, tc)
	if err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	if got.Outcome != OutcomeAccepted {
		t.Fatalf("compatible breaking transition must be accepted; got %s (%s)", got.Outcome, got.Detail)
	}
	if tc.typecheckCalls != 1 {
		t.Errorf("breaking transition must run one typecheck; ran %d", tc.typecheckCalls)
	}
}

// TestValidateSurfacesToolchainUnavailable is acceptance criterion 3: a breaking
// transition with no usable toolchain surfaces the §67 message and actions
// rather than accepting or rejecting.
func TestValidateSurfacesToolchainUnavailable(t *testing.T) {
	base, cand := breakingCandidate(t)

	tc := &fakeToolchain{available: false, typecheckErr: fmt.Errorf("should never run"), t: t}
	got, err := ValidateCandidate(context.Background(), CandidateRequest{
		Workspace: seedWorkspace(t, base), Module: testModule, Current: base, Candidate: cand,
	}, tc)
	if err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	if got.Outcome != OutcomeToolchainUnavailable {
		t.Fatalf("missing toolchain on a breaking transition must surface toolchain-unavailable; got %s", got.Outcome)
	}
	if got.Detail != ToolchainRequiredMessage {
		t.Errorf("toolchain-unavailable detail = %q, want the §67 message", got.Detail)
	}
	want := ToolchainUnavailableActions()
	if len(got.Actions) != len(want) {
		t.Fatalf("actions = %v, want %v", got.Actions, want)
	}
	for i := range want {
		if got.Actions[i] != want[i] {
			t.Errorf("action %d = %q, want %q", i, got.Actions[i], want[i])
		}
	}
	if tc.typecheckCalls != 0 {
		t.Errorf("an unavailable toolchain must never be asked to typecheck; ran %d", tc.typecheckCalls)
	}
}

// TestValidateNilToolchainIsUnavailable: a nil Toolchain is treated as
// unavailable, not a panic — a breaking transition still fails closed to the
// §67 path.
func TestValidateNilToolchainIsUnavailable(t *testing.T) {
	base, cand := breakingCandidate(t)
	got, err := ValidateCandidate(context.Background(), CandidateRequest{
		Workspace: seedWorkspace(t, base), Module: testModule, Current: base, Candidate: cand,
	}, nil)
	if err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	if got.Outcome != OutcomeToolchainUnavailable {
		t.Errorf("nil toolchain on a breaking transition must be unavailable; got %s", got.Outcome)
	}
}

// TestValidatePreparesWorkspace proves prepareCandidateWorkspace assembles §42's
// workspace: the candidate's new generated contracts plus the copied user
// extensions, in a throwaway directory that is not the caller's.
func TestValidatePreparesWorkspace(t *testing.T) {
	base, cand := breakingCandidate(t)
	ws := seedWorkspace(t, base)

	tc := &fakeToolchain{available: true, t: t, inspect: func(t *testing.T, dir string) {
		if dir == ws {
			t.Error("typecheck must run against a copy, not the caller's workspace")
		}
		// The candidate renamed Customer -> Client, so the generated tree must
		// carry the client package and not the stale customer one.
		if !exists(filepath.Join(dir, "internal/forge_generated/client/model.go")) {
			t.Error("candidate generated contract (client/model.go) missing from workspace")
		}
		if exists(filepath.Join(dir, "internal/forge_generated/customer/model.go")) {
			t.Error("stale current contract (customer/model.go) must be wiped from the candidate workspace")
		}
		// The user's copied extension must be present.
		if !exists(filepath.Join(dir, "internal/extensions/customer/hooks.go")) {
			t.Error("user extension not copied into the candidate workspace")
		}
	}}

	got, err := ValidateCandidate(context.Background(), CandidateRequest{
		Workspace: ws, Module: testModule, Current: base, Candidate: cand,
	}, tc)
	if err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	if got.Outcome != OutcomeAccepted {
		t.Fatalf("expected accepted, got %s (%s)", got.Outcome, got.Detail)
	}
	// The throwaway workspace is removed after validation.
	if tc.lastDir != "" && exists(tc.lastDir) {
		t.Errorf("candidate workspace %q must be cleaned up", tc.lastDir)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	base := sampleSpec(t)
	cases := []struct {
		name string
		req  CandidateRequest
	}{
		{"nil current", CandidateRequest{Module: testModule, Candidate: base}},
		{"nil candidate", CandidateRequest{Module: testModule, Current: base}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateCandidate(context.Background(), tc.req, &fakeToolchain{available: true, t: t}); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

// TestValidateNeutralNeedsNoModule: the fast path never compiles, so it must not
// require a module path — coupling it to Module would gate a §43 presentation
// edit on a value it never reads.
func TestValidateNeutralNeedsNoModule(t *testing.T) {
	base := sampleSpec(t)
	cand := cloneSpec(t, base)
	cand.Resources[0].Label = "Clients" // neutral relabel
	got, err := ValidateCandidate(context.Background(), CandidateRequest{
		Current: base, Candidate: cand, // no Module, no Workspace
	}, &fakeToolchain{available: true, t: t})
	if err != nil {
		t.Fatalf("neutral edit must not require a module: %v", err)
	}
	if got.Outcome != OutcomeAccepted {
		t.Errorf("neutral edit must be accepted; got %s", got.Outcome)
	}
}

// TestValidateBreakingNeedsWorkspaceAndModule: once a breaking transition passes
// the toolchain-availability gate, a missing workspace or module is a caller
// error, not a silent accept.
func TestValidateBreakingNeedsWorkspaceAndModule(t *testing.T) {
	base, cand := breakingCandidate(t)
	tc := &fakeToolchain{available: true, t: t}
	cases := []struct {
		name string
		req  CandidateRequest
	}{
		{"no workspace", CandidateRequest{Module: testModule, Current: base, Candidate: cand}},
		{"no module", CandidateRequest{Workspace: seedWorkspace(t, base), Current: base, Candidate: cand}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ValidateCandidate(context.Background(), c.req, tc); err == nil {
				t.Error("breaking transition must error, got nil")
			}
		})
	}
}

// TestGoToolchainTypechecks exercises the real GoToolchain against a tiny,
// dependency-free module: a good tree builds, a broken one fails with detail.
// This is the only test that shells out to `go`.
func TestGoToolchainTypechecks(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	tc := GoToolchain{}
	if !tc.Available(context.Background()) {
		t.Fatal("go is on PATH but Available reported false")
	}

	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/tc\n\ngo 1.25\n")
	writeFile(t, dir, "main.go", "package main\n\nfunc main() { _ = 1 }\n")
	if err := tc.Typecheck(context.Background(), dir); err != nil {
		t.Fatalf("a valid module must typecheck: %v", err)
	}

	writeFile(t, dir, "main.go", "package main\n\nfunc main() { return undefinedSymbol }\n")
	err := tc.Typecheck(context.Background(), dir)
	if err == nil {
		t.Fatal("a broken module must fail to typecheck")
	}
}

// --- workspace fixtures -----------------------------------------------------

// seedWorkspace builds a minimal on-disk project for spec s: the compiler-owned
// generated tree plus a user extension file under internal/extensions. It is not
// a compilable Gombit app — the tests that use it inject a fake toolchain — but
// it carries the two things prepareCandidateWorkspace must handle: a generated
// tree to replace and user extensions to preserve.
func seedWorkspace(t *testing.T, s *spec.ProjectSpec) string {
	t.Helper()
	dir := t.TempDir()
	files, err := Compile(s, testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := Materialize(dir, files); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	writeFile(t, dir, "go.mod", "module "+testModule+"\n\ngo 1.25\n")
	writeFile(t, dir, "internal/extensions/customer/hooks.go",
		"package customer\n\n// user-owned; never rewritten by Forge\n")
	return dir
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// snapshotTree records path -> content for every regular file under dir, so a
// test can prove validation left the caller's workspace byte-for-byte unchanged.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return out
}

func equalTrees(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
