package compiler

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
)

// WriteTarGz writes a deterministic gzip-compressed tar of files to w — the
// archive form Gombit Cloud's build API ingests (a `.tar.gz` of an ordinary
// Gombit application, gombit-cloud §48). It mirrors WriteZip: entries are written
// in the given order (Collect / BuildApplicationSource already sort them) with a
// fixed mode (executable → 0755, else 0644), a fixed timestamp, and no owner, so
// the same SourceFile collection yields a byte-identical archive (the determinism
// contract). Paths are forward-slash and repo-relative, so Cloud's ingester
// extracts an ordinary project tree.
//
// The GNU tar format is used so a long generated path (e.g. deep under
// internal/forge_generated/**) is stored losslessly and deterministically; the
// gzip header carries no name and a zero mtime, so it too is byte-stable.
func WriteTarGz(files []SourceFile, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		mode := int64(0o644)
		if f.Executable {
			mode = 0o755
		}
		hdr := &tar.Header{
			Name:     f.Path,
			Mode:     mode,
			Size:     int64(len(f.Content)),
			Typeflag: tar.TypeReg,
			ModTime:  exportModTime,
			Format:   tar.FormatGNU,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return fmt.Errorf("export: tar header %s: %w", f.Path, err)
		}
		if _, err := tw.Write(f.Content); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return fmt.Errorf("export: tar write %s: %w", f.Path, err)
		}
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return fmt.Errorf("export: finalize tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("export: finalize gzip: %w", err)
	}
	return nil
}
