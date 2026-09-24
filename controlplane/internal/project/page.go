package project

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Page operations (DESIGN.md §4.4, §18, M3). A page is a structured screen —
// resource_table, resource_form, resource_detail or dashboard — bound to a
// resource (except dashboard). Like the resource and field ops the caller
// supplies human-readable input; the backend mints the page's stable ID and
// derives its URL slug.
//
// A page is a frontend view: it generates no backend extension contract, so
// adding or removing one is ABI-neutral and commits without a compatibility
// build. The candidate still goes through the shared pipeline
// (classifyAndInsertLocked) so an edit that leaves the spec invalid — a page
// bound to a resource that does not exist, a navigation entry left pointing at a
// deleted page — is returned as invalid_spec rather than committed.

var (
	// ErrPageNotFound means the project's current spec has no page with the given
	// stable ID.
	ErrPageNotFound = errors.New("project: page not found")
	// ErrInvalidPageEdit means the page input is malformed (empty label, unknown
	// type, or a resource binding that contradicts the page type).
	ErrInvalidPageEdit = errors.New("project: invalid page edit")
)

// PageInput is the human-facing description of a page the editor supplies. The
// slug is derived from the label; the ID is minted.
type PageInput struct {
	Label string
	Type  spec.PageType
	// Resource is the bound resource's stable ID. Required for every page type
	// except dashboard, which must not carry one.
	Resource spec.ID
}

// AddPage creates a page from the input, deriving a unique slug and minting its
// stable ID, then submits the candidate. Adding a page is ABI-neutral, so it
// commits (unless the resulting spec is invalid — e.g. an unknown resource).
func (s *Service) AddPage(ctx context.Context, projectID uint, in PageInput, by uint) (CandidateResult, error) {
	if err := validatePageInput(in); err != nil {
		return CandidateResult{}, err
	}
	var result CandidateResult
	err := s.withLockedSpec(ctx, projectID, func(tx *gorm.DB, p Project, current *spec.ProjectSpec) error {
		if current == nil {
			return ErrNoSpec
		}
		candidate, err := current.Clone()
		if err != nil {
			return err
		}
		page := &spec.Page{
			ID:    spec.MustNewID(spec.KindPage),
			Slug:  derivePageSlug(in.Label, candidate),
			Label: in.Label,
			Type:  in.Type,
		}
		// A dashboard must not reference a resource; the resource page types must.
		// validatePageInput already enforced the presence/absence of Resource.
		if in.Type != spec.PageDashboard {
			page.Resource = in.Resource
		}
		candidate.Pages = append(candidate.Pages, page)
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}

// TableConfigInput is the human-facing configuration of a resource_table page
// the editor supplies: the page's display label, plus the table's heading,
// ordered columns, search toggle, exact-match filter fields and page size.
// Column IDs must belong to the page's bound resource, and each filter field
// must additionally be one the resource declares filterable — spec.Validate
// enforces both and surfaces a violation as invalid_spec.
type TableConfigInput struct {
	Label    string
	Title    string
	Columns  []spec.ID
	Search   bool
	Filters  []spec.ID
	PageSize int
}

// UpdateTableConfig sets a resource_table page's label and table configuration
// (DESIGN.md §4.4, §18). A page is a frontend view with no extension contract,
// so the edit is ABI-neutral and commits (unless the result is spec-invalid — a
// column that is not on the bound resource, or a negative page size). The table
// block is dropped when the configuration is entirely empty, so a page that
// configures nothing stays on the graph's column defaults rather than pinning an
// empty block.
func (s *Service) UpdateTableConfig(ctx context.Context, projectID uint, pageID spec.ID, in TableConfigInput, by uint) (CandidateResult, error) {
	if strings.TrimSpace(in.Label) == "" {
		return CandidateResult{}, fmt.Errorf("%w: a page label is required", ErrInvalidPageEdit)
	}
	if in.PageSize < 0 {
		return CandidateResult{}, fmt.Errorf("%w: page size must not be negative", ErrInvalidPageEdit)
	}
	var result CandidateResult
	err := s.withLockedSpec(ctx, projectID, func(tx *gorm.DB, p Project, current *spec.ProjectSpec) error {
		if current == nil {
			return ErrNoSpec
		}
		candidate, err := current.Clone()
		if err != nil {
			return err
		}
		page := candidate.FindPage(pageID)
		if page == nil {
			return ErrPageNotFound
		}
		if page.Type != spec.PageResourceTable {
			return fmt.Errorf("%w: only a resource_table page has a table configuration", ErrInvalidPageEdit)
		}
		page.Label = in.Label
		page.Table = tableConfigOrNil(in)
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}

// tableConfigOrNil builds a TableConfig from the input, or nil when the input
// configures nothing (so the page falls back to the graph's column defaults
// rather than serializing an empty block).
func tableConfigOrNil(in TableConfigInput) *spec.TableConfig {
	if strings.TrimSpace(in.Title) == "" && len(in.Columns) == 0 && !in.Search && len(in.Filters) == 0 && in.PageSize == 0 {
		return nil
	}
	return &spec.TableConfig{
		Title:    strings.TrimSpace(in.Title),
		Columns:  in.Columns,
		Search:   in.Search,
		Filters:  in.Filters,
		PageSize: in.PageSize,
	}
}

// FormConfigInput is the human-facing configuration of a resource_form page: the
// page's display label, the layout, and the ordered field subset. Field IDs must
// belong to the page's bound resource — spec.Validate enforces that.
type FormConfigInput struct {
	Label  string
	Layout string
	Fields []spec.ID
}

// UpdateFormConfig sets a resource_form page's label and form configuration
// (DESIGN.md §18, #52). It is ABI-neutral and commits, unless the result is
// spec-invalid (an unsupported layout, or a field not on the bound resource).
// The form block is dropped when the configuration is empty, so a page that
// configures nothing stays on the graph's field defaults.
func (s *Service) UpdateFormConfig(ctx context.Context, projectID uint, pageID spec.ID, in FormConfigInput, by uint) (CandidateResult, error) {
	if strings.TrimSpace(in.Label) == "" {
		return CandidateResult{}, fmt.Errorf("%w: a page label is required", ErrInvalidPageEdit)
	}
	var result CandidateResult
	err := s.withLockedSpec(ctx, projectID, func(tx *gorm.DB, p Project, current *spec.ProjectSpec) error {
		if current == nil {
			return ErrNoSpec
		}
		candidate, err := current.Clone()
		if err != nil {
			return err
		}
		page := candidate.FindPage(pageID)
		if page == nil {
			return ErrPageNotFound
		}
		if page.Type != spec.PageResourceForm {
			return fmt.Errorf("%w: only a resource_form page has a form configuration", ErrInvalidPageEdit)
		}
		page.Label = in.Label
		page.Form = formConfigOrNil(in)
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}

// formConfigOrNil builds a FormConfig from the input, or nil when it configures
// nothing (so the page falls back to the graph's field defaults).
func formConfigOrNil(in FormConfigInput) *spec.FormConfig {
	layout := strings.TrimSpace(in.Layout)
	if layout == "" && len(in.Fields) == 0 {
		return nil
	}
	return &spec.FormConfig{Layout: layout, Fields: in.Fields}
}

// DeletePage removes a page. A page generates no extension contract, so its
// removal is ABI-neutral and commits; a navigation entry that still points at
// the page fails validation (a dangling reference) and is returned as
// invalid_spec instead.
func (s *Service) DeletePage(ctx context.Context, projectID uint, pageID spec.ID, by uint) (CandidateResult, error) {
	var result CandidateResult
	err := s.withLockedSpec(ctx, projectID, func(tx *gorm.DB, p Project, current *spec.ProjectSpec) error {
		if current == nil {
			return ErrNoSpec
		}
		candidate, err := current.Clone()
		if err != nil {
			return err
		}
		if candidate.FindPage(pageID) == nil {
			return ErrPageNotFound
		}
		candidate.Pages = removePage(candidate.Pages, pageID)
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}

// validatePageInput rejects malformed page input before any spec is built. It
// checks the shape the input alone can prove wrong (empty label, unknown type,
// a resource binding that contradicts the type); resource *existence* is the
// spec validator's job, surfaced as invalid_spec.
func validatePageInput(in PageInput) error {
	if strings.TrimSpace(in.Label) == "" {
		return fmt.Errorf("%w: a page label is required", ErrInvalidPageEdit)
	}
	switch in.Type {
	case spec.PageResourceTable, spec.PageResourceForm, spec.PageResourceDetail:
		if in.Resource == "" {
			return fmt.Errorf("%w: a %s page must reference a resource", ErrInvalidPageEdit, in.Type)
		}
	case spec.PageDashboard:
		if in.Resource != "" {
			return fmt.Errorf("%w: a dashboard page must not reference a resource", ErrInvalidPageEdit)
		}
	default:
		return fmt.Errorf("%w: unknown page type %q", ErrInvalidPageEdit, in.Type)
	}
	return nil
}

// derivePageSlug folds a label into a valid, collision-free URL slug
// (spec.IsSlug: lower kebab-case). Uniqueness is enforced across the spec's
// existing pages so two pages never share a slug.
func derivePageSlug(label string, s *spec.ProjectSpec) string {
	base := kebabCase(label)
	if base == "" {
		base = "page"
	}
	taken := map[string]bool{}
	for _, p := range s.Pages {
		if p != nil {
			taken[p.Slug] = true
		}
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = base + "-" + strconv.Itoa(n)
	}
	return name
}

// kebabCase folds arbitrary text into a lower-kebab-case slug that satisfies
// spec.IsSlug: lowercase [a-z0-9-], single hyphens, no leading digit or hyphen,
// no trailing hyphen. It returns "" when the text has no usable alphanumeric
// content, so callers can fall back.
func kebabCase(text string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if b.Len() > 0 && !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	// A leading digit is illegal for a slug; prefix it.
	if out != "" && out[0] >= '0' && out[0] <= '9' {
		out = "p-" + out
	}
	return out
}

func removePage(pages []*spec.Page, id spec.ID) []*spec.Page {
	out := pages[:0:0]
	for _, p := range pages {
		if p == nil || p.ID != id {
			out = append(out, p)
		}
	}
	return out
}
