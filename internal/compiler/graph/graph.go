// Package graph builds the compiler's in-memory domain graph from a
// validated ProjectSpec (DESIGN.md §9 stage 2).
//
// The graph resolves every ID reference exactly once so later generation
// stages can walk typed pointers instead of re-scanning the spec. It is
// deterministic: every collection is ordered by the spec's authored order and
// never by map iteration, which is what lets generation be reproducible
// (DESIGN.md §9, §32).
package graph

import (
	"fmt"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Graph is the resolved domain model for one ProjectSpec.
type Graph struct {
	Spec *spec.ProjectSpec

	// Resources is in authored order.
	Resources []*Resource
	// Pages is in authored order.
	Pages []*Page
	// Navigation is in authored order.
	Navigation []*NavEntry

	byResourceID map[spec.ID]*Resource
	byPageID     map[spec.ID]*Page
	byFieldID    map[spec.ID]*Field
}

// Resource is a resolved resource with its fields and relationships.
type Resource struct {
	Spec *spec.Resource

	// Fields is in authored order and includes belongs_to fields.
	Fields []*Field
	// BelongsTo lists the outgoing relationships of this resource.
	BelongsTo []*Relationship
	// HasMany lists the inbound relationships pointing at this resource.
	//
	// These are derived from other resources' belongs_to fields and are never
	// authored directly (DESIGN.md §4.2).
	HasMany []*Relationship

	// Behavior holds the resource's CRUD settings with every field-ID list
	// resolved to pointers.
	Behavior Behavior

	byFieldID map[spec.ID]*Field
}

// Behavior is ResourceBehavior with its field references resolved.
type Behavior struct {
	Spec *spec.ResourceBehavior

	List         []*Field
	Searchable   []*Field
	Sortable     []*Field
	Filterable   []*Field
	Aggregatable []*Field
}

// Field is a resolved field.
type Field struct {
	Spec  *spec.Field
	Owner *Resource

	// Relationship is set when the field is a belongs_to.
	Relationship *Relationship
}

// Relationship is one resolved belongs_to edge, viewed from both ends.
type Relationship struct {
	// Field is the belongs_to field carrying the reference.
	Field *Field
	// From is the resource declaring the belongs_to.
	From *Resource
	// To is the referenced resource.
	To *Resource
}

// Page is a resolved page.
//
// The view fields are keyed strictly off Spec.Type, which validation
// guarantees agrees with the attached configuration blocks. A page therefore
// projects exactly one view and a generator can switch on Type alone.
type Page struct {
	Spec *spec.Page
	// Resource is nil for dashboard pages.
	Resource *Resource
	// Columns is the resolved table column order, including the default when
	// the page declares no columns of its own (see tableColumns). It is
	// populated only for a resource_table page and is empty for every other
	// type.
	Columns []*Field
	// Filters is the resolved set of fields the table exposes as exact-match
	// filter controls (#51), in authored order. Unlike Columns it has no
	// default — only what TableConfig.Filters lists. Populated only for a
	// resource_table page.
	Filters []*Field
	// FormFields is the resolved form field order, including the default when
	// the page declares no fields of its own (see formFields). It is
	// populated only for a resource_form page and is empty for every other
	// type.
	FormFields []*Field
	// CountCards, RecentLists and AggregateCards are populated only for a
	// dashboard page.
	CountCards     []*Card
	RecentLists    []*Card
	AggregateCards []*AggregateCard
}

// Card is a resolved dashboard card with its resource reference linked.
type Card struct {
	Spec     *spec.DashboardCard
	Resource *Resource
	// OrderBy is the resolved field a recent-list card orders by, newest first
	// (#54), or nil when the card declares none. Validation guarantees it is a
	// sortable date/datetime field on Resource when set.
	OrderBy *Field
}

// AggregateCard is a resolved dashboard aggregate card (#182): its resource and
// the field it aggregates, linked. Validation guarantees Field is a numeric
// field on Resource that the resource declares aggregatable, and Spec.Op is a
// supported function.
type AggregateCard struct {
	Spec     *spec.AggregateCard
	Resource *Resource
	Field    *Field
}

// NavEntry is a resolved navigation entry.
type NavEntry struct {
	Spec *spec.NavItem
	// Page is nil for external entries.
	Page *Page
}

// Build resolves a spec into a domain graph.
//
// The spec must already be valid: Build validates first and refuses to
// construct a graph over a broken spec, so later stages never have to defend
// against dangling references.
func Build(s *spec.ProjectSpec) (*Graph, error) {
	if diagnostics := spec.Validate(s); diagnostics != nil {
		return nil, fmt.Errorf("graph: refusing to build over an invalid spec: %w", diagnostics)
	}

	g := &Graph{
		Spec:         s,
		byResourceID: make(map[spec.ID]*Resource, len(s.Resources)),
		byPageID:     make(map[spec.ID]*Page, len(s.Pages)),
		byFieldID:    map[spec.ID]*Field{},
	}

	g.buildResources()
	if err := g.linkRelationships(); err != nil {
		return nil, err
	}
	g.resolveBehavior()
	g.buildPages()
	g.buildNavigation()

	return g, nil
}

func (g *Graph) buildResources() {
	for _, resourceSpec := range g.Spec.Resources {
		resource := &Resource{
			Spec:      resourceSpec,
			Fields:    make([]*Field, 0, len(resourceSpec.Fields)),
			byFieldID: make(map[spec.ID]*Field, len(resourceSpec.Fields)),
		}

		for _, fieldSpec := range resourceSpec.Fields {
			field := &Field{Spec: fieldSpec, Owner: resource}
			resource.Fields = append(resource.Fields, field)
			resource.byFieldID[fieldSpec.ID] = field
			g.byFieldID[fieldSpec.ID] = field
		}

		g.Resources = append(g.Resources, resource)
		g.byResourceID[resourceSpec.ID] = resource
	}
}

// linkRelationships wires belongs_to edges and derives the has_many inverse.
//
// Both sides are appended in resource-then-field authored order, so HasMany
// ordering is deterministic rather than dependent on map iteration.
//
// An unresolvable target is reported rather than skipped. Validation already
// rejects dangling references, so reaching this is a bug in the graph or the
// validator; failing here keeps the invariant that every belongs_to field in
// a built graph has a non-nil Relationship, which is what lets generators
// dereference it without a nil check.
func (g *Graph) linkRelationships() error {
	for _, from := range g.Resources {
		for _, field := range from.Fields {
			if field.Spec.Type != spec.TypeBelongsTo {
				continue
			}

			to := g.byResourceID[field.Spec.Target]
			if to == nil {
				return fmt.Errorf(
					"graph: belongs_to field %s on resource %s targets unknown resource %s",
					field.Spec.ID, from.Spec.ID, field.Spec.Target)
			}

			relationship := &Relationship{Field: field, From: from, To: to}
			field.Relationship = relationship
			from.BelongsTo = append(from.BelongsTo, relationship)
			to.HasMany = append(to.HasMany, relationship)
		}
	}
	return nil
}

// resolveBehavior turns each resource's field-ID lists into pointers so later
// stages never re-scan the spec to interpret them.
func (g *Graph) resolveBehavior() {
	for _, resource := range g.Resources {
		behavior := &resource.Spec.Behavior
		resource.Behavior = Behavior{
			Spec:         behavior,
			List:         resource.resolveFields(behavior.ListFields),
			Searchable:   resource.resolveFields(behavior.SearchableFields),
			Sortable:     resource.resolveFields(behavior.SortableFields),
			Filterable:   resource.resolveFields(behavior.FilterableFields),
			Aggregatable: resource.resolveFields(behavior.AggregatableFields),
		}
	}
}

// resolveFields maps field IDs to this resource's fields, preserving order.
// Validation guarantees each ID belongs to the resource.
func (r *Resource) resolveFields(ids []spec.ID) []*Field {
	if len(ids) == 0 {
		return nil
	}
	fields := make([]*Field, 0, len(ids))
	for _, id := range ids {
		if field := r.byFieldID[id]; field != nil {
			fields = append(fields, field)
		}
	}
	return fields
}

// buildPages projects each page into exactly the view its type defines.
//
// The switch is on Type, not on which config block happens to be non-nil:
// validation guarantees the two agree, and keying off Type is what makes the
// documented "empty for other page types" contract true.
func (g *Graph) buildPages() {
	for _, pageSpec := range g.Spec.Pages {
		page := &Page{Spec: pageSpec}

		if pageSpec.Resource != "" {
			page.Resource = g.byResourceID[pageSpec.Resource]
		}

		switch pageSpec.Type {
		case spec.PageResourceTable:
			if page.Resource != nil {
				page.Columns = page.Resource.tableColumns(pageSpec.Table)
				page.Filters = page.Resource.tableFilters(pageSpec.Table)
			}

		case spec.PageResourceForm:
			if page.Resource != nil {
				page.FormFields = page.Resource.formFields(pageSpec.Form)
			}

		case spec.PageDashboard:
			if pageSpec.Dashboard != nil {
				page.CountCards = g.resolveCards(pageSpec.Dashboard.CountCards)
				page.RecentLists = g.resolveCards(pageSpec.Dashboard.RecentLists)
				page.AggregateCards = g.resolveAggregateCards(pageSpec.Dashboard.AggregateCards)
			}

		case spec.PageResourceDetail:
			// A detail page renders the resource's own fields and its related
			// records; it carries no field-selection block of its own.
		}

		g.Pages = append(g.Pages, page)
		g.byPageID[pageSpec.ID] = page
	}
}

// tableColumns resolves the columns a resource_table renders.
//
// A page may omit its table block entirely — DESIGN.md §7 writes exactly that
// shape — so the default is resolved here rather than left to each generator:
//
//	explicit columns -> the resource's configured list fields -> scalar fields
//
// The final fallback excludes belongs_to because rendering a relationship as
// a column needs a display representation of the related record, which the
// MVP does not define. A relationship still appears as a column when it is
// named explicitly or configured as a list field.
func (r *Resource) tableColumns(table *spec.TableConfig) []*Field {
	if table != nil && len(table.Columns) > 0 {
		return r.resolveFields(table.Columns)
	}
	if len(r.Behavior.List) > 0 {
		return r.Behavior.List
	}
	return r.ScalarFields()
}

// tableFilters resolves the fields a resource_table exposes as filter controls.
// Unlike columns there is no default — a table filters only by the fields it
// names. Validation guarantees each is in the resource's FilterableFields.
func (r *Resource) tableFilters(table *spec.TableConfig) []*Field {
	if table != nil && len(table.Filters) > 0 {
		return r.resolveFields(table.Filters)
	}
	return nil
}

// formFields resolves the fields a resource_form renders.
//
// As with tableColumns the block is optional, and the default is every field
// in authored order. Relationships are included: a form is where a
// belongs_to becomes a relationship selector (DESIGN.md §18).
func (r *Resource) formFields(form *spec.FormConfig) []*Field {
	if form != nil && len(form.Fields) > 0 {
		return r.resolveFields(form.Fields)
	}
	return r.Fields
}

// resolveCards links each dashboard card to its resource, preserving order.
func (g *Graph) resolveCards(cards []spec.DashboardCard) []*Card {
	if len(cards) == 0 {
		return nil
	}
	resolved := make([]*Card, 0, len(cards))
	for i := range cards {
		card := &cards[i]
		c := &Card{
			Spec:     card,
			Resource: g.byResourceID[card.Resource],
		}
		if card.OrderBy != "" && c.Resource != nil {
			c.OrderBy = c.Resource.byFieldID[card.OrderBy]
		}
		resolved = append(resolved, c)
	}
	return resolved
}

// resolveAggregateCards links each aggregate card to its resource and field,
// preserving order. Validation guarantees Field belongs to Resource.
func (g *Graph) resolveAggregateCards(cards []spec.AggregateCard) []*AggregateCard {
	if len(cards) == 0 {
		return nil
	}
	resolved := make([]*AggregateCard, 0, len(cards))
	for i := range cards {
		card := &cards[i]
		c := &AggregateCard{
			Spec:     card,
			Resource: g.byResourceID[card.Resource],
		}
		if c.Resource != nil {
			c.Field = c.Resource.byFieldID[card.Field]
		}
		resolved = append(resolved, c)
	}
	return resolved
}

func (g *Graph) buildNavigation() {
	for _, itemSpec := range g.Spec.Navigation {
		entry := &NavEntry{Spec: itemSpec}
		if itemSpec.Target == spec.NavPage {
			entry.Page = g.byPageID[itemSpec.Page]
		}
		g.Navigation = append(g.Navigation, entry)
	}
}

// Resource returns the resolved resource for an ID, or nil.
func (g *Graph) Resource(id spec.ID) *Resource { return g.byResourceID[id] }

// Page returns the resolved page for an ID, or nil.
func (g *Graph) Page(id spec.ID) *Page { return g.byPageID[id] }

// Field returns the resolved field for an ID, or nil.
func (g *Graph) Field(id spec.ID) *Field { return g.byFieldID[id] }

// ScalarFields returns the resource's non-relationship fields in authored order.
func (r *Resource) ScalarFields() []*Field {
	fields := make([]*Field, 0, len(r.Fields))
	for _, field := range r.Fields {
		if field.Spec.Type.Scalar() {
			fields = append(fields, field)
		}
	}
	return fields
}

// Field returns a field owned by this resource, or nil.
func (r *Resource) Field(id spec.ID) *Field { return r.byFieldID[id] }

// CodeName is the frozen Go symbol for the resource (ADR-001 D3).
func (r *Resource) CodeName() string { return r.Spec.CodeName }

// CodeName is the frozen Go symbol for the field (ADR-001 D3).
func (f *Field) CodeName() string { return f.Spec.CodeName }
