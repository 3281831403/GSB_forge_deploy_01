package gen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// handlerSources returns the handlers.go and routes.go for a package.
func handlerSources(t *testing.T, g *graph.Graph, pkg string) (handlers, routes string) {
	t.Helper()
	files, err := Handlers(g)
	if err != nil {
		t.Fatalf("Handlers: %v", err)
	}
	for _, f := range files {
		switch f.Path {
		case GeneratedRoot + "/" + pkg + "/handlers.go":
			handlers = string(f.Content)
		case GeneratedRoot + "/" + pkg + "/routes.go":
			routes = string(f.Content)
		}
	}
	if handlers == "" || routes == "" {
		t.Fatalf("missing generated files for %s; got %v", pkg, paths(files))
	}
	return handlers, routes
}

// toggledGraph builds the standard fixture with the given toggles applied to
// every resource.
func toggledGraph(t *testing.T, create, update, del bool) *graph.Graph {
	t.Helper()
	g, _ := buildGraph(t)
	for _, resource := range g.Resources {
		resource.Spec.Behavior.CreateEnabled = create
		resource.Spec.Behavior.UpdateEnabled = update
		resource.Spec.Behavior.DeleteEnabled = del
	}
	return g
}

func TestHandlersFilePaths(t *testing.T) {
	g, _ := buildGraph(t)
	files, err := Handlers(g)
	if err != nil {
		t.Fatalf("Handlers: %v", err)
	}
	want := []string{
		"internal/forge_generated/customer/handlers.go",
		"internal/forge_generated/customer/routes.go",
		"internal/forge_generated/invoice/handlers.go",
		"internal/forge_generated/invoice/routes.go",
	}
	got := paths(files)
	if len(got) != len(want) {
		t.Fatalf("file paths: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestHandlerHeaderAndEnvelope(t *testing.T) {
	handlers, _ := handlerSources(t, buildGraph2(t), "customer")

	if !strings.HasPrefix(handlers, Banner) {
		t.Error("handlers.go must start with the DO-NOT-EDIT banner")
	}
	if !strings.Contains(handlers, "package customer\n") {
		t.Error("wrong package clause")
	}
	// D10 envelope via the contract package, not a Forge-specific shape.
	for _, want := range []string{
		"contract.Data[CustomerData]",
		"contract.DataMeta[[]CustomerData, contract.PageMeta]",
		"contract.ClampPage(",
		"contract.PageOffset(",
	} {
		if !strings.Contains(handlers, want) {
			t.Errorf("handler must use %q", want)
		}
	}
}

// buildGraph2 is buildGraph with all toggles on, for the common case.
func buildGraph2(t *testing.T) *graph.Graph { return toggledGraph(t, true, true, true) }

// TestOperationsFollowToggles is the acceptance criterion "disabled operations
// are not routed": each of create/update/delete appears only when enabled.
func TestOperationsFollowToggles(t *testing.T) {
	cases := []struct {
		name                   string
		create, update, delete bool
	}{
		{"none", false, false, false},
		{"create only", true, false, false},
		{"update only", false, true, false},
		{"delete only", false, false, true},
		{"all", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := toggledGraph(t, tc.create, tc.update, tc.delete)
			handlers, routes := handlerSources(t, g, "customer")

			// The operation-id string is unique to its route, so its presence
			// is a whitespace-independent signal the route was mounted.
			assertPresence(t, "create route", routes, `"create-customer"`, tc.create)
			assertPresence(t, "update route", routes, `"update-customer"`, tc.update)
			assertPresence(t, "delete route", routes, `"delete-customer"`, tc.delete)

			assertPresence(t, "create handler", handlers, "func (h *Handler) create(", tc.create)
			assertPresence(t, "update handler", handlers, "func (h *Handler) update(", tc.update)
			assertPresence(t, "delete handler", handlers, "func (h *Handler) delete(", tc.delete)

			// list and get are always present.
			if !strings.Contains(routes, `"list-customers"`) {
				t.Error("list must always be routed")
			}
			if !strings.Contains(routes, `"get-customer"`) {
				t.Error("get must always be routed")
			}

			// A handler method with no route would be dead code, and a route to
			// a missing method would not compile: the two must agree.
			assertPresence(t, "create write body", handlers, "CustomerWrite", tc.create || tc.update)
		})
	}
}

func assertPresence(t *testing.T, what, haystack, needle string, want bool) {
	t.Helper()
	if got := strings.Contains(haystack, needle); got != want {
		t.Errorf("%s present=%v, want %v (looking for %q)", what, got, want, needle)
	}
}

// TestRouteMethodsAndStatuses pins the HTTP verbs and non-default statuses.
func TestRouteMethodsAndStatuses(t *testing.T) {
	_, routes := handlerSources(t, buildGraph2(t), "customer")

	// Alignment-independent tokens.
	checks := map[string]string{
		"create uses POST":     "http.MethodPost",
		"create returns 201":   "http.StatusCreated",
		"update uses PUT":      "http.MethodPut",
		"delete uses DELETE":   "http.MethodDelete",
		"delete returns 204":   "http.StatusNoContent",
		"item path has {id}":   `prefix + "/customers/{id}"`,
		"collection path":      `prefix + "/customers"`,
		"delegates to framewk": `"github.com/gombit-dev/gombit/framework"`,
		"mounts via huma":      "huma.Register(api,",
	}
	for name, want := range checks {
		if !strings.Contains(routes, want) {
			t.Errorf("%s: missing %q", name, want)
		}
	}
}

// TestPathsAndOperationIDsFromStorageName checks the collection path and
// operation IDs derive from storage_name (plural) and the package (singular),
// kebab-cased.
func TestPathsAndOperationIDsFromStorageName(t *testing.T) {
	// A resource whose storage_name is multi-word, to exercise kebab-casing.
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{
				ID: id(spec.KindResource), Label: "Order line", CodeName: "OrderLine",
				StorageName: "order_lines",
				Fields: []*spec.Field{
					{ID: id(spec.KindField), Label: "Qty", Type: spec.TypeInteger, CodeName: "Qty", StorageName: "qty"},
				},
			},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	_, routes := handlerSources(t, g, "orderline")
	if !strings.Contains(routes, `Path:        prefix + "/order-lines"`) {
		t.Error("collection path should be kebab-cased plural storage name /order-lines")
	}
	if !strings.Contains(routes, `OperationID: "list-order-lines"`) {
		t.Error("list operation id should be list-order-lines")
	}
	if !strings.Contains(routes, `OperationID: "get-orderline"`) {
		t.Error("get operation id should use the singular package name")
	}
}

// TestListHandlerEmitsDeclaredQuerySurface: a resource that declares
// searchable/sortable/filterable fields generates the #260 list-query wiring —
// query params plus the database.FilterEq/Search/Ordering calls — and a
// belongs_to FK is filtered by default (no declaration needed, for #53).
func TestListHandlerEmitsDeclaredQuerySurface(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	vendorID := id(spec.KindResource)
	nameID, skuID, priceID := id(spec.KindField), id(spec.KindField), id(spec.KindField)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{
				ID: vendorID, Label: "Vendor", CodeName: "Vendor", StorageName: "vendors",
				Fields: []*spec.Field{
					{ID: id(spec.KindField), Label: "Name", Type: spec.TypeString, CodeName: "Name", StorageName: "name"},
				},
			},
			{
				ID: id(spec.KindResource), Label: "Product", CodeName: "Product", StorageName: "products",
				Fields: []*spec.Field{
					{ID: nameID, Label: "Name", Type: spec.TypeString, CodeName: "Name", StorageName: "name"},
					{ID: skuID, Label: "SKU", Type: spec.TypeString, CodeName: "SKU", StorageName: "sku"},
					{ID: priceID, Label: "Price", Type: spec.TypeInteger, CodeName: "Price", StorageName: "price"},
					{ID: id(spec.KindField), Label: "Vendor", Type: spec.TypeBelongsTo, CodeName: "Vendor",
						StorageName: "vendor_id", Target: vendorID, InverseLabel: "Products"},
				},
				Behavior: spec.ResourceBehavior{
					SearchableFields:   []spec.ID{nameID, skuID},
					SortableFields:     []spec.ID{nameID, priceID},
					FilterableFields:   []spec.ID{priceID},
					AggregatableFields: []spec.ID{priceID},
				},
			},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	handlers, _ := handlerSources(t, g, "product")

	// Query params on the list input.
	for _, want := range []string{`query:"search"`, `query:"ordering"`, `query:"price"`, `query:"vendor_id"`} {
		if !strings.Contains(handlers, want) {
			t.Errorf("list input missing %s\n%s", want, handlers)
		}
	}
	// name is searchable/sortable but not filterable, so it gets no filter param.
	if strings.Contains(handlers, `query:"name"`) {
		t.Error("name is not filterable and must not become a filter query param")
	}
	// The query is applied via the shared database primitives.
	wants := []string{
		`database.FilterEq(ctx, q, "price", database.FilterInt64, input.Price)`,
		`database.FilterEq(ctx, q, "vendor_id", database.FilterUint, input.VendorID)`,
		`database.Search(q, []string{"name", "sku"}, input.Search)`,
		`database.Ordering(ctx, q, input.Ordering, []string{"name", "price"}, "id")`,
		// Price is declared aggregatable (#182): the list gains an ?aggregate=
		// param, the ListMeta envelope, and the ParseAggregates/Aggregate wiring
		// over a session snapshot (before ordering/pagination).
		`query:"aggregate"`,
		`contract.DataMeta[[]ProductData, contract.ListMeta]`,
		`database.ParseAggregates(ctx, input.Aggregate, map[string]database.AggregateColumn{`,
		`"price": {Column: "price"},`,
		`database.Aggregate(ctx, q.Session(&gorm.Session{}), aggregateSpecs)`,
		`Aggregates: aggregates`,
	}
	for _, want := range wants {
		if !strings.Contains(handlers, want) {
			t.Errorf("generated list handler missing:\n\t%s\ngot:\n%s", want, handlers)
		}
	}
	// Only the declared numeric field is aggregatable; the non-numeric ones are not.
	if strings.Contains(handlers, `"name": {Column:`) {
		t.Error("a non-aggregatable field must not appear in the aggregate allowlist")
	}
}

// TestListHandlerWithoutCapabilitiesStaysPlain: a resource that declares no
// query capabilities and has no belongs_to keeps the plain paginated list —
// fixed id ordering, no search/ordering/filter params.
func TestListHandlerWithoutCapabilitiesStaysPlain(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{{
			ID: id(spec.KindResource), Label: "Note", CodeName: "Note", StorageName: "notes",
			Fields: []*spec.Field{
				{ID: id(spec.KindField), Label: "Body", Type: spec.TypeText, CodeName: "Body", StorageName: "body"},
			},
		}},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	handlers, _ := handlerSources(t, g, "note")

	if strings.Contains(handlers, `query:"search"`) || strings.Contains(handlers, `query:"ordering"`) {
		t.Error("a resource with no declared capabilities must not emit search/ordering params")
	}
	if strings.Contains(handlers, "database.FilterEq(") {
		t.Error("a resource with no filters and no belongs_to must emit no FilterEq calls")
	}
	if !strings.Contains(handlers, `q.Order("id")`) {
		t.Error("without a sortable set the list must keep the fixed id ordering")
	}
	// No aggregatable field: the plain PageMeta envelope, no ?aggregate= param,
	// no aggregate wiring — the output stays byte-identical to before #182.
	if !strings.Contains(handlers, "contract.PageMeta") {
		t.Error("a resource with no aggregatable field must keep the contract.PageMeta envelope")
	}
	if strings.Contains(handlers, "contract.ListMeta") || strings.Contains(handlers, `query:"aggregate"`) ||
		strings.Contains(handlers, "database.ParseAggregates(") {
		t.Error("a resource with no aggregatable field must emit no aggregate wiring")
	}
}

// TestDTOUsesStorageNameForJSON confirms the API field name is the stable
// storage_name, and the Go field the frozen code symbol (the same D2 split the
// model enforces).
func TestDTOUsesStorageNameForJSON(t *testing.T) {
	// Rename Email's storage to contact_email; the JSON key must follow it.
	g, ids := buildGraph(t)
	g.Resource(ids["customer"]).Fields[0].Spec.StorageName = "contact_email"
	// Re-validate to be safe.
	if d := spec.Validate(g.Spec); d != nil {
		t.Fatalf("mutated fixture invalid: %s", d.Error())
	}

	handlers, _ := handlerSources(t, g, "customer")
	line := fieldLine(t, handlers, "Email")
	if !strings.Contains(line, "string") || !strings.Contains(line, `json:"contact_email"`) {
		t.Errorf("DTO field must be code symbol Email with json tag contact_email, got: %s", line)
	}
}

func TestBelongsToRendersForeignKeyField(t *testing.T) {
	handlers, _ := handlerSources(t, buildGraph2(t), "invoice")
	// The relationship surfaces as the scalar FK in the DTO and write body.
	if !strings.Contains(handlers, "CustomerID uint") {
		t.Error("belongs_to should surface as CustomerID uint in the DTO")
	}
	if !strings.Contains(handlers, `json:"customer_id"`) {
		t.Error("FK JSON key should be the storage name customer_id")
	}
}

func TestHandlersAreDeterministic(t *testing.T) {
	g := buildGraph2(t)
	first, err := Handlers(g)
	if err != nil {
		t.Fatalf("Handlers: %v", err)
	}
	for i := 0; i < 20; i++ {
		next, err := Handlers(g)
		if err != nil {
			t.Fatalf("Handlers: %v", err)
		}
		if len(next) != len(first) {
			t.Fatalf("file count changed")
		}
		for j := range first {
			if first[j].Path != next[j].Path || !bytes.Equal(first[j].Content, next[j].Content) {
				t.Fatalf("run %d differs at %s", i, first[j].Path)
			}
		}
	}
}

func TestHandlersNilGraph(t *testing.T) {
	if _, err := Handlers(nil); err == nil {
		t.Fatal("expected an error for a nil graph")
	}
}

// TestHandlersRejectSamePackageFaultsAsModels confirms Handlers shares the
// package/name reservation, so it cannot emit an uncompilable package the model
// generator would have rejected.
func TestHandlersRejectSamePackageFaults(t *testing.T) {
	g := oneResourceGraph(t, "Main", "mains", field("Name", "name", spec.TypeString))
	if _, err := Handlers(g); err == nil {
		t.Fatal("Handlers must reject a resource folding to package main")
	}
}
