package gen

// handlersSrc renders handlers.go: the DTO, Huma input/output types, the CRUD
// handler methods, and the model→DTO converter. gofmt normalizes whitespace
// afterward, so the template favors clarity over precise indentation.
//
// Read semantics follow Gombit's own generated handlers: pagination via
// contract.ClampPage/PageOffset, the {data, meta} envelope via contract.Data
// and contract.DataMeta, and error translation via the database.Map*Error
// helpers (ADR-004 D3 — public APIs only).
const handlersSrc = `{{.Banner}}

package {{.Package}}

{{.ImportBlock}}

// {{.Type}}Data is the API representation of a {{.Type}}.
type {{.Data}} struct {
	ID uint ` + "`" + `json:"id" doc:"{{.Type}} identifier"` + "`" + `
{{- range .Fields}}
	{{.GoName}} {{.GoType}} ` + "`" + `json:"{{.JSONName}}" doc:"{{.GoName}}"` + "`" + `
{{- end}}
}

type list{{.Type}}Input struct {
	Page    int ` + "`" + `query:"page" doc:"1-based page"` + "`" + `
	PerPage int ` + "`" + `query:"per_page" doc:"Page size"` + "`" + `
{{- if .SearchColumns}}
	Search string ` + "`" + `query:"search" doc:"Case-insensitive search across searchable fields"` + "`" + `
{{- end}}
{{- if .SortColumns}}
	Ordering string ` + "`" + `query:"ordering" doc:"Sort field; prefix with - for descending"` + "`" + `
{{- end}}
{{- range .Filters}}
	{{.GoName}} string ` + "`" + `query:"{{.QueryName}}" doc:"Filter by {{.QueryName}}"` + "`" + `
{{- end}}
{{- if .AggregateColumns}}
	Aggregate string ` + "`" + `query:"aggregate" doc:"Server-side aggregates, comma-separated <func>:<field> (e.g. sum:total)"` + "`" + `
{{- end}}
}

type list{{.Type}}Output struct {
	Body contract.DataMeta[[]{{.Data}}, contract.{{if .AggregateColumns}}ListMeta{{else}}PageMeta{{end}}]
}

type get{{.Type}}Input struct {
	ID string ` + "`" + `path:"id" doc:"{{.Type}} identifier"` + "`" + `
}

type get{{.Type}}Output struct {
	Body contract.Data[{{.Data}}]
}
{{if or .Create .Update}}
// {{.Type}}Write is the writable body shared by create and update.
type {{.Type}}Write struct {
{{- range .Fields}}
	{{.GoName}} {{.GoType}} ` + "`" + `json:"{{.JSONName}}{{if .Optional}},omitempty{{end}}" doc:"{{.GoName}}"` + "`" + `
{{- end}}
}
{{end}}
{{- if .Create}}
type create{{.Type}}Input struct {
	Body {{.Type}}Write
}

type create{{.Type}}Output struct {
	Body contract.Data[{{.Data}}]
}
{{- end}}
{{- if .Update}}
type update{{.Type}}Input struct {
	ID   string ` + "`" + `path:"id" doc:"{{.Type}} identifier"` + "`" + `
	Body {{.Type}}Write
}

type update{{.Type}}Output struct {
	Body contract.Data[{{.Data}}]
}
{{- end}}
{{- if .Delete}}
type delete{{.Type}}Input struct {
	ID string ` + "`" + `path:"id" doc:"{{.Type}} identifier"` + "`" + `
}

type delete{{.Type}}Output struct{}
{{- end}}

// Handler serves {{.Package}} HTTP operations over GORM.
type Handler struct {
	DB *gorm.DB
}

func (h *Handler) list(ctx context.Context, input *list{{.Type}}Input) (*list{{.Type}}Output, error) {
	page, perPage := contract.ClampPage(input.Page, input.PerPage)
	q := h.DB.WithContext(ctx).Model(&{{.Type}}{})
{{- if or .Filters .SortColumns}}
	var err error
{{- end}}
{{- range .Filters}}
	if q, err = database.FilterEq(ctx, q, "{{.Column}}", database.{{.Kind}}, input.{{.GoName}}); err != nil {
		return nil, err
	}
{{- end}}
{{- if .SearchColumns}}
	q = database.Search(q, []string{ {{range .SearchColumns}}"{{.}}", {{end}}}, input.Search)
{{- end}}

	// Filters and search are applied before COUNT so the total reflects the
	// filtered set and pagination stays correct.
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, contract.WithContext(ctx, contract.Internal("list {{.PluralID}}"))
	}
{{- if .AggregateColumns}}

	// Aggregates run over the same filtered/searched query, before pagination
	// (and before ordering, which an aggregate SELECT cannot carry), over a
	// session snapshot so the ordering/limit applied below do not reach them.
	aggregateSpecs, err := database.ParseAggregates(ctx, input.Aggregate, map[string]database.AggregateColumn{
{{- range .AggregateColumns}}
		"{{.Field}}": {Column: "{{.Column}}"},
{{- end}}
	})
	if err != nil {
		return nil, err
	}
	aggregates, err := database.Aggregate(ctx, q.Session(&gorm.Session{}), aggregateSpecs)
	if err != nil {
		return nil, err
	}
{{- end}}
{{- if .SortColumns}}
	if q, err = database.Ordering(ctx, q, input.Ordering, []string{ {{range .SortColumns}}"{{.}}", {{end}}}, "id"); err != nil {
		return nil, err
	}
{{- else}}
	q = q.Order("id")
{{- end}}

	var rows []{{.Type}}
	if err := q.Offset(contract.PageOffset(page, perPage)).Limit(perPage).Find(&rows).Error; err != nil {
		return nil, contract.WithContext(ctx, contract.Internal("list {{.PluralID}}"))
	}

	items := make([]{{.Data}}, 0, len(rows))
	for _, row := range rows {
		items = append(items, to{{.Data}}(row))
	}
	return &list{{.Type}}Output{
		Body: contract.DataMeta[[]{{.Data}}, contract.{{if .AggregateColumns}}ListMeta{{else}}PageMeta{{end}}]{
			Data: items,
			Meta: &contract.{{if .AggregateColumns}}ListMeta{Page: page, PerPage: perPage, Total: total, Aggregates: aggregates}{{else}}PageMeta{Page: page, PerPage: perPage, Total: total}{{end}},
		},
	}, nil
}

func (h *Handler) get(ctx context.Context, input *get{{.Type}}Input) (*get{{.Type}}Output, error) {
	id, err := strconv.ParseUint(input.ID, 10, 64)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.NotFound("{{.SingularID}} not found"))
	}
	var row {{.Type}}
	if err := h.DB.WithContext(ctx).First(&row, uint(id)).Error; err != nil {
		return nil, database.MapLoadError(ctx, err, "{{.SingularID}} not found", "load {{.SingularID}}")
	}
	return &get{{.Type}}Output{Body: contract.Data[{{.Data}}]{Data: to{{.Data}}(row)}}, nil
}
{{- if .Create}}

func (h *Handler) create(ctx context.Context, input *create{{.Type}}Input) (*create{{.Type}}Output, error) {
	row := {{.Type}}{
{{- range .Fields}}
		{{.GoName}}: input.Body.{{.GoName}},
{{- end}}
	}
	if err := h.DB.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, database.MapPersistError(ctx, err, "{{.SingularID}} already exists", "create {{.SingularID}}")
	}
	return &create{{.Type}}Output{Body: contract.Data[{{.Data}}]{Data: to{{.Data}}(row)}}, nil
}
{{- end}}
{{- if .Update}}

func (h *Handler) update(ctx context.Context, input *update{{.Type}}Input) (*update{{.Type}}Output, error) {
	id, err := strconv.ParseUint(input.ID, 10, 64)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.NotFound("{{.SingularID}} not found"))
	}
	var row {{.Type}}
	if err := h.DB.WithContext(ctx).First(&row, uint(id)).Error; err != nil {
		return nil, database.MapLoadError(ctx, err, "{{.SingularID}} not found", "load {{.SingularID}}")
	}
{{- range .Fields}}
	row.{{.GoName}} = input.Body.{{.GoName}}
{{- end}}
	if err := h.DB.WithContext(ctx).Save(&row).Error; err != nil {
		return nil, database.MapPersistError(ctx, err, "{{.SingularID}} already exists", "update {{.SingularID}}")
	}
	return &update{{.Type}}Output{Body: contract.Data[{{.Data}}]{Data: to{{.Data}}(row)}}, nil
}
{{- end}}
{{- if .Delete}}

func (h *Handler) delete(ctx context.Context, input *delete{{.Type}}Input) (*delete{{.Type}}Output, error) {
	id, err := strconv.ParseUint(input.ID, 10, 64)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.NotFound("{{.SingularID}} not found"))
	}
	result := h.DB.WithContext(ctx).Delete(&{{.Type}}{}, uint(id))
	if result.Error != nil {
		return nil, database.MapDeleteError(ctx, result.Error, "{{.SingularID}} is referenced by other records", "delete {{.SingularID}}")
	}
	if result.RowsAffected == 0 {
		return nil, contract.WithContext(ctx, contract.NotFound("{{.SingularID}} not found"))
	}
	return &delete{{.Type}}Output{}, nil
}
{{- end}}

func to{{.Data}}(row {{.Type}}) {{.Data}} {
	return {{.Data}}{
		ID: row.ID,
{{- range .Fields}}
		{{.GoName}}: row.{{.GoName}},
{{- end}}
	}
}
`

// routesSrc renders routes.go: the package's Register entry point. Gombit does
// not discover feature packages by reflection, so main.go calls Register
// explicitly (ADR-001 §34 static, deterministic registration). Only the
// operations the resource enables are mounted.
const routesSrc = `{{.Banner}}

package {{.Package}}

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/gombit-dev/gombit/framework"
)

// Register mounts {{.Package}} Huma routes onto the application. Gombit emits
// the OpenAPI document and TypeScript client from these registrations.
func Register(app *framework.App) {
	h := &Handler{DB: app.DB()}
	prefix := app.Config().API.Prefix
	api := app.API()

	huma.Register(api, huma.Operation{
		OperationID: "list-{{.PluralID}}",
		Method:      http.MethodGet,
		Path:        prefix + "{{.CollectionPath}}",
		Summary:     "List {{.PluralID}}",
		Tags:        []string{"{{.Type}}"},
	}, h.list)

	huma.Register(api, huma.Operation{
		OperationID: "get-{{.SingularID}}",
		Method:      http.MethodGet,
		Path:        prefix + "{{.CollectionPath}}/{id}",
		Summary:     "Get a {{.SingularID}}",
		Tags:        []string{"{{.Type}}"},
	}, h.get)
{{- if .Create}}

	huma.Register(api, huma.Operation{
		OperationID:   "create-{{.SingularID}}",
		Method:        http.MethodPost,
		Path:          prefix + "{{.CollectionPath}}",
		Summary:       "Create a {{.SingularID}}",
		Tags:          []string{"{{.Type}}"},
		DefaultStatus: http.StatusCreated,
	}, h.create)
{{- end}}
{{- if .Update}}

	huma.Register(api, huma.Operation{
		OperationID: "update-{{.SingularID}}",
		Method:      http.MethodPut,
		Path:        prefix + "{{.CollectionPath}}/{id}",
		Summary:     "Update a {{.SingularID}}",
		Tags:        []string{"{{.Type}}"},
	}, h.update)
{{- end}}
{{- if .Delete}}

	huma.Register(api, huma.Operation{
		OperationID:   "delete-{{.SingularID}}",
		Method:        http.MethodDelete,
		Path:          prefix + "{{.CollectionPath}}/{id}",
		Summary:       "Delete a {{.SingularID}}",
		Tags:          []string{"{{.Type}}"},
		DefaultStatus: http.StatusNoContent,
	}, h.delete)
{{- end}}
}
`

// adminSrc renders admin.go: registration of the model with Gombit's admin via
// admin.Register (ADR-004 D3). Fields are left to Gombit's derivation from the
// GORM model; slug, labels and the action toggles are declared here.
const adminSrc = `{{.Banner}}

package {{.Package}}

import (
	"github.com/gombit-dev/gombit/admin"
	"github.com/gombit-dev/gombit/framework"
)

// RegisterAdmin registers {{.Type}} with the Gombit admin. Called explicitly
// from main; Gombit does not discover feature packages by reflection.
func RegisterAdmin(app *framework.App) error {
	return admin.Register(app, {{.Type}}{}, admin.Options{
		Slug:     {{.Slug}},
		Singular: {{.Singular}},
{{- if .Plural}}
		Plural:   {{.Plural}},
{{- end}}
		Actions: admin.Actions{
			List:   true,
			Detail: true,
			Create: {{.Create}},
			Update: {{.Update}},
			Delete: {{.Delete}},
		},
	})
}
`
