package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// projectWithTablePage creates a project with a resource and a resource_table
// page, returning the service, project id and the table page's stable ID — a
// navigable target for the navigation tests.
func projectWithTablePage(t *testing.T) (*project.Service, uint, spec.ID) {
	t.Helper()
	svc, projectID, resID := projectWithResource(t)
	if _, err := svc.AddPage(context.Background(), projectID,
		project.PageInput{Label: "Orders", Type: spec.PageResourceTable, Resource: resID}, 7); err != nil {
		t.Fatalf("add table page: %v", err)
	}
	return svc, projectID, headSpec(t, svc, projectID).Pages[0].ID
}

func TestSetNavigationCommits(t *testing.T) {
	svc, projectID, pageID := projectWithTablePage(t)

	res, err := svc.SetNavigation(context.Background(), projectID, []project.NavItemInput{
		{Label: "Orders", Target: spec.NavPage, Page: pageID},
		{Label: "Docs", Target: spec.NavExternal, URL: "https://example.com"},
	}, 7)
	if err != nil {
		t.Fatalf("set navigation: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted || res.Class != "neutral" {
		t.Fatalf("outcome=%s class=%s, want committed/neutral", res.Outcome, res.Class)
	}
	nav := headSpec(t, svc, projectID).Navigation
	if len(nav) != 2 {
		t.Fatalf("want 2 nav entries, got %d", len(nav))
	}
	// Authored order preserved; each entry carries only its target's field, and an
	// ID was minted.
	if nav[0].Label != "Orders" || nav[0].Target != spec.NavPage || nav[0].Page != pageID || nav[0].URL != "" {
		t.Errorf("entry 0 = %+v", nav[0])
	}
	if nav[1].Label != "Docs" || nav[1].Target != spec.NavExternal || nav[1].URL != "https://example.com" || nav[1].Page != "" {
		t.Errorf("entry 1 = %+v", nav[1])
	}
	if nav[0].ID == "" || nav[0].ID == nav[1].ID {
		t.Error("navigation entries must have distinct minted IDs")
	}
}

// TestSetNavigationReplaces: a second call replaces the whole list, not appends.
func TestSetNavigationReplaces(t *testing.T) {
	svc, projectID, pageID := projectWithTablePage(t)
	ctx := context.Background()

	if _, err := svc.SetNavigation(ctx, projectID, []project.NavItemInput{{Label: "A", Target: spec.NavPage, Page: pageID}}, 7); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := svc.SetNavigation(ctx, projectID, []project.NavItemInput{{Label: "B", Target: spec.NavExternal, URL: "https://example.com"}}, 7); err != nil {
		t.Fatalf("second: %v", err)
	}
	nav := headSpec(t, svc, projectID).Navigation
	if len(nav) != 1 || nav[0].Label != "B" {
		t.Errorf("navigation must be replaced, not appended; got %+v", nav)
	}
}

// TestSetNavigationDanglingPageInvalid: a page entry pointing at a nonexistent
// page is caught by the spec validator as invalid_spec.
func TestSetNavigationDanglingPageInvalid(t *testing.T) {
	svc, projectID, _ := projectWithTablePage(t)

	res, err := svc.SetNavigation(context.Background(), projectID, []project.NavItemInput{
		{Label: "Ghost", Target: spec.NavPage, Page: spec.MustNewID(spec.KindPage)},
	}, 7)
	if err != nil {
		t.Fatalf("set navigation: %v", err)
	}
	if res.Outcome != project.OutcomeInvalidSpec {
		t.Fatalf("dangling nav page outcome = %s, want invalid_spec", res.Outcome)
	}
}

func TestSetNavigationErrors(t *testing.T) {
	svc, projectID, pageID := projectWithTablePage(t)
	ctx := context.Background()

	cases := map[string][]project.NavItemInput{
		"empty label":      {{Label: "  ", Target: spec.NavPage, Page: pageID}},
		"page w/o target":  {{Label: "X", Target: spec.NavPage}},
		"external w/o url": {{Label: "X", Target: spec.NavExternal}},
		"unknown target":   {{Label: "X", Target: spec.NavTarget("popover")}},
	}
	for name, items := range cases {
		if _, err := svc.SetNavigation(ctx, projectID, items, 7); !errors.Is(err, project.ErrInvalidNavEdit) {
			t.Errorf("%s: err = %v, want ErrInvalidNavEdit", name, err)
		}
	}
}
