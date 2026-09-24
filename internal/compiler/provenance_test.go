package compiler

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

func TestNewProvenanceFillsAllFields(t *testing.T) {
	p := NewProvenance("v0.1.5", "rev_123")

	if p.SpecVersion != spec.SpecVersion {
		t.Errorf("spec_version = %d, want %d", p.SpecVersion, spec.SpecVersion)
	}
	if p.CompilerVersion != CompilerVersion {
		t.Errorf("compiler_version = %q, want %q", p.CompilerVersion, CompilerVersion)
	}
	if p.ExtensionABIVersion != ExtensionABIVersion {
		t.Errorf("extension_abi_version = %q, want %q", p.ExtensionABIVersion, ExtensionABIVersion)
	}
	if p.GombitVersion != "v0.1.5" {
		t.Errorf("gombit_version = %q, want v0.1.5", p.GombitVersion)
	}
	if p.SourceProjectRevision != "rev_123" {
		t.Errorf("source_project_revision = %q, want rev_123", p.SourceProjectRevision)
	}
}

func TestProvenanceJSONHasEveryFieldAndInertNote(t *testing.T) {
	data, err := ProvenanceJSON(NewProvenance("v0.1.5", "rev_123"))
	if err != nil {
		t.Fatalf("provenance json: %v", err)
	}

	// Every documented provenance field is present as a JSON key.
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	for _, key := range []string{
		"spec_version", "compiler_version", "gombit_version", "extension_abi_version", "source_project_revision",
	} {
		if _, ok := m[key]; !ok {
			t.Errorf("forge.json missing field %q", key)
		}
	}
	// The inertness / no-round-trip guarantee is stated in the file itself.
	note, _ := m["note"].(string)
	if !strings.Contains(note, "Not a sync anchor") || !strings.Contains(note, "no round-trip") {
		t.Errorf("forge.json must document that it is inert with no round-trip promise; got note %q", note)
	}
}

func TestProvenanceJSONIsDeterministic(t *testing.T) {
	a, _ := ProvenanceJSON(NewProvenance("v0.1.5", "rev_123"))
	b, _ := ProvenanceJSON(NewProvenance("v0.1.5", "rev_123"))
	if !bytes.Equal(a, b) {
		t.Error("provenance JSON must be deterministic")
	}
}

func TestWriteProvenance(t *testing.T) {
	dir := t.TempDir()
	p := NewProvenance("v0.1.5", "rev_123")
	if err := WriteProvenance(dir, p); err != nil {
		t.Fatalf("write provenance: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "forge.json"))
	if err != nil {
		t.Fatalf("read forge.json: %v", err)
	}
	want, _ := ProvenanceJSON(p)
	if !bytes.Equal(got, want) {
		t.Error("WriteProvenance must write the provenance JSON to forge.json")
	}
}
