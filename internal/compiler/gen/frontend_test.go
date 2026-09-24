package gen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

func frontendFiles(t *testing.T, g *graph.Graph) map[string]string {
	t.Helper()
	files, err := Frontend(g)
	if err != nil {
		t.Fatalf("Frontend: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[f.Path] = string(f.Content)
	}
	return out
}

func TestFrontendFileLayout(t *testing.T) {
	files := frontendFiles(t, buildGraph2(t))

	want := []string{
		// Detail/form/table/dashboard are all page-driven (named by the page slug).
		"frontend/src/forge_generated/customer/CustomerDetailPage.tsx",
		"frontend/src/forge_generated/customer/EditCustomerFormPage.tsx",
		"frontend/src/forge_generated/customer/CustomersTablePage.tsx",
		"frontend/src/forge_generated/invoice/InvoiceDetailPage.tsx",
		"frontend/src/forge_generated/invoice/EditInvoiceFormPage.tsx",
		"frontend/src/forge_generated/invoice/InvoicesTablePage.tsx",
		"frontend/src/forge_generated/dashboard/HomeDashboardPage.tsx",
		"frontend/src/forge_generated/resources.tsx",
	}
	for _, path := range want {
		if _, ok := files[path]; !ok {
			t.Errorf("missing generated file %s", path)
		}
	}
	if len(files) != len(want) {
		t.Errorf("file count: got %d want %d", len(files), len(want))
	}
}

func TestFrontendBannerOnEveryFile(t *testing.T) {
	for path, src := range frontendFiles(t, buildGraph2(t)) {
		if !strings.HasPrefix(src, tsBanner) {
			t.Errorf("%s missing the DO-NOT-EDIT banner", path)
		}
	}
}

// TestFrontendConsumesGeneratedClient is the D3 contract: pages use Gombit's
// generated OpenAPI client and types, not a hand-rolled one.
func TestFrontendConsumesGeneratedClient(t *testing.T) {
	files := frontendFiles(t, buildGraph2(t))
	table := files["frontend/src/forge_generated/customer/CustomersTablePage.tsx"]
	form := files["frontend/src/forge_generated/customer/EditCustomerFormPage.tsx"]
	detail := files["frontend/src/forge_generated/customer/CustomerDetailPage.tsx"]

	tableWants := []string{
		`from "../../api/client"`,
		`from "../../api/generated/client"`,
		`from "../../api/generated/schema"`,
		`paths["/api/v1/customers"]["get"]`,
		// The customer resource declares sortable fields, so the table always
		// wires the ?ordering= param (search stays off until a page opts in).
		`await client.GET("/api/v1/customers", { params: { query: { page, per_page: PAGE_SIZE, ordering: ordering || undefined } } })`,
	}
	for _, w := range tableWants {
		if !strings.Contains(table, w) {
			t.Errorf("table page must contain %q", w)
		}
	}

	formWants := []string{
		`from "react-hook-form"`,
		`from "../../api/formErrors"`,
		`applyContractErrors(setError, err)`,
		`await client.POST("/api/v1/customers", { body })`,
		`await client.PUT("/api/v1/customers/{id}"`,
	}
	for _, w := range formWants {
		if !strings.Contains(form, w) {
			t.Errorf("form page must contain %q", w)
		}
	}

	if !strings.Contains(detail, `await client.GET("/api/v1/customers/{id}"`) {
		t.Error("detail page must GET the record by id")
	}
	// No hand-rolled fetch/axios anywhere.
	for path, src := range files {
		if strings.Contains(src, "fetch(") || strings.Contains(src, "axios") {
			t.Errorf("%s must not hand-roll HTTP; use the Gombit client", path)
		}
	}
}

// TestFrontendFieldInputs maps each field type to the right form control.
func TestFrontendFieldInputs(t *testing.T) {
	files := frontendFiles(t, buildGraph2(t))
	customer := files["frontend/src/forge_generated/customer/EditCustomerFormPage.tsx"]
	invoice := files["frontend/src/forge_generated/invoice/EditInvoiceFormPage.tsx"]

	checks := []struct {
		name, src, want string
	}{
		{"string is text input", customer, `<input type="text" {...register("contact_email"`},
		{"boolean is checkbox", customer, `<input type="checkbox" {...register("active")`},
		{"enum is a select", customer, `<select {...register("tier")`},
		{"enum option free", customer, `<option value="free">{ "free" }</option>`},
		{"datetime is text (RFC3339 round-trips)", customer, `<input type="text" placeholder="YYYY-MM-DDTHH:MM:SSZ" {...register("joined_at")`},
		{"belongs_to is a relationship select", invoice, `<select {...register("customer_id"`},
		{"relationship options from the target", invoice, `{CustomerOptions.map((o) => (`},
		{"decimal is text", invoice, `<input type="text" {...register("total"`},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(c.src, c.want) {
				t.Errorf("missing %q", c.want)
			}
		})
	}
}

// TestFrontendFormValueTypes checks the TS form value type maps field types to
// string/number/boolean.
func TestFrontendFormValueTypes(t *testing.T) {
	files := frontendFiles(t, buildGraph2(t))
	invoice := files["frontend/src/forge_generated/invoice/EditInvoiceFormPage.tsx"]

	for _, want := range []string{
		"customer_id: number;", // belongs_to FK
		"total: string;",       // decimal
	} {
		if !strings.Contains(invoice, want) {
			t.Errorf("form values type must contain %q", want)
		}
	}
}

// TestFrontendRegistryRoutes checks resources.tsx registers the four routes per
// resource and imports the page components.
func TestFrontendRegistryRoutes(t *testing.T) {
	registry := frontendFiles(t, buildGraph2(t))["frontend/src/forge_generated/resources.tsx"]

	for _, want := range []string{
		`import { CustomerDetailPage } from "./customer/CustomerDetailPage"`,
		`import { EditCustomerFormPage } from "./customer/EditCustomerFormPage"`,
		`import { CustomersTablePage } from "./customer/CustomersTablePage"`,
		`{ path: "customers", element: <CustomersTablePage /> }`,
		// Form and detail routes are page-driven: the UI lives at the page's own
		// slug, not the resource route base.
		`{ path: "edit-customer/new", element: <EditCustomerFormPage /> }`,
		`{ path: "customer/:id", element: <CustomerDetailPage /> }`,
		`{ path: "edit-customer/:id/edit", element: <EditCustomerFormPage /> }`,
		`{ slug: "customers", title: "Customers", listPath: "/customers" }`,
		`export const generatedResourceRoutes: RouteObject[]`,
		`export const generatedResources: GeneratedResource[]`,
	} {
		if !strings.Contains(registry, want) {
			t.Errorf("registry must contain %q", want)
		}
	}
}

// TestFrontendPathUsesStorageName checks routes and API paths are kebab-cased
// storage names, exercised with a multi-word resource.
func TestFrontendPathUsesStorageName(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	resID := id(spec.KindResource)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{
				ID: resID, Label: "Order line", CodeName: "OrderLine",
				StorageName: "order_lines",
				Fields: []*spec.Field{
					{ID: id(spec.KindField), Label: "Qty", Type: spec.TypeInteger, CodeName: "Qty", StorageName: "qty"},
				},
			},
		},
		Pages: []*spec.Page{
			{ID: id(spec.KindPage), Slug: "order-lines", Label: "Order lines", Type: spec.PageResourceTable, Resource: resID},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// The API collection path comes from the resource storage name; the table's
	// own route comes from the page slug (here the same kebab string).
	table := frontendFiles(t, g)["frontend/src/forge_generated/orderline/OrderLinesTablePage.tsx"]
	if !strings.Contains(table, `client.GET("/api/v1/order-lines", { params:`) {
		t.Error("API path should be the kebab-cased storage name")
	}
	registry := frontendFiles(t, g)["frontend/src/forge_generated/resources.tsx"]
	if !strings.Contains(registry, `{ path: "order-lines", element: <OrderLinesTablePage /> }`) {
		t.Error("route should be the page slug")
	}
}

// TestFrontendHonorsToggles is the contract the handlers already enforce: the
// UI's write operations match the routes. It uses partial toggles so an
// always-full-CRUD implementation fails.
func TestFrontendHonorsToggles(t *testing.T) {
	t.Run("create only", func(t *testing.T) {
		g := toggledFrontendGraph(t, true, false, false)
		files := frontendFiles(t, g)
		form := files["frontend/src/forge_generated/customer/EditCustomerFormPage.tsx"]
		list := files["frontend/src/forge_generated/customer/CustomersTablePage.tsx"]
		detail := files["frontend/src/forge_generated/customer/CustomerDetailPage.tsx"]
		registry := files["frontend/src/forge_generated/resources.tsx"]

		if !strings.Contains(form, `client.POST(`) {
			t.Error("create-only form must POST")
		}
		if strings.Contains(form, `client.PUT(`) {
			t.Error("create-only form must not PUT")
		}
		if strings.Contains(form, "useEffect") {
			t.Error("create-only form must not load a record")
		}
		// The New link targets the form page's own slug, not the resource route.
		if !strings.Contains(list, `to="/edit-customer/new"`) {
			t.Error("create-only table must show a New link to the form page")
		}
		if strings.Contains(detail, "/edit") {
			t.Error("create-only detail must not show an Edit link")
		}
		if !strings.Contains(registry, `{ path: "edit-customer/new"`) {
			t.Error("registry must route the form page /new when create is on")
		}
		if strings.Contains(registry, `/:id/edit`) {
			t.Error("registry must not route /:id/edit when update is off")
		}
	})

	t.Run("update only", func(t *testing.T) {
		g := toggledFrontendGraph(t, false, true, false)
		files := frontendFiles(t, g)
		form := files["frontend/src/forge_generated/customer/EditCustomerFormPage.tsx"]
		list := files["frontend/src/forge_generated/customer/CustomersTablePage.tsx"]
		registry := files["frontend/src/forge_generated/resources.tsx"]

		if !strings.Contains(form, `client.PUT(`) {
			t.Error("update-only form must PUT")
		}
		if strings.Contains(form, `client.POST(`) {
			t.Error("update-only form must not POST")
		}
		if !strings.Contains(form, "const editing = true;") {
			t.Error("update-only form is always editing")
		}
		if strings.Contains(list, `/edit-customer/new`) {
			t.Error("update-only table must not show a New link")
		}
		if strings.Contains(registry, `{ path: "edit-customer/new"`) {
			t.Error("registry must not route /new when create is off")
		}
		if !strings.Contains(registry, `{ path: "edit-customer/:id/edit"`) {
			t.Error("registry must route the form page /:id/edit when update is on")
		}
	})

	t.Run("read only", func(t *testing.T) {
		g := toggledFrontendGraph(t, false, false, false)
		files := frontendFiles(t, g)

		if _, ok := files["frontend/src/forge_generated/customer/EditCustomerFormPage.tsx"]; ok {
			t.Error("a read-only resource must not get a form page")
		}
		registry := files["frontend/src/forge_generated/resources.tsx"]
		if strings.Contains(registry, "FormPage") {
			t.Error("registry must not import or route a form page for a read-only resource")
		}
		// The table (page-driven, independent of toggles) and detail still exist.
		for _, p := range []string{"CustomersTablePage.tsx", "CustomerDetailPage.tsx"} {
			if _, ok := files["frontend/src/forge_generated/customer/"+p]; !ok {
				t.Errorf("read-only resource still needs %s", p)
			}
		}
		// A read-only table shows no New link.
		if strings.Contains(files["frontend/src/forge_generated/customer/CustomersTablePage.tsx"], `/customers/new`) {
			t.Error("read-only table must not show a New link")
		}
	})
}

// toggledFrontendGraph applies the given toggles to every resource.
func toggledFrontendGraph(t *testing.T, create, update, del bool) *graph.Graph {
	t.Helper()
	g, _ := buildGraph(t)
	for _, resource := range g.Resources {
		resource.Spec.Behavior.CreateEnabled = create
		resource.Spec.Behavior.UpdateEnabled = update
		resource.Spec.Behavior.DeleteEnabled = del
	}
	return g
}

// TestFrontendDateFieldsRoundTrip guards the RFC 3339 representation: date and
// datetime use a text input holding the wire string, not a native
// date/datetime-local control that would corrupt the time.Time JSON.
func TestFrontendDateFieldsRoundTrip(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	eventID := id(spec.KindResource)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{
				ID: eventID, Label: "Event", CodeName: "Event", StorageName: "events",
				Behavior: spec.ResourceBehavior{CreateEnabled: true},
				Fields: []*spec.Field{
					{ID: id(spec.KindField), Label: "Day", Type: spec.TypeDate, CodeName: "Day", StorageName: "day"},
					{ID: id(spec.KindField), Label: "At", Type: spec.TypeDatetime, CodeName: "At", StorageName: "at"},
				},
			},
		},
		Pages: []*spec.Page{
			{ID: id(spec.KindPage), Slug: "edit-event", Label: "Edit event", Type: spec.PageResourceForm, Resource: eventID},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	form := frontendFiles(t, g)["frontend/src/forge_generated/event/EditEventFormPage.tsx"]

	// No native date controls, which cannot hold or produce RFC 3339.
	if strings.Contains(form, `type="date"`) || strings.Contains(form, `type="datetime-local"`) {
		t.Errorf("date fields must not use native date controls:\n%s", form)
	}
	if !strings.Contains(form, `<input type="text" placeholder="YYYY-MM-DDT00:00:00Z" {...register("day"`) {
		t.Error("date field must be a text input with an RFC 3339 placeholder")
	}
	if !strings.Contains(form, `<input type="text" placeholder="YYYY-MM-DDTHH:MM:SSZ" {...register("at"`) {
		t.Error("datetime field must be a text input with an RFC 3339 placeholder")
	}
	// The values are typed as strings (the wire format), not Date objects.
	if !strings.Contains(form, "day: string;") || !strings.Contains(form, "at: string;") {
		t.Error("date/datetime form values must be strings")
	}
	// An empty date must be dropped from the request body: time.Time rejects
	// "" but accepts a missing key.
	if !strings.Contains(form, `if (body.day === "") {`) || !strings.Contains(form, `delete (body as Partial<EventFormValues>).day;`) {
		t.Errorf("empty date must be omitted from the request body:\n%s", form)
	}
	if !strings.Contains(form, `if (body.at === "") {`) {
		t.Error("empty datetime must be omitted from the request body")
	}
}

// TestFrontendOmitsEmptyDecimal covers the other wire type that rejects "":
// decimal.Decimal. An empty optional decimal must be dropped from the body, not
// POSTed as "".
func TestFrontendOmitsEmptyDecimal(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	quoteID := id(spec.KindResource)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{
				ID: quoteID, Label: "Quote", CodeName: "Quote", StorageName: "quotes",
				Behavior: spec.ResourceBehavior{CreateEnabled: true},
				Fields: []*spec.Field{
					// Optional decimal, and a plain string alongside it.
					{ID: id(spec.KindField), Label: "Fee", Type: spec.TypeDecimal, CodeName: "Fee", StorageName: "fee"},
					{ID: id(spec.KindField), Label: "Note", Type: spec.TypeString, CodeName: "Note", StorageName: "note"},
				},
			},
		},
		Pages: []*spec.Page{
			{ID: id(spec.KindPage), Slug: "edit-quote", Label: "Edit quote", Type: spec.PageResourceForm, Resource: quoteID},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	form := frontendFiles(t, g)["frontend/src/forge_generated/quote/EditQuoteFormPage.tsx"]

	if !strings.Contains(form, `if (body.fee === "") {`) {
		t.Errorf("empty decimal must be omitted from the request body:\n%s", form)
	}
	// A plain string must NOT be omitted — "" is a valid string value.
	if strings.Contains(form, `if (body.note === "") {`) {
		t.Error("a string field must not be omitted; \"\" is a valid value")
	}
}

// TestFrontendEscapesLabels guards against a spec label breaking the module:
// unconstrained human text must become an inert JS string, not JSX or an
// unterminated literal.
func TestFrontendEscapesLabels(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	itemID := id(spec.KindResource)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{
				ID: itemID, Label: `Item "X"`, LabelPlural: "Items {n}",
				CodeName: "Item", StorageName: "items",
				Behavior: spec.ResourceBehavior{CreateEnabled: true},
				Fields: []*spec.Field{
					{ID: id(spec.KindField), Label: `Day "d"`, Type: spec.TypeString, CodeName: "Day", StorageName: "day", Required: true},
					{ID: id(spec.KindField), Label: "Tier", Type: spec.TypeEnum, CodeName: "Tier", StorageName: "tier",
						EnumValues: []spec.EnumValue{{Value: `a"b`}}},
				},
			},
		},
		// The table title carries the same unconstrained text so its escaping in
		// the table <h1> is exercised.
		Pages: []*spec.Page{
			{ID: id(spec.KindPage), Slug: "items", Label: "Items {n}", Type: spec.PageResourceTable, Resource: itemID},
			{ID: id(spec.KindPage), Slug: "edit-item", Label: "Edit item", Type: spec.PageResourceForm, Resource: itemID},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	files := frontendFiles(t, g)
	list := files["frontend/src/forge_generated/item/ItemsTablePage.tsx"]
	form := files["frontend/src/forge_generated/item/EditItemFormPage.tsx"]

	// The title "Items {n}" must be an inert string, not a JSX expression.
	if !strings.Contains(list, `<h1>{ "Items {n}" }</h1>`) {
		t.Errorf("table title must be escaped as a JS string:\n%s", list)
	}
	// A quote in a field label must be escaped inside the required message.
	if !strings.Contains(form, `required: "Day \"d\" is required"`) {
		t.Errorf("field label with a quote must be escaped in the required message:\n%s", form)
	}
	// A quote in an enum value must be escaped in both the attribute and text.
	if !strings.Contains(form, `<option value="a\"b">{ "a\"b" }</option>`) {
		t.Errorf("enum value with a quote must be escaped:\n%s", form)
	}
	// The raw unescaped forms must not appear.
	if strings.Contains(list, `<h1>Items {n}</h1>`) {
		t.Error("raw unescaped plural leaked into JSX")
	}
}

func TestFrontendIsDeterministic(t *testing.T) {
	g := buildGraph2(t)
	first, err := Frontend(g)
	if err != nil {
		t.Fatalf("Frontend: %v", err)
	}
	for i := 0; i < 20; i++ {
		next, err := Frontend(g)
		if err != nil {
			t.Fatalf("Frontend: %v", err)
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

// TestFrontendTableHonorsConfiguredColumns: the customers table pins columns
// [email, active], so those headers appear and the unlisted fields (tier,
// joined) do not — the table is driven by TableConfig.columns, not every field.
func TestFrontendTableHonorsConfiguredColumns(t *testing.T) {
	table := frontendFiles(t, buildGraph2(t))["frontend/src/forge_generated/customer/CustomersTablePage.tsx"]

	// Email is sortable (a toggle button carrying its label); Active is a plain
	// header. Both configured columns appear; the assertion tracks each one's
	// rendering rather than assuming a plain <th> for the sortable one.
	for _, want := range []string{
		`onClick={() => toggleSort("contact_email")}`,
		`{ "Email" }`,
		`<th>{ "Active" }</th>`,
	} {
		if !strings.Contains(table, want) {
			t.Errorf("configured column header missing: %q", want)
		}
	}
	for _, unwanted := range []string{`{ "Tier" }`, `{ "Joined" }`} {
		if strings.Contains(table, unwanted) {
			t.Errorf("unconfigured column leaked into the table: %q", unwanted)
		}
	}
}

// TestFrontendTablePaginates checks the runtime-pagination slice of #51: the
// table sends the configured page size as per_page, pages through the result
// using the handler's PageMeta total, and renders Prev/Next controls.
func TestFrontendTablePaginates(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	res := id(spec.KindResource)
	build := func(pageSize int) *spec.ProjectSpec {
		table := &spec.TableConfig{PageSize: pageSize}
		if pageSize == 0 {
			table = nil // unconfigured: the default page size applies
		}
		return &spec.ProjectSpec{
			SpecVersion: spec.SpecVersion,
			Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
			Database:    spec.Database{Driver: spec.DriverPostgres},
			Auth:        spec.Auth{Mode: spec.AuthCookie},
			Resources: []*spec.Resource{
				{ID: res, Label: "Customer", CodeName: "Customer", StorageName: "customers",
					Fields: []*spec.Field{{ID: id(spec.KindField), Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"}}},
			},
			Pages: []*spec.Page{
				{ID: id(spec.KindPage), Slug: "customers", Label: "Customers", Type: spec.PageResourceTable, Resource: res, Table: table},
			},
		}
	}

	tableFor := func(s *spec.ProjectSpec) string {
		if d := spec.Validate(s); d != nil {
			t.Fatalf("fixture invalid: %s", d.Error())
		}
		g, err := graph.Build(s)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return frontendFiles(t, g)["frontend/src/forge_generated/customer/CustomersTablePage.tsx"]
	}

	// A configured page size is used verbatim.
	configured := tableFor(build(10))
	if !strings.Contains(configured, "const PAGE_SIZE = 10;") {
		t.Error("configured page size must drive PAGE_SIZE")
	}
	for _, want := range []string{
		`per_page: PAGE_SIZE`,
		`listed.meta?.total`,
		`aria-label="Pagination"`,
		`disabled={page <= 1}`,
		`disabled={page >= pageCount}`,
	} {
		if !strings.Contains(configured, want) {
			t.Errorf("paginated table must contain %q", want)
		}
	}

	// An unconfigured table falls back to the default page size (25).
	if def := tableFor(build(0)); !strings.Contains(def, "const PAGE_SIZE = 25;") {
		t.Error("an unconfigured table must default PAGE_SIZE to 25")
	}
}

// TestFrontendTableSearch: a table page that enables search over a resource
// declaring a searchable field renders the search box and wires the ?search=
// query param; a table without it keeps the plain paginated query unchanged.
func TestFrontendTableSearch(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	res := id(spec.KindResource)
	email := id(spec.KindField)
	build := func(search bool) *spec.ProjectSpec {
		behavior := spec.ResourceBehavior{}
		if search {
			behavior.SearchableFields = []spec.ID{email}
		}
		return &spec.ProjectSpec{
			SpecVersion: spec.SpecVersion,
			Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
			Database:    spec.Database{Driver: spec.DriverPostgres},
			Auth:        spec.Auth{Mode: spec.AuthCookie},
			Resources: []*spec.Resource{
				{ID: res, Label: "Customer", CodeName: "Customer", StorageName: "customers",
					Behavior: behavior,
					Fields:   []*spec.Field{{ID: email, Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"}}},
			},
			Pages: []*spec.Page{
				{ID: id(spec.KindPage), Slug: "customers", Label: "Customers", Type: spec.PageResourceTable, Resource: res,
					Table: &spec.TableConfig{Search: search}},
			},
		}
	}
	tableFor := func(s *spec.ProjectSpec) string {
		if d := spec.Validate(s); d != nil {
			t.Fatalf("fixture invalid: %s", d.Error())
		}
		g, err := graph.Build(s)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return frontendFiles(t, g)["frontend/src/forge_generated/customer/CustomersTablePage.tsx"]
	}

	searched := tableFor(build(true))
	for _, want := range []string{
		`const [search, setSearch] = useState("");`,
		`type="search"`,
		`search: search || undefined`,
		`}, [client, page, search]);`,
		`setPage(1);`,
	} {
		if !strings.Contains(searched, want) {
			t.Errorf("search-enabled table must contain %q", want)
		}
	}

	// Without search the query and deps are exactly the plain paginated shape —
	// no search state, box, or param leaks in.
	plain := tableFor(build(false))
	for _, absent := range []string{"const [search", `type="search"`, "search:"} {
		if strings.Contains(plain, absent) {
			t.Errorf("table without search must not contain %q", absent)
		}
	}
	if !strings.Contains(plain, `query: { page, per_page: PAGE_SIZE } }`) {
		t.Error("non-search table must keep the plain paginated query")
	}
	if !strings.Contains(plain, `}, [client, page]);`) {
		t.Error("non-search table must keep the plain effect deps")
	}
}

// TestFrontendTableSort: a table whose columns include sortable fields renders
// those headers as ?ordering= toggles and wires the param; a table with no
// sortable column keeps plain headers and the paginated query unchanged.
func TestFrontendTableSort(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	res := id(spec.KindResource)
	name := id(spec.KindField)
	active := id(spec.KindField)
	build := func(sortable bool) *spec.ProjectSpec {
		behavior := spec.ResourceBehavior{}
		if sortable {
			behavior.SortableFields = []spec.ID{name}
		}
		return &spec.ProjectSpec{
			SpecVersion: spec.SpecVersion,
			Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
			Database:    spec.Database{Driver: spec.DriverPostgres},
			Auth:        spec.Auth{Mode: spec.AuthCookie},
			Resources: []*spec.Resource{
				{ID: res, Label: "Customer", CodeName: "Customer", StorageName: "customers",
					Behavior: behavior,
					Fields: []*spec.Field{
						{ID: name, Label: "Name", Type: spec.TypeString, CodeName: "Name", StorageName: "name"},
						{ID: active, Label: "Active", Type: spec.TypeBoolean, CodeName: "Active", StorageName: "active"},
					}},
			},
			Pages: []*spec.Page{
				{ID: id(spec.KindPage), Slug: "customers", Label: "Customers", Type: spec.PageResourceTable, Resource: res},
			},
		}
	}
	tableFor := func(s *spec.ProjectSpec) string {
		if d := spec.Validate(s); d != nil {
			t.Fatalf("fixture invalid: %s", d.Error())
		}
		g, err := graph.Build(s)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return frontendFiles(t, g)["frontend/src/forge_generated/customer/CustomersTablePage.tsx"]
	}

	sorted := tableFor(build(true))
	for _, want := range []string{
		`const [ordering, setOrdering] = useState("");`,
		`const toggleSort = (col: string) => {`,
		`onClick={() => toggleSort("name")}`,
		`aria-sort={ordering === "name" ? "ascending" : ordering === "-name" ? "descending" : "none"}`,
		`ordering === "name" ? " ▲" : ordering === "-name" ? " ▼" : ""`,
		`ordering: ordering || undefined`,
		`}, [client, page, ordering]);`,
		// A non-sortable column in the same table stays a plain header.
		`<th>{ "Active" }</th>`,
	} {
		if !strings.Contains(sorted, want) {
			t.Errorf("sortable table must contain %q", want)
		}
	}

	plain := tableFor(build(false))
	for _, absent := range []string{"ordering", "toggleSort", "<button type=\"button\" onClick={() => toggleSort"} {
		if strings.Contains(plain, absent) {
			t.Errorf("table with no sortable column must not contain %q", absent)
		}
	}
	if !strings.Contains(plain, `query: { page, per_page: PAGE_SIZE } }`) {
		t.Error("non-sortable table must keep the plain paginated query")
	}
}

// TestFrontendTableFilters: a table exposing filter controls renders a
// type-derived widget per filter field (text/number/select) and wires each
// ?<storage_name>= query param; a table with no filters is unchanged.
func TestFrontendTableFilters(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	res := id(spec.KindResource)
	name := id(spec.KindField)
	age := id(spec.KindField)
	tier := id(spec.KindField)
	active := id(spec.KindField)
	build := func(filters []spec.ID) *spec.ProjectSpec {
		return &spec.ProjectSpec{
			SpecVersion: spec.SpecVersion,
			Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
			Database:    spec.Database{Driver: spec.DriverPostgres},
			Auth:        spec.Auth{Mode: spec.AuthCookie},
			Resources: []*spec.Resource{
				{ID: res, Label: "Customer", CodeName: "Customer", StorageName: "customers",
					Behavior: spec.ResourceBehavior{FilterableFields: []spec.ID{name, age, tier, active}},
					Fields: []*spec.Field{
						{ID: name, Label: "Name", Type: spec.TypeString, CodeName: "Name", StorageName: "name"},
						{ID: age, Label: "Age", Type: spec.TypeInteger, CodeName: "Age", StorageName: "age"},
						{ID: tier, Label: "Tier", Type: spec.TypeEnum, CodeName: "Tier", StorageName: "tier",
							EnumValues: []spec.EnumValue{{Value: "free", Label: "Free"}, {Value: "pro"}}},
						{ID: active, Label: "Active", Type: spec.TypeBoolean, CodeName: "Active", StorageName: "active"},
					}},
			},
			Pages: []*spec.Page{
				{ID: id(spec.KindPage), Slug: "customers", Label: "Customers", Type: spec.PageResourceTable, Resource: res,
					Table: &spec.TableConfig{Filters: filters}},
			},
		}
	}
	tableFor := func(s *spec.ProjectSpec) string {
		if d := spec.Validate(s); d != nil {
			t.Fatalf("fixture invalid: %s", d.Error())
		}
		g, err := graph.Build(s)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return frontendFiles(t, g)["frontend/src/forge_generated/customer/CustomersTablePage.tsx"]
	}

	filtered := tableFor(build([]spec.ID{name, age, tier, active}))
	for _, want := range []string{
		`const [filters, setFilters] = useState<Record<string, string>>({});`,
		`const setFilter = (col: string, value: string) => {`,
		// string → text, integer → number.
		`type="text"`,
		`onChange={(e) => setFilter("name", e.target.value)}`,
		`type="number"`,
		`onChange={(e) => setFilter("age", e.target.value)}`,
		// enum → select with an "any" option and the enum options.
		`onChange={(e) => setFilter("tier", e.target.value)}`,
		`<option value="">{ "Tier: any" }</option>`,
		`<option value="free">{ "Free" }</option>`,
		// boolean → yes/no/any select.
		`onChange={(e) => setFilter("active", e.target.value)}`,
		`<option value="true">{ "Yes" }</option>`,
		`<option value="false">{ "No" }</option>`,
		// query params + deps.
		`"name": filters["name"] || undefined`,
		`"active": filters["active"] || undefined`,
		`}, [client, page, filters]);`,
	} {
		if !strings.Contains(filtered, want) {
			t.Errorf("filtered table must contain %q", want)
		}
	}

	plain := tableFor(build(nil))
	for _, absent := range []string{"filters", "setFilter"} {
		if strings.Contains(plain, absent) {
			t.Errorf("table with no filters must not contain %q", absent)
		}
	}
	if !strings.Contains(plain, `query: { page, per_page: PAGE_SIZE } }`) {
		t.Error("non-filter table must keep the plain paginated query")
	}
}

// TestFrontendDetailEmbedsRelated (#53): a detail page embeds each has_many
// relationship as a table fetched from the related collection filtered by the
// back-reference FK; a resource with no has_many has no related section.
func TestFrontendDetailEmbedsRelated(t *testing.T) {
	files := frontendFiles(t, buildGraph2(t))

	// Customer has_many Invoice (Invoice.customer_id → Customer), so the customer
	// detail embeds invoices filtered by customer_id.
	customer := files["frontend/src/forge_generated/customer/CustomerDetailPage.tsx"]
	for _, want := range []string{
		`type Related0Row = NonNullable<`,
		`const RELATED_PAGE_SIZE = 10;`,
		`const [related0, setRelated0] = useState<Related0Row[]>([]);`,
		`await client.GET("/api/v1/invoices", { params: { query: { "customer_id": id, per_page: RELATED_PAGE_SIZE } } })`,
		`{related0.map((row) => (`,
		`<Link to="/invoices">View all`,
	} {
		if !strings.Contains(customer, want) {
			t.Errorf("customer detail must embed the invoices relationship: missing %q", want)
		}
	}

	// Invoice has no has_many relationship, so its detail has no related section.
	invoice := files["frontend/src/forge_generated/invoice/InvoiceDetailPage.tsx"]
	for _, absent := range []string{"RELATED_PAGE_SIZE", "related0", `aria-label="Related records"`} {
		if strings.Contains(invoice, absent) {
			t.Errorf("invoice detail (no has_many) must have no related section: found %q", absent)
		}
	}
}

// TestFrontendMultipleTablesPerResource is a #51 acceptance point: two
// resource_table pages can target one resource and generate independent tables
// with their own component, route and columns.
func TestFrontendMultipleTablesPerResource(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	cust := id(spec.KindResource)
	email := id(spec.KindField)
	active := id(spec.KindField)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{
				ID: cust, Label: "Customer", CodeName: "Customer", StorageName: "customers",
				Fields: []*spec.Field{
					{ID: email, Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"},
					{ID: active, Label: "Active", Type: spec.TypeBoolean, CodeName: "Active", StorageName: "active"},
				},
			},
		},
		Pages: []*spec.Page{
			{ID: id(spec.KindPage), Slug: "all-customers", Label: "All customers", Type: spec.PageResourceTable,
				Resource: cust, Table: &spec.TableConfig{Columns: []spec.ID{email}}},
			{ID: id(spec.KindPage), Slug: "active-customers", Label: "Active customers", Type: spec.PageResourceTable,
				Resource: cust, Table: &spec.TableConfig{Columns: []spec.ID{active}}},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	files := frontendFiles(t, g)

	all := files["frontend/src/forge_generated/customer/AllCustomersTablePage.tsx"]
	act := files["frontend/src/forge_generated/customer/ActiveCustomersTablePage.tsx"]
	if all == "" || act == "" {
		t.Fatalf("both table pages must be generated; got files %v", paths(frontendFilesSlice(t, g)))
	}
	// Each honors its own single column.
	if !strings.Contains(all, `<th>{ "Email" }</th>`) || strings.Contains(all, `{ "Active" }`) {
		t.Error("all-customers table must show only its Email column")
	}
	if !strings.Contains(act, `<th>{ "Active" }</th>`) || strings.Contains(act, `{ "Email" }`) {
		t.Error("active-customers table must show only its Active column")
	}
	// Both routes are registered independently, and both appear in nav metadata.
	registry := files["frontend/src/forge_generated/resources.tsx"]
	for _, want := range []string{
		`{ path: "all-customers", element: <AllCustomersTablePage /> }`,
		`{ path: "active-customers", element: <ActiveCustomersTablePage /> }`,
		`{ slug: "all-customers", title: "All customers", listPath: "/all-customers" }`,
		`{ slug: "active-customers", title: "Active customers", listPath: "/active-customers" }`,
	} {
		if !strings.Contains(registry, want) {
			t.Errorf("registry must contain %q", want)
		}
	}
}

// TestFrontendBranding is the #56 acceptance point: the spec's branding is
// generated as generatedBranding, applying defaults (app name → project name,
// appearance → system) and honoring a configured appearance/accent.
func TestFrontendBranding(t *testing.T) {
	// buildGraph2 sets no branding, so it defaults: app name from the project,
	// appearance "system".
	registry := frontendFiles(t, buildGraph2(t))["frontend/src/forge_generated/resources.tsx"]
	for _, want := range []string{
		`export const generatedBranding: Branding = {`,
		`appName: "Acme"`,
		`appearance: "system"`,
	} {
		if !strings.Contains(registry, want) {
			t.Errorf("default branding must contain %q", want)
		}
	}

	// A configured branding is generated verbatim.
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	res := id(spec.KindResource)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Branding:    &spec.Branding{AppName: "Shopfront", LogoRef: "/logo.svg", AccentColor: "#ff0000", Appearance: "dark"},
		Resources: []*spec.Resource{
			{ID: res, Label: "Customer", CodeName: "Customer", StorageName: "customers",
				Fields: []*spec.Field{{ID: id(spec.KindField), Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"}}},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	reg := frontendFiles(t, g)["frontend/src/forge_generated/resources.tsx"]
	for _, want := range []string{
		`appName: "Shopfront"`,
		`logoRef: "/logo.svg"`,
		`accentColor: "#ff0000"`,
		`appearance: "dark"`,
	} {
		if !strings.Contains(reg, want) {
			t.Errorf("configured branding must contain %q", want)
		}
	}
}

// TestFrontendNavigation is the #55 acceptance point: the spec's ordered
// navigation is generated as generatedNavigation, in authored order, with each
// entry pointing at a page route (dashboard / resource list) or an external URL.
func TestFrontendNavigation(t *testing.T) {
	registry := frontendFiles(t, buildGraph2(t))["frontend/src/forge_generated/resources.tsx"]

	if !strings.Contains(registry, `export const generatedNavigation: NavEntry[]`) {
		t.Fatal("registry must export generatedNavigation")
	}
	// Authored order: dashboard, resource list, external.
	want := []string{
		`{ label: "Home", to: "/home", external: false }`,
		`{ label: "Customers", to: "/customers", external: false }`,
		`{ label: "Docs", to: "https://example.com/docs", external: true }`,
	}
	last := -1
	for _, w := range want {
		i := strings.Index(registry, w)
		if i < 0 {
			t.Errorf("generatedNavigation must contain %q", w)
			continue
		}
		if i < last {
			t.Errorf("navigation entry out of authored order: %q", w)
		}
		last = i
	}
}

// TestFrontendDashboard is the #54 acceptance point: a dashboard page renders
// count cards with a real total (from the list PageMeta) and recent-list
// sections. buildGraph2's recent list declares no OrderBy, so it stays a labeled
// section with a "View all" link and fetches no records — the fallback when a
// card names no temporal field (records are exercised in
// TestFrontendDashboardRecentRecords). No chart designer.
func TestFrontendDashboard(t *testing.T) {
	files := frontendFiles(t, buildGraph2(t))
	dash := files["frontend/src/forge_generated/dashboard/HomeDashboardPage.tsx"]
	if dash == "" {
		t.Fatal("a dashboard page must generate a dashboard component")
	}

	// Count card: reads the total from the customers list via per_page=1.
	for _, want := range []string{
		`<h1>{ "Home" }</h1>`,
		`className="count-cards"`,
		`{ "Customers" }`,
		`client.GET("/api/v1/customers", { params: { query: { per_page: 1 } } })`,
		`listed.meta?.total ?? 0`,
	} {
		if !strings.Contains(dash, want) {
			t.Errorf("dashboard must contain %q", want)
		}
	}

	// Recent list without an OrderBy: a labeled section linking to the invoices
	// table, with NO record fetch (a fetch needs a temporal OrderBy).
	if !strings.Contains(dash, `{ "Recent invoices" }`) {
		t.Error("dashboard must render the recent-list section label")
	}
	if !strings.Contains(dash, `<Link to="/invoices">View all</Link>`) {
		t.Error("recent list must link to the related table page")
	}
	if strings.Contains(dash, "/api/v1/invoices") {
		t.Error("a recent list with no OrderBy must not fetch records")
	}

	// The dashboard is registered as a route and as nav metadata.
	registry := files["frontend/src/forge_generated/resources.tsx"]
	if !strings.Contains(registry, `{ path: "home", element: <HomeDashboardPage /> }`) {
		t.Error("dashboard must be registered as a route")
	}
	if !strings.Contains(registry, `{ slug: "home", title: "Home", listPath: "/home" }`) {
		t.Error("dashboard must appear in generatedResources (nav)")
	}
}

// TestFrontendDashboardRecentRecords (#54): a recent list that declares an
// OrderBy (a sortable date/datetime field) fetches the newest records
// (?ordering=-<field>&per_page=<limit>) and renders them; a list with no OrderBy
// keeps the section + View-all with no fetch.
func TestFrontendDashboardRecentRecords(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	res := id(spec.KindResource)
	due := id(spec.KindField)
	amount := id(spec.KindField)
	build := func(order spec.ID, limit int) *spec.ProjectSpec {
		return &spec.ProjectSpec{
			SpecVersion: spec.SpecVersion,
			Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
			Database:    spec.Database{Driver: spec.DriverPostgres},
			Auth:        spec.Auth{Mode: spec.AuthCookie},
			Resources: []*spec.Resource{
				{ID: res, Label: "Invoice", LabelPlural: "Invoices", CodeName: "Invoice", StorageName: "invoices",
					Behavior: spec.ResourceBehavior{SortableFields: []spec.ID{due}},
					Fields: []*spec.Field{
						{ID: due, Label: "Due", Type: spec.TypeDate, CodeName: "Due", StorageName: "due"},
						{ID: amount, Label: "Amount", Type: spec.TypeDecimal, CodeName: "Amount", StorageName: "amount"},
					}},
			},
			Pages: []*spec.Page{
				{ID: id(spec.KindPage), Slug: "home", Label: "Home", Type: spec.PageDashboard,
					Dashboard: &spec.DashboardConfig{RecentLists: []spec.DashboardCard{
						{Label: "Recent invoices", Resource: res, Limit: limit, OrderBy: order}}}},
			},
		}
	}
	dashFor := func(s *spec.ProjectSpec) string {
		if d := spec.Validate(s); d != nil {
			t.Fatalf("fixture invalid: %s", d.Error())
		}
		g, err := graph.Build(s)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return frontendFiles(t, g)["frontend/src/forge_generated/dashboard/HomeDashboardPage.tsx"]
	}

	withRecords := dashFor(build(due, 7))
	for _, want := range []string{
		`type Recent0Row = NonNullable<`,
		`const [recent0, setRecent0] = useState<Recent0Row[]>([]);`,
		`await client.GET("/api/v1/invoices", { params: { query: { ordering: "-due", per_page: 7 } } })`,
		`{recent0.map((row) => (`,
		`<th>{ "Due" }</th>`,
		`{recent0.length === 0 ? <p>{ "No Recent invoices yet." }</p> : null}`,
	} {
		if !strings.Contains(withRecords, want) {
			t.Errorf("recent list with OrderBy must contain %q", want)
		}
	}
	// Unconfigured limit falls back to the default (5).
	if def := dashFor(build(due, 0)); !strings.Contains(def, `per_page: 5 }`) {
		t.Error("a recent list with no limit must default per_page to 5")
	}

	// No OrderBy: section + View-all only, no fetch, no records state.
	plain := dashFor(build("", 5))
	for _, absent := range []string{"/api/v1/invoices", "recent0", "Recent0Row"} {
		if strings.Contains(plain, absent) {
			t.Errorf("a recent list without OrderBy must not fetch records: found %q", absent)
		}
	}
}

// TestFrontendDashboardAggregateCards (#182): an aggregate card fetches its
// server-side aggregate from the list endpoint (?aggregate=<op>:<field>&per_page=1,
// gombit #273) and renders the value from meta.aggregates; a dashboard with only
// aggregate cards still wires the client/effect imports (via HasFetches).
func TestFrontendDashboardAggregateCards(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	res := id(spec.KindResource)
	total := id(spec.KindField)
	count := id(spec.KindField)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: res, Label: "Invoice", LabelPlural: "Invoices", CodeName: "Invoice", StorageName: "invoices",
				Behavior: spec.ResourceBehavior{AggregatableFields: []spec.ID{total, count}},
				Fields: []*spec.Field{
					{ID: total, Label: "Total", Type: spec.TypeDecimal, CodeName: "Total", StorageName: "total"},
					{ID: count, Label: "Lines", Type: spec.TypeInteger, CodeName: "Lines", StorageName: "line_count"},
				}},
		},
		Pages: []*spec.Page{
			{ID: id(spec.KindPage), Slug: "home", Label: "Home", Type: spec.PageDashboard,
				Dashboard: &spec.DashboardConfig{AggregateCards: []spec.AggregateCard{
					{Label: "Total invoiced", Resource: res, Field: total, Op: spec.AggregateSum},
					{Label: "Max lines", Resource: res, Field: count, Op: spec.AggregateMax},
				}}},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dash := frontendFiles(t, g)["frontend/src/forge_generated/dashboard/HomeDashboardPage.tsx"]
	if dash == "" {
		t.Fatal("a dashboard page must generate a dashboard component")
	}
	for _, want := range []string{
		// The client/effect imports are emitted even with no count card / records.
		`import { useEffect, useState } from "react";`,
		`import { useApiClient } from "../../api/client";`,
		`const [aggregates, setAggregates] = useState<(string | null)[]>([null, null, ]);`,
		`client.GET("/api/v1/invoices", { params: { query: { aggregate: "sum:total", per_page: 1 } } })`,
		`listed.meta?.aggregates?.["sum:total"] ?? "0"`,
		`client.GET("/api/v1/invoices", { params: { query: { aggregate: "max:line_count", per_page: 1 } } })`,
		`listed.meta?.aggregates?.["max:line_count"] ?? "0"`,
		`className="aggregate-cards"`,
		`{ "Total invoiced" }`,
		`{aggregates[0] ?? "…"}`,
		`{ "Max lines" }`,
		`{aggregates[1] ?? "…"}`,
	} {
		if !strings.Contains(dash, want) {
			t.Errorf("aggregate-card dashboard must contain %q\n%s", want, dash)
		}
	}
	// No count cards and no records here, so the schema (paths) import — needed
	// only by a records recent list — must not appear.
	if strings.Contains(dash, `import type { paths }`) {
		t.Error("an aggregate-only dashboard must not import the row schema types")
	}
}

// TestFrontendDetailRelatedSections is the #53 acceptance point: a page-driven
// detail renders its own fields plus a section per has_many relationship, with a
// "View all" link to the related resource's table page when it has one. It does
// not embed related records (that awaits Gombit list filtering).
func TestFrontendDetailRelatedSections(t *testing.T) {
	files := frontendFiles(t, buildGraph2(t))
	// buildGraph2 has Invoice belongs_to Customer, and an invoices table page.
	detail := files["frontend/src/forge_generated/customer/CustomerDetailPage.tsx"]
	if detail == "" {
		t.Fatal("the resource_detail page must generate a detail component")
	}
	// The customer's own field appears, and the has_many section links to the
	// invoices table (the belongs_to has no inverse label, so the related
	// resource's plural label names the section).
	if !strings.Contains(detail, "Related") {
		t.Error("detail must render a Related section for has_many relationships")
	}
	// buildGraph's invoice has no plural label, so the section falls back to the
	// singular label "Invoice".
	if !strings.Contains(detail, `<Link to="/invoices">View all { "Invoice" }</Link>`) {
		t.Errorf("detail must link to the related resource's table page:\n%s", detail)
	}
	// The table row links to the detail page's slug (customer), not the route base.
	table := files["frontend/src/forge_generated/customer/CustomersTablePage.tsx"]
	if !strings.Contains(table, "/customer/${row.id}") {
		t.Error("table rows must link to the detail page slug")
	}

	// Invoice has no has_many, so its detail has no Related section.
	if strings.Contains(files["frontend/src/forge_generated/invoice/InvoiceDetailPage.tsx"], "Related") {
		t.Error("a resource with no has_many must have no Related section")
	}
}

// TestFrontendDetailRelatedWithoutTablePage: a has_many whose related resource
// has no table page still renders its section heading, but no "View all" link.
func TestFrontendDetailRelatedWithoutTablePage(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	customer := id(spec.KindResource)
	invoice := id(spec.KindResource)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: customer, Label: "Customer", CodeName: "Customer", StorageName: "customers",
				Fields: []*spec.Field{{ID: id(spec.KindField), Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"}}},
			{ID: invoice, Label: "Invoice", LabelPlural: "Invoices", CodeName: "Invoice", StorageName: "invoices",
				Fields: []*spec.Field{{ID: id(spec.KindField), Label: "Customer", Type: spec.TypeBelongsTo, CodeName: "Customer", StorageName: "customer_id", Required: true, Target: customer, InverseLabel: "Their invoices"}}},
		},
		Pages: []*spec.Page{
			// Customer detail, but NO invoice table page.
			{ID: id(spec.KindPage), Slug: "customer", Label: "Customer", Type: spec.PageResourceDetail, Resource: customer},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	detail := frontendFiles(t, g)["frontend/src/forge_generated/customer/CustomerDetailPage.tsx"]
	// The inverse label names the section, and there is no "View all" link.
	if !strings.Contains(detail, `{ "Their invoices" }`) {
		t.Errorf("section must use the belongs_to inverse label:\n%s", detail)
	}
	if strings.Contains(detail, "View all") {
		t.Error("no View all link when the related resource has no table page")
	}
}

// TestFrontendRelationshipSelector is the #52 acceptance point: a belongs_to
// field renders as a relationship selector whose options load from the target
// resource's list, showing the target's first text field.
func TestFrontendRelationshipSelector(t *testing.T) {
	// buildGraph2: Invoice belongs_to Customer; Customer's first string field is
	// Email (storage contact_email).
	form := frontendFiles(t, buildGraph2(t))["frontend/src/forge_generated/invoice/EditInvoiceFormPage.tsx"]

	for _, want := range []string{
		`const [CustomerOptions, setCustomerOptions] = useState<{ id: string; label: string }[]>([]);`,
		`await client.GET("/api/v1/customers", { params: { query: { per_page: 100 } } })`,
		`label: String(r["contact_email"] ?? r.id)`,
		`<select {...register("customer_id"`,
		`{CustomerOptions.map((o) => (`,
		`<option key={o.id} value={o.id}>{o.label}</option>`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("relationship selector must contain %q\n%s", want, form)
		}
	}
	// The FK is still numeric on submit.
	if !strings.Contains(form, "customer_id: number;") {
		t.Error("belongs_to FK stays a number in the form values")
	}
}

// TestFrontendFormHonorsConfig is the #52 follow-up: the generated form honors
// FormConfig — the configured field subset/order and the layout applied to the
// field container.
func TestFrontendFormHonorsConfig(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	res := id(spec.KindResource)
	email := id(spec.KindField)
	name := id(spec.KindField)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: res, Label: "Customer", CodeName: "Customer", StorageName: "customers",
				Behavior: spec.ResourceBehavior{CreateEnabled: true},
				Fields: []*spec.Field{
					{ID: name, Label: "Name", Type: spec.TypeString, CodeName: "Name", StorageName: "name"},
					{ID: email, Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"},
				}},
		},
		Pages: []*spec.Page{
			// Configure a two-column form showing only Email.
			{ID: id(spec.KindPage), Slug: "edit-customer", Label: "Edit customer", Type: spec.PageResourceForm, Resource: res,
				Form: &spec.FormConfig{Layout: "two_column", Fields: []spec.ID{email}}},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	form := frontendFiles(t, g)["frontend/src/forge_generated/customer/EditCustomerFormPage.tsx"]

	if !strings.Contains(form, `className="form-fields form-layout-two_column"`) {
		t.Errorf("form must apply the configured layout:\n%s", form)
	}
	// Only the configured field (Email) renders; Name is excluded.
	if !strings.Contains(form, `register("email"`) {
		t.Error("configured field must render")
	}
	if strings.Contains(form, `register("name"`) {
		t.Error("unconfigured field must not render")
	}
}

// TestFrontendFormDefaultLayout: an unconfigured form defaults to single_column
// and renders every field.
func TestFrontendFormDefaultLayout(t *testing.T) {
	form := frontendFiles(t, buildGraph2(t))["frontend/src/forge_generated/customer/EditCustomerFormPage.tsx"]
	if !strings.Contains(form, `className="form-fields form-layout-single_column"`) {
		t.Errorf("an unconfigured form must default to single_column:\n%s", form)
	}
}

// TestFrontendFormIsPageDriven is the #52 acceptance point: a resource_form
// page generates the create/edit UI at its own slug, the table "New" and detail
// "Edit" links target that slug, and a resource with no form page gets no
// create/edit UI at all.
func TestFrontendFormIsPageDriven(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	withForm := id(spec.KindResource)
	noForm := id(spec.KindResource)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: withForm, Label: "Customer", CodeName: "Customer", StorageName: "customers",
				Behavior: spec.ResourceBehavior{CreateEnabled: true, UpdateEnabled: true},
				Fields:   []*spec.Field{{ID: id(spec.KindField), Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"}}},
			{ID: noForm, Label: "Audit", CodeName: "Audit", StorageName: "audits",
				Behavior: spec.ResourceBehavior{CreateEnabled: true, UpdateEnabled: true},
				Fields:   []*spec.Field{{ID: id(spec.KindField), Label: "Note", Type: spec.TypeString, CodeName: "Note", StorageName: "note"}}},
		},
		Pages: []*spec.Page{
			{ID: id(spec.KindPage), Slug: "customers", Label: "Customers", Type: spec.PageResourceTable, Resource: withForm},
			{ID: id(spec.KindPage), Slug: "audits", Label: "Audits", Type: spec.PageResourceTable, Resource: noForm},
			// Only the customer has a form page; the audit has none. Both have a
			// detail page so the "Edit" link (or its absence) can be checked.
			{ID: id(spec.KindPage), Slug: "edit-customer", Label: "Edit customer", Type: spec.PageResourceForm, Resource: withForm},
			{ID: id(spec.KindPage), Slug: "customer", Label: "Customer", Type: spec.PageResourceDetail, Resource: withForm},
			{ID: id(spec.KindPage), Slug: "audit", Label: "Audit", Type: spec.PageResourceDetail, Resource: noForm},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	files := frontendFiles(t, g)

	// The customer's form page is generated at its slug; the table "New" and
	// detail "Edit" links target that slug (POST/PUT still hit the resource API).
	form := files["frontend/src/forge_generated/customer/EditCustomerFormPage.tsx"]
	if form == "" {
		t.Fatal("the resource_form page must generate a form component")
	}
	if !strings.Contains(form, `await client.POST("/api/v1/customers"`) {
		t.Error("the form still POSTs the resource's normal API endpoint")
	}
	if !strings.Contains(files["frontend/src/forge_generated/customer/CustomersTablePage.tsx"], `to="/edit-customer/new"`) {
		t.Error("the customer table 'New' link must target the form page slug")
	}
	if !strings.Contains(files["frontend/src/forge_generated/customer/CustomerDetailPage.tsx"], "/edit-customer/${id}/edit") {
		t.Error("the customer detail 'Edit' link must target the form page slug")
	}

	// The audit has no form page: no create/edit UI, no New/Edit links, no routes.
	for path := range files {
		if strings.Contains(path, "/audit/") && strings.Contains(path, "FormPage") {
			t.Errorf("a resource with no form page must get no create/edit UI; found %s", path)
		}
	}
	if strings.Contains(files["frontend/src/forge_generated/audit/AuditsTablePage.tsx"], "/new") {
		t.Error("the audit table must show no 'New' link (no form page)")
	}
	if strings.Contains(files["frontend/src/forge_generated/audit/AuditDetailPage.tsx"], "/edit") {
		t.Error("the audit detail must show no 'Edit' link (no form page)")
	}
	registry := files["frontend/src/forge_generated/resources.tsx"]
	if strings.Contains(registry, `element: <AuditFormPage`) || strings.Contains(registry, "audits/new") {
		t.Error("no form route for a resource without a form page")
	}
}

// TestFrontendNoImplicitList is the other #51 acceptance point, extended for the
// #53 page-driven detail: a resource with no pages at all gets no list page and
// no detail page — no file, route or nav entry — while a listed resource's list
// still comes from its table page.
func TestFrontendNoImplicitList(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	listed := id(spec.KindResource)
	unlisted := id(spec.KindResource)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: listed, Label: "Customer", CodeName: "Customer", StorageName: "customers",
				Fields: []*spec.Field{{ID: id(spec.KindField), Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"}}},
			{ID: unlisted, Label: "Secret", CodeName: "Secret", StorageName: "secrets",
				Fields: []*spec.Field{{ID: id(spec.KindField), Label: "Code", Type: spec.TypeString, CodeName: "Code", StorageName: "code"}}},
		},
		Pages: []*spec.Page{
			{ID: id(spec.KindPage), Slug: "customers", Label: "Customers", Type: spec.PageResourceTable, Resource: listed},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	files := frontendFiles(t, g)

	// The page-less resource gets no frontend files at all — no table, no detail.
	for path := range files {
		if strings.Contains(path, "/secret/") {
			t.Errorf("a resource with no pages must get no frontend files; found %s", path)
		}
	}
	// No route or nav entry for the page-less resource.
	registry := files["frontend/src/forge_generated/resources.tsx"]
	if strings.Contains(registry, `{ path: "secrets", element:`) {
		t.Error("no implicit list route for a resource without a table page")
	}
	if strings.Contains(registry, `slug: "secrets"`) {
		t.Error("no nav entry for a resource without a table page")
	}
	if strings.Contains(registry, `SecretDetailPage`) {
		t.Error("no detail route for a resource without a detail page")
	}
}

// TestPascalSlugIsInjective guards the component-name derivation: two distinct,
// valid slugs must never fold to the same identifier, or two committed pages
// could fail to build (the #134 pattern). The digit-adjacency case is the one
// that a naive PascalCase collides ("a1" vs "a-1").
func TestPascalSlugIsInjective(t *testing.T) {
	slugs := []string{"a1", "a-1", "ab", "a-b", "active-customers", "x1y", "x-1y", "order-lines"}
	seen := map[string]string{}
	for _, s := range slugs {
		got := pascalSlug(s)
		if other, dup := seen[got]; dup {
			t.Errorf("pascalSlug(%q) == pascalSlug(%q) == %q; must be injective", s, other, got)
		}
		seen[got] = s
	}
}

// TestFrontendDigitAdjacentSlugsBuild is the end-to-end proof of the fix: two
// resource_table pages with the once-colliding slugs "a1" and "a-1" now build
// to two distinct table components rather than tripping the Frontend guard.
func TestFrontendDigitAdjacentSlugsBuild(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	res := id(spec.KindResource)
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: res, Label: "Customer", CodeName: "Customer", StorageName: "customers",
				Fields: []*spec.Field{{ID: id(spec.KindField), Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"}}},
		},
		Pages: []*spec.Page{
			{ID: id(spec.KindPage), Slug: "a1", Label: "A1", Type: spec.PageResourceTable, Resource: res},
			{ID: id(spec.KindPage), Slug: "a-1", Label: "A 1", Type: spec.PageResourceTable, Resource: res},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture invalid: %s", d.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	files, err := Frontend(g)
	if err != nil {
		t.Fatalf("Frontend must not fail on distinct digit-adjacent slugs: %v", err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f.Path] = true
	}
	for _, want := range []string{
		"frontend/src/forge_generated/customer/A1TablePage.tsx",
		"frontend/src/forge_generated/customer/A_1TablePage.tsx",
	} {
		if !got[want] {
			t.Errorf("missing distinct table component %s", want)
		}
	}
}

func frontendFilesSlice(t *testing.T, g *graph.Graph) []File {
	t.Helper()
	files, err := Frontend(g)
	if err != nil {
		t.Fatalf("Frontend: %v", err)
	}
	return files
}

func TestFrontendNilGraph(t *testing.T) {
	if _, err := Frontend(nil); err == nil {
		t.Fatal("expected an error for a nil graph")
	}
}

func TestFrontendRejectsReservedResourceName(t *testing.T) {
	g := oneResourceGraph(t, "Main", "mains", field("Name", "name", spec.TypeString))
	if _, err := Frontend(g); err == nil {
		t.Fatal("Frontend must reject a resource folding to package main")
	}
}
