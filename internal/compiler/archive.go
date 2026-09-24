package compiler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// ArchiveRoot is the directory orphaned extension code is moved into on resource
// deletion (ADR-001 §46). Its leading dot is load-bearing: cmd/go skips a
// directory whose name begins with "." or "_", so archived source sits outside
// the active build graph and `go build ./...` can never hit its now-dangling
// imports (§48, §80, §96).
const ArchiveRoot = ".forge/orphaned"

// ManifestName is the per-archive metadata file associating each archived tree
// with its original resource stable ID (§47).
const ManifestName = "manifest.json"

// ArchivedExtension records one resource's extension code that was moved out of
// the active tree.
type ArchivedExtension struct {
	// ResourceID is the deleted resource's original stable ID — the durable
	// association §47 requires, kept because a relabel or a code rename must not
	// lose track of which archived code belonged to what.
	ResourceID spec.ID `json:"resource_id"`
	CodeName   string  `json:"code_name"`
	// OriginalPath is where the code lived under internal/extensions/**, and
	// ArchivedPath is where it now lives under .forge/orphaned/**. Both are
	// slash-separated and module-relative, so the move is recoverable by hand.
	OriginalPath string `json:"original_path"`
	ArchivedPath string `json:"archived_path"`
}

// ArchiveManifest is the metadata written beside an archived set.
type ArchiveManifest struct {
	Key        string              `json:"key"`
	Extensions []ArchivedExtension `json:"extensions"`
}

// ArchiveExtensions moves the user extension code for each deleted resource out
// of internal/extensions/** into .forge/orphaned/<key>/** and records a manifest
// (ADR-001 §46-48, D13).
//
// It is called after a deletion is approved and its dependencies are resolved
// (the caller's responsibility — see AnalyzeDeletions); it does not re-check
// blockers. key is the caller's archive key, typically a revision ID (§46).
//
// The three invariants it holds:
//
//   - It moves rather than copies. Leaving the source under internal/extensions
//     while its generated dependency is gone is the in-place orphaning §48
//     forbids, because `go build ./...` would then fail on a dangling import. The
//     destination is always beneath ArchiveRoot, which is dot-prefixed and thus
//     outside the build graph, so in-place orphaning is structurally impossible
//     (§48, §96).
//   - It never rewrites the archived bytes (§47): the move preserves them, and
//     the copy fallback for a cross-device rename copies verbatim.
//   - It never silently deletes custom source (§47): it refuses to overwrite an
//     existing archive destination, and the manifest keeps every association.
//
// A deleted resource with no extension directory is skipped — there is nothing
// to archive. Results are in the order of the deleted slice.
func ArchiveExtensions(dir, key string, deleted []DeletedResource) ([]ArchivedExtension, error) {
	if dir == "" {
		return nil, fmt.Errorf("compiler: ArchiveExtensions needs a project directory")
	}
	if err := checkArchiveKey(key); err != nil {
		return nil, err
	}

	var archived []ArchivedExtension
	for _, d := range deleted {
		srcRel := gen.ExtensionPackageDirForCodeName(d.CodeName)
		srcAbs := filepath.Join(dir, filepath.FromSlash(srcRel))

		info, err := os.Stat(srcAbs)
		if errors.Is(err, fs.ErrNotExist) {
			continue // resource had no hand-written extension code
		}
		if err != nil {
			return archived, fmt.Errorf("compiler: %s: %w", srcRel, err)
		}
		if !info.IsDir() {
			return archived, fmt.Errorf("compiler: extension path %s is not a directory", srcRel)
		}

		destRel := path.Join(ArchiveRoot, key, path.Base(srcRel))
		destAbs := filepath.Join(dir, filepath.FromSlash(destRel))
		if _, err := os.Stat(destAbs); err == nil {
			return archived, fmt.Errorf("compiler: archive destination %s already exists; refusing to overwrite archived source", destRel)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return archived, fmt.Errorf("compiler: %s: %w", destRel, err)
		}

		if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
			return archived, fmt.Errorf("compiler: %s: %w", destRel, err)
		}
		if err := moveDir(srcAbs, destAbs); err != nil {
			return archived, fmt.Errorf("compiler: archiving %s: %w", srcRel, err)
		}
		entry := ArchivedExtension{
			ResourceID:   d.ID,
			CodeName:     d.CodeName,
			OriginalPath: srcRel,
			ArchivedPath: destRel,
		}
		// Record the association the instant the source moves, not after the whole
		// batch. The source is now gone from internal/extensions and a retry would
		// skip it (nothing left to move), so if a later resource in the batch fails
		// and we returned before writing, this move's association would be lost for
		// good — the durable record §47 promises. Persisting per move keeps the
		// manifest consistent with what is actually on disk however the batch ends.
		if err := writeManifest(dir, key, []ArchivedExtension{entry}); err != nil {
			return archived, err
		}
		archived = append(archived, entry)
	}

	return archived, nil
}

// checkArchiveKey rejects a key that is not a single, safe path element, so a
// crafted key cannot escape ArchiveRoot into the active tree or out of the
// project.
func checkArchiveKey(key string) error {
	if key == "" {
		return fmt.Errorf("compiler: ArchiveExtensions needs an archive key")
	}
	if strings.ContainsAny(key, `/\`) {
		return fmt.Errorf("compiler: archive key %q must be a single path element", key)
	}
	if key == "." || key == ".." {
		return fmt.Errorf("compiler: archive key %q is not a valid path element", key)
	}
	return nil
}

// writeManifest writes (or extends) the archive manifest for key. It merges into
// an existing manifest rather than overwriting, so archiving into the same key
// twice never drops an earlier association (§47).
func writeManifest(dir, key string, newly []ArchivedExtension) error {
	manifestRel := path.Join(ArchiveRoot, key, ManifestName)
	manifestAbs := filepath.Join(dir, filepath.FromSlash(manifestRel))

	manifest := ArchiveManifest{Key: key}
	if existing, err := os.ReadFile(manifestAbs); err == nil {
		if err := json.Unmarshal(existing, &manifest); err != nil {
			return fmt.Errorf("compiler: reading existing archive manifest %s: %w", manifestRel, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("compiler: %s: %w", manifestRel, err)
	}
	manifest.Key = key
	manifest.Extensions = append(manifest.Extensions, newly...)

	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("compiler: encoding archive manifest: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(manifestAbs, encoded, 0o644); err != nil {
		return fmt.Errorf("compiler: writing archive manifest %s: %w", manifestRel, err)
	}
	return nil
}

// moveDir moves src to dst. It prefers an atomic rename and falls back to a
// verbatim recursive copy plus removal when the rename crosses a device
// boundary, so the archived bytes are always identical to the originals.
func moveDir(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyDirVerbatim(src, dst); err != nil {
		// A half-written destination would trip the "refuse to overwrite" guard on
		// the next attempt and wedge every retry. The source is untouched (copy,
		// not move), so discarding the partial copy is safe and lets a retry
		// proceed. Best effort: report the copy error, not a cleanup error.
		_ = os.RemoveAll(dst)
		return err
	}
	return os.RemoveAll(src)
}

// copyDirVerbatim copies the whole tree at src to dst, preserving every entry,
// file mode and symlink — unlike copyTree (used for the candidate typecheck), it
// skips nothing and dereferences nothing, because an archive must be a faithful,
// recoverable copy.
func copyDirVerbatim(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// Preserve the link itself rather than copying its target's bytes, so
			// "verbatim" holds across a device boundary too.
			linkTarget, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		return copyFile(p, target, d)
	})
}
