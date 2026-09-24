package gen

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/gombit"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

const hooksModule = "example.com/app"

// hookedGraph returns the two-resource fixture with the given events enabled on
// the customer resource (the invoice resource stays hook-free).
func hookedGraph(t *testing.T, events ...spec.HookEvent) *graph.Graph {
	t.Helper()
	g, _ := buildGraph(t)
	hooks := make([]*spec.Hook, 0, len(events))
	for _, e := range events {
		hooks = append(hooks, &spec.Hook{ID: spec.MustNewID(spec.KindHook), Event: e})
	}
	g.Resources[0].Spec.Hooks = hooks
	if d := spec.Validate(g.Spec); d != nil {
		t.Fatalf("hooked fixture invalid:\n%s", d.Error())
	}
	return g
}

func hooksSource(t *testing.T, files []File, pkg string) string {
	t.Helper()
	want := GeneratedRoot + "/" + pkg + "/hooks.go"
	for _, file := range files {
		if file.Path == want {
			return string(file.Content)
		}
	}
	t.Fatalf("no generated file at %s; got %v", want, paths(files))
	return ""
}

// TestHooksEmitsOnlyForResourcesWithHooks: the contract file exists only for a
// resource that declares hooks, so a hook-free spec's tree is unchanged.
func TestHooksEmitsOnlyForResourcesWithHooks(t *testing.T) {
	hookless, _ := buildGraph(t)
	if files, err := Hooks(hookless); err != nil || len(files) != 0 {
		t.Fatalf("hook-free graph must emit no hook files; got %v (err %v)", paths(files), err)
	}

	g := hookedGraph(t, spec.HookAfterCreate)
	files, err := Hooks(g)
	if err != nil {
		t.Fatalf("Hooks: %v", err)
	}
	if got := paths(files); len(got) != 1 || got[0] != "internal/forge_generated/customer/hooks.go" {
		t.Fatalf("expected only customer/hooks.go; got %v", got)
	}
}

// TestHookContractMethodsPerEvent: every enabled event becomes a contract method
// with the right signature, typed against the generated view/draft/change
// surfaces, in authored order.
func TestHookContractMethodsPerEvent(t *testing.T) {
	g := hookedGraph(t,
		spec.HookBeforeCreate, spec.HookAfterCreate,
		spec.HookBeforeUpdate, spec.HookAfterUpdate,
		spec.HookBeforeDelete, spec.HookAfterDelete,
	)
	files, _ := Hooks(g)
	body := collapseWS(interfaceBody(t, hooksSource(t, files, "customer"), "CustomerHooks"))

	want := []string{
		"BeforeCreate(ctx context.Context, draft *CustomerCreateDraft) error",
		"AfterCreate(ctx context.Context, created CustomerView) error",
		"BeforeUpdate(ctx context.Context, current CustomerView, changes *CustomerUpdateChanges) error",
		"AfterUpdate(ctx context.Context, updated CustomerView) error",
		"BeforeDelete(ctx context.Context, current CustomerView) error",
		"AfterDelete(ctx context.Context, deleted CustomerView) error",
	}
	last := -1
	for _, sig := range want {
		at := strings.Index(body, sig)
		if at == -1 {
			t.Errorf("contract missing method %q; body:\n%s", sig, body)
			continue
		}
		if at < last {
			t.Errorf("method %q out of authored order", sig)
		}
		last = at
	}
}

// TestHookRegistrationSurface: the static, reflection-free registration API.
func TestHookRegistrationSurface(t *testing.T) {
	g := hookedGraph(t, spec.HookAfterCreate)
	files, err := Hooks(g)
	if err != nil {
		t.Fatalf("Hooks: %v", err)
	}
	src := collapseWS(hooksSource(t, files, "customer"))
	for _, want := range []string{
		"var registeredCustomerHooks CustomerHooks",
		"func RegisterCustomerHooks(h CustomerHooks) { registeredCustomerHooks = h }",
		"func CustomerHookImpl() CustomerHooks { return registeredCustomerHooks }",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("hooks.go missing %q; got:\n%s", want, src)
		}
	}
	// No reflection in generated registration: the registration is a plain typed
	// assignment, so the reflect package must not be imported or referenced.
	if strings.Contains(src, `"reflect"`) || strings.Contains(src, "reflect.") {
		t.Error("hook registration must not use the reflect package")
	}
}

// TestHookContractSurvivesRelabel is the §23/§34 stability guarantee: the
// contract's method signatures derive from the frozen code symbol and the event,
// so a relabel or storage rename leaves them identical (only doc comments, which
// echo the label, may differ).
func TestHookContractSurvivesRelabel(t *testing.T) {
	base := hookedGraph(t, spec.HookBeforeCreate, spec.HookAfterCreate)
	baseFiles, err := Hooks(base)
	if err != nil {
		t.Fatalf("Hooks(base): %v", err)
	}
	before := interfaceBody(t, hooksSource(t, baseFiles, "customer"), "CustomerHooks")

	// Relabel and rename storage on the same spec; code symbols and events frozen.
	for _, resource := range base.Resources {
		resource.Spec.Label = "Renamed " + resource.Spec.Label
		resource.Spec.StorageName += "_v2"
		for i, field := range resource.Fields {
			field.Spec.Label = "Renamed field"
			if strings.HasSuffix(field.Spec.StorageName, "_id") {
				field.Spec.StorageName = "renamed_ref_id"
			} else {
				field.Spec.StorageName = "renamed_col_" + string(rune('a'+i))
			}
		}
	}
	if d := spec.Validate(base.Spec); d != nil {
		t.Fatalf("renamed fixture invalid:\n%s", d.Error())
	}
	renamed, err := graph.Build(base.Spec)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	renamedFiles, err := Hooks(renamed)
	if err != nil {
		t.Fatalf("Hooks(renamed): %v", err)
	}
	after := interfaceBody(t, hooksSource(t, renamedFiles, "customer"), "CustomerHooks")

	if before != after {
		t.Errorf("hook contract signatures changed under relabel/storage rename;\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestHooksNilGraph(t *testing.T) {
	if _, err := Hooks(nil); err == nil {
		t.Error("Hooks(nil) must error")
	}
}

// --- stub ---

func stubSource(t *testing.T, files []File, pkg string) string {
	t.Helper()
	want := ExtensionsRoot + "/" + pkg + "/hooks.go"
	for _, file := range files {
		if file.Path == want {
			return string(file.Content)
		}
	}
	t.Fatalf("no stub at %s; got %v", want, paths(files))
	return ""
}

// TestHookStubImplementsContract: the stub declares Hooks with a compile-time
// assertion that it satisfies the generated contract, and a no-op method per
// event with the generated types qualified through the `generated` alias.
func TestHookStubImplementsContract(t *testing.T) {
	g := hookedGraph(t, spec.HookBeforeCreate, spec.HookAfterCreate, spec.HookBeforeUpdate)
	files, err := HookStubs(g, hooksModule)
	if err != nil {
		t.Fatalf("HookStubs: %v", err)
	}
	if got := paths(files); len(got) != 1 || got[0] != "internal/extensions/customer/hooks.go" {
		t.Fatalf("expected only the customer stub; got %v", got)
	}
	src := collapseWS(stubSource(t, files, "customer"))

	for _, want := range []string{
		"package customer",
		`generated "example.com/app/internal/forge_generated/customer"`,
		"type Hooks struct{}",
		"var _ generated.CustomerHooks = Hooks{}",
		"func (Hooks) BeforeCreate(ctx context.Context, draft *generated.CustomerCreateDraft) error {",
		"func (Hooks) AfterCreate(ctx context.Context, created generated.CustomerView) error {",
		"func (Hooks) BeforeUpdate(ctx context.Context, current generated.CustomerView, changes *generated.CustomerUpdateChanges) error {",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("stub missing %q; got:\n%s", want, src)
		}
	}
	// The stub is user-owned: it must NOT carry the compiler-owned DO-NOT-EDIT
	// banner (that would mark it for the wipe/regeneration it must never receive).
	if strings.Contains(src, "DO NOT EDIT") {
		t.Error("the one-time stub must not be marked DO NOT EDIT")
	}
}

func TestHookStubsNilGraphAndEmptyModule(t *testing.T) {
	g := hookedGraph(t, spec.HookAfterCreate)
	if _, err := HookStubs(nil, hooksModule); err == nil {
		t.Error("HookStubs(nil, …) must error")
	}
	if _, err := HookStubs(g, ""); err == nil {
		t.Error("HookStubs(…, \"\") must error")
	}
}

// --- wiring ---

// TestWiringRegistersHooks: the composition root imports the extension package
// and registers its Hooks, for hooked resources only.
func TestWiringRegistersHooks(t *testing.T) {
	g := hookedGraph(t, spec.HookAfterCreate)
	files, err := Wiring(g, hooksModule)
	if err != nil {
		t.Fatalf("Wiring: %v", err)
	}
	src := files[0].Content
	s := string(src)
	for _, want := range []string{
		`customerext "example.com/app/internal/extensions/customer"`,
		"customer.RegisterCustomerHooks(customerext.Hooks{})",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("register.go missing %q; got:\n%s", want, s)
		}
	}
	// The hook-free invoice resource must not be registered or imported.
	if strings.Contains(s, "invoiceext") || strings.Contains(s, "RegisterInvoiceHooks") {
		t.Errorf("hook-free resource must not appear in hook wiring; got:\n%s", s)
	}
}

// TestWiringUnchangedWithoutHooks: a hook-free graph produces the same register.go
// as before this stage — no extension import, no hook registration.
func TestWiringUnchangedWithoutHooks(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Wiring(g, hooksModule)
	s := string(files[0].Content)
	if strings.Contains(s, "ext ") || strings.Contains(s, "Hooks") || strings.Contains(s, "extensions") {
		t.Errorf("hook-free wiring must not reference hooks/extensions; got:\n%s", s)
	}
}

// TestFieldRefsRejectHookContractCollision: with hooks enabled, the contract
// interface <Code>Hooks is reserved, so a field ref that lands on it is caught;
// without hooks, the same field name is fine (conditional reservation).
func TestFieldRefsRejectHookContractCollision(t *testing.T) {
	// resource "Field" + field "Hooks" -> var FieldHooks.
	withHooks := refCollisionGraph(t, "Field", "Hooks")
	withHooks.Resources[0].Spec.Hooks = []*spec.Hook{
		{ID: spec.MustNewID(spec.KindHook), Event: spec.HookAfterCreate},
	}
	if d := spec.Validate(withHooks.Spec); d != nil {
		t.Fatalf("fixture invalid:\n%s", d.Error())
	}
	if _, err := FieldRefs(withHooks, hooksModule); err == nil {
		t.Error("with hooks enabled, FieldHooks var must be rejected (collides with FieldHooks contract)")
	}

	hookless := refCollisionGraph(t, "Field", "Hooks")
	if _, err := FieldRefs(hookless, hooksModule); err != nil {
		t.Errorf("without hooks, FieldHooks var is fine (no contract type); got error: %v", err)
	}
}

// TestGeneratedHooksCompileInGombitApp is the §90 integration check: enabling
// lifecycle hooks produces a generated contract, a composition root that
// registers the user stub, and a one-time stub that satisfies the contract —
// and all of it compiles together in a real Gombit application. It writes the
// stub under internal/extensions (its user-owned home), the rest under the
// generated tree, then builds.
func TestGeneratedHooksCompileInGombitApp(t *testing.T) {
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

	const sampleModule = "example.com/sample"

	// Enable every lifecycle event on the customer resource, exercising the
	// draft, view and change-set surfaces in one contract.
	g := hookedGraph(t,
		spec.HookBeforeCreate, spec.HookAfterCreate,
		spec.HookBeforeUpdate, spec.HookAfterUpdate,
		spec.HookBeforeDelete, spec.HookAfterDelete,
	)
	for _, resource := range g.Resources {
		resource.Spec.Behavior.CreateEnabled = true
		resource.Spec.Behavior.UpdateEnabled = true
		resource.Spec.Behavior.DeleteEnabled = true
		resource.Spec.Behavior.AdminVisible = true
	}

	// Every generated stage, plus the one-time stub.
	var generated []File
	for _, stage := range []struct {
		name string
		run  func(*graph.Graph) ([]File, error)
	}{
		{"models", Models}, {"views", Views}, {"mutations", Mutations},
		{"extension", Extension}, {"hooks", Hooks}, {"handlers", Handlers},
		{"admin", Admin},
	} {
		out, err := stage.run(g)
		if err != nil {
			t.Fatalf("%s: %v", stage.name, err)
		}
		generated = append(generated, out...)
	}
	for _, mod := range []struct {
		name string
		run  func(*graph.Graph, string) ([]File, error)
	}{
		{"fieldrefs", FieldRefs}, {"wiring", Wiring},
	} {
		out, err := mod.run(g, sampleModule)
		if err != nil {
			t.Fatalf("%s: %v", mod.name, err)
		}
		generated = append(generated, out...)
	}
	stubs, err := HookStubs(g, sampleModule)
	if err != nil {
		t.Fatalf("HookStubs: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "sample")
	req, err := gombit.ScaffoldRequestFor(specFromGraph(t, g), dir, sampleModule)
	if err != nil {
		t.Fatalf("scaffold request: %v", err)
	}
	req.Tidy = true
	if err := cli.Scaffold(context.Background(), req); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	for _, file := range append(generated, stubs...) {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(file.Path)), string(file.Content))
	}
	if out, err := runGoModTidy(t, dir); err != nil {
		t.Fatalf("go mod tidy:\n%s", out)
	}
	if out, err := runGoBuild(t, dir); err != nil {
		t.Fatalf("generated hook code did not compile in a Gombit app:\n%s", out)
	}
}
