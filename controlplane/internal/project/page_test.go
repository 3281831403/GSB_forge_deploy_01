package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// TestAddPageEachType is the #50 acceptance criterion: a page of every MVP type
// can be created — the resource types bound to a resource, the dashboard without
// one — and each is serialized into the head spec.
func TestAddPageEachType(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	ctx := context.Background()

	cases := []struct {
		label string
		typ   spec.PageType
		res   spec.ID
	}{
		{"Orders", spec.PageResourceTable, resID},
		{"New Order", spec.PageResourceForm, resID},
		{"Order", spec.PageResourceDetail, resID},
		{"Home", spec.PageDashboard, ""},
	}
	for _, c := range cases {
		res, err := svc.AddPage(ctx, projectID, project.PageInput{Label: c.label, Type: c.typ, Resource: c.res}, 7)
		if err != nil {
			t.Fatalf("add %s page: %v", c.typ, err)
		}
		if res.Outcome != project.OutcomeCommitted {
			t.Fatalf("add %s page outcome = %s, want committed", c.typ, res.Outcome)
		}
	}

	pages := headSpec(t, svc, projectID).Pages
	if len(pages) != len(cases) {
		t.Fatalf("want %d pages serialized, got %d", len(cases), len(pages))
	}
	for i, c := range cases {
		p := pages[i]
		if p.Label != c.label || p.Type != c.typ {
			t.Errorf("page[%d] = %s/%s, want %s/%s", i, p.Label, p.Type, c.label, c.typ)
		}
		if p.Resource != c.res {
			t.Errorf("page[%d] resource = %q, want %q", i, p.Resource, c.res)
		}
		if !spec.IsSlug(p.Slug) {
			t.Errorf("page[%d] slug %q is not a valid slug", i, p.Slug)
		}
	}
}

// TestAddPageDerivesUniqueSlugs: two pages with the same label get distinct,
// valid slugs so page URLs never collide.
func TestAddPageDerivesUniqueSlugs(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := svc.AddPage(ctx, projectID, project.PageInput{Label: "Orders", Type: spec.PageResourceTable, Resource: resID}, 7); err != nil {
			t.Fatalf("add page %d: %v", i, err)
		}
	}
	pages := headSpec(t, svc, projectID).Pages
	if len(pages) != 2 {
		t.Fatalf("want 2 pages, got %d", len(pages))
	}
	if pages[0].Slug == pages[1].Slug {
		t.Errorf("page slugs must be unique; both are %q", pages[0].Slug)
	}
}

// TestAddPageRejectsMalformedInput: input the request alone proves wrong is
// rejected with ErrInvalidPageEdit before any spec is built.
func TestAddPageRejectsMalformedInput(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	ctx := context.Background()

	cases := map[string]project.PageInput{
		"empty label":         {Label: "  ", Type: spec.PageResourceTable, Resource: resID},
		"unknown type":        {Label: "X", Type: spec.PageType("gallery"), Resource: resID},
		"resource w/o target": {Label: "X", Type: spec.PageResourceTable},
		"dashboard w/ target": {Label: "X", Type: spec.PageDashboard, Resource: resID},
	}
	for name, in := range cases {
		if _, err := svc.AddPage(ctx, projectID, in, 7); !errors.Is(err, project.ErrInvalidPageEdit) {
			t.Errorf("%s: err = %v, want ErrInvalidPageEdit", name, err)
		}
	}
}

// TestAddPageDanglingResourceIsInvalid: a well-formed request binding a resource
// that does not exist is caught by the spec validator as invalid_spec (not a
// committed, unbuildable revision).
func TestAddPageDanglingResourceIsInvalid(t *testing.T) {
	svc, projectID, _ := projectWithResource(t)

	res, err := svc.AddPage(context.Background(), projectID,
		project.PageInput{Label: "Ghosts", Type: spec.PageResourceTable, Resource: spec.MustNewID(spec.KindResource)}, 7)
	if err != nil {
		t.Fatalf("add page: %v", err)
	}
	if res.Outcome != project.OutcomeInvalidSpec {
		t.Fatalf("dangling resource binding outcome = %s, want invalid_spec", res.Outcome)
	}
	if len(res.Diagnostics) == 0 {
		t.Error("invalid_spec must carry diagnostics keyed to the offending page")
	}
}

// TestDeletePageIsNeutral: a page carries no extension ABI, so removing it
// commits as a neutral transition rather than being flagged breaking.
func TestDeletePageIsNeutral(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	ctx := context.Background()

	if _, err := svc.AddPage(ctx, projectID, project.PageInput{Label: "Orders", Type: spec.PageResourceTable, Resource: resID}, 7); err != nil {
		t.Fatalf("add: %v", err)
	}
	pageID := headSpec(t, svc, projectID).Pages[0].ID

	res, err := svc.DeletePage(ctx, projectID, pageID, 7)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted || res.Class != "neutral" {
		t.Fatalf("delete page outcome=%s class=%s, want committed/neutral", res.Outcome, res.Class)
	}
	if len(headSpec(t, svc, projectID).Pages) != 0 {
		t.Error("delete must remove the page from the head spec")
	}
}

// pageWithField adds a field and a resource_table page, returning the field ID
// and page ID, for the table-config tests.
func pageWithField(t *testing.T, svc *project.Service, projectID uint, resID spec.ID) (spec.ID, spec.ID) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.AddField(ctx, projectID, resID, project.FieldInput{Label: "Name", Type: spec.TypeString}, 7); err != nil {
		t.Fatalf("add field: %v", err)
	}
	if _, err := svc.AddPage(ctx, projectID, project.PageInput{Label: "Orders", Type: spec.PageResourceTable, Resource: resID}, 7); err != nil {
		t.Fatalf("add page: %v", err)
	}
	head := headSpec(t, svc, projectID)
	return head.Resources[0].Fields[0].ID, head.Pages[0].ID
}

func TestUpdateTableConfigCommits(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	fieldID, pageID := pageWithField(t, svc, projectID, resID)
	ctx := context.Background()

	// A table may enable search only if the resource declares a searchable
	// field (spec validation), so declare the string field searchable first.
	if _, err := svc.UpdateBehavior(ctx, projectID, resID, spec.ResourceBehavior{
		CreateEnabled: true, UpdateEnabled: true, DeleteEnabled: true, AdminVisible: true,
		SearchableFields: []spec.ID{fieldID},
	}, 7); err != nil {
		t.Fatalf("declare searchable field: %v", err)
	}

	res, err := svc.UpdateTableConfig(ctx, projectID, pageID, project.TableConfigInput{
		Label: "All orders", Title: "Every order", Columns: []spec.ID{fieldID}, Search: true, PageSize: 25,
	}, 7)
	if err != nil {
		t.Fatalf("update table config: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted || res.Class != "neutral" {
		t.Fatalf("outcome=%s class=%s, want committed/neutral", res.Outcome, res.Class)
	}
	page := headSpec(t, svc, projectID).Pages[0]
	if page.Label != "All orders" {
		t.Errorf("label = %q, want All orders", page.Label)
	}
	if page.Table == nil || page.Table.Title != "Every order" || !page.Table.Search || page.Table.PageSize != 25 {
		t.Fatalf("table config not persisted: %+v", page.Table)
	}
	if len(page.Table.Columns) != 1 || page.Table.Columns[0] != fieldID {
		t.Errorf("columns = %v, want [%s]", page.Table.Columns, fieldID)
	}
}

// TestUpdateTableConfigFilters: the editor can configure exact-match filter
// fields, but only ones the resource declares filterable — the subset rule.
func TestUpdateTableConfigFilters(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	fieldID, pageID := pageWithField(t, svc, projectID, resID)
	ctx := context.Background()

	// A filter over a field the resource hasn't declared filterable is rejected
	// as an invalid spec, not committed.
	bad, err := svc.UpdateTableConfig(ctx, projectID, pageID, project.TableConfigInput{
		Label: "Orders", Filters: []spec.ID{fieldID},
	}, 7)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if bad.Outcome != project.OutcomeInvalidSpec {
		t.Fatalf("filter over a non-filterable field must be invalid_spec, got %s", bad.Outcome)
	}

	// Declare the field filterable, then the same filter commits and persists.
	if _, err := svc.UpdateBehavior(ctx, projectID, resID, spec.ResourceBehavior{
		CreateEnabled: true, UpdateEnabled: true, DeleteEnabled: true, AdminVisible: true,
		FilterableFields: []spec.ID{fieldID},
	}, 7); err != nil {
		t.Fatalf("declare filterable: %v", err)
	}
	res, err := svc.UpdateTableConfig(ctx, projectID, pageID, project.TableConfigInput{
		Label: "Orders", Filters: []spec.ID{fieldID},
	}, 7)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted {
		t.Fatalf("outcome=%s, want committed", res.Outcome)
	}
	page := headSpec(t, svc, projectID).Pages[0]
	if page.Table == nil || len(page.Table.Filters) != 1 || page.Table.Filters[0] != fieldID {
		t.Errorf("filters not persisted: %+v", page.Table)
	}
}

// TestUpdateTableConfigEmptyDropsBlock: a save that configures nothing leaves no
// table block, so the page stays on the graph's column defaults.
func TestUpdateTableConfigEmptyDropsBlock(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	fieldID, pageID := pageWithField(t, svc, projectID, resID)
	ctx := context.Background()

	// First pin a config, then clear it.
	if _, err := svc.UpdateTableConfig(ctx, projectID, pageID, project.TableConfigInput{Label: "X", Columns: []spec.ID{fieldID}}, 7); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if _, err := svc.UpdateTableConfig(ctx, projectID, pageID, project.TableConfigInput{Label: "Orders"}, 7); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if page := headSpec(t, svc, projectID).Pages[0]; page.Table != nil {
		t.Errorf("an all-empty config must drop the table block; got %+v", page.Table)
	}
}

// TestUpdateTableConfigDanglingColumnInvalid: a column that is not on the bound
// resource is caught by the spec validator as invalid_spec, not committed.
func TestUpdateTableConfigDanglingColumnInvalid(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	_, pageID := pageWithField(t, svc, projectID, resID)

	res, err := svc.UpdateTableConfig(context.Background(), projectID, pageID, project.TableConfigInput{
		Label: "Orders", Columns: []spec.ID{spec.MustNewID(spec.KindField)},
	}, 7)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.Outcome != project.OutcomeInvalidSpec {
		t.Fatalf("dangling column outcome = %s, want invalid_spec", res.Outcome)
	}
}

func TestUpdateTableConfigErrors(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	_, pageID := pageWithField(t, svc, projectID, resID)
	ctx := context.Background()

	if _, err := svc.UpdateTableConfig(ctx, projectID, pageID, project.TableConfigInput{Label: "  "}, 7); !errors.Is(err, project.ErrInvalidPageEdit) {
		t.Errorf("empty label = %v, want ErrInvalidPageEdit", err)
	}
	if _, err := svc.UpdateTableConfig(ctx, projectID, pageID, project.TableConfigInput{Label: "X", PageSize: -1}, 7); !errors.Is(err, project.ErrInvalidPageEdit) {
		t.Errorf("negative page size = %v, want ErrInvalidPageEdit", err)
	}
	if _, err := svc.UpdateTableConfig(ctx, projectID, spec.MustNewID(spec.KindPage), project.TableConfigInput{Label: "X"}, 7); !errors.Is(err, project.ErrPageNotFound) {
		t.Errorf("unknown page = %v, want ErrPageNotFound", err)
	}

	// A dashboard page has no table configuration.
	if _, err := svc.AddPage(ctx, projectID, project.PageInput{Label: "Home", Type: spec.PageDashboard}, 7); err != nil {
		t.Fatalf("add dashboard: %v", err)
	}
	dashID := headSpec(t, svc, projectID).Pages[1].ID
	if _, err := svc.UpdateTableConfig(ctx, projectID, dashID, project.TableConfigInput{Label: "Home"}, 7); !errors.Is(err, project.ErrInvalidPageEdit) {
		t.Errorf("table config on a dashboard = %v, want ErrInvalidPageEdit", err)
	}
}

func TestUpdateFormConfigCommits(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	fieldID, _ := pageWithField(t, svc, projectID, resID)
	ctx := context.Background()

	// pageWithField adds a resource_table page; add a form page too.
	if _, err := svc.AddPage(ctx, projectID, project.PageInput{Label: "Edit order", Type: spec.PageResourceForm, Resource: resID}, 7); err != nil {
		t.Fatalf("add form page: %v", err)
	}
	formPageID := headSpec(t, svc, projectID).Pages[1].ID

	res, err := svc.UpdateFormConfig(ctx, projectID, formPageID, project.FormConfigInput{
		Label: "Create order", Layout: "two_column", Fields: []spec.ID{fieldID},
	}, 7)
	if err != nil {
		t.Fatalf("update form config: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted || res.Class != "neutral" {
		t.Fatalf("outcome=%s class=%s, want committed/neutral", res.Outcome, res.Class)
	}
	page := headSpec(t, svc, projectID).Pages[1]
	if page.Label != "Create order" || page.Form == nil || page.Form.Layout != "two_column" {
		t.Fatalf("form config not persisted: label=%q form=%+v", page.Label, page.Form)
	}
	if len(page.Form.Fields) != 1 || page.Form.Fields[0] != fieldID {
		t.Errorf("fields = %v, want [%s]", page.Form.Fields, fieldID)
	}
}

func TestUpdateFormConfigErrors(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	_, tablePageID := pageWithField(t, svc, projectID, resID)
	ctx := context.Background()

	// A resource_table page has no form configuration.
	if _, err := svc.UpdateFormConfig(ctx, projectID, tablePageID, project.FormConfigInput{Label: "X"}, 7); !errors.Is(err, project.ErrInvalidPageEdit) {
		t.Errorf("form config on a table page = %v, want ErrInvalidPageEdit", err)
	}
	// Empty label.
	if _, err := svc.UpdateFormConfig(ctx, projectID, tablePageID, project.FormConfigInput{Label: "  "}, 7); !errors.Is(err, project.ErrInvalidPageEdit) {
		t.Errorf("empty label = %v, want ErrInvalidPageEdit", err)
	}
	// Unknown page.
	if _, err := svc.UpdateFormConfig(ctx, projectID, spec.MustNewID(spec.KindPage), project.FormConfigInput{Label: "X"}, 7); !errors.Is(err, project.ErrPageNotFound) {
		t.Errorf("unknown page = %v, want ErrPageNotFound", err)
	}

	// An unsupported layout is caught by the spec validator.
	if _, err := svc.AddPage(ctx, projectID, project.PageInput{Label: "Edit", Type: spec.PageResourceForm, Resource: resID}, 7); err != nil {
		t.Fatalf("add form page: %v", err)
	}
	formPageID := headSpec(t, svc, projectID).Pages[1].ID
	res, err := svc.UpdateFormConfig(ctx, projectID, formPageID, project.FormConfigInput{Label: "Edit", Layout: "diagonal"}, 7)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.Outcome != project.OutcomeInvalidSpec {
		t.Errorf("bad layout outcome = %s, want invalid_spec", res.Outcome)
	}
}

func TestPageEditErrors(t *testing.T) {
	svc, projectID, _ := projectWithResource(t)
	ctx := context.Background()

	if _, err := svc.DeletePage(ctx, projectID, spec.MustNewID(spec.KindPage), 7); !errors.Is(err, project.ErrPageNotFound) {
		t.Errorf("unknown page = %v, want ErrPageNotFound", err)
	}
}
