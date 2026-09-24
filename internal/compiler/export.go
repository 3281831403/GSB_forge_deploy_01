package compiler

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// exportModTime is the fixed timestamp stamped on every archive entry so the
// same source tree exports byte-identically (the determinism contract). Its
// value is arbitrary but fixed; the exported files are ordinary source, and a
// consumer that cares about real mtimes reads them from the checkout, not the
// archive.
var exportModTime = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// exportSkipDirs are directory names never included in an export: version-control
// metadata and dependency/build output. Everything else under the project —
// scaffold, generated code and user extensions alike — is source the exporter
// hands over whole (ADR-001 §72, "own the code"). node_modules and dist are
// reproducible from source and would bloat the archive; .git is history, not
// source.
var exportSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
}

// exportSkipFiles are regular files never included in an export because they
// carry secrets, not source. A `gombit new` scaffold writes a random
// GOMBIT_JWT_SECRET into .env (AGENTS.md's non-reproducibility hazard), so
// shipping it inside a "here's your source" archive would hand over a live
// session-signing secret; the candidate workspace copy (#119 copyTree) excludes
// .env for the same reason. A blanked .env.example template is fine and is kept.
var exportSkipFiles = map[string]bool{
	".env":       true,
	".env.local": true,
}

// Export writes a deterministic ZIP of the Gombit project rooted at root to w
// (DESIGN.md §4.9, §28; ADR-001 §72). It archives the full repository tree —
// cmd/, internal/ (including internal/extensions/ user code), frontend/,
// database/, config/, docs/, gombit.yaml, go.mod, README.md — so the export is
// an ordinary Gombit repository with no Forge runtime dependency and no
// obligation to call Forge (P2, D10/D11). VCS metadata and dependency/build
// output are excluded (exportSkipDirs); every other file is included verbatim.
//
// Determinism: entries are written in sorted path order with a fixed timestamp,
// so the same tree yields a byte-identical archive. Paths use forward slashes.
// SourceFile is one file in an exported application's source tree: a
// forward-slash repo-relative path, its content, and whether it is executable.
// It is the shared unit every export target consumes — the ZIP archive and the
// GitHub push alike — so sanitization (source vs secrets/artifacts) and mode
// are decided once, in Collect, rather than per target.
type SourceFile struct {
	Path       string
	Content    []byte
	Executable bool
}

// Collect walks the materialized project at root and returns its sanitized
// source files in sorted path order (DESIGN.md §28; ADR-001 §72): scaffold,
// generated code and user extensions, minus VCS metadata and dependency/build
// output (exportSkipDirs) and secret files (exportSkipFiles). Symlinks are
// skipped (an export is source, and a link may point outside the tree). Content
// is read into memory — a project source tree is small — so the same collection
// feeds any export target. This is the single place export sanitization lives.
func Collect(root string) ([]SourceFile, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("export: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("export: %s is not a directory", root)
	}

	var files []SourceFile
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == root {
			return nil
		}
		if d.IsDir() {
			if exportSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if exportSkipFiles[d.Name()] {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("export: read %s: %w", rel, err)
		}
		files = append(files, SourceFile{
			Path:       filepath.ToSlash(rel),
			Content:    content,
			Executable: info.Mode()&0o111 != 0,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("export: walk %s: %w", root, err)
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// WriteZip writes the source files as a deterministic ZIP to w: entries in the
// given (sorted) order, each with a fixed timestamp and a normalized mode
// (executable → 0755, else 0644), so the same collection yields a byte-identical
// archive.
func WriteZip(files []SourceFile, w io.Writer) error {
	zw := zip.NewWriter(w)
	for _, f := range files {
		perm := fs.FileMode(0o644)
		if f.Executable {
			perm = 0o755
		}
		header := &zip.FileHeader{Name: f.Path, Method: zip.Deflate}
		header.SetMode(perm)
		header.Modified = exportModTime

		fw, err := zw.CreateHeader(header)
		if err != nil {
			_ = zw.Close()
			return fmt.Errorf("export: create entry %s: %w", f.Path, err)
		}
		if _, err := fw.Write(f.Content); err != nil {
			_ = zw.Close()
			return fmt.Errorf("export: write %s: %w", f.Path, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("export: finalize archive: %w", err)
	}
	return nil
}

// Export zips the sanitized source tree at root to w — the ZIP export target
// (DESIGN.md §4.9). It is Collect followed by WriteZip, so the ZIP and the
// GitHub push share the exact same sanitized SourceFile collection.
func Export(root string, w io.Writer) error {
	files, err := Collect(root)
	if err != nil {
		return err
	}
	return WriteZip(files, w)
}
