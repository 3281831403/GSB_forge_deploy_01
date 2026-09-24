package spec

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestMarshalIsDeterministic is the core reproducibility guarantee: the same
// spec must always produce the same bytes (DESIGN.md §9, ADR-001 §70).
func TestMarshalIsDeterministic(t *testing.T) {
	first, err := Marshal(validSpec())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Re-marshalling a freshly built but equal spec must match byte for byte.
	for i := range 20 {
		next, err := Marshal(validSpec())
		if err != nil {
			t.Fatalf("marshal %d: %v", i, err)
		}
		if !bytes.Equal(first, next) {
			t.Fatalf("marshal %d differs from first encoding:\nfirst:\n%s\ngot:\n%s", i, first, next)
		}
	}
}

// TestEmptyAndAbsentCollectionsEncodeIdentically guards the lineage anchor:
// an absent collection and an empty one describe the same spec, so they must
// not produce different digests (ADR-001 §60).
func TestEmptyAndAbsentCollectionsEncodeIdentically(t *testing.T) {
	absent := &ProjectSpec{SpecVersion: SpecVersion}
	empty := &ProjectSpec{
		SpecVersion: SpecVersion,
		Resources:   []*Resource{},
		Pages:       []*Page{},
		Navigation:  []*NavItem{},
	}

	absentJSON := mustMarshal(t, absent)
	emptyJSON := mustMarshal(t, empty)
	if !bytes.Equal(absentJSON, emptyJSON) {
		t.Errorf("absent and empty collections encode differently:\nabsent:\n%s\nempty:\n%s",
			absentJSON, emptyJSON)
	}

	absentHash, err := Hash(absent)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	emptyHash, err := Hash(empty)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if absentHash != emptyHash {
		t.Errorf("lineage digest differs for identical specs: %s vs %s", absentHash, emptyHash)
	}

	// Collections are always emitted, never null.
	if bytes.Contains(absentJSON, []byte("null")) {
		t.Errorf("canonical encoding contains null:\n%s", absentJSON)
	}
}

// TestNestedEmptyCollectionsEncodeIdentically covers the same rule one level
// down: a resource with no fields must encode the same either way.
func TestNestedEmptyCollectionsEncodeIdentically(t *testing.T) {
	withNil := validSpec()
	withNil.Resources[0].Fields = nil

	withEmpty := validSpec()
	withEmpty.Resources[0].Fields = []*Field{}

	if !bytes.Equal(mustMarshal(t, withNil), mustMarshal(t, withEmpty)) {
		t.Error("nil and empty field slices encode differently")
	}
}

// TestMarshalDoesNotMutateInput confirms canonicalization copies rather than
// rewriting the caller's spec.
func TestMarshalDoesNotMutateInput(t *testing.T) {
	s := &ProjectSpec{SpecVersion: SpecVersion}

	if _, err := Marshal(s); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if s.Resources != nil || s.Pages != nil || s.Navigation != nil {
		t.Error("Marshal mutated the caller's spec")
	}
}

func TestRoundTripIsLossless(t *testing.T) {
	original := validSpec()

	encoded, err := Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	decoded, err := Unmarshal(encoded)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	reencoded, err := Marshal(decoded)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}

	if !bytes.Equal(encoded, reencoded) {
		t.Errorf("round trip changed encoding:\nbefore:\n%s\nafter:\n%s", encoded, reencoded)
	}
}

// TestRoundTripPreservesIdentityAndSymbols guards the properties ADR-001 §70
// requires serialization to preserve.
func TestRoundTripPreservesIdentityAndSymbols(t *testing.T) {
	decoded, err := Unmarshal(mustMarshal(t, validSpec()))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	customer := decoded.FindResource(fixCustomer)
	if customer == nil {
		t.Fatal("customer resource lost in round trip")
	}
	if customer.CodeName != "Customer" || customer.StorageName != "customers" {
		t.Errorf("naming domains not preserved: code=%q storage=%q",
			customer.CodeName, customer.StorageName)
	}

	email := customer.FindField(fixCustomerEmail)
	if email == nil {
		t.Fatal("email field lost in round trip")
	}
	if email.CodeName != "Email" || email.StorageName != "email" || !email.Unique {
		t.Errorf("field attributes not preserved: %+v", email)
	}

	// Ordering is meaningful and must survive.
	wantOrder := []ID{fixCustomerName, fixCustomerEmail, fixCustomerActive, fixCustomerTier}
	if len(customer.Fields) != len(wantOrder) {
		t.Fatalf("field count changed: got %d want %d", len(customer.Fields), len(wantOrder))
	}
	for i, want := range wantOrder {
		if customer.Fields[i].ID != want {
			t.Errorf("field %d: got %s want %s", i, customer.Fields[i].ID, want)
		}
	}
}

func TestUnmarshalRejectsUnknownFields(t *testing.T) {
	// A spec written by a newer compiler must fail loudly rather than losing
	// the unknown data on the next write.
	_, err := Unmarshal([]byte(`{"spec_version":1,"surprise":true}`))
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestUnmarshalRejectsUnsupportedVersion(t *testing.T) {
	_, err := Unmarshal([]byte(`{"spec_version":99}`))
	if err == nil {
		t.Fatal("expected error for unsupported spec_version")
	}
}

func TestHashChangesWithContentAndIsStable(t *testing.T) {
	base := validSpec()

	first, err := Hash(base)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	again, err := Hash(validSpec())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if first != again {
		t.Errorf("hash unstable across equal specs: %s vs %s", first, again)
	}

	// A relabel is a real content change and must move the hash, even though
	// it leaves the extension ABI untouched.
	changed := validSpec()
	changed.Resources[0].Label = "Client"
	other, err := Hash(changed)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if other == first {
		t.Error("hash did not change after relabel")
	}
}

// TestGoldenCanonicalJSON pins the exact on-disk encoding. Regenerate with
// -update only when the schema deliberately changes.
func TestGoldenCanonicalJSON(t *testing.T) {
	got := mustMarshal(t, validSpec())
	golden := filepath.Join("testdata", "acme_crm.json")

	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Log("golden updated")
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with UPDATE_GOLDEN=1 to create): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("canonical encoding drifted from golden file.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func mustMarshal(t *testing.T, s *ProjectSpec) []byte {
	t.Helper()
	data, err := Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
