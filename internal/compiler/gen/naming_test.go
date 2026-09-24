package gen

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
	"gorm.io/gorm/schema"
)

// oneResourceGraph builds a graph with a single resource carrying the given
// fields, so naming and default edge cases can be exercised in isolation.
func oneResourceGraph(t *testing.T, code, storage string, fields ...*spec.Field) *graph.Graph {
	t.Helper()
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: spec.MustNewID(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: spec.MustNewID(spec.KindResource), Label: code, CodeName: code, StorageName: storage, Fields: fields},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid:\n%s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return g
}

func field(code, storage string, ft spec.FieldType) *spec.Field {
	return &spec.Field{ID: spec.MustNewID(spec.KindField), Label: code, Type: ft, CodeName: code, StorageName: storage}
}

// --- Finding 1: table name follows storage_name -----------------------------

// TestTableNameFollowsStorageName is the table-side counterpart to the column
// rule: GORM inflects a struct name ("Person" -> "people"), so storage_name
// must be pinned with a TableName method or a rename is silently dropped.
func TestTableNameFollowsStorageName(t *testing.T) {
	// code_name Person would inflect to table "people"; storage_name is
	// "persons", and that must win.
	g := oneResourceGraph(t, "Person", "persons", field("Name", "name", spec.TypeString))
	files, err := Models(g)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	src := string(files[0].Content)

	if !strings.Contains(src, `func (Person) TableName() string { return "persons" }`) {
		t.Errorf("model must pin the table to storage_name persons:\n%s", src)
	}
}

// TestTableNameUsesStorageNotInflection makes the drift concrete: a resource
// whose code symbol and storage_name would inflect differently.
func TestTableNameUsesStorageNotInflection(t *testing.T) {
	g := oneResourceGraph(t, "Customer", "clients", field("Name", "name", spec.TypeString))
	files, _ := Models(g)
	src := string(files[0].Content)

	if !strings.Contains(src, `return "clients"`) {
		t.Error("table must be the storage_name clients, not an inflection of Customer")
	}
}

// --- Finding 2: derived symbols are reserved/rejected -----------------------

// TestPackageNameKeywordRejected covers a valid code symbol whose lowercase
// form is a Go keyword, which would emit `package type`.
func TestPackageNameKeywordRejected(t *testing.T) {
	// "Type" is a legal exported identifier and a legal code_name, but
	// lowercases to the keyword "type".
	g := oneResourceGraph(t, "Type", "types", field("Name", "name", spec.TypeString))
	_, err := Models(g)
	if err == nil {
		t.Fatal("expected a resource whose package name is a keyword to be rejected")
	}
	// The rejection must be the explicit reservation check (ADR-001 §12), not
	// a gofmt parse error discovered after emitting `package type`.
	if !strings.Contains(err.Error(), "keyword") {
		t.Errorf("rejection should name the keyword collision, got: %v", err)
	}
}

// TestPackageNameReservedRejected covers valid code symbols that fold to a
// directory basename the go tool treats specially. Each would build-break or
// hide the model:
//
//	main      package main needs a func main and is not importable
//	internal  cmd/ cannot import an internal package
//	testdata  the go tool ignores testdata, so the compile gate never sees it
func TestPackageNameReservedRejected(t *testing.T) {
	cases := map[string]struct{ code, storage string }{
		"main":          {"Main", "mains"},
		"documentation": {"Documentation", "docs"},
		"internal":      {"Internal", "internals"},
		"testdata":      {"TestData", "test_data"},
	}
	for pkg, c := range cases {
		t.Run(pkg, func(t *testing.T) {
			g := oneResourceGraph(t, c.code, c.storage, field("Name", "name", spec.TypeString))
			_, err := Models(g)
			if err == nil {
				t.Fatalf("expected a resource folding to package %q to be rejected", pkg)
			}
			if !strings.Contains(err.Error(), pkg) {
				t.Errorf("rejection should name the reserved package %q, got: %v", pkg, err)
			}
		})
	}
}

// TestPackageFoldCollidingWithImportRejected covers a resource whose package
// name matches a package the generated code imports (framework, contract, gorm,
// …): the generated file would have two imports with the same local name and
// not compile.
func TestPackageFoldCollidingWithImportRejected(t *testing.T) {
	for _, name := range []string{"Framework", "Contract", "Gorm", "Admin"} {
		t.Run(name, func(t *testing.T) {
			g := oneResourceGraph(t, name, "things", field("Name", "name", spec.TypeString))
			g.Resources[0].Spec.Behavior.AdminVisible = true

			if _, err := Models(g); err == nil {
				t.Errorf("Models must reject a resource folding to imported package %q", strings.ToLower(name))
			}
			if _, err := Handlers(g); err == nil {
				t.Errorf("Handlers must reject a resource folding to imported package %q", strings.ToLower(name))
			}
			if _, err := Wiring(g, "example.com/app"); err == nil {
				t.Errorf("Wiring must reject a resource folding to imported package %q", strings.ToLower(name))
			}
		})
	}
}

// TestPackageFoldCollisionRejected covers two distinct code symbols that fold
// to the same package, whose generated directories would collide.
func TestPackageFoldCollisionRejected(t *testing.T) {
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: spec.MustNewID(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: spec.MustNewID(spec.KindResource), Label: "Customer", CodeName: "Customer", StorageName: "customers"},
			{ID: spec.MustNewID(spec.KindResource), Label: "Shouty", CodeName: "CUSTOMER", StorageName: "shouty"},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("distinct code symbols should be spec-valid; the fold clash is build health: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	files, err := Models(g)
	if err == nil {
		// Prove the concrete harm the guard prevents: two files at one path.
		seen := map[string]bool{}
		for _, f := range files {
			if seen[f.Path] {
				t.Fatalf("two models share path %s", f.Path)
			}
			seen[f.Path] = true
		}
		t.Fatal("expected the package fold collision to be rejected")
	}
	if !strings.Contains(err.Error(), "fold to package") {
		t.Errorf("rejection should name the package collision, got: %v", err)
	}
}

// TestBelongsToFieldCollisionRejected covers the FK-suffix collision: a
// belongs_to "Customer" derives "CustomerID", which must not silently coexist
// with a scalar field whose code symbol is already "CustomerID". This is now a
// spec-validity error (spec.GeneratedModelFieldName-based uniqueness, shared with
// the model generator), so it is caught before generation — graph.Build refuses
// the invalid spec. The generator keeps validateNames as a defensive check a
// validated spec cannot reach; the collision's own coverage lives in
// internal/spec (validate_test.go).
func TestBelongsToFieldCollisionRejected(t *testing.T) {
	target := spec.MustNewID(spec.KindResource)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: spec.MustNewID(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: target, Label: "Customer", CodeName: "Customer", StorageName: "customers"},
			{
				ID: spec.MustNewID(spec.KindResource), Label: "Invoice",
				CodeName: "Invoice", StorageName: "invoices",
				Fields: []*spec.Field{
					{
						ID: spec.MustNewID(spec.KindField), Label: "Customer", Type: spec.TypeBelongsTo,
						CodeName: "Customer", StorageName: "customer_id", Target: target,
					},
					// Scalar field whose code symbol collides with the FK's
					// derived name CustomerID.
					{
						ID: spec.MustNewID(spec.KindField), Label: "Legacy id", Type: spec.TypeInteger,
						CodeName: "CustomerID", StorageName: "legacy_customer_id",
					},
				},
			},
		},
	}
	if d := spec.Validate(s); !d.Has(spec.CodeDuplicateCode) {
		t.Fatalf("the CustomerID generated-field collision must be a spec-validity error; got %v", d)
	}
	// The graph refuses to build over the invalid spec, so the collision never
	// reaches generation.
	if _, err := graph.Build(s); err == nil {
		t.Fatal("graph.Build should refuse the collision spec")
	}
}

// A field whose code symbol matches a symbol the generated model defines (a
// gorm.Model field, or the TableName method) is now rejected by the spec
// validator, not just the generator — F0 #19 centralized the reservation table
// in spec so the validator and generator share it and cannot disagree. The
// validator coverage lives in internal/spec (validate_test.go). The generator
// keeps a defensive check against spec.ReservedModelSymbol for the derived-name
// path, which a validated spec cannot otherwise reach.

// TestResourceNameCollidingWithPackageSymbolRejected covers a resource whose
// code symbol is a package-level identifier the generated code defines (the
// handler type or the Register func). Both stages that emit the package must
// reject it, since the model type would redeclare the symbol.
func TestResourceNameCollidingWithPackageSymbolRejected(t *testing.T) {
	for _, name := range []string{"Handler", "Register", "RegisterAdmin"} {
		t.Run(name, func(t *testing.T) {
			g := oneResourceGraph(t, name, "things", field("Name", "name", spec.TypeString))
			g.Resources[0].Spec.Behavior.AdminVisible = true

			if _, err := Models(g); err == nil {
				t.Errorf("Models must reject a resource named %q", name)
			}
			if _, err := Handlers(g); err == nil {
				t.Errorf("Handlers must reject a resource named %q", name)
			}
			if _, err := Admin(g); err == nil {
				t.Errorf("Admin must reject a resource named %q", name)
			}
		})
	}
}

// TestReservedPackageSymbolsAreComplete pins the set against the symbols the
// generator actually emits, so removing one from the map fails here.
func TestReservedPackageSymbolsAreComplete(t *testing.T) {
	for _, want := range []string{"Handler", "Register", "RegisterAdmin"} {
		if _, ok := reservedPackageSymbols[want]; !ok {
			t.Errorf("%q must be a reserved package symbol; the generator emits it", want)
		}
	}
}

// TestGeneratedFieldShadowingGormModelRejected covers a belongs_to whose
// derived name would shadow a gorm.Model field. (Scalar code_names like "ID"
// are already rejected by the spec validator; the FK "+ID" derivation is not.)
func TestGeneratedFieldShadowingGormModelRejected(t *testing.T) {
	// A resource-reference field named "I" would derive Go field "IID" — no
	// clash. To hit a gorm.Model field via derivation we would need a belongs_to
	// deriving e.g. "ID"; that requires code symbol "", which is invalid. So
	// this guards the scalar path through the shared reservation table directly
	// instead (F0 #19: the generator consults spec's table, not a local copy).
	if _, reserved := spec.ReservedModelSymbol("CreatedAt"); !reserved {
		t.Fatal("the model reservation table must include CreatedAt")
	}
}

// --- Finding 3: defaults round-trip or are rejected -------------------------

// TestSafeStringDefaultRoundTrips proves a permitted string default survives
// both Go's struct-tag unquoting and GORM's tag parser, using the real GORM
// parser rather than asserting the raw text.
func TestSafeStringDefaultRoundTrips(t *testing.T) {
	values := []string{"free", "O'Brien", "hello world", "50%"}
	for _, v := range values {
		t.Run(v, func(t *testing.T) {
			f := field("Note", "note", spec.TypeString)
			f.Default = &v
			g := oneResourceGraph(t, "Doc", "docs", f)

			files, err := Models(g)
			if err != nil {
				t.Fatalf("Models: %v", err)
			}
			gormTag := extractGormTag(t, string(files[0].Content), "Note")
			def := schema.ParseTagSetting(gormTag, ";")["DEFAULT"]

			want := "'" + strings.ReplaceAll(v, "'", "''") + "'"
			if def != want {
				t.Errorf("default did not round-trip: got %q want %q (tag %q)", def, want, gormTag)
			}
		})
	}
}

// TestUnsafeStringDefaultRejectedBeforeGraph confirms the spec validator is the
// gate: a graph with an unrepresentable default never builds, so gen never sees
// it. (The spec package has its own table covering each character; this asserts
// the boundary the generator relies on.)
func TestUnsafeStringDefaultRejectedBeforeGraph(t *testing.T) {
	for _, v := range []string{"a;b", `a"b`, "a`b", `a\b`, "a\nb"} {
		t.Run(v, func(t *testing.T) {
			s := &spec.ProjectSpec{
				SpecVersion: spec.SpecVersion,
				Project:     spec.Project{ID: spec.MustNewID(spec.KindProject), Name: "Acme", Slug: "acme"},
				Database:    spec.Database{Driver: spec.DriverPostgres},
				Auth:        spec.Auth{Mode: spec.AuthCookie},
				Resources: []*spec.Resource{
					{
						ID: spec.MustNewID(spec.KindResource), Label: "Doc",
						CodeName: "Doc", StorageName: "docs",
						Fields: []*spec.Field{withDefault(field("Note", "note", spec.TypeString), v)},
					},
				},
			}
			if spec.Validate(s) == nil {
				t.Fatalf("spec validation should reject default %q", v)
			}
			if _, err := graph.Build(s); err == nil {
				t.Errorf("graph must refuse to build over an unrepresentable default %q", v)
			}
		})
	}
}

// TestGormDefaultDefensiveRejection covers gen's own guard directly, since a
// valid graph can no longer carry an unsafe default to it.
func TestGormDefaultDefensiveRejection(t *testing.T) {
	bad := "a;b"
	f := &graph.Field{Spec: &spec.Field{
		ID: spec.MustNewID(spec.KindField), Type: spec.TypeString,
		CodeName: "Note", StorageName: "note", Default: &bad,
	}}
	if _, err := gormDefault(f); err == nil {
		t.Fatal("gormDefault must reject an unrepresentable default as a defensive assertion")
	}
}

func withDefault(f *spec.Field, v string) *spec.Field {
	f.Default = &v
	return f
}

// extractGormTag pulls the gorm struct tag for a field out of generated source
// by parsing it the same way the Go runtime does.
func extractGormTag(t *testing.T, src, fieldName string) string {
	t.Helper()
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, fieldName+" ") {
			continue
		}
		start := strings.IndexByte(trimmed, '`')
		end := strings.LastIndexByte(trimmed, '`')
		if start < 0 || end <= start {
			t.Fatalf("no backtick struct tag on line: %s", line)
		}
		return reflect.StructTag(trimmed[start+1 : end]).Get("gorm")
	}
	t.Fatalf("no field %q in:\n%s", fieldName, src)
	return ""
}
