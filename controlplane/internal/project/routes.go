package project

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gombit-dev/gombit/auth"
	"github.com/gombit-dev/gombit/framework"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/org"
)

// cookieSecurityName is the OpenAPI security scheme the admin plane also uses;
// naming it here documents that these routes are cookie-session protected.
const cookieSecurityName = "cookieAuth"

// Register mounts the project routes on the app. It is called explicitly from
// main — Gombit does not discover feature packages by reflection.
//
// Every operation sits behind the cookie-session gate, so the API is only
// reachable by an authenticated user; per-org authorization then happens inside
// each handler against the caller's role. The gate is wired in one place and
// pinned by a test, so removing it from any operation fails a test rather than
// shipping.
func Register(app *framework.App) error {
	authSvc, err := auth.NewService(app.DB(), app.Config())
	if err != nil {
		return err
	}
	RegisterRoutes(app.API(), app.Config().API.Prefix,
		huma.Middlewares{authSvc.RequireCookieSession()}, NewService(app.DB()), org.NewService(app.DB()))
	return nil
}

// RegisterRoutes wires the project operations onto api behind gate, serving svc
// and authorizing through authz. It is separated from Register so a test can
// mount the real routes and gate on any huma.API without a full framework.App.
func RegisterRoutes(api huma.API, prefix string, gate huma.Middlewares, svc *Service, authz *org.Service) {
	h := &Handler{svc: svc, authz: authz}
	security := []map[string][]string{{cookieSecurityName: {}}}
	tags := []string{"Projects"}

	huma.Register(api, huma.Operation{
		OperationID:   "create-project",
		Method:        http.MethodPost,
		Path:          prefix + "/organizations/{orgID}/projects",
		Summary:       "Create a project in an organization",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.createProject)

	huma.Register(api, huma.Operation{
		OperationID: "list-projects",
		Method:      http.MethodGet,
		Path:        prefix + "/organizations/{orgID}/projects",
		Summary:     "List an organization's projects",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.listProjects)

	huma.Register(api, huma.Operation{
		OperationID: "get-project",
		Method:      http.MethodGet,
		Path:        prefix + "/projects/{projectID}",
		Summary:     "Get a project",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.getProject)

	huma.Register(api, huma.Operation{
		OperationID: "get-project-health",
		Method:      http.MethodGet,
		Path:        prefix + "/projects/{projectID}/health",
		Summary:     "Get a project's three-state health (spec / ABI / build)",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.getProjectHealth)

	huma.Register(api, huma.Operation{
		OperationID: "get-project-spec",
		Method:      http.MethodGet,
		Path:        prefix + "/projects/{projectID}/spec",
		Summary:     "Get a project's current (head revision) spec",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.getProjectSpec)

	huma.Register(api, huma.Operation{
		OperationID:   "submit-project-candidate",
		Method:        http.MethodPost,
		Path:          prefix + "/projects/{projectID}/revisions",
		Summary:       "Submit a candidate spec; on a compatible transition records a new revision",
		Description:   "Validates the candidate and classifies the ABI transition against the current head. Never builds (D8): a breaking transition is returned as a 409 with its reasons rather than committed.",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.submitCandidate)

	huma.Register(api, huma.Operation{
		OperationID:   "add-resource",
		Method:        http.MethodPost,
		Path:          prefix + "/projects/{projectID}/resources",
		Summary:       "Add a resource from a label; the backend mints its code symbol",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.addResource)

	huma.Register(api, huma.Operation{
		OperationID:   "rename-resource",
		Method:        http.MethodPatch,
		Path:          prefix + "/projects/{projectID}/resources/{resourceID}",
		Summary:       "Rename a resource (labels only; ABI-neutral)",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.renameResource)

	huma.Register(api, huma.Operation{
		OperationID: "delete-resource",
		Method:      http.MethodDelete,
		Path:        prefix + "/projects/{projectID}/resources/{resourceID}",
		Summary:     "Delete a resource after dependency analysis",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.deleteResource)

	huma.Register(api, huma.Operation{
		OperationID:   "add-field",
		Method:        http.MethodPost,
		Path:          prefix + "/projects/{projectID}/resources/{resourceID}/fields",
		Summary:       "Add a field to a resource; the backend mints its code symbol",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.addField)

	huma.Register(api, huma.Operation{
		OperationID:   "update-field",
		Method:        http.MethodPatch,
		Path:          prefix + "/projects/{projectID}/resources/{resourceID}/fields/{fieldID}",
		Summary:       "Update a field; a type change is ABI-breaking and returned for validation",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.updateField)

	huma.Register(api, huma.Operation{
		OperationID: "delete-field",
		Method:      http.MethodDelete,
		Path:        prefix + "/projects/{projectID}/resources/{resourceID}/fields/{fieldID}",
		Summary:     "Delete a field (ABI-breaking; returned for validation)",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.deleteField)

	huma.Register(api, huma.Operation{
		OperationID:   "update-resource-behavior",
		Method:        http.MethodPatch,
		Path:          prefix + "/projects/{projectID}/resources/{resourceID}/behavior",
		Summary:       "Set a resource's CRUD toggles, admin visibility and field selections",
		Description:   "Replaces the resource's whole behavior: an omitted toggle or field list resets to its zero value (off / empty), so the client must send the complete behavior, not a delta.",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.updateBehavior)

	huma.Register(api, huma.Operation{
		OperationID:   "add-relationship",
		Method:        http.MethodPost,
		Path:          prefix + "/projects/{projectID}/resources/{resourceID}/relationships",
		Summary:       "Add a belongs_to relationship to another resource (has_many derived)",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.addRelationship)

	huma.Register(api, huma.Operation{
		OperationID:   "add-page",
		Method:        http.MethodPost,
		Path:          prefix + "/projects/{projectID}/pages",
		Summary:       "Add a structured page (table, form, detail or dashboard); the backend derives its slug",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.addPage)

	huma.Register(api, huma.Operation{
		OperationID:   "update-table-config",
		Method:        http.MethodPatch,
		Path:          prefix + "/projects/{projectID}/pages/{pageID}/table",
		Summary:       "Configure a resource_table page (label, title, columns, search, page size)",
		Description:   "Replaces the page's table configuration: omitted columns/toggles reset to their defaults, so the client sends the complete configuration, not a delta. ABI-neutral.",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.updateTableConfig)

	huma.Register(api, huma.Operation{
		OperationID:   "update-form-config",
		Method:        http.MethodPatch,
		Path:          prefix + "/projects/{projectID}/pages/{pageID}/form",
		Summary:       "Configure a resource_form page (label, layout, field subset/order)",
		Description:   "Replaces the page's form configuration; the client sends the complete configuration, not a delta. ABI-neutral.",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.updateFormConfig)

	huma.Register(api, huma.Operation{
		OperationID: "delete-page",
		Method:      http.MethodDelete,
		Path:        prefix + "/projects/{projectID}/pages/{pageID}",
		Summary:     "Delete a page (ABI-neutral; commits unless a nav entry still references it)",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.deletePage)

	huma.Register(api, huma.Operation{
		OperationID:   "set-navigation",
		Method:        http.MethodPut,
		Path:          prefix + "/projects/{projectID}/navigation",
		Summary:       "Replace the project's ordered navigation (dashboard / resource list / external URL entries)",
		Description:   "Replaces the whole navigation in authored order; the client sends the complete list, not a delta. ABI-neutral.",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.setNavigation)

	huma.Register(api, huma.Operation{
		OperationID:   "set-branding",
		Method:        http.MethodPut,
		Path:          prefix + "/projects/{projectID}/branding",
		Summary:       "Set the project's branding (name, logo, accent color, appearance)",
		Description:   "Replaces the whole branding; an all-empty body clears it to the generated defaults. ABI-neutral.",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.setBranding)
}
