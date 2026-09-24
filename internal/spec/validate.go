package spec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Code identifies a class of validation failure. Codes are stable so the
// editor can map a diagnostic back to a specific control.
type Code string

const (
	CodeSpecVersion       Code = "spec_version_unsupported"
	CodeMissingID         Code = "missing_id"
	CodeMalformedID       Code = "malformed_id"
	CodeDuplicateID       Code = "duplicate_id"
	CodeInvalidSlug       Code = "invalid_slug"
	CodeDuplicateSlug     Code = "duplicate_slug"
	CodeInvalidStorage    Code = "invalid_storage_name"
	CodeDuplicateStore    Code = "duplicate_storage_name"
	CodeInvalidCodeName   Code = "invalid_code_name"
	CodeDuplicateCode     Code = "duplicate_code_name"
	CodeMissingLabel      Code = "missing_label"
	CodeUnknownType       Code = "unsupported_field_type"
	CodeDanglingRef       Code = "dangling_reference"
	CodeInvalidEnum       Code = "invalid_enum"
	CodeInvalidDriver     Code = "invalid_database_driver"
	CodeInvalidAuth       Code = "invalid_auth_mode"
	CodeInvalidPage       Code = "invalid_page"
	CodeInvalidNav        Code = "invalid_navigation"
	CodeEmptyProject      Code = "empty_project"
	CodeInvalidDefault    Code = "invalid_default"
	CodeReservedName      Code = "reserved_name"
	CodePageMismatch      Code = "page_config_mismatch"
	CodeInvalidHook       Code = "invalid_hook"
	CodeDuplicateForm     Code = "duplicate_form_page"
	CodeDuplicateDetail   Code = "duplicate_detail_page"
	CodeInvalidBranding   Code = "invalid_branding"
	CodeInvalidCapability Code = "invalid_query_capability"
)

// Diagnostic is one structured, machine-readable validation failure.
//
// Validation reports diagnostics rather than panicking or failing on the
// first problem, so the editor can surface every issue at once (ADR-001 §36).
type Diagnostic struct {
	Code    Code   `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
	// Entity is the stable ID of the offending entity when one is known, so
	// the editor can focus the right control even after a relabel.
	Entity ID `json:"entity,omitempty"`
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%s: %s (%s)", d.Path, d.Message, d.Code)
}

// Diagnostics is an ordered collection of validation failures.
type Diagnostics []Diagnostic

// Error renders the diagnostics as a single error message.
func (ds Diagnostics) Error() string {
	if len(ds) == 0 {
		return "spec: valid"
	}
	parts := make([]string, 0, len(ds))
	for _, diagnostic := range ds {
		parts = append(parts, diagnostic.String())
	}
	return fmt.Sprintf("spec: %d validation error(s):\n  %s", len(ds), strings.Join(parts, "\n  "))
}

// Has reports whether any diagnostic carries the given code.
func (ds Diagnostics) Has(code Code) bool {
	for _, diagnostic := range ds {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

// Codes returns the distinct codes present, sorted for stable comparison.
func (ds Diagnostics) Codes() []Code {
	seen := map[Code]struct{}{}
	for _, diagnostic := range ds {
		seen[diagnostic.Code] = struct{}{}
	}
	codes := make([]Code, 0, len(seen))
	for code := range seen {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	return codes
}

// validator accumulates diagnostics while walking a spec.
type validator struct {
	spec *ProjectSpec
	out  Diagnostics

	// seenIDs enforces that a stable ID identifies exactly one entity
	// anywhere in the spec (ADR-001 §36 "duplicate stable ID").
	seenIDs map[ID]string
}

func (v *validator) report(code Code, path string, entity ID, format string, args ...any) {
	v.out = append(v.out, Diagnostic{
		Code:    code,
		Path:    path,
		Entity:  entity,
		Message: fmt.Sprintf(format, args...),
	})
}

// claimID records an ID and reports a duplicate if it was already used.
func (v *validator) claimID(id ID, kind Kind, path string) {
	if id == "" {
		v.report(CodeMissingID, path, "", "missing stable id")
		return
	}
	if !id.Valid(kind) {
		v.report(CodeMalformedID, path, id, "malformed %s id %q", kind, id)
		return
	}
	if previous, taken := v.seenIDs[id]; taken {
		v.report(CodeDuplicateID, path, id, "stable id %q already used at %s", id, previous)
		return
	}
	v.seenIDs[id] = path
}

// Validate checks that a ProjectSpec is semantically well-formed.
//
// This answers only the "spec validity" question. It is deliberately
// independent of extension-ABI compatibility and of whether the project
// currently builds, which are separate states (ADR-001 §36).
//
// It returns nil when the spec is valid.
func Validate(s *ProjectSpec) Diagnostics {
	if s == nil {
		return Diagnostics{{
			Code: CodeEmptyProject, Path: "$", Message: "nil spec",
		}}
	}

	v := &validator{spec: s, seenIDs: map[ID]string{}}

	if s.SpecVersion != SpecVersion {
		v.report(CodeSpecVersion, "$.spec_version", "",
			"unsupported spec_version %d (expected %d)", s.SpecVersion, SpecVersion)
	}

	v.validateProject()
	v.validateResources()
	v.validatePages()
	v.validateNavigation()
	v.validateBranding()

	if len(v.out) == 0 {
		return nil
	}
	return v.out
}

func (v *validator) validateProject() {
	project := v.spec.Project
	v.claimID(project.ID, KindProject, "$.project.id")

	if strings.TrimSpace(project.Name) == "" {
		v.report(CodeMissingLabel, "$.project.name", project.ID, "project name is required")
	}
	if !IsSlug(project.Slug) {
		v.report(CodeInvalidSlug, "$.project.slug", project.ID,
			"project slug %q must be lower kebab-case", project.Slug)
	}

	switch v.spec.Database.Driver {
	case DriverPostgres, DriverSQLite, DriverMySQL:
	default:
		v.report(CodeInvalidDriver, "$.database.driver", project.ID,
			"unsupported database driver %q", v.spec.Database.Driver)
	}

	switch v.spec.Auth.Mode {
	case AuthCookie, AuthJWT:
	default:
		// "none" lands here on purpose: Gombit scaffolds jwt or cookie only,
		// so rejecting it at spec validation is the earliest honest failure
		// rather than letting it reach the toolchain and be refused there.
		v.report(CodeInvalidAuth, "$.auth.mode", project.ID,
			"unsupported auth mode %q (want %q or %q)",
			v.spec.Auth.Mode, AuthCookie, AuthJWT)
	}
}

func (v *validator) validateResources() {
	resourceCodeNames := map[string]ID{}
	resourceStorage := map[string]ID{}

	for resourceIndex, resource := range v.spec.Resources {
		path := fmt.Sprintf("$.resources[%d]", resourceIndex)
		if resource == nil {
			v.report(CodeEmptyProject, path, "", "nil resource entry")
			continue
		}

		v.claimID(resource.ID, KindResource, path+".id")

		if strings.TrimSpace(resource.Label) == "" {
			v.report(CodeMissingLabel, path+".label", resource.ID, "resource label is required")
		}

		if !IsExportedGoIdent(resource.CodeName) {
			v.report(CodeInvalidCodeName, path+".code_name", resource.ID,
				"code_name %q is not an exported Go identifier", resource.CodeName)
		} else if owner, taken := resourceCodeNames[resource.CodeName]; taken {
			v.report(CodeDuplicateCode, path+".code_name", resource.ID,
				"code_name %q already used by resource %s", resource.CodeName, owner)
		} else {
			resourceCodeNames[resource.CodeName] = resource.ID
		}

		if !IsStorageName(resource.StorageName) {
			v.report(CodeInvalidStorage, path+".storage_name", resource.ID,
				"storage_name %q must be lower_snake_case", resource.StorageName)
		} else if owner, taken := resourceStorage[resource.StorageName]; taken {
			v.report(CodeDuplicateStore, path+".storage_name", resource.ID,
				"storage_name %q already used by resource %s", resource.StorageName, owner)
		} else {
			resourceStorage[resource.StorageName] = resource.ID
		}

		v.validateFields(resource, path)
		v.validateBehavior(resource, path)
		v.validateHooks(resource, path)
	}
}

// validateHooks checks a resource's enabled lifecycle hooks: each has a valid
// claimed ID and a known event, and no event is enabled twice (the generated
// contract has one method per event, so a duplicate event would generate a
// duplicate method).
func (v *validator) validateHooks(resource *Resource, resourcePath string) {
	seenEvents := map[HookEvent]ID{}
	for hookIndex, hook := range resource.Hooks {
		path := fmt.Sprintf("%s.hooks[%d]", resourcePath, hookIndex)
		if hook == nil {
			v.report(CodeInvalidHook, path, resource.ID, "nil hook entry")
			continue
		}
		v.claimID(hook.ID, KindHook, path+".id")
		if !hook.Event.Valid() {
			v.report(CodeInvalidHook, path+".event", hook.ID,
				"unsupported lifecycle event %q", hook.Event)
			continue
		}
		if owner, dup := seenEvents[hook.Event]; dup {
			v.report(CodeInvalidHook, path+".event", hook.ID,
				"lifecycle event %q already enabled by hook %s", hook.Event, owner)
			continue
		}
		seenEvents[hook.Event] = hook.ID
	}
}

func (v *validator) validateFields(resource *Resource, resourcePath string) {
	// Code symbols and storage names must be unique within the resource's own
	// namespace; two resources may each legitimately have Email (ADR-001 §7).
	fieldCodeNames := map[string]ID{}
	fieldStorage := map[string]ID{}
	// generatedNames tracks the Go struct-field name each field emits — a
	// belongs_to derives its key as <CodeName>ID, which bare code_name uniqueness
	// never sees — so a belongs_to and a scalar that both generate the same struct
	// field are caught here, at edit time, rather than as a duplicate-field build
	// break (ADR-001 §12).
	generatedNames := map[string]ID{}

	for fieldIndex, field := range resource.Fields {
		path := fmt.Sprintf("%s.fields[%d]", resourcePath, fieldIndex)
		if field == nil {
			v.report(CodeEmptyProject, path, resource.ID, "nil field entry")
			continue
		}

		v.claimID(field.ID, KindField, path+".id")

		if strings.TrimSpace(field.Label) == "" {
			v.report(CodeMissingLabel, path+".label", field.ID, "field label is required")
		}

		if !field.Type.Valid() {
			v.report(CodeUnknownType, path+".type", field.ID,
				"unsupported field type %q", field.Type)
		}

		switch {
		case !IsExportedGoIdent(field.CodeName):
			v.report(CodeInvalidCodeName, path+".code_name", field.ID,
				"code_name %q is not an exported Go identifier", field.CodeName)
		// The generated model already defines these, so minting one would emit a
		// duplicate field or a field/method clash (ADR-001 §12).
		case IsReservedCodeName(field.CodeName):
			reason, _ := ReservedModelSymbol(field.CodeName)
			v.report(CodeReservedName, path+".code_name", field.ID,
				"code_name %q is reserved: %s", field.CodeName, reason)
		default:
			if owner, taken := fieldCodeNames[field.CodeName]; taken {
				v.report(CodeDuplicateCode, path+".code_name", field.ID,
					"code_name %q already used by field %s on this resource", field.CodeName, owner)
			} else {
				fieldCodeNames[field.CodeName] = field.ID
				// Only checked once the bare code_name is unique; a bare collision
				// already invalidates the spec, so this avoids a redundant report.
				generated := GeneratedModelFieldName(field.CodeName, field.Type)
				if generated != field.CodeName {
					if reason, reserved := ReservedModelSymbol(generated); reserved {
						v.report(CodeReservedName, path+".code_name", field.ID,
							"belongs_to code_name %q derives key %q, which is reserved: %s", field.CodeName, generated, reason)
					}
				}
				if owner, taken := generatedNames[generated]; taken {
					v.report(CodeDuplicateCode, path+".code_name", field.ID,
						"code_name %q generates Go struct field %q, already generated by field %s on this resource", field.CodeName, generated, owner)
				} else {
					generatedNames[generated] = field.ID
				}
			}
		}

		switch {
		case !IsStorageName(field.StorageName):
			v.report(CodeInvalidStorage, path+".storage_name", field.ID,
				"storage_name %q must be lower_snake_case", field.StorageName)
		case IsReservedStorageName(field.StorageName):
			reason, _ := ReservedStorageName(field.StorageName)
			v.report(CodeReservedName, path+".storage_name", field.ID,
				"storage_name %q is reserved: %s", field.StorageName, reason)
		default:
			if owner, taken := fieldStorage[field.StorageName]; taken {
				v.report(CodeDuplicateStore, path+".storage_name", field.ID,
					"storage_name %q already used by field %s on this resource", field.StorageName, owner)
			} else {
				fieldStorage[field.StorageName] = field.ID
			}
		}

		v.validateFieldType(field, path)
		v.validateFieldDefault(field, path)
	}
}

func (v *validator) validateFieldType(field *Field, path string) {
	switch field.Type {
	case TypeEnum:
		if len(field.EnumValues) == 0 {
			v.report(CodeInvalidEnum, path+".enum_values", field.ID,
				"enum field must declare at least one value")
		}
		seen := map[string]struct{}{}
		for valueIndex, enumValue := range field.EnumValues {
			valuePath := fmt.Sprintf("%s.enum_values[%d]", path, valueIndex)
			if strings.TrimSpace(enumValue.Value) == "" {
				v.report(CodeInvalidEnum, valuePath, field.ID, "enum value must not be empty")
				continue
			}
			if _, duplicate := seen[enumValue.Value]; duplicate {
				v.report(CodeInvalidEnum, valuePath, field.ID,
					"duplicate enum value %q", enumValue.Value)
				continue
			}
			seen[enumValue.Value] = struct{}{}
		}

	case TypeBelongsTo:
		if field.Target == "" {
			v.report(CodeDanglingRef, path+".target", field.ID,
				"belongs_to field must name a target resource")
			return
		}
		// A relationship pointing at a resource that no longer exists is the
		// canonical invalid state called out in ADR-001 §36.
		if v.spec.FindResource(field.Target) == nil {
			v.report(CodeDanglingRef, path+".target", field.ID,
				"belongs_to target %q does not exist", field.Target)
		}

	default:
		if len(field.EnumValues) > 0 {
			v.report(CodeInvalidEnum, path+".enum_values", field.ID,
				"enum_values is only valid on an enum field, not %q", field.Type)
		}
		if field.Target != "" {
			v.report(CodeDanglingRef, path+".target", field.ID,
				"target is only valid on a belongs_to field, not %q", field.Type)
		}
	}
}

// validateFieldDefault checks the declared default against the field's type.
//
// Defaults are stored as strings so the spec stays a plain JSON document, but
// an unparseable default is a semantic error, not a presentation detail: it
// would reach the generated model and the migration as a literal.
func (v *validator) validateFieldDefault(field *Field, path string) {
	if field.Default == nil {
		return
	}
	value := *field.Default
	defaultPath := path + ".default"

	switch field.Type {
	case TypeString, TypeText:
		// Any string is a legal value, but the default must also be
		// representable in generated output.
		if ok, bad := IsSafeDefaultValue(value); !ok {
			v.report(CodeInvalidDefault, defaultPath, field.ID,
				"default %q contains %q, which cannot be represented in generated output", value, bad)
		}

	case TypeInteger:
		// Base 10 is explicit so hex and Go underscore literals are refused.
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			v.report(CodeInvalidDefault, defaultPath, field.ID,
				"default %q is not a valid integer", value)
		}

	case TypeDecimal:
		if !isDecimalLiteral(value) {
			v.report(CodeInvalidDefault, defaultPath, field.ID,
				"default %q is not a valid decimal", value)
		}

	case TypeBoolean:
		// Deliberately stricter than strconv.ParseBool, which also accepts
		// "1", "t" and "TRUE". The default is emitted verbatim into the
		// generated model and the migration, so only the two literals
		// generation produces are legal.
		if value != "true" && value != "false" {
			v.report(CodeInvalidDefault, defaultPath, field.ID,
				`default %q is not a valid boolean (want "true" or "false")`, value)
		}

	case TypeDatetime:
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			v.report(CodeInvalidDefault, defaultPath, field.ID,
				"default %q is not an RFC 3339 datetime", value)
		}

	case TypeDate:
		if _, err := time.Parse(time.DateOnly, value); err != nil {
			v.report(CodeInvalidDefault, defaultPath, field.ID,
				"default %q is not a YYYY-MM-DD date", value)
		}

	case TypeEnum:
		// A default outside the declared values can never be selected. When it
		// is a declared value it is also emitted as a literal, so it must be
		// representable too.
		for _, enumValue := range field.EnumValues {
			if enumValue.Value == value {
				if ok, bad := IsSafeDefaultValue(value); !ok {
					v.report(CodeInvalidDefault, defaultPath, field.ID,
						"default %q contains %q, which cannot be represented in generated output", value, bad)
				}
				return
			}
		}
		v.report(CodeInvalidDefault, defaultPath, field.ID,
			"default %q is not one of the declared enum values", value)

	case TypeBelongsTo:
		v.report(CodeInvalidDefault, defaultPath, field.ID,
			"a belongs_to field may not declare a default")
	}
}

// validateBehavior checks that every field list references a field that
// actually belongs to this resource.
func (v *validator) validateBehavior(resource *Resource, resourcePath string) {
	lists := []struct {
		name string
		ids  []ID
	}{
		{"list_fields", resource.Behavior.ListFields},
		{"searchable_fields", resource.Behavior.SearchableFields},
		{"sortable_fields", resource.Behavior.SortableFields},
		{"filterable_fields", resource.Behavior.FilterableFields},
		{"aggregatable_fields", resource.Behavior.AggregatableFields},
	}

	for _, list := range lists {
		for index, fieldID := range list.ids {
			path := fmt.Sprintf("%s.behavior.%s[%d]", resourcePath, list.name, index)
			field := resource.FindField(fieldID)
			if field == nil {
				v.report(CodeDanglingRef, path, fieldID,
					"%s references field %q which is not on this resource", list.name, fieldID)
				continue
			}
			// The query capabilities constrain field type: the compiler
			// translates these allowlists into Gombit's per-field
			// filterable/sortable/searchable modifiers (gombit #260), which
			// reject types they cannot express. Catch it here, against the
			// resource, rather than as a downstream generate/build failure.
			if allowed, ok := capabilityAllowedTypes[list.name]; ok && !allowed[field.Type] {
				v.report(CodeInvalidCapability, path, fieldID,
					"%s cannot include field %q of type %q; supported types are %s",
					list.name, fieldID, field.Type, allowed.list())
			}
		}
	}
}

// typeSet is a set of field types a query capability accepts.
type typeSet map[FieldType]bool

// list renders the accepted types in the canonical declaration order, for a
// deterministic diagnostic message.
func (s typeSet) list() string {
	order := []FieldType{TypeString, TypeText, TypeInteger, TypeDecimal, TypeBoolean, TypeDatetime, TypeDate, TypeEnum, TypeBelongsTo}
	var names []string
	for _, t := range order {
		if s[t] {
			names = append(names, string(t))
		}
	}
	return strings.Join(names, ", ")
}

// capabilityAllowedTypes maps a behavior field-list name to the field types it
// accepts, mirroring Gombit's #260 per-type rules (resourcegen typeAllowsFilter/
// Search/Sort). list_fields and sortable_fields have no type restriction (every
// scalar and the belongs_to FK is orderable and listable), so they are absent
// and skip the check.
var capabilityAllowedTypes = map[string]typeSet{
	// Exact-match filter: text is for search not equality, and decimal/date/
	// datetime coercion is deferred (ranges come later).
	"filterable_fields": {TypeString: true, TypeInteger: true, TypeBoolean: true, TypeEnum: true, TypeBelongsTo: true},
	// Case-insensitive LIKE across text-like columns.
	"searchable_fields": {TypeString: true, TypeText: true, TypeEnum: true},
	// Numeric aggregate (SUM/AVG/MIN/MAX): the numeric field types only. A
	// belongs_to FK is stored numeric but aggregating foreign keys is
	// meaningless, so it is excluded (gombit #273).
	"aggregatable_fields": {TypeInteger: true, TypeDecimal: true},
}

func (v *validator) validatePages() {
	slugs := map[string]ID{}
	// The MVP allows at most one resource_form page per resource, so "the form
	// page" is unambiguous: the compiler generates its create/edit UI at the
	// page's own route and every "New"/"Edit" link targets it (DESIGN.md §18).
	formPageByResource := map[ID]ID{}
	// Likewise at most one resource_detail page per resource, so "the detail
	// page" a table row links to is unambiguous (DESIGN.md §4.4).
	detailPageByResource := map[ID]ID{}

	for pageIndex, page := range v.spec.Pages {
		path := fmt.Sprintf("$.pages[%d]", pageIndex)
		if page == nil {
			v.report(CodeEmptyProject, path, "", "nil page entry")
			continue
		}

		v.claimID(page.ID, KindPage, path+".id")

		if strings.TrimSpace(page.Label) == "" {
			v.report(CodeMissingLabel, path+".label", page.ID, "page label is required")
		}
		if !IsSlug(page.Slug) {
			v.report(CodeInvalidSlug, path+".slug", page.ID,
				"page slug %q must be lower kebab-case", page.Slug)
		} else if owner, taken := slugs[page.Slug]; taken {
			v.report(CodeDuplicateSlug, path+".slug", page.ID,
				"page slug %q already used by page %s", page.Slug, owner)
		} else {
			slugs[page.Slug] = page.ID
		}

		switch page.Type {
		case PageResourceTable, PageResourceForm, PageResourceDetail:
			v.validatePageConfigMatchesType(page, path)
			v.validateResourcePage(page, path)
			if page.Type == PageResourceForm && page.Resource != "" {
				if owner, taken := formPageByResource[page.Resource]; taken {
					v.report(CodeDuplicateForm, path+".resource", page.ID,
						"resource %s already has a form page (%s); at most one is allowed", page.Resource, owner)
				} else {
					formPageByResource[page.Resource] = page.ID
				}
			}
			if page.Type == PageResourceDetail && page.Resource != "" {
				if owner, taken := detailPageByResource[page.Resource]; taken {
					v.report(CodeDuplicateDetail, path+".resource", page.ID,
						"resource %s already has a detail page (%s); at most one is allowed", page.Resource, owner)
				} else {
					detailPageByResource[page.Resource] = page.ID
				}
			}
		case PageDashboard:
			v.validatePageConfigMatchesType(page, path)
			v.validateDashboardPage(page, path)
		default:
			v.report(CodeInvalidPage, path+".type", page.ID,
				"unsupported page type %q", page.Type)
		}
	}
}

// validatePageConfigMatchesType enforces that a page carries only the config
// block its type uses.
//
// Without this a resource_form could carry a Table block, or a table page a
// Form block, and every consumer would have to guess which one is
// authoritative. Making type the invariant lets the domain graph project
// exactly one view per page and lets generators trust it.
//
// A missing block is deliberately allowed: it means "use the defaults" (a
// table page with no explicit columns falls back to the resource's configured
// list fields), which is well defined. Only a contradictory block is an error.
func (v *validator) validatePageConfigMatchesType(page *Page, path string) {
	permitted := map[PageType]string{
		PageResourceTable:  "table",
		PageResourceForm:   "form",
		PageResourceDetail: "",
		PageDashboard:      "dashboard",
	}[page.Type]

	present := []struct {
		name string
		set  bool
	}{
		{"table", page.Table != nil},
		{"form", page.Form != nil},
		{"dashboard", page.Dashboard != nil},
	}

	for _, block := range present {
		if block.set && block.name != permitted {
			v.report(CodePageMismatch, path+"."+block.name, page.ID,
				"a %s page must not carry a %s configuration block",
				page.Type, block.name)
		}
	}
}

func (v *validator) validateResourcePage(page *Page, path string) {
	if page.Resource == "" {
		v.report(CodeDanglingRef, path+".resource", page.ID,
			"%s page must reference a resource", page.Type)
		return
	}

	resource := v.spec.FindResource(page.Resource)
	if resource == nil {
		v.report(CodeDanglingRef, path+".resource", page.ID,
			"page references resource %q which does not exist", page.Resource)
		return
	}

	// Column and form field references must belong to the page's resource.
	if page.Table != nil {
		for index, fieldID := range page.Table.Columns {
			if resource.FindField(fieldID) == nil {
				v.report(CodeDanglingRef, fmt.Sprintf("%s.table.columns[%d]", path, index), fieldID,
					"column references field %q which is not on resource %s", fieldID, resource.CodeName)
			}
		}
		if page.Table.PageSize < 0 {
			v.report(CodeInvalidPage, path+".table.page_size", page.ID,
				"page_size must not be negative")
		}
		// A table exposes only capabilities the resource declared (the resource
		// defines what queries are permitted; each table which permitted ones it
		// surfaces). Enabling the search box requires the resource to declare at
		// least one searchable field, or the generated ?search= control would
		// have nothing to match.
		if page.Table.Search && len(resource.Behavior.SearchableFields) == 0 {
			v.report(CodeInvalidCapability, path+".table.search", page.ID,
				"table enables search but resource %s declares no searchable_fields", resource.CodeName)
		}
		// Each filter control must reference a field the resource declared
		// filterable — the table exposes a subset of the resource's filter
		// capability. A filter field need not also be a visible column.
		filterable := make(map[ID]bool, len(resource.Behavior.FilterableFields))
		for _, fieldID := range resource.Behavior.FilterableFields {
			filterable[fieldID] = true
		}
		for index, fieldID := range page.Table.Filters {
			fpath := fmt.Sprintf("%s.table.filters[%d]", path, index)
			if resource.FindField(fieldID) == nil {
				v.report(CodeDanglingRef, fpath, fieldID,
					"filter references field %q which is not on resource %s", fieldID, resource.CodeName)
				continue
			}
			if !filterable[fieldID] {
				v.report(CodeInvalidCapability, fpath, fieldID,
					"filter references field %q which resource %s does not declare filterable", fieldID, resource.CodeName)
			}
		}
	}
	if page.Form != nil {
		for index, fieldID := range page.Form.Fields {
			if resource.FindField(fieldID) == nil {
				v.report(CodeDanglingRef, fmt.Sprintf("%s.form.fields[%d]", path, index), fieldID,
					"form references field %q which is not on resource %s", fieldID, resource.CodeName)
			}
		}
		switch page.Form.Layout {
		case "", "single_column", "two_column", "section_groups":
		default:
			v.report(CodeInvalidPage, path+".form.layout", page.ID,
				"unsupported form layout %q", page.Form.Layout)
		}
	}
}

func (v *validator) validateDashboardPage(page *Page, path string) {
	if page.Resource != "" {
		v.report(CodeInvalidPage, path+".resource", page.ID,
			"dashboard page must not reference a resource")
	}
	if page.Dashboard == nil {
		return
	}

	cards := []struct {
		name  string
		cards []DashboardCard
	}{
		{"count_cards", page.Dashboard.CountCards},
		{"recent_lists", page.Dashboard.RecentLists},
	}
	for _, group := range cards {
		for index, card := range group.cards {
			cardPath := fmt.Sprintf("%s.dashboard.%s[%d]", path, group.name, index)
			resource := v.spec.FindResource(card.Resource)
			if resource == nil {
				v.report(CodeDanglingRef, cardPath+".resource", page.ID,
					"dashboard card references resource %q which does not exist", card.Resource)
			}
			if strings.TrimSpace(card.Label) == "" {
				v.report(CodeMissingLabel, cardPath+".label", page.ID, "dashboard card label is required")
			}
			v.validateCardOrderBy(page, cardPath, group.name, card, resource)
		}
	}

	for index, card := range page.Dashboard.AggregateCards {
		cardPath := fmt.Sprintf("%s.dashboard.aggregate_cards[%d]", path, index)
		resource := v.spec.FindResource(card.Resource)
		if resource == nil {
			v.report(CodeDanglingRef, cardPath+".resource", page.ID,
				"aggregate card references resource %q which does not exist", card.Resource)
		}
		if strings.TrimSpace(card.Label) == "" {
			v.report(CodeMissingLabel, cardPath+".label", page.ID, "aggregate card label is required")
		}
		v.validateAggregateCard(page, cardPath, card, resource)
	}
}

// validateAggregateCard checks a dashboard aggregate card (#182): the op must be
// one of sum/avg/min/max, and the field must be a numeric field the card's
// resource declares aggregatable, so the generated ?aggregate=<op>:<field> is a
// permitted request against the list endpoint (gombit #273).
func (v *validator) validateAggregateCard(page *Page, cardPath string, card AggregateCard, resource *Resource) {
	if !card.Op.Valid() {
		v.report(CodeInvalidPage, cardPath+".op", page.ID,
			"aggregate card op %q must be one of sum, avg, min, max", card.Op)
	}
	if resource == nil {
		return
	}
	field := resource.FindField(card.Field)
	if field == nil {
		v.report(CodeDanglingRef, cardPath+".field", card.Field,
			"aggregate card field %q is not on resource %s", card.Field, resource.CodeName)
		return
	}
	aggregatable := false
	for _, id := range resource.Behavior.AggregatableFields {
		if id == card.Field {
			aggregatable = true
			break
		}
	}
	if !aggregatable {
		v.report(CodeInvalidCapability, cardPath+".field", card.Field,
			"aggregate card field %q must be declared aggregatable on resource %s", card.Field, resource.CodeName)
	}
}

// validateCardOrderBy checks a dashboard card's OrderBy (#54). A count card must
// not set it (it orders nothing). On a recent list it is optional, but when set
// it must reference a date/datetime field the card's resource declares sortable,
// so recency has real temporal meaning and the generated ?ordering=-<field> is a
// permitted ordering.
func (v *validator) validateCardOrderBy(page *Page, cardPath, group string, card DashboardCard, resource *Resource) {
	if group == "count_cards" {
		if card.OrderBy != "" {
			v.report(CodeInvalidPage, cardPath+".order_by", page.ID,
				"a count card does not order records; order_by is only valid on a recent list")
		}
		return
	}
	// recent_lists
	if card.OrderBy == "" || resource == nil {
		return
	}
	field := resource.FindField(card.OrderBy)
	if field == nil {
		v.report(CodeDanglingRef, cardPath+".order_by", card.OrderBy,
			"recent list order_by references field %q which is not on resource %s", card.OrderBy, resource.CodeName)
		return
	}
	sortable := false
	for _, id := range resource.Behavior.SortableFields {
		if id == card.OrderBy {
			sortable = true
			break
		}
	}
	if !sortable {
		v.report(CodeInvalidCapability, cardPath+".order_by", card.OrderBy,
			"recent list order_by field %q must be declared sortable on resource %s", card.OrderBy, resource.CodeName)
	}
	if field.Type != TypeDate && field.Type != TypeDatetime {
		v.report(CodeInvalidCapability, cardPath+".order_by", card.OrderBy,
			"recent list order_by field %q must be a date or datetime (got %q)", card.OrderBy, field.Type)
	}
}

func (v *validator) validateNavigation() {
	for index, item := range v.spec.Navigation {
		path := fmt.Sprintf("$.navigation[%d]", index)
		if item == nil {
			v.report(CodeEmptyProject, path, "", "nil navigation entry")
			continue
		}

		v.claimID(item.ID, KindNav, path+".id")

		if strings.TrimSpace(item.Label) == "" {
			v.report(CodeMissingLabel, path+".label", item.ID, "navigation label is required")
		}

		switch item.Target {
		case NavPage:
			if item.URL != "" {
				v.report(CodeInvalidNav, path+".url", item.ID,
					"page navigation entry must not set url")
			}
			if item.Page == "" {
				v.report(CodeDanglingRef, path+".page", item.ID,
					"page navigation entry must reference a page")
			} else if target := v.spec.FindPage(item.Page); target == nil {
				v.report(CodeDanglingRef, path+".page", item.ID,
					"navigation references page %q which does not exist", item.Page)
			} else if target.Type != PageDashboard && target.Type != PageResourceTable {
				// §4.5: navigation points to a dashboard or a resource list. A
				// detail/form page routes on an :id and cannot be a nav destination.
				v.report(CodeInvalidNav, path+".page", item.ID,
					"navigation must point to a dashboard or a resource_table page, not a %s page", target.Type)
			}

		case NavExternal:
			if item.Page != "" {
				v.report(CodeInvalidNav, path+".page", item.ID,
					"external navigation entry must not reference a page")
			}
			if !strings.HasPrefix(item.URL, "http://") && !strings.HasPrefix(item.URL, "https://") {
				v.report(CodeInvalidNav, path+".url", item.ID,
					"external navigation url %q must be http(s)", item.URL)
			}

		default:
			v.report(CodeInvalidNav, path+".target", item.ID,
				"unsupported navigation target %q", item.Target)
		}
	}
}

// validateBranding checks the MVP branding settings (DESIGN.md §19). Every field
// is optional; only a set-but-malformed value is an error. The accent color must
// be a hex color and appearance one of the three supported modes.
func (v *validator) validateBranding() {
	b := v.spec.Branding
	if b == nil {
		return
	}
	if b.AccentColor != "" && !isHexColor(b.AccentColor) {
		v.report(CodeInvalidBranding, "$.branding.accent_color", "",
			"accent color %q must be a hex color like #2563eb", b.AccentColor)
	}
	switch b.Appearance {
	case "", "light", "dark", "system":
	default:
		v.report(CodeInvalidBranding, "$.branding.appearance", "",
			"appearance %q must be light, dark or system", b.Appearance)
	}
}

// isHexColor reports whether s is a #rgb or #rrggbb hex color.
func isHexColor(s string) bool {
	if len(s) != 4 && len(s) != 7 {
		return false
	}
	if s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
