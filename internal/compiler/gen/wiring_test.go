package gen

import (
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

func wiringSource(t *testing.T, adminCustomer, adminInvoice bool) string {
	t.Helper()
	g, ids := buildGraph(t)
	g.Resource(ids["customer"]).Spec.Behavior.AdminVisible = adminCustomer
	g.Resource(ids["invoice"]).Spec.Behavior.AdminVisible = adminInvoice

	files, err := Wiring(g, "example.com/app")
	if err != nil {
		t.Fatalf("Wiring: %v", err)
	}
	if len(files) != 1 || files[0].Path != "internal/forge_generated/register.go" {
		t.Fatalf("expected one register.go, got %v", paths(files))
	}
	return string(files[0].Content)
}

func TestWiringRegistersEveryResource(t *testing.T) {
	src := wiringSource(t, true, false)

	for _, want := range []string{
		"package forge_generated",
		`"github.com/gombit-dev/gombit/framework"`,
		`"example.com/app/internal/forge_generated/customer"`,
		`"example.com/app/internal/forge_generated/invoice"`,
		"func RegisterAll(app *framework.App) error",
		"customer.Register(app)",
		"invoice.Register(app)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("register.go must contain %q", want)
		}
	}
}

// TestWiringAdminFollowsVisibility calls RegisterAdmin only for admin-visible
// resources, matching which admin.go files the admin stage emitted.
func TestWiringAdminFollowsVisibility(t *testing.T) {
	src := wiringSource(t, true, false)

	if !strings.Contains(src, "customer.RegisterAdmin(app)") {
		t.Error("admin-visible customer must be admin-registered")
	}
	if strings.Contains(src, "invoice.RegisterAdmin(app)") {
		t.Error("non-visible invoice must not be admin-registered (no admin.go exists)")
	}
}

func TestWiringNeedsModule(t *testing.T) {
	g, _ := buildGraph(t)
	if _, err := Wiring(g, ""); err == nil {
		t.Error("Wiring must require a module path")
	}
	if _, err := Wiring(nil, "example.com/app"); err == nil {
		t.Error("Wiring must reject a nil graph")
	}
}

func TestWiringRejectsReservedResourceName(t *testing.T) {
	g := oneResourceGraph(t, "Main", "mains", field("Name", "name", spec.TypeString))
	if _, err := Wiring(g, "example.com/app"); err == nil {
		t.Fatal("Wiring must reject a resource folding to package main")
	}
}
