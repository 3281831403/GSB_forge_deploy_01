package spec

import (
	"encoding/json"
	"fmt"
	"sort"
)

// SymbolStatus is the state of a generated source symbol within a namespace.
type SymbolStatus string

const (
	// SymbolLive means the symbol is currently emitted for its owning entity.
	SymbolLive SymbolStatus = "live"
	// SymbolTombstoned means the symbol was minted, then retired when its entity
	// was deleted or renamed. It is retained, never reused: reviving the name for
	// a different entity would silently change a frozen source ABI (ADR-001 §20).
	SymbolTombstoned SymbolStatus = "tombstoned"
)

// Namespace identifies a generated namespace within which source symbols must be
// unique (ADR-001 §7). Uniqueness is per namespace, not global: two fields in
// different resources may both be Email, because each resource's accessors are a
// separate namespace; two fields in the same resource may not.
//
// The known namespaces are constructed through the helpers below so their keys
// have one spelling. The resource-type namespace is a constant; per-resource
// namespaces are keyed by the owning resource's stable ID (never its name, which
// can change — ADR-001 §4). §7's lifecycle-hook namespace has no helper yet: it
// arrives with the extension ABI work (#18+), not by callers hand-spelling a
// "hooks:"+id key.
type Namespace string

// NamespaceResource is the namespace of generated resource type names.
const NamespaceResource Namespace = "resource"

// FieldNamespace is the field/accessor namespace of one resource, keyed by the
// resource's stable ID so a resource relabel cannot move the namespace.
func FieldNamespace(resource ID) Namespace {
	return Namespace("fields:" + string(resource))
}

// Sentinel errors from the ledger's mutators, so callers (minting, #18) can
// distinguish a collision from other failures.
var (
	// ErrSymbolTaken means a symbol is already recorded in the namespace (live
	// or tombstoned) and therefore cannot be minted. Minting disambiguates to a
	// free symbol before recording; this is the guard that a bug did not skip it.
	ErrSymbolTaken = fmt.Errorf("spec: symbol already recorded in namespace")
	// ErrSymbolNotLive means Tombstone was asked to retire a symbol that is not
	// currently live (free, or already tombstoned).
	ErrSymbolNotLive = fmt.Errorf("spec: symbol is not live")
)

// entry is one symbol's record: its status and the entity that owns it.
type entry struct {
	Status   SymbolStatus `json:"status"`
	EntityID ID           `json:"entity_id"`
}

// Ledger is the source-symbol history, per namespace (ADR-001 §11). For each
// namespace it holds a map from a minted symbol to its status and owning entity.
// A symbol is added once and never removed: deleting an entity tombstones its
// symbol so the name is never silently recycled (§20). This retained history is
// what §70 requires persisted with the project and what makes symbol allocation
// reconstructible across revisions.
//
// The zero value is usable: the query and marshal paths treat a nil map as
// empty, and Record lazily initializes it, so a Ledger embedded by value in a
// larger struct works without a constructor. NewLedger and UnmarshalJSON are
// conveniences that produce the same thing. The map is unexported so the
// no-recycle and no-silent-overwrite invariants hold through the methods rather
// than being a caller's responsibility.
type Ledger struct {
	namespaces map[Namespace]map[string]entry
}

// NewLedger returns an empty ledger. The zero Ledger is equally usable; this is
// a convenience for a caller that prefers an explicit constructor.
func NewLedger() *Ledger {
	return &Ledger{namespaces: map[Namespace]map[string]entry{}}
}

// Clone returns a deep copy of the ledger, so a candidate operation (a
// code-symbol refactor, §13) can update a copy and leave the accepted ledger
// untouched until the candidate is committed. A nil receiver clones to an empty
// ledger.
func (l *Ledger) Clone() *Ledger {
	if l == nil || l.namespaces == nil {
		return NewLedger()
	}
	namespaces := make(map[Namespace]map[string]entry, len(l.namespaces))
	for ns, table := range l.namespaces {
		copied := make(map[string]entry, len(table))
		for symbol, e := range table {
			copied[symbol] = e
		}
		namespaces[ns] = copied
	}
	return &Ledger{namespaces: namespaces}
}

// Status reports a symbol's status and whether it is recorded at all. A symbol
// that is not recorded is free — available to mint. Callers testing for
// availability should prefer IsFree, which names the intent.
func (l *Ledger) Status(ns Namespace, symbol string) (status SymbolStatus, recorded bool) {
	e, ok := l.namespaces[ns][symbol]
	if !ok {
		return "", false
	}
	return e.Status, true
}

// IsFree reports whether a symbol may be minted in a namespace: it is free only
// if it has never been recorded there. A tombstoned symbol is not free — it is
// retained precisely so it is not handed out again.
func (l *Ledger) IsFree(ns Namespace, symbol string) bool {
	_, recorded := l.Status(ns, symbol)
	return !recorded
}

// IsLive reports whether a symbol is currently emitted in a namespace.
func (l *Ledger) IsLive(ns Namespace, symbol string) bool {
	s, recorded := l.Status(ns, symbol)
	return recorded && s == SymbolLive
}

// IsTombstoned reports whether a symbol is recorded but retired in a namespace.
func (l *Ledger) IsTombstoned(ns Namespace, symbol string) bool {
	s, recorded := l.Status(ns, symbol)
	return recorded && s == SymbolTombstoned
}

// OwnerOf returns the stable ID of the entity that owns a symbol, and whether
// the symbol is recorded. Useful for lineage: a tombstoned symbol still names
// the entity it belonged to.
func (l *Ledger) OwnerOf(ns Namespace, symbol string) (ID, bool) {
	e, ok := l.namespaces[ns][symbol]
	if !ok {
		return "", false
	}
	return e.EntityID, true
}

// Record adds a new live symbol owned by entity. It returns ErrSymbolTaken if
// the symbol is already recorded in the namespace — live or tombstoned — because
// a symbol is minted once and never reused (§20). Minting (#18) is responsible
// for disambiguating to a free symbol first; Record refuses to be the place a
// collision or a recycled tombstone slips through.
func (l *Ledger) Record(ns Namespace, symbol string, entity ID) error {
	if _, taken := l.namespaces[ns][symbol]; taken {
		return fmt.Errorf("%w: %q in %q", ErrSymbolTaken, symbol, ns)
	}
	if l.namespaces == nil {
		l.namespaces = map[Namespace]map[string]entry{}
	}
	table := l.namespaces[ns]
	if table == nil {
		table = map[string]entry{}
		l.namespaces[ns] = table
	}
	table[symbol] = entry{Status: SymbolLive, EntityID: entity}
	return nil
}

// Tombstone retires a live symbol — its entity was deleted or its code name was
// refactored. It returns ErrSymbolNotLive if the symbol is not currently live
// (free, or already tombstoned). The owning entity ID is retained so history
// stays reconstructible.
func (l *Ledger) Tombstone(ns Namespace, symbol string) error {
	e, ok := l.namespaces[ns][symbol]
	if !ok || e.Status != SymbolLive {
		return fmt.Errorf("%w: %q in %q", ErrSymbolNotLive, symbol, ns)
	}
	l.retire(ns, symbol, e)
	return nil
}

// retire flips a live entry to tombstoned in place, preserving its owner: §11
// keeps entity_id on a tombstoned symbol so the allocation history stays
// reconstructible (§70), and UnmarshalJSON refuses a ledger missing it. Both
// tombstoning paths (Tombstone and TombstoneEntity) go through here, so the
// transition has exactly one implementation and cannot drift into dropping the
// owner. Callers establish liveness first; retire does not re-check.
func (l *Ledger) retire(ns Namespace, symbol string, e entry) {
	e.Status = SymbolTombstoned
	l.namespaces[ns][symbol] = e
}

// MarshalJSON renders the ledger as the §11 structure. encoding/json emits map
// keys in sorted order, so the output is deterministic and Git-friendly: the
// same history serializes to the same bytes, and an added symbol changes one
// line rather than reshuffling the file. Use json.MarshalIndent for the on-disk
// form. A nil map (the zero Ledger) marshals as {}, not null, so a persisted
// project file never carries a surprising null ledger.
func (l *Ledger) MarshalJSON() ([]byte, error) {
	if l.namespaces == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(l.namespaces)
}

// UnmarshalJSON reconstructs a ledger from its persisted form, rejecting an
// unknown status so a corrupt or hand-edited file fails loudly rather than
// loading a symbol in an undefined state.
func (l *Ledger) UnmarshalJSON(data []byte) error {
	var raw map[Namespace]map[string]entry
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for ns, table := range raw {
		for symbol, e := range table {
			if e.Status != SymbolLive && e.Status != SymbolTombstoned {
				return fmt.Errorf("spec: ledger symbol %q in %q has unknown status %q", symbol, ns, e.Status)
			}
			if e.EntityID == "" {
				return fmt.Errorf("spec: ledger symbol %q in %q has no entity_id", symbol, ns)
			}
		}
	}
	if raw == nil {
		raw = map[Namespace]map[string]entry{}
	}
	l.namespaces = raw
	return nil
}

// Namespaces returns the namespace keys present in the ledger, sorted, so
// callers that walk the ledger (diffing, reporting) get a deterministic order
// without reaching into the unexported map.
func (l *Ledger) Namespaces() []Namespace {
	out := make([]Namespace, 0, len(l.namespaces))
	for ns := range l.namespaces {
		out = append(out, ns)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Symbols returns the symbols recorded in a namespace, sorted, whatever their
// status. With Namespaces it completes a deterministic walk of the whole
// history — Namespaces → Symbols → Status/OwnerOf reconstructs the ledger's full
// contents (§70) — without exposing the unexported map. An unknown namespace
// yields an empty slice.
func (l *Ledger) Symbols(ns Namespace) []string {
	table := l.namespaces[ns]
	out := make([]string, 0, len(table))
	for symbol := range table {
		out = append(out, symbol)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TombstonedSymbol identifies a symbol that was retired, for a caller to report
// or audit.
type TombstonedSymbol struct {
	Namespace Namespace
	Symbol    string
}

// TombstoneEntity retires every live symbol the ledger records as owned by
// entityID, across all namespaces — the operation performed when a semantic
// entity is deleted (ADR-001 §10). The symbols are retained (tombstoned, not
// removed), so a later unrelated entity can never reuse them (§75) and Mint
// skips them.
//
// It returns the symbols it retired, sorted, for reporting; the result is empty
// when the entity owned no live symbol. It is idempotent (a symbol already
// tombstoned is left as-is and not returned) and never touches a symbol owned by
// a different entity. Deleting a resource that cascades to its fields is the
// caller's loop over each deleted entity, one TombstoneEntity per entity.
func (l *Ledger) TombstoneEntity(entityID ID) []TombstonedSymbol {
	var retired []TombstonedSymbol
	for ns, table := range l.namespaces {
		for symbol, e := range table {
			if e.EntityID == entityID && e.Status == SymbolLive {
				l.retire(ns, symbol, e)
				retired = append(retired, TombstonedSymbol{Namespace: ns, Symbol: symbol})
			}
		}
	}
	sort.Slice(retired, func(i, j int) bool {
		if retired[i].Namespace != retired[j].Namespace {
			return retired[i].Namespace < retired[j].Namespace
		}
		return retired[i].Symbol < retired[j].Symbol
	})
	return retired
}
