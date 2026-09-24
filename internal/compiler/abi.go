package compiler

import (
	"fmt"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Fingerprint is the extension_abi_sha256 of a spec's backend extension surface
// (ADR-001 §39): the stable digest user extension code binds to. Two specs that
// differ only in labels, storage names, page/navigation order or other
// presentation carry the same fingerprint; a source-symbol rename or an
// extension-visible type change changes it.
//
// The spec must be semantically valid — Fingerprint builds the domain graph,
// which refuses an invalid spec — so a fingerprint is only defined for a spec
// that could compile.
func Fingerprint(s *spec.ProjectSpec) (string, error) {
	abi, err := extensionABI(s)
	if err != nil {
		return "", err
	}
	return abi.Sum()
}

// ClassifyEdit classifies a candidate spec transition against the current spec
// (ADR-001 §37-41), the ABI-diff step of the candidate mutation pipeline.
//
// Both specs must already be semantically valid (the pipeline validates before
// it reaches ABI classification, §37); ClassifyEdit builds each graph and
// returns an error if either is invalid. The result says whether the candidate
// is ABI-neutral (may commit without user code compiling, §38), additive
// (compatible new surface, §40) or breaking (needs candidate compatibility
// validation before it can become current, §41).
func ClassifyEdit(current, candidate *spec.ProjectSpec) (gen.Transition, error) {
	currentABI, err := extensionABI(current)
	if err != nil {
		return gen.Transition{}, fmt.Errorf("compiler: current spec: %w", err)
	}
	candidateABI, err := extensionABI(candidate)
	if err != nil {
		return gen.Transition{}, fmt.Errorf("compiler: candidate spec: %w", err)
	}
	return gen.ClassifyTransition(currentABI, candidateABI)
}

func extensionABI(s *spec.ProjectSpec) (gen.ABI, error) {
	g, err := graph.Build(s)
	if err != nil {
		return gen.ABI{}, fmt.Errorf("compiler: %w", err)
	}
	return gen.ExtensionABI(g)
}
