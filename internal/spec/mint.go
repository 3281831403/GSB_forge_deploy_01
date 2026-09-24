package spec

import (
	"fmt"
	"strconv"
)

// maxMintAttempts bounds the disambiguation search. A namespace legitimately
// holding a thousand numbered variants of one base is already pathological; a
// reserved set or ledger that swallows a whole run of suffixes is a bug in that
// namespace, not a search to run forever. Mint fails closed at this bound (D14)
// rather than spin, since it takes no context to cancel.
const maxMintAttempts = 1000

// kindFallback is the base symbol used when a label has no usable content to
// normalize (Normalize returns ok=false — an empty, all-punctuation, or
// unrepresentable label). It is derived from the entity kind so the minted
// symbol is still meaningful and disambiguation yields Field2, Field3 rather
// than a fragment. Every Kind has an entry so a fallback always exists.
var kindFallback = map[Kind]string{
	KindProject:  "Project",
	KindResource: "Resource",
	KindField:    "Field",
	KindRelation: "Relation",
	KindPage:     "Page",
	KindAction:   "Action",
	KindHook:     "Hook",
	KindNav:      "Nav",
}

// Mint allocates a frozen source symbol for an entity in a namespace, resolving
// collisions at mint time (ADR-001 §5, §8–9). It:
//
//  1. normalizes the label to a candidate identifier — or, for a label with no
//     usable content, falls back to a symbol derived from the entity's kind;
//  2. selects the first candidate that is neither already recorded in the
//     namespace (live or tombstoned — §10, a tombstoned symbol is never reused)
//     nor reserved, appending a numeric suffix as needed: Email, Email2,
//     Email3, …;
//  3. records that symbol against entityID in the ledger and returns it.
//
// The symbol is frozen before Mint returns, so a collision never enters an
// accepted ProjectSpec and no state is created that would need a breaking
// refactor merely to compile (§9). Minting is once per entity: if the entity
// already owns a live symbol in the namespace, Mint returns it unchanged — so a
// retry (a double-submit, or a transaction replayed after a later failure)
// converges rather than allocating a second symbol, and a relabel does not
// re-mint (the code symbol is a frozen ABI, D1).
//
// reserved reports whether a candidate collides with a generated or framework
// symbol in this namespace (e.g. IsReservedCodeName for a resource's field
// namespace). It may be nil when the namespace reserves nothing. Mint bounds its
// search (maxMintAttempts) and fails closed rather than looping forever if a
// namespace's reserved set or ledger swallows a whole run of a base's suffixes.
func Mint(l *Ledger, ns Namespace, label string, entityID ID, reserved func(string) bool) (string, error) {
	if l == nil {
		return "", fmt.Errorf("spec: Mint needs a ledger")
	}

	// Idempotent: an entity that already owns a live symbol here keeps it (§5,
	// minted once). O(symbols in namespace), which today is one resource's
	// fields; an owner index can come later if a namespace ever grows large.
	for _, sym := range l.Symbols(ns) {
		if owner, ok := l.OwnerOf(ns, sym); ok && owner == entityID && l.IsLive(ns, sym) {
			return sym, nil
		}
	}

	base, ok := Normalize(label)
	if !ok {
		base = kindFallback[entityID.Kind()]
		if base == "" {
			return "", fmt.Errorf("spec: cannot mint for %q: label %q is unrepresentable and kind %q has no fallback",
				entityID, label, entityID.Kind())
		}
	}

	for n := 1; n <= maxMintAttempts; n++ {
		candidate := base
		if n > 1 {
			candidate = base + strconv.Itoa(n)
		}
		if !l.IsFree(ns, candidate) {
			continue // taken: live, or a tombstone that is never reused (§10)
		}
		if reserved != nil && reserved(candidate) {
			continue // collides with a generated/framework symbol (§12)
		}
		if err := l.Record(ns, candidate, entityID); err != nil {
			// IsFree was just true, so this should not happen; surface it rather
			// than returning a symbol the ledger did not accept.
			return "", fmt.Errorf("spec: freezing minted symbol %q: %w", candidate, err)
		}
		return candidate, nil
	}
	return "", fmt.Errorf("spec: cannot mint in %q: %q and every numbered variant through %s%d are taken or reserved",
		ns, base, base, maxMintAttempts)
}
