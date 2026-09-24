package spec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// canonicalIndent is the fixed indentation of canonical JSON. It is part of
// the on-disk contract: changing it would rewrite every stored spec.
const canonicalIndent = "  "

// Marshal renders the spec as canonical JSON (DESIGN.md §7).
//
// The encoding is deterministic: struct field order fixes key order, authored
// slice order is preserved, and no timestamps or map iteration leak in. The
// same spec therefore always produces the same bytes, so storing a
// semantically unchanged spec produces no Git churn (ADR-001 §70).
//
// Authored order is preserved rather than sorted because order is meaningful:
// it drives field order in forms and entry order in navigation.
//
// Encoding is canonical over Go representation as well as over content: an
// absent collection and an empty one describe the same spec, so both encode
// as []. Without this a nil slice would encode as null and shift the lineage
// digest for a semantically identical spec (ADR-001 §60).
func Marshal(s *ProjectSpec) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("spec: marshal nil spec")
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", canonicalIndent)
	// Struct field names are fixed by the schema and never contain HTML-unsafe
	// characters; escaping them would corrupt user labels such as "R&D".
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(canonical(s)); err != nil {
		return nil, fmt.Errorf("spec: marshal: %w", err)
	}
	return buf.Bytes(), nil
}

// canonical returns a copy of s with every always-emitted collection non-nil.
//
// Collections tagged omitempty need no treatment: nil and empty both vanish
// from the output, so they are already canonical. Only the always-emitted
// ones (resources, pages, navigation, fields) can differ, and those are
// normalized here. The input is copied rather than mutated so callers never
// observe Marshal rewriting their spec.
func canonical(s *ProjectSpec) *ProjectSpec {
	clone := *s
	clone.Pages = nonNil(s.Pages)
	clone.Navigation = nonNil(s.Navigation)

	clone.Resources = make([]*Resource, len(s.Resources))
	for i, resource := range s.Resources {
		if resource == nil {
			// Invalid, but marshalling runs before validation in some paths;
			// preserve the entry so the diagnostic stays meaningful.
			continue
		}
		resourceClone := *resource
		resourceClone.Fields = nonNil(resource.Fields)
		clone.Resources[i] = &resourceClone
	}
	if s.Resources == nil {
		clone.Resources = []*Resource{}
	}

	return &clone
}

// nonNil replaces a nil slice with an empty one, leaving contents untouched.
func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// Unmarshal decodes canonical JSON into a ProjectSpec.
//
// Decoding rejects unknown fields so that a spec written by a newer compiler
// fails loudly here rather than silently losing data on the next write.
func Unmarshal(data []byte) (*ProjectSpec, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var s ProjectSpec
	if err := decoder.Decode(&s); err != nil {
		return nil, fmt.Errorf("spec: unmarshal: %w", err)
	}
	if s.SpecVersion != SpecVersion {
		return nil, fmt.Errorf(
			"spec: unsupported spec_version %d (this compiler understands %d)",
			s.SpecVersion, SpecVersion,
		)
	}
	return &s, nil
}

// Hash returns the SHA-256 of the canonical encoding.
//
// Revisions record this digest to anchor lineage (ADR-001 §60).
func Hash(s *ProjectSpec) (string, error) {
	data, err := Marshal(s)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
