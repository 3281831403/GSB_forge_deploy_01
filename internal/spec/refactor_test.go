package spec

import "testing"

// refactorFixture builds a one-resource, one-field spec and a ledger recording
// their frozen symbols live — the accepted state a refactor starts from.
func refactorFixture(t *testing.T) (*ProjectSpec, *Ledger, ID, ID) {
	t.Helper()
	res := fixID(KindResource, "1")
	fld := fixID(KindField, "11")
	s := &ProjectSpec{
		SpecVersion: SpecVersion,
		Project:     Project{ID: fixID(KindProject, "1"), Name: "Acme", Slug: "acme"},
		Database:    Database{Driver: DriverPostgres},
		Auth:        Auth{Mode: AuthCookie},
		Resources: []*Resource{
			{
				ID: res, Label: "Customer", CodeName: "Customer", StorageName: "customers",
				Fields: []*Field{
					{ID: fld, Label: "Email", Type: TypeString, CodeName: "Email", StorageName: "email"},
				},
			},
		},
	}
	if d := Validate(s); d != nil {
		t.Fatalf("fixture invalid:\n%s", d.Error())
	}
	l := NewLedger()
	if err := l.Record(NamespaceResource, "Customer", res); err != nil {
		t.Fatalf("record resource: %v", err)
	}
	if err := l.Record(FieldNamespace(res), "Email", fld); err != nil {
		t.Fatalf("record field: %v", err)
	}
	return s, l, res, fld
}

// TestRefactorResourceSymbol is the core §13/§55 operation: a deliberate code
// refactor Customer→Client changes the frozen symbol and the ledger, but leaves
// the label untouched (that is what makes it distinct from a relabel) and does
// not mutate the accepted spec or ledger.
func TestRefactorResourceSymbol(t *testing.T) {
	s, l, res, _ := refactorFixture(t)

	result, err := RefactorCodeName(s, l, res, "Client")
	if err != nil {
		t.Fatalf("RefactorCodeName: %v", err)
	}

	// Candidate carries the new symbol; the label is unchanged.
	got := result.Spec.FindResource(res)
	if got.CodeName != "Client" {
		t.Errorf("code_name = %q, want Client", got.CodeName)
	}
	if got.Label != "Customer" {
		t.Errorf("a refactor must not change the label; got %q", got.Label)
	}
	// Ledger: old tombstoned (never reused), new live and owned by the entity.
	if !result.Ledger.IsTombstoned(NamespaceResource, "Customer") {
		t.Error("old symbol Customer must be tombstoned")
	}
	if !result.Ledger.IsLive(NamespaceResource, "Client") {
		t.Error("new symbol Client must be live")
	}
	if owner, _ := result.Ledger.OwnerOf(NamespaceResource, "Client"); owner != res {
		t.Errorf("Client must be owned by %s; got %s", res, owner)
	}

	// Inputs untouched: the accepted spec and ledger are unchanged.
	if s.FindResource(res).CodeName != "Customer" {
		t.Error("RefactorCodeName must not mutate the input spec")
	}
	if !l.IsLive(NamespaceResource, "Customer") || !l.IsFree(NamespaceResource, "Client") {
		t.Error("RefactorCodeName must not mutate the input ledger")
	}
}

// TestRefactorFieldSymbol: the §13 example Email→ContactEmail on a field.
func TestRefactorFieldSymbol(t *testing.T) {
	s, l, res, fld := refactorFixture(t)

	result, err := RefactorCodeName(s, l, fld, "ContactEmail")
	if err != nil {
		t.Fatalf("RefactorCodeName: %v", err)
	}
	ns := FieldNamespace(res)
	if result.Spec.FindResource(res).FindField(fld).CodeName != "ContactEmail" {
		t.Error("field code_name must be refactored")
	}
	if !result.Ledger.IsTombstoned(ns, "Email") || !result.Ledger.IsLive(ns, "ContactEmail") {
		t.Error("field ledger must tombstone Email and record ContactEmail")
	}
	// The refactored candidate is still a valid spec.
	if d := Validate(result.Spec); d != nil {
		t.Errorf("refactored candidate invalid:\n%s", d.Error())
	}
}

func TestRefactorRejects(t *testing.T) {
	s, l, res, fld := refactorFixture(t)

	// A second resource so we can test a live-symbol collision.
	other := fixID(KindResource, "2")
	s.Resources = append(s.Resources, &Resource{
		ID: other, Label: "Invoice", CodeName: "Invoice", StorageName: "invoices",
		Fields: []*Field{{ID: fixID(KindField, "21"), Label: "Total", Type: TypeString, CodeName: "Total", StorageName: "total"}},
	})
	if err := l.Record(NamespaceResource, "Invoice", other); err != nil {
		t.Fatalf("record: %v", err)
	}

	cases := []struct {
		name   string
		entity ID
		symbol string
	}{
		{"not exported", res, "client"},
		{"reserved field symbol", fld, "ID"},
		{"collides with a live symbol", res, "Invoice"},
		{"same symbol is a no-op", res, "Customer"},
		{"unknown resource", fixID(KindResource, "9"), "Whatever"},
		{"non-refactorable kind", fixID(KindPage, "1"), "Whatever"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RefactorCodeName(s, l, tc.entity, tc.symbol); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

// TestRefactorRefusesTombstonedSymbol: a symbol retired by an earlier refactor is
// never handed back out (§20).
func TestRefactorRefusesTombstonedSymbol(t *testing.T) {
	s, l, res, _ := refactorFixture(t)

	first, err := RefactorCodeName(s, l, res, "Client")
	if err != nil {
		t.Fatalf("first refactor: %v", err)
	}
	// Now Customer is tombstoned in the candidate ledger; refactoring back to it
	// must be refused.
	if _, err := RefactorCodeName(first.Spec, first.Ledger, res, "Customer"); err == nil {
		t.Error("refactoring back to a tombstoned symbol must be refused")
	}
}

// TestNormalizeIdentifiersTowardLabels is the §14 operation: symbols that drifted
// from their labels are refactored toward what the label would mint; the ledger
// tracks each move.
func TestNormalizeIdentifiersTowardLabels(t *testing.T) {
	res := fixID(KindResource, "1")
	fld := fixID(KindField, "11")
	s := &ProjectSpec{
		SpecVersion: SpecVersion,
		Project:     Project{ID: fixID(KindProject, "1"), Name: "Acme", Slug: "acme"},
		Database:    Database{Driver: DriverPostgres},
		Auth:        Auth{Mode: AuthCookie},
		Resources: []*Resource{
			{
				// Label drifted from the frozen symbol (§14 example).
				ID: res, Label: "Client", CodeName: "Customer", StorageName: "customers",
				Fields: []*Field{
					{ID: fld, Label: "Contact Email", Type: TypeString, CodeName: "Email", StorageName: "email"},
				},
			},
		},
	}
	l := NewLedger()
	_ = l.Record(NamespaceResource, "Customer", res)
	_ = l.Record(FieldNamespace(res), "Email", fld)

	result, err := NormalizeIdentifiers(s, l)
	if err != nil {
		t.Fatalf("NormalizeIdentifiers: %v", err)
	}

	wantRes, _ := Normalize("Client")
	wantFld, _ := Normalize("Contact Email")
	got := result.Spec.FindResource(res)
	if got.CodeName != wantRes {
		t.Errorf("resource symbol = %q, want %q", got.CodeName, wantRes)
	}
	if got.FindField(fld).CodeName != wantFld {
		t.Errorf("field symbol = %q, want %q", got.FindField(fld).CodeName, wantFld)
	}
	if !result.Ledger.IsTombstoned(NamespaceResource, "Customer") || !result.Ledger.IsLive(NamespaceResource, wantRes) {
		t.Error("ledger must retire Customer and record the normalized resource symbol")
	}
	if d := Validate(result.Spec); d != nil {
		t.Errorf("normalized candidate invalid:\n%s", d.Error())
	}
}

// TestNormalizeIdentifiersLeavesAlignedSymbols: an entity whose symbol already
// matches its label is untouched, and no spurious tombstone is created.
func TestNormalizeIdentifiersLeavesAlignedSymbols(t *testing.T) {
	res := fixID(KindResource, "1")
	s := &ProjectSpec{
		SpecVersion: SpecVersion,
		Project:     Project{ID: fixID(KindProject, "1"), Name: "Acme", Slug: "acme"},
		Database:    Database{Driver: DriverPostgres},
		Auth:        Auth{Mode: AuthCookie},
		Resources: []*Resource{
			{ID: res, Label: "Widget", CodeName: "Widget", StorageName: "widgets"},
		},
	}
	l := NewLedger()
	_ = l.Record(NamespaceResource, "Widget", res)

	result, err := NormalizeIdentifiers(s, l)
	if err != nil {
		t.Fatalf("NormalizeIdentifiers: %v", err)
	}
	if result.Spec.FindResource(res).CodeName != "Widget" {
		t.Error("an already-aligned symbol must be left unchanged")
	}
	if result.Ledger.IsTombstoned(NamespaceResource, "Widget") {
		t.Error("no tombstone should be created for an unchanged symbol")
	}
}

// TestNormalizeIdentifiersIsDeterministic: the operation is a pure function of
// its inputs.
func TestNormalizeIdentifiersIsDeterministic(t *testing.T) {
	res := fixID(KindResource, "1")
	s := &ProjectSpec{
		SpecVersion: SpecVersion,
		Project:     Project{ID: fixID(KindProject, "1"), Name: "Acme", Slug: "acme"},
		Database:    Database{Driver: DriverPostgres},
		Auth:        Auth{Mode: AuthCookie},
		Resources: []*Resource{
			{ID: res, Label: "Client", CodeName: "Customer", StorageName: "customers"},
		},
	}
	l := NewLedger()
	_ = l.Record(NamespaceResource, "Customer", res)

	first, err := NormalizeIdentifiers(s, l)
	if err != nil {
		t.Fatalf("NormalizeIdentifiers: %v", err)
	}
	for i := 0; i < 10; i++ {
		next, err := NormalizeIdentifiers(s, l)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if next.Spec.FindResource(res).CodeName != first.Spec.FindResource(res).CodeName {
			t.Fatalf("run %d: normalization not deterministic", i)
		}
	}
}

// TestLedgerCloneIsIndependent: mutating a clone must not touch the original.
func TestLedgerCloneIsIndependent(t *testing.T) {
	l := NewLedger()
	res := fixID(KindResource, "1")
	_ = l.Record(NamespaceResource, "Customer", res)

	clone := l.Clone()
	_ = clone.Tombstone(NamespaceResource, "Customer")
	_ = clone.Record(NamespaceResource, "Client", res)

	if !l.IsLive(NamespaceResource, "Customer") {
		t.Error("mutating the clone must not tombstone the original")
	}
	if !l.IsFree(NamespaceResource, "Client") {
		t.Error("mutating the clone must not add to the original")
	}
}
