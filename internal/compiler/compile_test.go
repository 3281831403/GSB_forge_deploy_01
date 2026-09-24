package compiler

import (
	"bytes"
	"go/format"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// gofmtBytes is the independent gofmt check the formatter test uses, so it
// verifies the generator's output rather than reusing the generator's own
// formatter.
const testModule = "example.com/app"

func gofmtBytes(src []byte) ([]byte, error) { return format.Source(src) }

// sampleSpec is a two-resource project exercising relationships, every CRUD
// toggle and admin visibility — the M0 target shape.
func sampleSpec(t *testing.T) *spec.ProjectSpec {
	t.Helper()

	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	customer := id(spec.KindResource)
	invoice := id(spec.KindResource)
	invoiceTotal := id(spec.KindField)

	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{
				ID: customer, Label: "Customer", LabelPlural: "Customers",
				CodeName: "Customer", StorageName: "customers",
				Behavior: spec.ResourceBehavior{
					CreateEnabled: true, UpdateEnabled: true, DeleteEnabled: true, AdminVisible: true,
				},
				Fields: []*spec.Field{
					{ID: id(spec.KindField), Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email", Required: true, Unique: true},
					{ID: id(spec.KindField), Label: "Active", Type: spec.TypeBoolean, CodeName: "Active", StorageName: "active"},
				},
			},
			{
				ID: invoice, Label: "Invoice", LabelPlural: "Invoices",
				CodeName: "Invoice", StorageName: "invoices",
				// Total is declared aggregatable (#182), so the invoice list handler
				// gains the ?aggregate= surface and ListMeta envelope — the e2e boots
				// it and asserts a real server-side SUM/MAX against Gombit v0.1.12.
				Behavior: spec.ResourceBehavior{CreateEnabled: true, AdminVisible: false, AggregatableFields: []spec.ID{invoiceTotal}},
				Fields: []*spec.Field{
					{ID: id(spec.KindField), Label: "Customer", Type: spec.TypeBelongsTo, CodeName: "Customer", StorageName: "customer_id", Required: true, Target: customer},
					{ID: invoiceTotal, Label: "Total", Type: spec.TypeDecimal, CodeName: "Total", StorageName: "total", Required: true},
				},
			},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("sample spec invalid: %s", d.Error())
	}
	return s
}

// sampleSpecWithTable is the sample plus a customers resource_table page and a
// resource_form page, for the frontend assertions: table and form generation
// are page-driven (#51, #52), so a spec needs those pages to produce the list
// and create/edit pages. The base sampleSpec stays page-free so the deletion
// tests exercise relationship blockers in isolation.
func sampleSpecWithTable(t *testing.T) *spec.ProjectSpec {
	t.Helper()
	s := sampleSpec(t)
	// Exercise the list-query surface end to end (#51): declare Email searchable
	// and Active filterable, and have the customers table enable search and an
	// Active filter — so the generated backend exposes ?search= and ?active= and
	// the e2e boots and serves them.
	s.Resources[0].Behavior.SearchableFields = []spec.ID{s.Resources[0].Fields[0].ID}
	s.Resources[0].Behavior.FilterableFields = []spec.ID{s.Resources[0].Fields[1].ID}
	s.Pages = []*spec.Page{
		{ID: spec.MustNewID(spec.KindPage), Slug: "customers", Label: "Customers", Type: spec.PageResourceTable, Resource: s.Resources[0].ID,
			Table: &spec.TableConfig{Search: true, Filters: []spec.ID{s.Resources[0].Fields[1].ID}}},
		{ID: spec.MustNewID(spec.KindPage), Slug: "edit-customer", Label: "Edit customer", Type: spec.PageResourceForm, Resource: s.Resources[0].ID},
		{ID: spec.MustNewID(spec.KindPage), Slug: "customer", Label: "Customer", Type: spec.PageResourceDetail, Resource: s.Resources[0].ID},
		{ID: spec.MustNewID(spec.KindPage), Slug: "home", Label: "Home", Type: spec.PageDashboard,
			Dashboard: &spec.DashboardConfig{CountCards: []spec.DashboardCard{{Label: "Customers", Resource: s.Resources[0].ID}}}},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("sample spec with table invalid: %s", d.Error())
	}
	return s
}

// TestCompileIsDeterministic is the issue #10 acceptance criterion: the same
// spec compiles to a byte-identical tree, every time.
func TestCompileIsDeterministic(t *testing.T) {
	s := sampleSpec(t)

	first, err := Compile(s, testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("compile produced no files")
	}

	for i := 0; i < 30; i++ {
		next, err := Compile(s, testModule)
		if err != nil {
			t.Fatalf("compile %d: %v", i, err)
		}
		if len(next) != len(first) {
			t.Fatalf("run %d: file count changed %d -> %d", i, len(first), len(next))
		}
		for j := range first {
			if first[j].Path != next[j].Path {
				t.Fatalf("run %d: file %d path changed %q -> %q", i, j, first[j].Path, next[j].Path)
			}
			if !bytes.Equal(first[j].Content, next[j].Content) {
				t.Fatalf("run %d: content of %s changed", i, first[j].Path)
			}
		}
	}
}

// TestCompileTreeShape checks the full set of files produced for the sample:
// model/handlers/routes for every resource, admin only for the visible one.
func TestCompileTreeShape(t *testing.T) {
	files, err := Compile(sampleSpecWithTable(t), testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	got := map[string]bool{}
	for _, f := range files {
		got[f.Path] = true
	}

	mustHave := []string{
		"internal/forge_generated/customer/model.go",
		"internal/forge_generated/customer/view.go",
		"internal/forge_generated/customer/mutation.go",
		"internal/forge_generated/customer/fields.go",
		"internal/forge_generated/invoice/fields.go",
		// Shared extension-ABI rejection package.
		"internal/forge_generated/extension/extension.go",
		"internal/forge_generated/customer/handlers.go",
		"internal/forge_generated/customer/routes.go",
		"internal/forge_generated/customer/admin.go",
		"internal/forge_generated/invoice/model.go",
		"internal/forge_generated/invoice/view.go",
		"internal/forge_generated/invoice/mutation.go",
		"internal/forge_generated/invoice/handlers.go",
		"internal/forge_generated/invoice/routes.go",
		// Frontend stage output. The list is page-driven (named by the customers
		// table page's slug); detail/form are resource-driven.
		"frontend/src/forge_generated/customer/CustomersTablePage.tsx",
		"frontend/src/forge_generated/customer/CustomerDetailPage.tsx",
		"frontend/src/forge_generated/customer/EditCustomerFormPage.tsx",
		"frontend/src/forge_generated/dashboard/HomeDashboardPage.tsx",
		"frontend/src/forge_generated/resources.tsx",
		// Composition root (wiring stage).
		"internal/forge_generated/register.go",
	}
	for _, path := range mustHave {
		if !got[path] {
			t.Errorf("missing generated file %s", path)
		}
	}
	// Invoice is not admin-visible, so it has no admin.go.
	if got["internal/forge_generated/invoice/admin.go"] {
		t.Error("invoice is not admin-visible and must not have admin.go")
	}
}

// TestCompilePathsAreUnique guards the invariant Generate enforces: no two
// files share a path, so nothing is dropped when the tree is written out.
func TestCompilePathsAreUnique(t *testing.T) {
	files, err := Compile(sampleSpec(t), testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f.Path] {
			t.Errorf("duplicate path %s", f.Path)
		}
		seen[f.Path] = true
	}
}

// TestCompileOrderIsStageThenAuthored checks stages emit in order (models,
// handlers, admin) and resources within a stage in authored order.
func TestCompileOrderIsStageThenAuthored(t *testing.T) {
	files, err := Compile(sampleSpec(t), testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// The first files must be the model stage (both resources) before any
	// handler file appears.
	firstHandler, firstModel := -1, -1
	for i, f := range files {
		if strings.HasSuffix(f.Path, "/model.go") && firstModel == -1 {
			firstModel = i
		}
		if strings.HasSuffix(f.Path, "/handlers.go") && firstHandler == -1 {
			firstHandler = i
		}
	}
	if firstModel == -1 || firstHandler == -1 {
		t.Fatal("expected both model and handler files")
	}
	if firstModel > firstHandler {
		t.Error("model stage must be emitted before the handler stage")
	}

	// Within the model stage, customer precedes invoice (authored order).
	custModel, invModel := -1, -1
	for i, f := range files {
		switch f.Path {
		case "internal/forge_generated/customer/model.go":
			custModel = i
		case "internal/forge_generated/invoice/model.go":
			invModel = i
		}
	}
	if custModel > invModel {
		t.Error("resources must be emitted in authored order (customer before invoice)")
	}
}

// TestCompileEveryFileIsFormatted confirms the formatter stage ran: every Go
// file is already gofmt-clean, so writing the tree needs no post-processing.
func TestCompileEveryFileIsFormatted(t *testing.T) {
	files, err := Compile(sampleSpec(t), testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, f := range files {
		if !strings.HasSuffix(f.Path, ".go") {
			continue
		}
		formatted, err := gofmtBytes(f.Content)
		if err != nil {
			t.Fatalf("gofmt %s: %v", f.Path, err)
		}
		if !bytes.Equal(formatted, f.Content) {
			t.Errorf("%s is not gofmt-clean; the formatter stage did not run", f.Path)
		}
	}
}

// TestCompileRefusesInvalidSpec confirms the pipeline rejects a spec the graph
// will not build.
func TestCompileRefusesInvalidSpec(t *testing.T) {
	s := sampleSpec(t)
	s.Project.Slug = "Not A Slug"
	if _, err := Compile(s, testModule); err == nil {
		t.Fatal("compile must refuse an invalid spec")
	}
}

// TestGenerateRejectsDuplicatePaths gives the path-collision guard teeth by
// running a stage twice, so two stages emit the same paths.
func TestGenerateRejectsDuplicatePaths(t *testing.T) {
	saved := fileStages
	defer func() { fileStages = saved }()
	fileStages = []stage{
		{"models", gen.Models},
		{"models-again", gen.Models},
	}

	g, err := graph.Build(sampleSpec(t))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := Generate(g, testModule); err == nil {
		t.Fatal("Generate must reject two stages producing the same path")
	}
}

func TestCompileNilAndEmpty(t *testing.T) {
	if _, err := Compile(nil, testModule); err == nil {
		t.Error("compile(nil) must error")
	}
	if _, err := Generate(nil, testModule); err == nil {
		t.Error("generate(nil) must error")
	}
}
