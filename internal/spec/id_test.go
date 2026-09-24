package spec

import (
	"errors"
	"testing"
	"time"
)

func TestNewIDIsWellFormed(t *testing.T) {
	for _, kind := range Kinds() {
		id, err := NewID(kind)
		if err != nil {
			t.Fatalf("NewID(%s): %v", kind, err)
		}
		if !id.Valid(kind) {
			t.Errorf("NewID(%s) produced invalid id %q", kind, id)
		}
		if id.Kind() != kind {
			t.Errorf("Kind(): got %s want %s", id.Kind(), kind)
		}
	}
}

func TestNewIDIsUnique(t *testing.T) {
	const count = 5000
	seen := make(map[ID]struct{}, count)
	for range count {
		id := MustNewID(KindField)
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("duplicate id minted: %s", id)
		}
		seen[id] = struct{}{}
	}
}

// TestIDsSortByCreationTime documents the ULID ordering property: the
// timestamp prefix makes IDs lexicographically sortable by mint time.
func TestIDsSortByCreationTime(t *testing.T) {
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	fixedRand := func(b []byte) (int, error) {
		for i := range b {
			b[i] = 0
		}
		return len(b), nil
	}

	earlier, err := newIDAt(KindField, base, fixedRand)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	later, err := newIDAt(KindField, base.Add(time.Second), fixedRand)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	if !(earlier < later) {
		t.Errorf("expected %s < %s", earlier, later)
	}
}

func TestNewIDPropagatesEntropyFailure(t *testing.T) {
	sentinel := errors.New("no entropy")
	_, err := newIDAt(KindField, time.Now(), func([]byte) (int, error) { return 0, sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("expected entropy error, got %v", err)
	}
}

func TestIDValid(t *testing.T) {
	valid := MustNewID(KindResource)

	tests := []struct {
		name string
		id   ID
		kind Kind
		want bool
	}{
		{"minted id", valid, KindResource, true},
		{"wrong kind", valid, KindField, false},
		{"no separator", ID("res01K2M6RXQ8CJ00000000000000"), KindResource, false},
		{"short body", ID("res_01K2M6"), KindResource, false},
		{"empty", ID(""), KindResource, false},
		{"excluded letter I", ID("res_01K2M6RXQ8CJ0000000000000I"), KindResource, false},
		{"lowercase body", ID("res_01k2m6rxq8cj00000000000000"), KindResource, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.id.Valid(test.kind); got != test.want {
				t.Errorf("Valid(%s) on %q: got %v want %v", test.kind, test.id, got, test.want)
			}
		})
	}
}
