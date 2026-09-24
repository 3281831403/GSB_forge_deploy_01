package buildworker

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/gombit-dev/gombit-forge/internal/compiler"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// SourceAssembler assembles a Cloud build's source archive from a revision's
// spec using the shared compiler path — the same BuildApplicationSource the
// GitHub export uses — then serializes it as a deterministic `.tar.gz`.
// gombitVersion is stamped into the app's provenance.
type SourceAssembler struct {
	tc            compiler.SourceToolchain
	gombitVersion string
}

// NewSourceAssembler wires the assembler over a source toolchain (the real one
// is compiler.GombitToolchain{CLI: &gombit.CLI{}}).
func NewSourceAssembler(tc compiler.SourceToolchain, gombitVersion string) *SourceAssembler {
	return &SourceAssembler{tc: tc, gombitVersion: gombitVersion}
}

// AssembleTarGz compiles sp to a full Gombit application source tree and writes
// it as a deterministic `.tar.gz` to w. The Go module path is synthesized from
// the Cloud project id: Cloud compiles the app standalone, so the path only has
// to be valid and internally consistent, not a resolvable repository.
func (a *SourceAssembler) AssembleTarGz(ctx context.Context, sp *spec.ProjectSpec, cloudProjectID, revisionRef string, w io.Writer) error {
	files, err := compiler.BuildApplicationSource(ctx, a.tc, compiler.ApplicationSourceRequest{
		Spec:       sp,
		Module:     moduleForCloudProject(cloudProjectID),
		Provenance: compiler.NewProvenance(a.gombitVersion, revisionRef),
	})
	if err != nil {
		return fmt.Errorf("assemble source: %w", err)
	}
	return compiler.WriteTarGz(files, w)
}

// moduleForCloudProject builds a valid Go module path for a Cloud build from the
// Cloud project id. The build is compiled in isolation by Cloud, so this only
// needs to be a stable, valid path, not a real (fetchable) repository.
func moduleForCloudProject(cloudProjectID string) string {
	elem := sanitizeModuleElem(cloudProjectID)
	if elem == "" {
		elem = "app"
	}
	return "gombit.build/" + elem
}

// sanitizeModuleElem keeps only characters valid in a Go module path element, so
// an unexpected id can't yield an invalid module path.
func sanitizeModuleElem(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '.', r == '_', r == '~':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}
