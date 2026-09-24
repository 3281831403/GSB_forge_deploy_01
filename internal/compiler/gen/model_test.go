package gen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

func ptr(s string) *string { return &s }

// buildGraph assembles a two-resource graph exercising every field type.
func buildGraph(t *testing.T) (*graph.Graph, map[string]spec.ID) {
	t.Helper()

	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	ids := map[string]spec.ID{
		"customer": id(spec.KindResource),
		"invoice":  id(spec.KindResource),
		"email":    id(spec.KindField),
		"active":   id(spec.KindField),
		"tier":     id(spec.KindField),
		"joined":   id(spec.KindField),
		"custFK":   id(spec.KindField),
		"total":    id(spec.KindField),
		"count":    id(spec.KindField),
		"due":      id(spec.KindField),
		"homePage": id(spec.KindPage),
		"custPage": id(spec.KindPage),
	}

	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{
				ID: ids["customer"], Label: "Customer",
				CodeName: "Customer", StorageName: "customers",
				Fields: []*spec.Field{
					// code_name Email deliberately differs from storage_name
					// contact_email: the column tag must follow storage_name.
					{
						ID: ids["email"], Label: "Email", Type: spec.TypeString,
						CodeName: "Email", StorageName: "contact_email",
						Required: true, Unique: true,
					},
					{
						ID: ids["active"], Label: "Active", Type: spec.TypeBoolean,
						CodeName: "Active", StorageName: "active", Default: ptr("true"),
					},
					{
						ID: ids["tier"], Label: "Tier", Type: spec.TypeEnum,
						CodeName: "Tier", StorageName: "tier",
						EnumValues: []spec.EnumValue{{Value: "free"}, {Value: "pro"}},
						Default:    ptr("free"),
					},
					{
						ID: ids["joined"], Label: "Joined", Type: spec.TypeDatetime,
						CodeName: "Joined", StorageName: "joined_at",
					},
				},
				// Declared query capabilities, so the generated customer list
				// handler — and the compile test that builds it against real
				// Gombit — exercise the #260 search/sort/filter wiring.
				Behavior: spec.ResourceBehavior{
					SearchableFields: []spec.ID{ids["email"], ids["tier"]},
					SortableFields:   []spec.ID{ids["email"], ids["joined"]},
					FilterableFields: []spec.ID{ids["tier"]},
				},
			},
			{
				ID: ids["invoice"], Label: "Invoice",
				CodeName: "Invoice", StorageName: "invoices",
				Fields: []*spec.Field{
					{
						ID: ids["custFK"], Label: "Customer", Type: spec.TypeBelongsTo,
						CodeName: "Customer", StorageName: "customer_id",
						Required: true, Target: ids["customer"],
					},
					{
						ID: ids["total"], Label: "Total", Type: spec.TypeDecimal,
						CodeName: "Total", StorageName: "total", Required: true,
					},
					{
						ID: ids["count"], Label: "Line count", Type: spec.TypeInteger,
						CodeName: "LineCount", StorageName: "line_count", Index: true,
					},
					{
						ID: ids["due"], Label: "Due date", Type: spec.TypeDate,
						CodeName: "DueDate", StorageName: "due_date",
					},
				},
			},
		},
		// One resource_table page per resource. The customers table pins explicit
		// columns; the invoices table omits them so the graph's default (scalar
		// fields) applies. Table generation is page-driven (#51), so these are what
		// produce the list pages.
		Pages: []*spec.Page{
			{
				ID: ids["custPage"], Slug: "customers", Label: "Customers", Type: spec.PageResourceTable,
				Resource: ids["customer"],
				Table:    &spec.TableConfig{Title: "Customers", Columns: []spec.ID{ids["email"], ids["active"]}},
			},
			{
				ID: id(spec.KindPage), Slug: "invoices", Label: "Invoices", Type: spec.PageResourceTable,
				Resource: ids["invoice"],
			},
			// One resource_form page per resource (the MVP allows at most one).
			// Form generation is page-driven (#52), so these produce the create/edit
			// pages; slugs avoid the word "form" so the component reads cleanly.
			{ID: id(spec.KindPage), Slug: "edit-customer", Label: "Edit customer", Type: spec.PageResourceForm, Resource: ids["customer"]},
			{ID: id(spec.KindPage), Slug: "edit-invoice", Label: "Edit invoice", Type: spec.PageResourceForm, Resource: ids["invoice"]},
			// One resource_detail page per resource (page-driven detail, #53). The
			// singular slug keeps the component name CustomerDetailPage/InvoiceDetailPage.
			{ID: id(spec.KindPage), Slug: "customer", Label: "Customer", Type: spec.PageResourceDetail, Resource: ids["customer"]},
			{ID: id(spec.KindPage), Slug: "invoice", Label: "Invoice", Type: spec.PageResourceDetail, Resource: ids["invoice"]},
			// A dashboard with a count card (customers) and a recent list (invoices) (#54).
			{ID: ids["homePage"], Slug: "home", Label: "Home", Type: spec.PageDashboard,
				Dashboard: &spec.DashboardConfig{
					CountCards:  []spec.DashboardCard{{Label: "Customers", Resource: ids["customer"]}},
					RecentLists: []spec.DashboardCard{{Label: "Recent invoices", Resource: ids["invoice"], Limit: 5}},
				}},
		},
		// Ordered navigation (#55): a dashboard entry, a resource-list entry and an
		// external link.
		Navigation: []*spec.NavItem{
			{ID: id(spec.KindNav), Label: "Home", Target: spec.NavPage, Page: ids["homePage"]},
			{ID: id(spec.KindNav), Label: "Customers", Target: spec.NavPage, Page: ids["custPage"]},
			{ID: id(spec.KindNav), Label: "Docs", Target: spec.NavExternal, URL: "https://example.com/docs"},
		},
	}

	if diagnostics := spec.Validate(s); diagnostics != nil {
		t.Fatalf("fixture spec is invalid:\n%s", diagnostics.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	return g, ids
}

// modelSource returns the generated model.go for a resource package name.
func modelSource(t *testing.T, files []File, pkg string) string {
	t.Helper()
	want := GeneratedRoot + "/" + pkg + "/model.go"
	for _, file := range files {
		if file.Path == want {
			return string(file.Content)
		}
	}
	t.Fatalf("no generated file at %s; got %v", want, paths(files))
	return ""
}

func paths(files []File) []string {
	out := make([]string, len(files))
	for i, file := range files {
		out[i] = file.Path
	}
	return out
}

func TestModelsFilePaths(t *testing.T) {
	g, _ := buildGraph(t)
	files, err := Models(g)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}

	// One file per resource, in authored order, under the compiler-owned root.
	want := []string{
		"internal/forge_generated/customer/model.go",
		"internal/forge_generated/invoice/model.go",
	}
	got := paths(files)
	if len(got) != len(want) {
		t.Fatalf("file count: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestModelHeaderBannerAndPackage(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Models(g)
	src := modelSource(t, files, "customer")

	if !strings.HasPrefix(src, Banner) {
		t.Errorf("model must start with the DO-NOT-EDIT banner, got:\n%s", src[:min(len(src), 80)])
	}
	if !strings.Contains(src, "package customer\n") {
		t.Error("model must declare package customer")
	}
	if !strings.Contains(src, "gorm.Model\n") {
		t.Error("model must embed gorm.Model")
	}
	if !strings.Contains(src, "type Customer struct") {
		t.Error("model struct must be named by the code symbol")
	}
}

// TestColumnFollowsStorageNameNotCodeName is the D2 crux: the column tag must
// track storage_name, not the Go field name GORM would otherwise derive from
// the code symbol. Without an explicit column tag a rename of storage_name is
// silently lost.
func TestColumnFollowsStorageNameNotCodeName(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Models(g)
	src := modelSource(t, files, "customer")

	// gofmt aligns struct fields with padding, so match the field line
	// tolerant of run-length whitespace rather than a fixed single space.
	emailLine := fieldLine(t, src, "Email")
	if !strings.Contains(emailLine, "string") {
		t.Errorf("Email field must have Go type string, got: %s", emailLine)
	}
	if !strings.Contains(emailLine, "column:contact_email") {
		t.Errorf("column tag must be the storage_name contact_email, got: %s", emailLine)
	}
	// The bug this guards against: GORM deriving the column from the field name.
	if strings.Contains(emailLine, "column:email;") || strings.Contains(emailLine, "column:email\"") {
		t.Errorf("column must not be derived from the code symbol, got: %s", emailLine)
	}
}

// fieldLine returns the generated struct-field line beginning with name.
func fieldLine(t *testing.T, src, name string) string {
	t.Helper()
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name+" ") {
			return line
		}
	}
	t.Fatalf("no field line for %q in:\n%s", name, src)
	return ""
}

func TestScalarFieldTags(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Models(g)
	customer := modelSource(t, files, "customer")
	invoice := modelSource(t, files, "invoice")

	// Assert only the gorm tag body, which is independent of gofmt alignment.
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"required unique string", customer, `gorm:"column:contact_email;size:255;not null;uniqueIndex"`},
		{"boolean with default", customer, `gorm:"column:active;default:true"`},
		{"enum default is quoted", customer, `gorm:"column:tier;default:'free'"`},
		{"datetime has no size", customer, `gorm:"column:joined_at"`},
		{"decimal is numeric not null", invoice, `gorm:"column:total;type:numeric;not null"`},
		{"integer index", invoice, `gorm:"column:line_count;index"`},
		{"date type", invoice, `gorm:"column:due_date;type:date"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !strings.Contains(test.src, test.want) {
				t.Errorf("missing tag fragment %q in:\n%s", test.want, test.src)
			}
		})
	}
}

// TestDecimalIsNeverFloat guards the money-as-float hazard: the flagship
// example's Invoice.total must be an exact decimal type.
func TestDecimalIsNeverFloat(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Models(g)
	invoice := modelSource(t, files, "invoice")

	if !strings.Contains(fieldLine(t, invoice, "Total"), "decimal.Decimal") {
		t.Error("decimal field must be decimal.Decimal")
	}
	if strings.Contains(invoice, "float32") || strings.Contains(invoice, "float64") {
		t.Errorf("a decimal field must never be a float:\n%s", invoice)
	}
	if !strings.Contains(invoice, `"github.com/shopspring/decimal"`) {
		t.Error("decimal import missing")
	}
}

// TestBelongsToBecomesForeignKey checks a relationship maps to a scalar FK
// column named by <CodeName>ID with the uint key type.
func TestBelongsToBecomesForeignKey(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Models(g)
	invoice := modelSource(t, files, "invoice")

	if !strings.Contains(fieldLine(t, invoice, "CustomerID"), "uint") {
		t.Error("belongs_to must generate a <Name>ID uint foreign key")
	}
	if !strings.Contains(invoice, "column:customer_id;not null") {
		t.Error("FK column must follow storage_name and honor required")
	}
	// The related struct is not embedded, so the invoice package must not
	// import the customer package (association wiring is a handler concern).
	if strings.Contains(invoice, "forge_generated/customer") {
		t.Error("model must not import the related resource package")
	}
}

func TestOnlyNeededImports(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Models(g)

	customer := modelSource(t, files, "customer")
	// Customer has no decimal field, so it must not import decimal.
	if strings.Contains(customer, "shopspring/decimal") {
		t.Error("customer model imports decimal it does not use")
	}
	// It does use time.
	if !strings.Contains(customer, `"time"`) {
		t.Error("customer model should import time")
	}
}

// TestModelsAreDeterministic is the reproducibility contract: same graph,
// byte-identical output, every time.
func TestModelsAreDeterministic(t *testing.T) {
	g, _ := buildGraph(t)

	first, err := Models(g)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	for i := 0; i < 25; i++ {
		next, err := Models(g)
		if err != nil {
			t.Fatalf("Models: %v", err)
		}
		if len(next) != len(first) {
			t.Fatalf("file count changed across runs")
		}
		for j := range first {
			if first[j].Path != next[j].Path || !bytes.Equal(first[j].Content, next[j].Content) {
				t.Fatalf("run %d differs at file %d (%s)", i, j, first[j].Path)
			}
		}
	}
}

func TestModelsNilGraph(t *testing.T) {
	if _, err := Models(nil); err == nil {
		t.Fatal("expected an error for a nil graph")
	}
}

// TestResolveTypeRejectsUnknown proves an unmapped field type is surfaced
// rather than emitted as a zero type. A validated spec can't carry one, so the
// field is constructed directly.
func TestResolveTypeRejectsUnknown(t *testing.T) {
	field := &graph.Field{Spec: &spec.Field{
		ID: spec.MustNewID(spec.KindField), Type: spec.FieldType("uuid"),
		CodeName: "Ref", StorageName: "ref",
	}}
	if _, err := resolveType(field); err == nil {
		t.Fatal("expected an error for an unmapped field type")
	}
}

// TestEmptyResourceStillGeneratesModel covers a resource with no fields: it
// must still produce a compilable struct embedding gorm.Model.
func TestEmptyResourceStillGeneratesModel(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: id(spec.KindResource), Label: "Tag", CodeName: "Tag", StorageName: "tags"},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	files, err := Models(g)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	src := modelSource(t, files, "tag")
	if !strings.Contains(src, "type Tag struct") || !strings.Contains(src, "gorm.Model") {
		t.Errorf("empty resource should still yield a gorm.Model struct:\n%s", src)
	}
}
