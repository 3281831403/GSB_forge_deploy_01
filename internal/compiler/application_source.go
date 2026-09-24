package compiler

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gombit-dev/gombit-forge/internal/gombit"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// SourceToolchain is the explicit seam for the external tools that assembling an
// application's source tree needs: Gombit's scaffold and migration generation,
// and `go mod tidy`. BuildApplicationSource drives only this interface, so the
// toolchain shelling lives behind one boundary (never ad-hoc in an HTTP handler)
// and the assembly is unit-testable with a fake. GombitToolchain is the
// production implementation.
type SourceToolchain interface {
	Scaffold(ctx context.Context, req gombit.ScaffoldRequest) error
	Tidy(ctx context.Context, dir string) error
	MakeMigrations(ctx context.Context, req gombit.MakeMigrationsRequest) error
}

// GombitToolchain is the production SourceToolchain: the Gombit CLI plus
// `go mod tidy`. Tidy is a plain `go` invocation (Gombit's CLI has no tidy
// verb); keeping it here means callers never shell out themselves.
type GombitToolchain struct {
	CLI *gombit.CLI
}

func (g GombitToolchain) cli() *gombit.CLI {
	if g.CLI != nil {
		return g.CLI
	}
	return &gombit.CLI{}
}

func (g GombitToolchain) Scaffold(ctx context.Context, req gombit.ScaffoldRequest) error {
	return g.cli().Scaffold(ctx, req)
}

func (g GombitToolchain) MakeMigrations(ctx context.Context, req gombit.MakeMigrationsRequest) error {
	return g.cli().MakeMigrations(ctx, req)
}

func (g GombitToolchain) Tidy(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, "go", "mod", "tidy")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod tidy: %w\n%s", err, out)
	}
	return nil
}

// ApplicationSourceRequest describes an export's source assembly.
type ApplicationSourceRequest struct {
	// Spec is the revision's ProjectSpec to compile.
	Spec *spec.ProjectSpec
	// Module is the exported application's Go module path.
	Module string
	// Name is the application name passed to the scaffold; the project slug or
	// name are reasonable choices. Defaults to the module's last element.
	Name string
	// Provenance is written to forge.json (the caller resolves the Gombit version
	// and source revision).
	Provenance Provenance
}

// BuildApplicationSource assembles the complete, sanitized source of an exported
// Gombit application from a revision (the Revision → ApplicationSource path). It
// is the single artifact every export target consumes — ZIP, GitHub, and later
// the Cloud build submit — so all of them ship exactly the same tree.
//
// The steps compose Forge's existing pieces over the coarse Gombit boundary
// rather than reimplementing any of them (P4/D12): the toolchain scaffolds the
// canonical base tree; Compile + Materialize lay down the generated code;
// CompositionRoot writes the Forge-owned main; README and forge.json are added;
// the toolchain tidies modules and generates the migration; and Collect returns
// the sanitized SourceFile set. The scratch directory is created and removed
// here, so no caller manages a workspace.
func BuildApplicationSource(ctx context.Context, tc SourceToolchain, req ApplicationSourceRequest) ([]SourceFile, error) {
	if req.Spec == nil {
		return nil, fmt.Errorf("compiler: application source needs a spec")
	}
	if req.Module == "" {
		return nil, fmt.Errorf("compiler: application source needs a module path")
	}
	name := req.Name
	if name == "" {
		name = filepath.Base(req.Module)
	}

	dir, err := os.MkdirTemp("", "forge-export-*")
	if err != nil {
		return nil, fmt.Errorf("compiler: application source workspace: %w", err)
	}
	defer os.RemoveAll(dir)

	// Canonical base tree from Gombit's scaffold (postgres + cookie — the locked
	// managed defaults D4/D5).
	if err := tc.Scaffold(ctx, gombit.ScaffoldRequest{
		Dir: dir, Name: name, Module: req.Module,
		Database: gombit.DatabasePostgres, Auth: gombit.AuthCookie, UI: gombit.UIMinimal,
		Tidy: true,
	}); err != nil {
		return nil, fmt.Errorf("compiler: scaffold: %w", err)
	}

	// Generated code.
	files, err := Compile(req.Spec, req.Module)
	if err != nil {
		return nil, fmt.Errorf("compiler: compile: %w", err)
	}
	if err := Materialize(dir, files); err != nil {
		return nil, fmt.Errorf("compiler: materialize: %w", err)
	}

	// Forge-owned composition root, plus the export's README and provenance.
	root, err := CompositionRoot(req.Module)
	if err != nil {
		return nil, err
	}
	if err := writeRootFile(dir, CompositionRootPath, root); err != nil {
		return nil, err
	}
	if err := WriteReadme(dir, req.Spec); err != nil {
		return nil, fmt.Errorf("compiler: readme: %w", err)
	}
	if err := WriteProvenance(dir, req.Provenance); err != nil {
		return nil, fmt.Errorf("compiler: provenance: %w", err)
	}

	// Toolchain finalization: resolve the generated imports and generate the
	// migration for the compiled model set.
	if err := tc.Tidy(ctx, dir); err != nil {
		return nil, fmt.Errorf("compiler: tidy: %w", err)
	}
	models, err := MigrationModelsForSpec(req.Spec, req.Module)
	if err != nil {
		return nil, fmt.Errorf("compiler: migration models: %w", err)
	}
	if err := tc.MakeMigrations(ctx, gombit.MakeMigrationsRequest{
		Dir: dir, Name: "initial", Driver: gombit.DatabasePostgres, Models: models,
	}); err != nil {
		return nil, fmt.Errorf("compiler: makemigrations: %w", err)
	}

	return Collect(dir)
}

// writeRootFile writes content to a repo-relative path under dir, creating parents.
func writeRootFile(dir, rel string, content []byte) error {
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("compiler: mkdir for %s: %w", rel, err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		return fmt.Errorf("compiler: write %s: %w", rel, err)
	}
	return nil
}
