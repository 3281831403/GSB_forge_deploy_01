package gen

import (
	"bytes"
	"fmt"
	"path"
	"strings"
	"text/template"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Handlers generates handlers.go and routes.go per resource under
// internal/forge_generated/<resource>/ (DESIGN.md §9 stages 4–5).
//
// The generated code is an ordinary consumer of Gombit's public packages
// (ADR-004 D3): Huma operations over framework.App, the contract response
// envelope (D10 {data, meta}), and the database error mappers. It contains no
// Forge-specific router, ORM layer or response shape. OpenAPI and the
// TypeScript client are produced by Gombit from these Huma registrations, not
// reimplemented here (DESIGN.md P4).
//
// One list and one get operation are always generated; create, update and
// delete follow the resource's behavior toggles (DESIGN.md §4.3). Files come
// back in the graph's authored resource order, handlers.go before routes.go.
func Handlers(g *graph.Graph) ([]File, error) {
	if g == nil {
		return nil, fmt.Errorf("gen: nil graph")
	}
	if err := validatePackages(g); err != nil {
		return nil, err
	}

	files := make([]File, 0, len(g.Resources)*2)
	for _, resource := range g.Resources {
		if err := validateNames(resource); err != nil {
			return nil, err
		}
		view := newResourceView(resource)

		handlers, err := renderTemplate(handlersTemplate, view)
		if err != nil {
			return nil, err
		}
		handlersFile, err := formatGo(path.Join(PackageDir(resource), "handlers.go"), handlers)
		if err != nil {
			return nil, err
		}

		routes, err := renderTemplate(routesTemplate, view)
		if err != nil {
			return nil, err
		}
		routesFile, err := formatGo(path.Join(PackageDir(resource), "routes.go"), routes)
		if err != nil {
			return nil, err
		}

		files = append(files, handlersFile, routesFile)
	}
	return files, nil
}

// resourceView is the template data for one resource's handlers and routes.
type resourceView struct {
	Banner  string // the DO-NOT-EDIT banner, first line of every file
	Package string // customer
	Type    string // Customer
	Data    string // CustomerData
	Fields  []fieldView

	// CollectionPath is the resource's collection route, e.g. "/customers".
	CollectionPath string
	// Item ID fragments used in operation IDs.
	SingularID string // customer
	PluralID   string // customers

	Create bool
	Update bool
	Delete bool

	// The declared list-query surface (gombit #260), translated from the
	// resource's capability allowlists. Empty slices mean the list handler emits
	// no search/ordering/filter params and keeps its plain paginated shape.
	Filters       []filterView // one exact-match filter per filterable field + every belongs_to FK
	SearchColumns []string     // DB columns ?search= LIKEs across; empty ⇒ no search param
	SortColumns   []string     // DB columns ?ordering= may sort by; empty ⇒ no ordering param
	// AggregateColumns are the fields this resource declares aggregatable
	// (gombit #273). When non-empty the list handler gains an ?aggregate= param
	// and returns contract.ListMeta (PageMeta + aggregates); when empty the
	// handler keeps its plain contract.PageMeta shape, byte for byte.
	AggregateColumns []aggregateColumnView

	NeedsTime    bool
	NeedsDecimal bool

	// ImportBlock is the handler's complete `import (...)` block, built in Go
	// so the stdlib and third-party groups stay separated (template whitespace
	// trimming would otherwise collapse them into one group).
	ImportBlock string
}

// fieldView is one field's projection into the DTO and write model.
type fieldView struct {
	GoName   string // Email, or CustomerID for a belongs_to
	GoType   string // string, int64, decimal.Decimal, uint, ...
	JSONName string // storage_name, the stable API field name
	Optional bool   // omitempty in the write body
}

// filterView is one exact-match list filter: a string query param the handler
// coerces server-side to Kind and applies via database.FilterEq.
type filterView struct {
	GoName    string // list-input struct field, e.g. Email / CustomerID
	QueryName string // query-param name = the field's storage/column name
	Column    string // DB column the filter matches on
	Kind      string // database.FilterKind constant name, e.g. "FilterString"
}

// aggregateColumnView is one field a resource exposes to server-side
// aggregation: the ?aggregate=<op>:<field> field name (the storage/column name)
// mapped to its DB column via database.AggregateColumn.
type aggregateColumnView struct {
	Field  string // the ?aggregate= field name = the storage/column name
	Column string // DB column the aggregate function is applied to
}

// filterKind maps a Forge field type to the database.FilterKind constant the
// generated handler passes to database.FilterEq. Only filterable types reach
// here (validated in spec.validateBehavior); string and enum share FilterString.
func filterKind(t spec.FieldType) string {
	switch t {
	case spec.TypeInteger:
		return "FilterInt64"
	case spec.TypeBoolean:
		return "FilterBool"
	case spec.TypeBelongsTo:
		return "FilterUint"
	default: // string, enum
		return "FilterString"
	}
}

func newResourceView(resource *graph.Resource) resourceView {
	view := resourceView{
		Banner:         Banner,
		Package:        PackageName(resource),
		Type:           resource.CodeName(),
		Data:           resource.CodeName() + "Data",
		CollectionPath: "/" + kebab(resource.Spec.StorageName),
		SingularID:     kebab(PackageName(resource)),
		PluralID:       kebab(resource.Spec.StorageName),
		Create:         resource.Spec.Behavior.CreateEnabled,
		Update:         resource.Spec.Behavior.UpdateEnabled,
		Delete:         resource.Spec.Behavior.DeleteEnabled,
	}

	for _, field := range resource.Fields {
		mapping, _ := resolveType(field) // validated: never errors on a built graph
		if mapping.importPath == "time" {
			view.NeedsTime = true
		}
		if strings.HasPrefix(mapping.goType, "decimal.") {
			view.NeedsDecimal = true
		}
		view.Fields = append(view.Fields, fieldView{
			GoName:   goFieldName(field),
			GoType:   mapping.goType,
			JSONName: field.Spec.StorageName,
			Optional: !field.Spec.Required,
		})
	}

	// Translate the resource's declared query capabilities into the list-query
	// surface (gombit #260). A belongs_to FK is filterable by default — the same
	// isFilterable rule Gombit applies — so a detail page's has_many list can
	// fetch GET /children?<parent>_id=<id> with no extra declaration (#53); every
	// other filter opts in via FilterableFields. Field order is the resource's
	// authored field order for determinism, not the allowlist order.
	filterable := map[spec.ID]bool{}
	for _, f := range resource.Behavior.Filterable {
		filterable[f.Spec.ID] = true
	}
	for _, field := range resource.Fields {
		if !filterable[field.Spec.ID] && field.Spec.Type != spec.TypeBelongsTo {
			continue
		}
		view.Filters = append(view.Filters, filterView{
			GoName:    goFieldName(field),
			QueryName: field.Spec.StorageName,
			Column:    field.Spec.StorageName,
			Kind:      filterKind(field.Spec.Type),
		})
	}
	for _, f := range resource.Behavior.Searchable {
		view.SearchColumns = append(view.SearchColumns, f.Spec.StorageName)
	}
	for _, f := range resource.Behavior.Sortable {
		view.SortColumns = append(view.SortColumns, f.Spec.StorageName)
	}
	for _, f := range resource.Behavior.Aggregatable {
		view.AggregateColumns = append(view.AggregateColumns, aggregateColumnView{
			Field:  f.Spec.StorageName,
			Column: f.Spec.StorageName,
		})
	}

	// Import order is fixed, not map-derived, so output stays deterministic.
	std := []string{"context", "strconv"}
	if view.NeedsTime {
		std = append(std, "time")
	}
	third := []string{
		"github.com/gombit-dev/gombit/contract",
		"github.com/gombit-dev/gombit/database",
	}
	if view.NeedsDecimal {
		third = append(third, "github.com/shopspring/decimal")
	}
	third = append(third, "gorm.io/gorm")
	view.ImportBlock = importBlock(std, third)
	return view
}

// importBlock renders a two-group import block: stdlib, a blank line, then
// third-party. Each group is already in sorted order.
func importBlock(std, third []string) string {
	var b strings.Builder
	b.WriteString("import (\n")
	for _, path := range std {
		fmt.Fprintf(&b, "\t%q\n", path)
	}
	b.WriteString("\n")
	for _, path := range third {
		fmt.Fprintf(&b, "\t%q\n", path)
	}
	b.WriteString(")")
	return b.String()
}

// kebab converts a lower_snake_case storage identifier to kebab-case for use
// in a URL path or operation ID. Storage names are validated lower_snake_case,
// so this is a straight underscore-to-hyphen substitution.
func kebab(storageName string) string {
	return strings.ReplaceAll(storageName, "_", "-")
}

func renderTemplate(tmpl *template.Template, view any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, view); err != nil {
		return "", fmt.Errorf("gen: execute %s template: %w", tmpl.Name(), err)
	}
	return buf.String(), nil
}

var handlersTemplate = template.Must(template.New("handlers").Parse(handlersSrc))

var routesTemplate = template.Must(template.New("routes").Parse(routesSrc))

var adminTemplate = template.Must(template.New("admin").Parse(adminSrc))
