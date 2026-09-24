package project_test

import (
	"context"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
)

func TestSetBrandingCommits(t *testing.T) {
	svc, projectID, _ := projectWithResource(t)

	res, err := svc.SetBranding(context.Background(), projectID, project.BrandingInput{
		AppName: "Shopfront", LogoRef: "/logo.svg", AccentColor: "#2563eb", Appearance: "dark",
	}, 7)
	if err != nil {
		t.Fatalf("set branding: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted || res.Class != "neutral" {
		t.Fatalf("outcome=%s class=%s, want committed/neutral", res.Outcome, res.Class)
	}
	b := headSpec(t, svc, projectID).Branding
	if b == nil || b.AppName != "Shopfront" || b.AccentColor != "#2563eb" || b.Appearance != "dark" || b.LogoRef != "/logo.svg" {
		t.Fatalf("branding not persisted: %+v", b)
	}
}

// TestSetBrandingEmptyDropsBlock: an all-empty save clears the branding block.
func TestSetBrandingEmptyDropsBlock(t *testing.T) {
	svc, projectID, _ := projectWithResource(t)
	ctx := context.Background()

	if _, err := svc.SetBranding(ctx, projectID, project.BrandingInput{AppName: "X"}, 7); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if _, err := svc.SetBranding(ctx, projectID, project.BrandingInput{}, 7); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if b := headSpec(t, svc, projectID).Branding; b != nil {
		t.Errorf("an all-empty branding must drop the block; got %+v", b)
	}
}

// TestSetBrandingInvalidIsRejected: a malformed accent color or appearance is
// caught by spec.Validate as invalid_spec, not committed.
func TestSetBrandingInvalidIsRejected(t *testing.T) {
	svc, projectID, _ := projectWithResource(t)
	ctx := context.Background()

	cases := map[string]project.BrandingInput{
		"bad accent":     {AccentColor: "royalblue"},
		"bad appearance": {Appearance: "neon"},
	}
	for name, in := range cases {
		res, err := svc.SetBranding(ctx, projectID, in, 7)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Outcome != project.OutcomeInvalidSpec {
			t.Errorf("%s: outcome = %s, want invalid_spec", name, res.Outcome)
		}
	}
}
