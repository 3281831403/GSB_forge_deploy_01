package compiler

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// StubFiles returns the one-time, user-owned extension stubs a spec calls for
// (today, a hooks.go per resource that declares lifecycle hooks — ADR-001 §35).
//
// These are NOT part of the compiler-owned tree that Compile returns and
// Materialize writes; they live under internal/extensions/** (D7) and are
// written create-if-absent by WriteStubs. StubFiles is separate from Compile so
// the generated-tree flow can never accidentally wipe or overwrite user code.
// module is the application's Go module path, needed for the generated-package
// import inside a stub.
func StubFiles(s *spec.ProjectSpec, module string) ([]gen.File, error) {
	g, err := graph.Build(s)
	if err != nil {
		return nil, fmt.Errorf("compiler: %w", err)
	}
	return gen.HookStubs(g, module)
}

// WriteStubs writes each stub into the project rooted at dir only if the target
// does not already exist, and reports which files it created.
//
// This is the one place Forge writes under internal/extensions, and it is
// deliberately create-only: an existing file is left exactly as the developer
// left it (ADR-001 §35, §90 — the stub is offered once and never rewritten). It
// refuses any path outside the extensions root, so a generator bug or crafted
// path cannot escape user-owned territory into the generated tree or out of the
// project.
func WriteStubs(dir string, stubs []gen.File) (created []string, err error) {
	if dir == "" {
		return nil, fmt.Errorf("compiler: WriteStubs needs a project directory")
	}
	for _, f := range stubs {
		if err := checkExtensionPath(f.Path); err != nil {
			return created, err
		}
	}

	for _, f := range stubs {
		target := filepath.Join(dir, filepath.FromSlash(f.Path))
		if _, statErr := os.Stat(target); statErr == nil {
			continue // exists: never overwrite user-owned code
		} else if !os.IsNotExist(statErr) {
			return created, fmt.Errorf("compiler: %s: %w", f.Path, statErr)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return created, fmt.Errorf("compiler: %s: %w", f.Path, err)
		}
		if err := os.WriteFile(target, f.Content, 0o644); err != nil {
			return created, fmt.Errorf("compiler: %s: %w", f.Path, err)
		}
		created = append(created, f.Path)
	}
	return created, nil
}

// checkExtensionPath rejects a path that is not a clean, relative, slash path
// under the extensions root — the user-owned counterpart to checkGeneratedPath.
// It keeps stub writing from landing in the generated tree or outside the
// project, whether by a generator bug or a crafted path.
func checkExtensionPath(p string) error {
	if p == "" {
		return fmt.Errorf("compiler: empty stub file path")
	}
	if strings.ContainsRune(p, '\\') {
		return fmt.Errorf("compiler: stub file path %q contains a backslash; paths are slash-separated", p)
	}
	if path.IsAbs(p) || path.Clean(p) != p {
		return fmt.Errorf("compiler: stub file path %q is not a clean relative path", p)
	}
	root := gen.ExtensionsRoot
	if p == root || strings.HasPrefix(p, root+"/") {
		return nil
	}
	return fmt.Errorf("compiler: stub file path %q is outside the extensions root %q", p, root)
}
