package compiler

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
)

// ExtensionsRoot is the user-owned code directory (ADR-001 §17). Forge may
// compile it, observe whether files exist, and archive it with explicit consent,
// and it may create a one-time stub under it (WriteStubs) — but it never
// rewrites an existing implementation. Materialize (the generated-tree wipe)
// writes nothing here. The value is owned by the gen package.
const ExtensionsRoot = gen.ExtensionsRoot

// generatedRoots are the directories Forge wholly owns (ADR-001 §16): it wipes
// and recreates them on every materialization and writes nothing outside them.
// Everything Compile emits must live under one of these. It is unexported —
// exported it would be a package-level slice governing recursive deletion in a
// user's project that any importer could repoint. Read it through GeneratedRoots.
var generatedRoots = []string{gen.GeneratedRoot, gen.FrontendRoot}

// GeneratedRoots returns the directories Forge wholly owns and recreates on
// every materialization. It returns a copy so a caller cannot widen the set that
// Materialize wipes.
func GeneratedRoots() []string { return slices.Clone(generatedRoots) }

// Materialize writes the compiled files into the project rooted at dir.
//
// It recreates the generated roots rather than merging into them, so a resource
// the spec deleted, renamed or split leaves no stale generated file behind
// (ADR-001 §16). Ownership is at the directory level (§18): Materialize touches
// only the generated roots, never internal/extensions/** (§17) or anything else.
//
// It is careful about the destructive half, not just the writes:
//
//   - It refuses an empty file set. A compile that produced no files is a caller
//     error (a mishandled Compile error hands over a nil slice), never a reason
//     to delete a project's generated code — so it fails closed (D14) rather
//     than wiping and reporting success.
//   - It validates every path is a clean relative path under a generated root
//     before touching disk, so a generator bug or crafted path cannot write into
//     user territory (§18).
//   - It stages then swaps with renames only. Each root's new contents are
//     written into a sibling staging directory (pass 1); then, per root, the
//     live tree is moved aside with a rename and the staging directory renamed
//     into its place (pass 2). Both are renames, which never recurse, so an
//     interrupted materialization leaves each generated root either fully
//     updated or untouched — never a half-written tree (unlike a recursive
//     delete, which can tear a root then fail). The swap is per root: with more
//     than one root a failure between swaps can leave one root updated and
//     another not, which re-running converges. A process killed in the brief
//     window between the two renames leaves the root absent, its previous
//     contents in the retired sibling and the new ones in the staging sibling;
//     re-running materialization, or renaming the retired sibling back,
//     recovers it. Both siblings are named with a leading dot so the Go
//     toolchain ignores them rather than compiling a stray copy of the tree
//     (see hiddenSibling).
func Materialize(dir string, files []gen.File) error {
	if dir == "" {
		return fmt.Errorf("compiler: Materialize needs a project directory")
	}
	if len(files) == 0 {
		return fmt.Errorf("compiler: refusing to materialize an empty file set (a compile that produced no files is not a reason to delete %v)", generatedRoots)
	}
	for _, f := range files {
		if err := checkGeneratedPath(f.Path); err != nil {
			return err
		}
	}

	type rootStage struct {
		live     string // the live generated root, under dir
		staging  string // its sibling staging directory
		hasFiles bool
	}
	stages := make([]rootStage, 0, len(generatedRoots))
	// cleanup removes every root's staging directory, whether or not it has been
	// reached yet, so a failure partway through pass 1 leaves nothing behind.
	cleanup := func() {
		for _, root := range generatedRoots {
			_ = os.RemoveAll(hiddenSibling(filepath.Join(dir, filepath.FromSlash(root)), ".forge-staging"))
		}
	}

	// Pass 1: write every root's new contents into a staging directory. Nothing
	// in the live tree is touched, so a failure here leaves the project untouched.
	for _, root := range generatedRoots {
		live := filepath.Join(dir, filepath.FromSlash(root))
		staging := hiddenSibling(live, ".forge-staging")
		if err := os.RemoveAll(staging); err != nil { // leftovers from an earlier crash
			cleanup()
			return fmt.Errorf("compiler: clearing staging for %s: %w", root, err)
		}
		s := rootStage{live: live, staging: staging}
		for _, f := range files {
			rel, ok := underRoot(f.Path, root)
			if !ok {
				continue
			}
			target := filepath.Join(staging, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				cleanup()
				return fmt.Errorf("compiler: %s: %w", f.Path, err)
			}
			if err := os.WriteFile(target, f.Content, 0o644); err != nil {
				cleanup()
				return fmt.Errorf("compiler: %s: %w", f.Path, err)
			}
			s.hasFiles = true
		}
		stages = append(stages, s)
	}

	// Pass 2: swap each root with renames only. A rename never recurses, so it
	// cannot half-succeed the way RemoveAll can — the old tree is moved aside
	// atomically, the new tree moved in, and only then is the old tree deleted
	// (best effort), where a failure costs a stale *.forge-old directory and
	// nothing else. On failure here the staging directory is deliberately NOT
	// cleaned up: it is the recovery artifact.
	for _, s := range stages {
		retired := hiddenSibling(s.live, ".forge-old")
		_ = os.RemoveAll(retired) // leftovers from an earlier crash

		if _, err := os.Stat(s.live); err == nil {
			if err := os.Rename(s.live, retired); err != nil {
				return fmt.Errorf("compiler: retiring %s: %w", s.live, err)
			}
		}
		if s.hasFiles {
			if err := os.Rename(s.staging, s.live); err != nil {
				// The install failed; put the old tree back. If even that fails,
				// the root is missing — name where both halves are so it can be
				// recovered by hand.
				if rerr := os.Rename(retired, s.live); rerr != nil {
					return fmt.Errorf("compiler: installing %s: %w; and restoring the previous tree failed: %v — the previous tree is at %s and the new one at %s",
						s.live, err, rerr, retired, s.staging)
				}
				return fmt.Errorf("compiler: installing %s: %w", s.live, err)
			}
		}
		// No files for this root: it is wiped (§16) — the old tree went to
		// retired above and is deleted with it now. A leftover retired tree is
		// cosmetic, so its removal is best effort and off the critical path.
		_ = os.RemoveAll(retired)
	}
	return nil
}

// hiddenSibling returns a sibling of p (same parent) named "."+base+suffix, so
// the Go toolchain ignores it: cmd/go skips a directory whose name begins with
// "." or "_". The staging and retired trees are full copies of a generated
// tree; without the leading dot, `go build ./...`, `go vet ./...` and gopls in
// the user's exported project (D10) would compile them — most often the previous
// generation, which is being replaced precisely because it no longer matches the
// spec. The dot keeps them out of the build while leaving them visible to `ls -a`
// and to an operator recovering from a failed materialization.
func hiddenSibling(p, suffix string) string {
	return filepath.Join(filepath.Dir(p), "."+filepath.Base(p)+suffix)
}

// underRoot reports whether the slash path p lies within root, and if so its
// path relative to root.
func underRoot(p, root string) (rel string, ok bool) {
	if p == root {
		return "", true
	}
	if strings.HasPrefix(p, root+"/") {
		return p[len(root)+1:], true
	}
	return "", false
}

// checkGeneratedPath rejects a path that is not a clean, relative, slash path
// under one of the generated roots. This is the guard that keeps generated
// output from landing in user-owned (internal/extensions) or out-of-tree
// locations, whether by a generator bug or a crafted path (§18): a "..", an
// absolute path, a backslash (which the slash-only path package treats as an
// ordinary rune but filepath.Join on Windows would resolve as a separator into
// user territory), or a non-canonical path that cleans to somewhere else are all
// refused.
func checkGeneratedPath(p string) error {
	if p == "" {
		return fmt.Errorf("compiler: empty generated file path")
	}
	if strings.ContainsRune(p, '\\') {
		return fmt.Errorf("compiler: generated file path %q contains a backslash; paths are slash-separated", p)
	}
	if path.IsAbs(p) || path.Clean(p) != p {
		return fmt.Errorf("compiler: generated file path %q is not a clean relative path", p)
	}
	for _, root := range generatedRoots {
		if p == root || strings.HasPrefix(p, root+"/") {
			return nil
		}
	}
	return fmt.Errorf("compiler: generated file path %q is outside the generated roots %v (Forge never writes user-owned or out-of-tree files)", p, generatedRoots)
}
