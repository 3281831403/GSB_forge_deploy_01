package spec

import (
	"fmt"
	"strconv"
)

// Explicit code-symbol refactoring (ADR-001 §13-14, §55, D6).
//
// A relabel changes only the label and a storage rename only the storage_name;
// both keep the frozen code symbol, so both are ABI-neutral (§55). Changing the
// code symbol is a different, deliberate operation — it alters the generated API
// (CustomerView → ClientView), so it is ABI-breaking and must be classified
// breaking and proven compatible before it commits (§92). The operations here
// perform that spec+ledger change; the caller classifies (compiler.ClassifyEdit)
// and validates (compiler.ValidateCandidate). Forge never rewrites user
// extension code as part of a refactor (D8) — only the code_name and the ledger
// move.

// RefactorResult is a candidate produced by an explicit code-symbol refactor: a
// new spec carrying the changed symbol(s) and a new ledger with each old symbol
// tombstoned and each new one recorded live. Both are fresh values; the inputs
// are left untouched so a rejected candidate discards cleanly.
type RefactorResult struct {
	Spec   *ProjectSpec
	Ledger *Ledger
}

// RefactorCodeName performs an explicit source-symbol refactor of one entity
// (ADR-001 §13, §55, D6): it changes the resource or field identified by
// entityID to newSymbol. This is distinct from a relabel (label only) and a
// storage rename (storage_name only); it changes the generated API, so the
// caller must classify the result breaking and prove user code compatible before
// committing.
//
// It validates newSymbol the way the spec validator will (an exported Go
// identifier, not a reserved field symbol) and refuses a symbol that is not free
// in the entity's namespace — already live for another entity, or tombstoned and
// therefore never reusable (§20). The old symbol is tombstoned and the new one
// recorded in a cloned ledger; the spec is cloned with the new code_name. Inputs
// are not mutated.
func RefactorCodeName(s *ProjectSpec, ledger *Ledger, entityID ID, newSymbol string) (RefactorResult, error) {
	if s == nil {
		return RefactorResult{}, fmt.Errorf("spec: RefactorCodeName needs a spec")
	}
	if ledger == nil {
		return RefactorResult{}, fmt.Errorf("spec: RefactorCodeName needs a ledger")
	}

	loc, err := locateSymbol(s, entityID)
	if err != nil {
		return RefactorResult{}, err
	}
	if err := validateNewSymbol(loc, newSymbol, ledger); err != nil {
		return RefactorResult{}, err
	}

	candidate, err := s.Clone()
	if err != nil {
		return RefactorResult{}, err
	}
	if err := setCodeName(candidate, entityID, newSymbol); err != nil {
		return RefactorResult{}, err
	}
	cLedger := ledger.Clone()
	refreeze(cLedger, loc.namespace, loc.current, newSymbol, entityID)

	return RefactorResult{Spec: candidate, Ledger: cLedger}, nil
}

// NormalizeIdentifiers refactors every resource and field whose frozen code
// symbol has drifted from its current label toward the symbol the label would
// mint (ADR-001 §14) — the batch form of RefactorCodeName. Frozen symbols can
// leave creation-time terminology in the generated API (label "Client", symbol
// "Customer"); this is the explicit operation that reconciles them.
//
// It is MANUAL and never automatic: nothing in the compiler pipeline calls it,
// and it must never run merely because a project is compiled or exported (§14).
// Because it changes code symbols it is ABI-breaking exactly like a single
// refactor, and its result requires candidate validation before it commits.
//
// Entities are processed in authored order; each entity's target symbol is
// disambiguated against the evolving ledger, so two labels that fold to the same
// base get distinct symbols deterministically. An entity whose label cannot be
// normalized, or whose target cannot be freed, keeps its current symbol.
//
// "Toward the label" is best effort, bounded by §20: it cannot reach the exact
// label-minted symbol when that symbol is tombstoned or held live by another
// entity, so it settles on the first free disambiguated variant instead. Two
// entities swapping symbols therefore degrade to numbered variants
// (Customer2/Client2) rather than trading names, because neither retired symbol
// may be reused. This is deterministic and order-dependent by construction.
func NormalizeIdentifiers(s *ProjectSpec, ledger *Ledger) (RefactorResult, error) {
	if s == nil {
		return RefactorResult{}, fmt.Errorf("spec: NormalizeIdentifiers needs a spec")
	}
	if ledger == nil {
		return RefactorResult{}, fmt.Errorf("spec: NormalizeIdentifiers needs a ledger")
	}

	candidate, err := s.Clone()
	if err != nil {
		return RefactorResult{}, err
	}
	cLedger := ledger.Clone()

	for _, resource := range candidate.Resources {
		if resource == nil {
			continue
		}
		target := normalizedSymbol(cLedger, NamespaceResource, resource.Label, resource.ID, nil, resource.CodeName)
		if target != resource.CodeName {
			refreeze(cLedger, NamespaceResource, resource.CodeName, target, resource.ID)
			resource.CodeName = target
		}
		ns := FieldNamespace(resource.ID)
		for _, field := range resource.Fields {
			if field == nil {
				continue
			}
			targetField := normalizedSymbol(cLedger, ns, field.Label, field.ID, IsReservedCodeName, field.CodeName)
			if targetField != field.CodeName {
				refreeze(cLedger, ns, field.CodeName, targetField, field.ID)
				field.CodeName = targetField
			}
		}
	}

	return RefactorResult{Spec: candidate, Ledger: cLedger}, nil
}

// symbolLocation is where a refactorable code symbol lives: its namespace, the
// entity's current symbol, and the reserved-symbol predicate for that namespace.
type symbolLocation struct {
	namespace Namespace
	current   string
	reserved  func(string) bool
}

// locateSymbol finds the namespace and current code symbol for a resource or
// field. Only resources and fields carry refactorable source symbols; anything
// else is rejected.
func locateSymbol(s *ProjectSpec, entityID ID) (symbolLocation, error) {
	switch entityID.Kind() {
	case KindResource:
		r := s.FindResource(entityID)
		if r == nil {
			return symbolLocation{}, fmt.Errorf("spec: no resource %s to refactor", entityID)
		}
		// Resource type names reserve nothing beyond being exported and unique;
		// uniqueness is enforced through the ledger's free check. Legality beyond
		// exported+free (e.g. a collision with a generator-owned package-level
		// symbol) is validated downstream by ClassifyEdit/spec.Validate/
		// ValidateCandidate on the returned candidate — so a nil error from
		// RefactorCodeName means "accepted as a candidate", not "fully legal to
		// commit".
		return symbolLocation{namespace: NamespaceResource, current: r.CodeName}, nil
	case KindField:
		for _, r := range s.Resources {
			if r == nil {
				continue
			}
			if f := r.FindField(entityID); f != nil {
				return symbolLocation{
					namespace: FieldNamespace(r.ID),
					current:   f.CodeName,
					reserved:  IsReservedCodeName,
				}, nil
			}
		}
		return symbolLocation{}, fmt.Errorf("spec: no field %s to refactor", entityID)
	default:
		return symbolLocation{}, fmt.Errorf("spec: %s (kind %q) has no refactorable code symbol", entityID, entityID.Kind())
	}
}

func validateNewSymbol(loc symbolLocation, newSymbol string, ledger *Ledger) error {
	if newSymbol == loc.current {
		return fmt.Errorf("spec: refactor to %q is a no-op (symbol unchanged)", newSymbol)
	}
	if !IsExportedGoIdent(newSymbol) {
		return fmt.Errorf("spec: %q is not an exported Go identifier", newSymbol)
	}
	if loc.reserved != nil && loc.reserved(newSymbol) {
		return fmt.Errorf("spec: %q is a reserved symbol in %q", newSymbol, loc.namespace)
	}
	if !ledger.IsFree(loc.namespace, newSymbol) {
		return fmt.Errorf("spec: symbol %q is not available in %q (already used or tombstoned — symbols are never reused)", newSymbol, loc.namespace)
	}
	return nil
}

// setCodeName sets the code_name of the resource or field with entityID in s.
func setCodeName(s *ProjectSpec, entityID ID, symbol string) error {
	if r := s.FindResource(entityID); r != nil {
		r.CodeName = symbol
		return nil
	}
	for _, r := range s.Resources {
		if r == nil {
			continue
		}
		if f := r.FindField(entityID); f != nil {
			f.CodeName = symbol
			return nil
		}
	}
	return fmt.Errorf("spec: no entity %s in candidate", entityID)
}

// refreeze retires the old symbol (if live) and records the new one against the
// entity, the ledger half of a code-symbol refactor (§13). It leaves a
// not-currently-live old symbol alone: a ledger that never recorded it (drift, a
// spec authored without minting) is made consistent going forward rather than
// erroring.
func refreeze(l *Ledger, ns Namespace, oldSymbol, newSymbol string, entityID ID) {
	if oldSymbol != "" && l.IsLive(ns, oldSymbol) {
		_ = l.Tombstone(ns, oldSymbol)
	}
	// Record can only fail if the symbol is already recorded; callers reach here
	// only after an IsFree check (RefactorCodeName) or a normalizedSymbol search
	// that returns a free symbol, so a failure would be a logic error — but if it
	// somehow occurs the ledger simply keeps its prior entry, never overwriting.
	_ = l.Record(ns, newSymbol, entityID)
}

// normalizedSymbol computes the symbol label would mint in ns, disambiguated
// against the ledger and the reserved set, and returns current unchanged when it
// is already normalized or no free target can be found. It mirrors Mint's
// selection but, unlike Mint, is not idempotent-per-entity: it is used precisely
// to move an entity off its creation-time symbol.
func normalizedSymbol(l *Ledger, ns Namespace, label string, entityID ID, reserved func(string) bool, current string) string {
	base, ok := Normalize(label)
	if !ok {
		base = kindFallback[entityID.Kind()]
	}
	if base == "" {
		return current
	}
	for n := 1; n <= maxMintAttempts; n++ {
		candidate := base
		if n > 1 {
			candidate = base + strconv.Itoa(n)
		}
		if candidate == current {
			// The current symbol is already this (numbered) normalization: no drift.
			return current
		}
		if !l.IsFree(ns, candidate) {
			continue
		}
		if reserved != nil && reserved(candidate) {
			continue
		}
		return candidate
	}
	return current
}
