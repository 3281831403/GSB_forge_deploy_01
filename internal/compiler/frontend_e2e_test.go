package compiler

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gombit-dev/gombit-forge/internal/gombit"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// TestFrontendEndToEnd is the frontend go/no-go gate (#186), the missing twin of
// TestM0EndToEnd. The backend is proven end to end there; the generated frontend
// (frontend/src/forge_generated/** + resources.tsx) is otherwise validated only
// by exact-string assertions in internal/compiler/gen and by the backend
// booting. Nothing builds the generated TypeScript or wires it into a scaffolded
// app, so a regression in the integration seam — the resources.tsx registry
// contract, the ../../api/generated / useApiClient imports, the openapi-fetch
// call shapes, or the scaffold's generatedResourceRoutes / generatedResources
// consumption — would ship green.
//
// This test closes that gap for a spec with all four page kinds: scaffold a
// Gombit app, write the compiled tree, point the scaffold's frontend shell at
// the generated registry (and drop the demo Product wiring), boot the backend,
// generate the TypeScript client from the live /openapi.json, then build the
// frontend and assert it succeeds.
//
// It is heavier than the M0 gate — it installs the scaffolded app's npm deps and
// runs a real tsc + vite build — so it is gated the same way (skips in -short and
// when any of gombit, atlas, go, docker, node, npm, npx is absent) and left to a
// slower lane rather than the fast merge gate.
func TestFrontendEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping frontend end-to-end harness in -short")
	}
	for _, bin := range []string{gombit.DefaultBinary, "atlas", "go", "docker", "node", "npm", "npx"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: %v", bin, err)
		}
	}
	cli := &gombit.CLI{}
	version, err := cli.Version(context.Background())
	if err != nil {
		t.Fatalf("gombit version: %v", err)
	}
	if err := gombit.CheckSupported(version); err != nil {
		t.Skipf("installed toolchain unsupported: %v", err)
	}

	// The frontend build (npm install + tsc + vite) dominates and is slow.
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	const module = "example.com/app"
	dir := filepath.Join(t.TempDir(), "app")

	// --- scaffold + generate ------------------------------------------------
	if err := cli.Scaffold(ctx, gombit.ScaffoldRequest{
		Dir: dir, Name: "app", Module: module,
		Database: gombit.DatabasePostgres, Auth: gombit.AuthCookie, UI: gombit.UIMinimal,
		Tidy: true,
	}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	s := frontendSpec(t)
	files, err := Compile(s, module)
	if err != nil {
		t.Fatalf("compile app: %v", err)
	}
	for _, f := range files {
		writeAppFile(t, dir, f.Path, f.Content)
	}
	writeAppFile(t, dir, CompositionRootPath, mustCompositionRoot(t, module))

	// --- wire the generated frontend into the scaffold shell ----------------
	// This is the integration step the tutorial (chapter 8) documents as
	// intended; here it is exercised for real.
	wireGeneratedFrontend(t, dir)

	runCmd(t, ctx, dir, nil, "go", "mod", "tidy")

	// --- migration + boot (same flow as the M0 gate) ------------------------
	models, err := MigrationModelsForSpec(s, module)
	if err != nil {
		t.Fatalf("migration models: %v", err)
	}
	if err := cli.MakeMigrations(ctx, gombit.MakeMigrationsRequest{
		Dir: dir, Name: "initial", Driver: gombit.DatabasePostgres, Models: models,
	}); err != nil {
		t.Fatalf("makemigrations: %v", err)
	}

	pg := startPostgres(t, ctx)
	defer pg.stop()

	env := []string{
		"GOMBIT_DATABASE_DRIVER=postgres",
		"GOMBIT_DATABASE_DSN=" + pg.dsn,
		"GOMBIT_JWT_SECRET=forge-fe-e2e-secret-please-change-0001",
		"GOMBIT_AUTH_MODE=cookie",
		"GOMBIT_COOKIE_SECURE=false",
		"GOMBIT_COOKIE_SAMESITE=Lax",
	}
	runCmd(t, ctx, dir, env, gombit.DefaultBinary, "db", "migrate")

	bin := filepath.Join(dir, "server")
	runCmd(t, ctx, dir, nil, "go", "build", "-o", bin, "./cmd/server")

	port := freePort(t)
	appEnv := append(env, "GOMBIT_HTTP_ADDR=127.0.0.1:"+port)
	server := startServer(t, ctx, dir, bin, appEnv)
	defer server.stop()

	base := "http://127.0.0.1:" + port
	waitForHTTP(t, base+"/livez")

	// --- generate the TypeScript client from the LIVE contract --------------
	// The openapi document reflects the routes the composition root actually
	// mounts (our resources, not the scaffold's demo Product), so the generated
	// client's types are exactly what the generated pages import against.
	openapiPath := filepath.Join(dir, "openapi.json")
	fetchToFile(t, base+"/openapi.json", openapiPath)
	runCmd(t, ctx, dir, nil, gombit.DefaultBinary, "client", "generate",
		"--spec", "openapi.json", "--out", "frontend/src/api/generated", "--force")

	// --- build the generated frontend ---------------------------------------
	frontendDir := filepath.Join(dir, "frontend")
	// No lockfile ships in the scaffold, so install rather than ci.
	runCmd(t, ctx, frontendDir, nil, "npm", "install", "--no-audit", "--no-fund", "--loglevel=error")
	// package.json build = "tsc --noEmit && vite build": typechecks the generated
	// TypeScript against the generated client and then bundles it. Either failing
	// is a real integration-seam regression.
	runCmd(t, ctx, frontendDir, nil, "npm", "run", "build")

	// The bundle exists, so vite build actually produced output, not just exited 0.
	if entries, err := os.ReadDir(filepath.Join(frontendDir, "dist")); err != nil || len(entries) == 0 {
		t.Errorf("vite build produced no dist output (err=%v)", err)
	}

	t.Logf("frontend gate cleared: generated React app built for spec with %d pages", len(s.Pages))
}

// frontendSpec is a spec exercising every page kind and the frontend surfaces the
// generators emit: a customer table (search + filter + sort), a customer form, a
// customer detail (which embeds its has_many invoices), an invoice detail, and a
// dashboard with a count card and a server-side aggregate card, plus navigation
// and branding. Building the React app for this spec typechecks all of that
// generated TypeScript against the real generated client.
func frontendSpec(t *testing.T) *spec.ProjectSpec {
	t.Helper()
	s := sampleSpecWithTable(t)

	customer := s.Resources[0]
	invoice := s.Resources[1]
	// sampleSpec declares Invoice.Total aggregatable; make Email/Active sortable
	// so the customer table's headers become ?ordering= toggles too.
	customer.Behavior.SortableFields = []spec.ID{customer.Fields[0].ID, customer.Fields[1].ID}
	invoiceTotal := invoice.Fields[1].ID // Total (decimal, aggregatable)

	// Enrich the dashboard with a server-side aggregate card over the invoices.
	for _, p := range s.Pages {
		if p.Type == spec.PageDashboard && p.Dashboard != nil {
			p.Dashboard.AggregateCards = []spec.AggregateCard{
				{Label: "Total invoiced", Resource: invoice.ID, Field: invoiceTotal, Op: spec.AggregateSum},
			}
		}
	}

	// An invoice detail page, so both a detail with an embedded related list
	// (customer → invoices) and a plain detail are generated.
	s.Pages = append(s.Pages, &spec.Page{
		ID: spec.MustNewID(spec.KindPage), Slug: "invoice", Label: "Invoice",
		Type: spec.PageResourceDetail, Resource: invoice.ID,
	})

	// Navigation drives the generated registry's generatedNavigation.
	var homePage, customersPage spec.ID
	for _, p := range s.Pages {
		switch p.Slug {
		case "home":
			homePage = p.ID
		case "customers":
			customersPage = p.ID
		}
	}
	s.Navigation = []*spec.NavItem{
		{ID: spec.MustNewID(spec.KindNav), Label: "Home", Target: spec.NavPage, Page: homePage},
		{ID: spec.MustNewID(spec.KindNav), Label: "Customers", Target: spec.NavPage, Page: customersPage},
	}
	s.Branding = &spec.Branding{AppName: "Acme CRM", AccentColor: "#6f42c1", Appearance: "system"}

	if d := spec.Validate(s); d != nil {
		t.Fatalf("frontend spec invalid:\n%s", d.Error())
	}
	return s
}

// wireGeneratedFrontend performs the scaffold → Forge frontend integration the
// tutorial documents: it re-points frontend/src/resources.tsx at the generated
// registry and removes the scaffold's demo Product wiring, whose pages import
// OpenAPI types (paths["/api/v1/products"]) that no longer exist once the
// composition root unmounts the demo resource. Each step asserts it actually
// changed the scaffold, so a drift in the scaffold's shape fails loudly here
// rather than silently leaving the integration unexercised.
func wireGeneratedFrontend(t *testing.T, dir string) {
	t.Helper()
	fe := filepath.Join(dir, "frontend", "src")

	// resources.tsx re-exports the generated registry (generatedResources,
	// generatedResourceRoutes, generatedNavigation, generatedBranding), which the
	// scaffold's router.tsx and layouts/AppLayout.tsx already consume from it.
	resources := filepath.Join(fe, "resources.tsx")
	before := readTextFile(t, resources)
	if !strings.Contains(before, "generatedResourceRoutes") {
		t.Fatalf("scaffold resources.tsx no longer defines the registry contract; integration step is stale:\n%s", before)
	}
	writeFileString(t, resources,
		"// Re-points the scaffold registry at Forge's generated one (#186).\n"+
			"export * from \"./forge_generated/resources\";\n")

	// Remove the demo Product pages, whose OpenAPI types vanish once the demo
	// resource is unmounted.
	for _, page := range []string{"pages/ProductListPage.tsx", "pages/ProductFormPage.tsx"} {
		p := filepath.Join(fe, page)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected scaffold demo page %s to remove: %v", page, err)
		}
		if err := os.Remove(p); err != nil {
			t.Fatalf("remove %s: %v", page, err)
		}
	}

	// Drop the demo Product imports and routes from the router; every such line
	// names ProductListPage or ProductFormPage, and the generated routes come in
	// through generatedResourceRoutes instead.
	router := filepath.Join(fe, "app", "router.tsx")
	src := readTextFile(t, router)
	var kept []string
	var dropped int
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "ProductListPage") || strings.Contains(line, "ProductFormPage") {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	if dropped == 0 {
		t.Fatalf("router.tsx has no demo Product wiring to remove; integration step is stale:\n%s", src)
	}
	out := strings.Join(kept, "\n")
	if strings.Contains(out, "Product") {
		t.Fatalf("router.tsx still references the demo Product after removal:\n%s", out)
	}
	writeFileString(t, router, out)
}

// fetchToFile GETs url and writes the body to path, failing on any error or a
// non-2xx status.
func fetchToFile(t *testing.T, url, path string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func writeFileString(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
