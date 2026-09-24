package compiler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// CompilerVersion is the Forge compiler version stamped into export provenance.
// Pre-alpha; bump deliberately when the generated output's shape changes.
const CompilerVersion = "0.1.0"

// ExtensionABIVersion is the version of the backend extension ABI contract
// (ADR-001 §68) stamped into export provenance.
const ExtensionABIVersion = "1"

// provenanceNote is recorded inside forge.json so a reader of the exported repo
// sees the inertness guarantee in the file itself, not only in the docs.
const provenanceNote = "Informational provenance only. Not a sync anchor: Forge does not re-import this file and makes no round-trip promise (ADR-001 §73, D11/D15)."

// Provenance is the inert build metadata written to forge.json on export
// (DESIGN.md §29; ADR-001 §32, §73). It records what produced the export —
// spec, compiler, Gombit and extension-ABI versions, and the source revision —
// for reproducibility and auditing.
//
// It is informational only. It is NOT a sync anchor: Forge never re-imports
// forge.json and makes no round-trip promise (D11/D15), so nothing here is a
// contract the exported code must uphold.
type Provenance struct {
	SpecVersion           int    `json:"spec_version"`
	CompilerVersion       string `json:"compiler_version"`
	GombitVersion         string `json:"gombit_version"`
	ExtensionABIVersion   string `json:"extension_abi_version"`
	SourceProjectRevision string `json:"source_project_revision"`
	Note                  string `json:"note"`
}

// NewProvenance builds the provenance for an export, filling the versions the
// compiler owns (spec, compiler, extension ABI) and taking the two the caller
// resolves at export time: the pinned Gombit toolchain version and the source
// project revision the export was compiled from.
func NewProvenance(gombitVersion, sourceProjectRevision string) Provenance {
	return Provenance{
		SpecVersion:           spec.SpecVersion,
		CompilerVersion:       CompilerVersion,
		GombitVersion:         gombitVersion,
		ExtensionABIVersion:   ExtensionABIVersion,
		SourceProjectRevision: sourceProjectRevision,
		Note:                  provenanceNote,
	}
}

// ProvenanceJSON renders the provenance as pretty-printed JSON with a trailing
// newline. Struct field order is fixed, so the same provenance renders
// byte-identically (the determinism contract).
func ProvenanceJSON(p Provenance) ([]byte, error) {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("provenance: %w", err)
	}
	return append(b, '\n'), nil
}

// WriteProvenance writes forge.json to the export root. Like the README it is a
// single root-level file, written directly rather than through Materialize.
func WriteProvenance(dir string, p Provenance) error {
	b, err := ProvenanceJSON(p)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "forge.json"), b, 0o644)
}
