// Package spec defines ProjectSpec, the declarative source of truth for a
// Forge application (DESIGN.md P1, §7).
//
// The canonical storage form is JSON (DESIGN.md §7). Serialization preserves
// stable IDs, frozen code symbols and authored ordering so that semantically
// identical specs never produce formatting churn (ADR-001 §70).
//
// Three naming domains are kept deliberately separate (ADR-001 D2, §3):
//
//	ID           immutable semantic identity
//	Label        human presentation, freely mutable
//	StorageName  database mapping, mutable through migration intent
//	CodeName     generated source ABI, frozen unless an explicit refactor
package spec

// SpecVersion is the ProjectSpec schema version understood by this package.
const SpecVersion = 1

// FieldType is a semantic field type in the Forge MVP (DESIGN.md §4.2).
//
// has_many is derived from BelongsTo rather than authored; many-to-many is
// deferred.
type FieldType string

const (
	TypeString    FieldType = "string"
	TypeText      FieldType = "text"
	TypeInteger   FieldType = "integer"
	TypeDecimal   FieldType = "decimal"
	TypeBoolean   FieldType = "boolean"
	TypeDatetime  FieldType = "datetime"
	TypeDate      FieldType = "date"
	TypeEnum      FieldType = "enum"
	TypeBelongsTo FieldType = "belongs_to"
)

// FieldTypes lists every supported MVP field type in a stable order.
func FieldTypes() []FieldType {
	return []FieldType{
		TypeString, TypeText, TypeInteger, TypeDecimal, TypeBoolean,
		TypeDatetime, TypeDate, TypeEnum, TypeBelongsTo,
	}
}

// Valid reports whether t is a supported MVP field type.
func (t FieldType) Valid() bool {
	for _, candidate := range FieldTypes() {
		if t == candidate {
			return true
		}
	}
	return false
}

// Scalar reports whether the type carries a value directly rather than a
// reference to another resource.
func (t FieldType) Scalar() bool { return t.Valid() && t != TypeBelongsTo }

// DatabaseDriver is the persistence driver for the generated application.
//
// Managed hosting is PostgreSQL-only in the MVP (DESIGN.md D4); exported
// applications may target any driver Gombit supports.
type DatabaseDriver string

const (
	DriverPostgres DatabaseDriver = "postgres"
	DriverSQLite   DatabaseDriver = "sqlite"
	DriverMySQL    DatabaseDriver = "mysql"
)

// AuthMode is the authentication mode of the generated application.
//
// Managed applications default to cookie/session auth (DESIGN.md D5); JWT
// stays available for exported, API-oriented applications (§20).
//
// There is deliberately no "none": Gombit scaffolds jwt or cookie only, so an
// unauthenticated mode could be written into a spec but never built. Adding it
// would mean teaching Gombit the mode first (DESIGN.md P4).
type AuthMode string

const (
	AuthCookie AuthMode = "cookie"
	AuthJWT    AuthMode = "jwt"
)

// PageType enumerates the structured MVP page types (DESIGN.md §4.4).
//
// There is deliberately no freeform canvas (DESIGN.md P3, D6).
type PageType string

const (
	PageResourceTable  PageType = "resource_table"
	PageResourceForm   PageType = "resource_form"
	PageResourceDetail PageType = "resource_detail"
	PageDashboard      PageType = "dashboard"
)

// HookEvent is a backend lifecycle-extension point (ADR-001 §20). The set is
// deliberately small and closed; this is not a general plugin system.
type HookEvent string

const (
	HookBeforeCreate HookEvent = "before_create"
	HookAfterCreate  HookEvent = "after_create"
	HookBeforeUpdate HookEvent = "before_update"
	HookAfterUpdate  HookEvent = "after_update"
	HookBeforeDelete HookEvent = "before_delete"
	HookAfterDelete  HookEvent = "after_delete"
)

// HookEvents lists every supported lifecycle event in a stable order — the
// authored order the generated contract and stub follow (create, update,
// delete; before then after within each).
func HookEvents() []HookEvent {
	return []HookEvent{
		HookBeforeCreate, HookAfterCreate,
		HookBeforeUpdate, HookAfterUpdate,
		HookBeforeDelete, HookAfterDelete,
	}
}

// Valid reports whether e is a supported lifecycle event.
func (e HookEvent) Valid() bool {
	for _, candidate := range HookEvents() {
		if e == candidate {
			return true
		}
	}
	return false
}

// ProjectSpec is the complete declarative description of a Forge application.
//
// Field order in this struct defines key order in canonical JSON.
type ProjectSpec struct {
	SpecVersion int         `json:"spec_version"`
	Project     Project     `json:"project"`
	Database    Database    `json:"database"`
	Auth        Auth        `json:"auth"`
	Resources   []*Resource `json:"resources"`
	Pages       []*Page     `json:"pages"`
	Navigation  []*NavItem  `json:"navigation"`
	Branding    *Branding   `json:"branding,omitempty"`
}

// Project carries project-level identity and presentation.
type Project struct {
	ID    ID     `json:"id"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Label string `json:"label,omitempty"`
}

// Database selects the persistence driver.
type Database struct {
	Driver DatabaseDriver `json:"driver"`
}

// Auth selects the authentication mode.
type Auth struct {
	Mode AuthMode `json:"mode"`
}

// Resource is one addressable domain entity.
type Resource struct {
	ID          ID     `json:"id"`
	Label       string `json:"label"`
	LabelPlural string `json:"label_plural,omitempty"`
	// CodeName is the frozen Go symbol for this resource (ADR-001 D3).
	CodeName string `json:"code_name"`
	// StorageName is the database table name (ADR-001 D2).
	StorageName string `json:"storage_name"`

	Fields []*Field `json:"fields"`

	Behavior ResourceBehavior `json:"behavior"`

	// Hooks are the enabled backend lifecycle extension points for this
	// resource, in authored order (ADR-001 §20, §34-35). Empty when the
	// resource has no custom behavior — the common case — so the field is
	// omitted from canonical JSON and adds nothing to a hook-free spec.
	Hooks []*Hook `json:"hooks,omitempty"`
}

// Hook is one enabled lifecycle extension point on a resource.
//
// Identity is the stable ID (ADR-001 D1); the Event names which lifecycle
// point. A hook has no code_name — the generated contract method name derives
// from the Event (AfterCreate, BeforeUpdate, …), a frozen mapping, so a hook
// mints no source symbol and never participates in symbol allocation.
type Hook struct {
	ID    ID        `json:"id"`
	Event HookEvent `json:"event"`
}

// ResourceBehavior captures per-resource CRUD and presentation settings
// (DESIGN.md §4.3).
type ResourceBehavior struct {
	CreateEnabled bool `json:"create_enabled"`
	UpdateEnabled bool `json:"update_enabled"`
	DeleteEnabled bool `json:"delete_enabled"`
	AdminVisible  bool `json:"admin_visible"`

	// The following reference field IDs belonging to the owning resource.
	ListFields       []ID `json:"list_fields,omitempty"`
	SearchableFields []ID `json:"searchable_fields,omitempty"`
	SortableFields   []ID `json:"sortable_fields,omitempty"`
	FilterableFields []ID `json:"filterable_fields,omitempty"`
	// AggregatableFields are the numeric fields (integer/decimal) this resource
	// exposes to server-side aggregation (#182). Each opts one field into the
	// gombit #273 aggregate contract, so the generated list endpoint accepts
	// ?aggregate=<op>:<field> over it; a dashboard aggregate card exposes a
	// subset. Non-numeric fields are rejected — an aggregate over them is
	// meaningless — mirroring how filterable/searchable constrain their types.
	AggregatableFields []ID `json:"aggregatable_fields,omitempty"`
}

// Field is one attribute of a Resource.
type Field struct {
	ID    ID        `json:"id"`
	Label string    `json:"label"`
	Type  FieldType `json:"type"`
	// CodeName is the frozen Go symbol / extension accessor name (ADR-001 D3, §23).
	CodeName string `json:"code_name"`
	// StorageName is the database column name (ADR-001 D2).
	StorageName string `json:"storage_name"`

	Required bool `json:"required,omitempty"`
	Unique   bool `json:"unique,omitempty"`
	Index    bool `json:"index,omitempty"`

	// Default is the literal default value, encoded as written in the spec.
	Default *string `json:"default,omitempty"`

	// EnumValues is required when Type is enum and empty otherwise.
	EnumValues []EnumValue `json:"enum_values,omitempty"`

	// Target is the referenced Resource ID when Type is belongs_to.
	Target ID `json:"target,omitempty"`
	// InverseLabel names the derived has_many side on the target resource.
	InverseLabel string `json:"inverse_label,omitempty"`
}

// EnumValue is one permitted value of an enum field.
type EnumValue struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// Page is one structured screen in the generated application.
type Page struct {
	ID    ID       `json:"id"`
	Slug  string   `json:"slug"`
	Label string   `json:"label"`
	Type  PageType `json:"type"`

	// Resource is required for every page type except dashboard.
	Resource ID `json:"resource,omitempty"`

	Table     *TableConfig     `json:"table,omitempty"`
	Form      *FormConfig      `json:"form,omitempty"`
	Dashboard *DashboardConfig `json:"dashboard,omitempty"`
}

// TableConfig configures a resource_table page (DESIGN.md §18).
type TableConfig struct {
	Title   string `json:"title,omitempty"`
	Columns []ID   `json:"columns,omitempty"`
	Search  bool   `json:"search,omitempty"`
	// Filters are the field IDs this table exposes as exact-match filter
	// controls (#51). Each must reference a field on the page's resource that is
	// in ResourceBehavior.FilterableFields — a table exposes a subset of the
	// filter capability the resource declares. A filter field need not also be a
	// visible column.
	Filters  []ID `json:"filters,omitempty"`
	PageSize int  `json:"page_size,omitempty"`
}

// FormConfig configures a resource_form page (DESIGN.md §18).
//
// Layout is structured; there is no absolute positioning.
type FormConfig struct {
	Layout string `json:"layout,omitempty"`
	Fields []ID   `json:"fields,omitempty"`
}

// DashboardConfig configures the MVP dashboard (DESIGN.md §4.4).
//
// There is deliberately no arbitrary chart designer.
type DashboardConfig struct {
	CountCards     []DashboardCard `json:"count_cards,omitempty"`
	RecentLists    []DashboardCard `json:"recent_lists,omitempty"`
	AggregateCards []AggregateCard `json:"aggregate_cards,omitempty"`
}

// AggregateOp is a server-side numeric aggregate function (gombit #273). The
// four values map directly to the contract's sum/avg/min/max.
type AggregateOp string

const (
	AggregateSum AggregateOp = "sum"
	AggregateAvg AggregateOp = "avg"
	AggregateMin AggregateOp = "min"
	AggregateMax AggregateOp = "max"
)

// Valid reports whether o is one of the four supported aggregate functions.
func (o AggregateOp) Valid() bool {
	switch o {
	case AggregateSum, AggregateAvg, AggregateMin, AggregateMax:
		return true
	default:
		return false
	}
}

// AggregateCard is a dashboard card showing one server-side numeric aggregate
// over a resource's field (#182): a labeled sum/avg/min/max computed by the
// generated list endpoint (?aggregate=<op>:<field>, gombit #273) over the whole
// matching set. The aggregate is never computed client-side — that would be
// wrong over a paginated/filtered set and would ship every row to the browser —
// so Field must be declared in the resource's AggregatableFields.
type AggregateCard struct {
	Label    string      `json:"label"`
	Resource ID          `json:"resource"`
	Field    ID          `json:"field"`
	Op       AggregateOp `json:"op"`
}

// DashboardCard is a count card or recent-record list bound to a resource.
type DashboardCard struct {
	Label    string `json:"label"`
	Resource ID     `json:"resource"`
	Limit    int    `json:"limit,omitempty"`
	// OrderBy is the field a recent-list card orders by, newest first (#54). It
	// must be a date/datetime field on the card's resource that the resource
	// declares sortable, so "recent" has real temporal semantics — the generated
	// card fetches ?ordering=-<field>. It is only meaningful on a recent list; a
	// count card must leave it empty. When empty on a recent list, the card shows
	// a section and a "View all" link rather than records.
	OrderBy ID `json:"order_by,omitempty"`
}

// NavTarget selects what a navigation entry points at (DESIGN.md §4.5).
type NavTarget string

const (
	NavPage     NavTarget = "page"
	NavExternal NavTarget = "external"
)

// NavItem is one ordered navigation entry.
type NavItem struct {
	ID     ID        `json:"id"`
	Label  string    `json:"label"`
	Target NavTarget `json:"target"`

	Page ID     `json:"page,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Branding carries MVP branding settings (DESIGN.md §19).
type Branding struct {
	AppName     string `json:"app_name,omitempty"`
	LogoRef     string `json:"logo_ref,omitempty"`
	AccentColor string `json:"accent_color,omitempty"`
	Appearance  string `json:"appearance,omitempty"`
}

// Clone returns a deep copy of the spec through its canonical encoding, so a
// candidate mutation (a code-symbol refactor, §13) can change a copy and leave
// the accepted spec untouched. The round-trip preserves every stable ID and the
// authored order, so identity and ordering survive the clone.
func (s *ProjectSpec) Clone() (*ProjectSpec, error) {
	data, err := Marshal(s)
	if err != nil {
		return nil, err
	}
	return Unmarshal(data)
}

// FindResource returns the resource with the given ID, or nil.
//
// Nil entries are skipped rather than dereferenced: JSON such as
// "resources":[null,...] decodes successfully, and validation must be able to
// report that as a diagnostic instead of crashing while looking up an
// unrelated reference.
func (s *ProjectSpec) FindResource(id ID) *Resource {
	for _, resource := range s.Resources {
		if resource != nil && resource.ID == id {
			return resource
		}
	}
	return nil
}

// FindPage returns the page with the given ID, or nil. Nil entries are skipped.
func (s *ProjectSpec) FindPage(id ID) *Page {
	for _, page := range s.Pages {
		if page != nil && page.ID == id {
			return page
		}
	}
	return nil
}

// FindField returns the field with the given ID, or nil. Nil entries are skipped.
func (r *Resource) FindField(id ID) *Field {
	for _, field := range r.Fields {
		if field != nil && field.ID == id {
			return field
		}
	}
	return nil
}
