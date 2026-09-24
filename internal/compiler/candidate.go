package compiler

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Outcome is the result of validating a candidate spec transition for
// compatibility with existing user extension code (ADR-001 §37-43).
//
// It is the ABI-compatibility question only — distinct from spec validity and
// from build health (the three states of ADR-001 §36 stay separate). A
// candidate that reaches validation is already spec-valid; validation decides
// whether it may become the current revision without stranding user code.
type Outcome int

const (
	// OutcomeAccepted means the candidate is compatible: either the transition
	// is ABI-neutral or additive, so compatibility holds without compiling user
	// code (§38, §40), or it is breaking and the candidate workspace typechecked
	// (§42). The caller may commit the candidate as the next revision.
	OutcomeAccepted Outcome = iota
	// OutcomeRejected means the candidate is a breaking transition whose
	// workspace failed to typecheck: existing extension code is incompatible with
	// the new generated contracts (§42). The current accepted revision must stay
	// unchanged.
	OutcomeRejected
	// OutcomeToolchainUnavailable means the candidate is a breaking transition
	// that requires compilation, but no usable toolchain exists (§66-67). Forge
	// must not silently accept it; the caller surfaces the required-compilation
	// message and the remediation actions rather than committing.
	OutcomeToolchainUnavailable
)

func (o Outcome) String() string {
	switch o {
	case OutcomeAccepted:
		return "accepted"
	case OutcomeRejected:
		return "rejected"
	case OutcomeToolchainUnavailable:
		return "toolchain_unavailable"
	default:
		return fmt.Sprintf("Outcome(%d)", int(o))
	}
}

// ToolchainRequiredMessage is the §67 message shown when a breaking candidate
// needs a typecheck but no toolchain is available. It is a fixed part of the UX
// contract, exported so the control plane and editor render the same words.
const ToolchainRequiredMessage = "This change modifies the generated extension API and must be type-checked before it can be committed."

// ToolchainAction is one remediation option offered when the toolchain is
// unavailable (§67). ADR-002 owns the provisioning these actions trigger; here
// they are only the structured choices to present.
type ToolchainAction string

const (
	ActionInstallManagedToolchain    ToolchainAction = "install_managed_toolchain"
	ActionConfigureExistingToolchain ToolchainAction = "configure_existing_toolchain"
	ActionCancel                     ToolchainAction = "cancel"
)

// Label is the human-readable action text (§67).
func (a ToolchainAction) Label() string {
	switch a {
	case ActionInstallManagedToolchain:
		return "Install managed toolchain"
	case ActionConfigureExistingToolchain:
		return "Configure existing toolchain"
	case ActionCancel:
		return "Cancel"
	default:
		return string(a)
	}
}

// ToolchainUnavailableActions returns the §67 remediation actions in their
// fixed presentation order.
func ToolchainUnavailableActions() []ToolchainAction {
	return []ToolchainAction{
		ActionInstallManagedToolchain,
		ActionConfigureExistingToolchain,
		ActionCancel,
	}
}

// Validation is the full result of ValidateCandidate: the outcome, the
// transition classification that produced it, and human-facing detail.
type Validation struct {
	Outcome    Outcome
	Transition gen.Transition
	// Detail explains a non-accepted outcome: the typecheck output for a
	// Rejected candidate, or ToolchainRequiredMessage for an unavailable
	// toolchain. Empty for Accepted.
	Detail string
	// Actions are the remediation options for OutcomeToolchainUnavailable (§67),
	// nil otherwise.
	Actions []ToolchainAction
}

// Toolchain is the seam through which candidate compatibility validation runs a
// real typecheck (ADR-001 §66). It is an interface so the compiler stays
// testable without a Go installation and so a sandboxed build worker can supply
// its own executor (DESIGN.md §27), mirroring the injectable gombit.Runner.
type Toolchain interface {
	// Available reports whether a usable toolchain exists. A false result drives
	// the §67 toolchain-unavailable path rather than a silent accept.
	Available(ctx context.Context) bool
	// Typecheck compiles the workspace rooted at dir, returning a non-nil error
	// (carrying the compiler output) when it does not build.
	//
	// It must not mutate dir. Build-health evaluation (Evaluate) typechecks the
	// user's real project in place and relies on this — `go build ./...` writes
	// nothing, and any implementation substituted for GoToolchain must hold the
	// same guarantee.
	Typecheck(ctx context.Context, dir string) error
}

// CandidateRequest describes one candidate compatibility validation.
type CandidateRequest struct {
	// Workspace is the existing project directory: the scaffolded Gombit app
	// with the current generated tree and the user's extensions. It is only
	// read — validation happens in a throwaway copy so the accepted revision's
	// project is never touched (§42, "the current accepted revision remains
	// unchanged"). It is required only for a breaking transition that reaches the
	// typecheck.
	Workspace string
	// Module is the generated application's Go module path.
	Module string
	// Current is the accepted revision's spec; Candidate is the proposed one.
	// Both must be semantically valid — ClassifyEdit builds each graph.
	Current   *spec.ProjectSpec
	Candidate *spec.ProjectSpec
}

// ValidateCandidate decides whether a candidate spec transition may become the
// current revision (ADR-001 §37-43, §66-67).
//
// It classifies the transition first. A neutral or additive transition is
// accepted immediately: compatibility is proven from the ABI diff without
// compiling user code (§38, §40), so it commits even while unrelated user code
// is broken — the §43 guarantee that a broken build never globally freezes
// editing. A breaking transition requires a real typecheck (§41-42): the
// candidate's generated contracts plus the user's copied extensions are
// assembled in a throwaway workspace and built. If it compiles, the candidate is
// accepted; if not, it is rejected and the current revision stands. If no
// toolchain is available for that typecheck, the candidate is neither accepted
// nor rejected — the toolchain-unavailable outcome carries the §67 message and
// actions so the caller can prompt rather than silently accept.
//
// ValidateCandidate never mutates the workspace; a rejected or unavailable
// outcome leaves the accepted revision's project exactly as it was.
func ValidateCandidate(ctx context.Context, req CandidateRequest, tc Toolchain) (Validation, error) {
	if req.Current == nil || req.Candidate == nil {
		return Validation{}, fmt.Errorf("compiler: ValidateCandidate needs both current and candidate specs")
	}

	transition, err := ClassifyEdit(req.Current, req.Candidate)
	if err != nil {
		return Validation{}, err
	}

	// Neutral and additive transitions are compatible by the ABI diff alone; the
	// toolchain is never consulted, which is exactly what keeps an unrelated
	// presentation edit available while user code is broken (§43).
	if transition.Class != gen.ClassBreaking {
		return Validation{Outcome: OutcomeAccepted, Transition: transition}, nil
	}

	// Breaking: a compatibility proof requires compiling the candidate against
	// the user's extensions (§42).
	if tc == nil || !tc.Available(ctx) {
		return Validation{
			Outcome:    OutcomeToolchainUnavailable,
			Transition: transition,
			Detail:     ToolchainRequiredMessage,
			Actions:    ToolchainUnavailableActions(),
		}, nil
	}

	// Module and Workspace are consumed only here, on the typecheck path — the
	// neutral/additive fast path never compiles, so it must not be coupled to a
	// value it never reads (that would gate the §43 "commits while user code is
	// broken" edit on a module path it doesn't need).
	if req.Workspace == "" {
		return Validation{}, fmt.Errorf("compiler: ValidateCandidate needs a workspace directory to typecheck a breaking transition")
	}
	if req.Module == "" {
		return Validation{}, fmt.Errorf("compiler: ValidateCandidate needs a module path to compile the candidate workspace")
	}

	dir, cleanup, err := prepareCandidateWorkspace(req.Workspace, req.Candidate, req.Module)
	if err != nil {
		return Validation{}, err
	}
	defer cleanup()

	if buildErr := tc.Typecheck(ctx, dir); buildErr != nil {
		return Validation{
			Outcome:    OutcomeRejected,
			Transition: transition,
			Detail:     buildErr.Error(),
		}, nil
	}
	return Validation{Outcome: OutcomeAccepted, Transition: transition}, nil
}

// prepareCandidateWorkspace assembles the temporary workspace of §42: a copy of
// the existing project (its Gombit skeleton, go.mod and user extensions) with
// the candidate's generated contracts materialized over the generated roots. It
// returns the workspace directory and a cleanup func the caller must invoke.
//
// The copy is what makes validation side-effect-free: Materialize wipes and
// rewrites the generated roots, so it runs against the throwaway copy, never the
// caller's workspace.
func prepareCandidateWorkspace(workspace string, candidate *spec.ProjectSpec, module string) (dir string, cleanup func(), err error) {
	files, err := Compile(candidate, module)
	if err != nil {
		return "", nil, fmt.Errorf("compiler: compiling candidate: %w", err)
	}

	tmp, err := os.MkdirTemp("", "forge-candidate-*")
	if err != nil {
		return "", nil, fmt.Errorf("compiler: candidate workspace: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	dest := filepath.Join(tmp, "workspace")
	if err := copyTree(workspace, dest); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("compiler: copying workspace: %w", err)
	}
	if err := Materialize(dest, files); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("compiler: materializing candidate: %w", err)
	}
	return dest, cleanup, nil
}

// copyTree recursively copies the directory src into dst, preserving file modes.
//
// It skips dot-prefixed entries and node_modules: the typecheck needs the Go
// skeleton, go.mod/go.sum and user extensions, not a scaffold's .env, .git or a
// frontend's dependency tree. Skipping .env in particular keeps the random
// GOMBIT_JWT_SECRET (AGENTS.md, "the scaffold is not byte-reproducible") out of
// the copy, and skipping node_modules keeps the copy cheap.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				return fs.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		return copyFile(p, filepath.Join(dst, rel), d)
	})
}

func copyFile(src, dst string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}

// GoRunner executes one Go toolchain command in dir and returns its combined
// output. It is injectable so GoToolchain's command construction is testable
// without shelling out, mirroring gombit.Runner.
type GoRunner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// GoToolchain is the production Toolchain: it typechecks a candidate workspace
// with the system `go` binary. ADR-002's managed local-toolchain fallback (the
// Install/Configure actions of §67) is out of scope here; this covers the
// "usable system toolchain exists" case.
type GoToolchain struct {
	// Bin is the go executable; "go" on PATH when empty.
	Bin string
	// Run executes commands; execGoRunner when nil.
	Run GoRunner
}

// compile-time proof that GoToolchain satisfies the seam.
var _ Toolchain = GoToolchain{}

func (g GoToolchain) bin() string {
	if g.Bin == "" {
		return "go"
	}
	return g.Bin
}

// Available reports whether the go binary can be found (§67's "usable
// toolchain"). ADR-002 owns provisioning a managed one when it cannot.
func (g GoToolchain) Available(ctx context.Context) bool {
	_, err := exec.LookPath(g.bin())
	return err == nil
}

// Typecheck builds every package in the workspace. `go build ./...` compiles
// without writing binaries, so it is a pure compatibility proof: it fails
// exactly when the candidate's generated contracts and the user's extensions no
// longer typecheck together.
//
// Any nonzero exit is reported as incompatibility. In principle that conflates a
// genuine compile error with a broken build environment (a module missing from
// the cache on a cold worker). That conflation is unreachable for a candidate
// prepared by prepareCandidateWorkspace: generation emits only stdlib and the
// fixed set of Gombit imports the seeded workspace's go.mod already resolves, so
// a candidate never introduces a new module requirement — a download failure can
// only mean the base workspace was already unbuildable. A future toolchain that
// resolves new dependencies must separate a setup failure from a compile failure
// before this drives a user-facing rejection.
func (g GoToolchain) Typecheck(ctx context.Context, dir string) error {
	run := g.Run
	if run == nil {
		run = execGoRunner
	}
	out, err := run(ctx, dir, g.bin(), "build", "./...")
	if err != nil {
		return fmt.Errorf("candidate does not typecheck: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func execGoRunner(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	return command.CombinedOutput()
}
