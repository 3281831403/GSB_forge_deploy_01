package gen

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/gombit"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// TestGeneratedHandlersCompileInGombitApp is the issue #7 acceptance check that
// the generated handlers and routes compile against the real Gombit framework.
//
// The model, handlers and routes import framework, contract, database, huma and
// gorm, so a self-contained module is not enough: the test scaffolds an actual
// Gombit application (which resolves the whole framework graph), writes the
// generated package into it, and runs `go build ./...`.
//
// It requires an installed Gombit toolchain, so — like internal/gombit's
// integration tests — it skips when gombit is absent or in -short. CI, which
// has no gombit binary, exercises the structural unit tests instead.
func TestGeneratedHandlersCompileInGombitApp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping gombit-app compile test in -short")
	}
	if _, err := exec.LookPath(gombit.DefaultBinary); err != nil {
		t.Skipf("gombit not on PATH: %v", err)
	}

	cli := &gombit.CLI{}
	version, err := cli.Version(context.Background())
	if err != nil {
		t.Fatalf("gombit version: %v", err)
	}
	if err := gombit.CheckSupported(version); err != nil {
		t.Skipf("installed toolchain unsupported: %v", err)
	}

	// A resource with every toggle on and admin visible, so every stage emits.
	g, _ := buildGraph(t)
	for _, resource := range g.Resources {
		resource.Spec.Behavior.CreateEnabled = true
		resource.Spec.Behavior.UpdateEnabled = true
		resource.Spec.Behavior.DeleteEnabled = true
		resource.Spec.Behavior.AdminVisible = true
	}

	const sampleModule = "example.com/sample"

	models, err := Models(g)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	views, err := Views(g)
	if err != nil {
		t.Fatalf("Views: %v", err)
	}
	mutations, err := Mutations(g)
	if err != nil {
		t.Fatalf("Mutations: %v", err)
	}
	extension, err := Extension(g)
	if err != nil {
		t.Fatalf("Extension: %v", err)
	}
	fieldRefs, err := FieldRefs(g, sampleModule)
	if err != nil {
		t.Fatalf("FieldRefs: %v", err)
	}
	handlers, err := Handlers(g)
	if err != nil {
		t.Fatalf("Handlers: %v", err)
	}
	adminFiles, err := Admin(g)
	if err != nil {
		t.Fatalf("Admin: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "sample")
	req, err := gombit.ScaffoldRequestFor(specFromGraph(t, g), dir, sampleModule)
	if err != nil {
		t.Fatalf("scaffold request: %v", err)
	}
	req.Tidy = true // resolve the framework graph (cached from prior builds)
	if err := cli.Scaffold(context.Background(), req); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	generated := append(append(append(append(append(append(models, views...), mutations...), extension...), fieldRefs...), handlers...), adminFiles...)
	for _, file := range generated {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(file.Path)), string(file.Content))
	}

	// The generated model pulls in shopspring/decimal, which a bare scaffold
	// does not require, so re-resolve the module graph before building.
	if out, err := runGoModTidy(t, dir); err != nil {
		t.Fatalf("go mod tidy after writing generated files:\n%s", out)
	}

	if out, err := runGoBuild(t, dir); err != nil {
		t.Fatalf("generated code did not compile in a Gombit app:\n%s", out)
	}
}

func runGoModTidy(t *testing.T, dir string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// specFromGraph returns the spec a graph was built from; scaffolding needs a
// valid project (slug, driver, auth) which the test fixture provides.
func specFromGraph(t *testing.T, g *graph.Graph) *spec.ProjectSpec {
	t.Helper()
	if g.Spec == nil {
		t.Fatal("graph has no spec")
	}
	return g.Spec
}
