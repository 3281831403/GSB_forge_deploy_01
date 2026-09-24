package spec

import (
	"errors"
	"testing"
)

// TestDeletedSymbolCannotBeReused is #20's whole point and ADR-001 §75's
// rejected alternative: after fld_A's Email is deleted (tombstoned), the symbol
// cannot be handed to a new, unrelated field — so historical and archived source
// can never make two unrelated fields look identical. This is enforced at the
// ledger (Record refuses a tombstoned symbol); minting builds on it by skipping
// tombstoned candidates, which the minting tests cover.
func TestDeletedSymbolCannotBeReused(t *testing.T) {
	l := NewLedger()
	ns := FieldNamespace("res_A")
	if err := l.Record(ns, "Email", "fld_A"); err != nil {
		t.Fatal(err)
	}

	// Delete fld_A: its symbol is tombstoned, not removed.
	retired := l.TombstoneEntity("fld_A")
	if len(retired) != 1 || retired[0].Symbol != "Email" || retired[0].Namespace != ns {
		t.Fatalf("TombstoneEntity(fld_A) = %+v, want [{%q Email}]", retired, ns)
	}
	if !l.IsTombstoned(ns, "Email") || l.IsLive(ns, "Email") || l.IsFree(ns, "Email") {
		t.Error("after deletion Email must be tombstoned, not live, not free")
	}
	// Lineage survives deletion: §11 keeps entity_id on a tombstoned entry, so
	// history stays reconstructible and UnmarshalJSON can load the saved ledger.
	if owner, ok := l.OwnerOf(ns, "Email"); !ok || owner != "fld_A" {
		t.Errorf("tombstoned OwnerOf = (%q,%v), want (fld_A,true)", owner, ok)
	}

	// The reuse path is impossible: a new entity cannot record Email again.
	if err := l.Record(ns, "Email", "fld_B"); !errors.Is(err, ErrSymbolTaken) {
		t.Errorf("re-recording a tombstoned symbol = %v, want ErrSymbolTaken (§75)", err)
	}
}

// TestTombstoneEntityIsScopedToTheEntity: deleting one entity must not retire
// another's symbol, even in the same namespace.
func TestTombstoneEntityIsScopedToTheEntity(t *testing.T) {
	l := NewLedger()
	ns := FieldNamespace("res_A")
	_ = l.Record(ns, "Email", "fld_A")
	_ = l.Record(ns, "Name", "fld_B")

	l.TombstoneEntity("fld_A")

	if !l.IsTombstoned(ns, "Email") {
		t.Error("fld_A's Email should be tombstoned")
	}
	if !l.IsLive(ns, "Name") {
		t.Error("fld_B's Name must be untouched when fld_A is deleted")
	}
}

// TestTombstoneEntityIsIdempotent: deleting the same entity twice retires
// nothing the second time.
func TestTombstoneEntityIsIdempotent(t *testing.T) {
	l := NewLedger()
	ns := FieldNamespace("res_A")
	_ = l.Record(ns, "Email", "fld_A")

	if got := l.TombstoneEntity("fld_A"); len(got) != 1 {
		t.Fatalf("first delete retired %d, want 1", len(got))
	}
	if got := l.TombstoneEntity("fld_A"); len(got) != 0 {
		t.Errorf("second delete retired %d, want 0 (idempotent)", len(got))
	}
}

// TestTombstoneEntityAcrossNamespaces: an entity that owns symbols in more than
// one namespace has all of them retired, returned sorted.
func TestTombstoneEntityAcrossNamespaces(t *testing.T) {
	l := NewLedger()
	// Synthetic: today only Resource and Field carry code_names, so a reachable
	// ledger holds one symbol per entity (a resource in "resource", a field in
	// its own "fields:<id>"). This records one owner in two namespaces with the
	// SAME symbol, to exercise the cross-namespace sweep and to pin the namespace
	// comparator — the symbols tie, so only the namespace decides the order. Real
	// multi-namespace ownership arrives with the hooks namespace (#18+).
	nsA := FieldNamespace("res_A")
	if err := l.Record(NamespaceResource, "Sig", "res_A"); err != nil {
		t.Fatal(err)
	}
	if err := l.Record(nsA, "Sig", "res_A"); err != nil {
		t.Fatal(err)
	}

	retired := l.TombstoneEntity("res_A")
	if len(retired) != 2 {
		t.Fatalf("retired %d symbols, want 2", len(retired))
	}
	// Symbols tie, so namespace order alone decides: "fields:res_A" < "resource".
	if retired[0].Namespace != nsA || retired[0].Symbol != "Sig" {
		t.Errorf("retired[0] = %+v, want {%q Sig}", retired[0], nsA)
	}
	if retired[1].Namespace != NamespaceResource || retired[1].Symbol != "Sig" {
		t.Errorf("retired[1] = %+v, want {resource Sig}", retired[1])
	}
}

// TestTombstoneEntityUnknownReturnsEmpty: deleting an entity the ledger never
// recorded is a no-op.
func TestTombstoneEntityUnknownReturnsEmpty(t *testing.T) {
	l := NewLedger()
	_ = l.Record(FieldNamespace("res_A"), "Email", "fld_A")
	if got := l.TombstoneEntity("fld_ZZZ"); len(got) != 0 {
		t.Errorf("deleting an unknown entity retired %d symbols, want 0", len(got))
	}
}
