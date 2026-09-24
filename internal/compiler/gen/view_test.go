package gen

import (
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// viewSource returns the generated view.go for a resource package name.
func viewSource(t *testing.T, files []File, pkg string) string {
	t.Helper()
	want := GeneratedRoot + "/" + pkg + "/view.go"
	for _, file := range files {
		if file.Path == want {
			return string(file.Content)
		}
	}
	t.Fatalf("no generated file at %s; got %v", want, paths(files))
	return ""
}

func TestViewsFilePaths(t *testing.T) {
	g, _ := buildGraph(t)
	files, err := Views(g)
	if err != nil {
		t.Fatalf("Views: %v", err)
	}

	// One file per resource, in authored order, under the compiler-owned root.
	want := []string{
		"internal/forge_generated/customer/view.go",
		"internal/forge_generated/invoice/view.go",
	}
	got := paths(files)
	if len(got) != len(want) {
		t.Fatalf("file count: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestViewHeaderBannerAndPackage(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Views(g)
	src := viewSource(t, files, "customer")

	if !strings.HasPrefix(src, Banner) {
		t.Errorf("view must start with the DO-NOT-EDIT banner, got:\n%s", src[:min(len(src), 80)])
	}
	if !strings.Contains(src, "package customer\n") {
		t.Error("view must declare package customer")
	}
}

// TestViewInterfaceAccessors checks the interface exposes an ID() accessor plus
// one accessor per field, named by the field's frozen code symbol and typed to
// the field's Go type — the extension ABI (ADR-001 §21, §23).
func TestViewInterfaceAccessors(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Views(g)
	src := viewSource(t, files, "customer")

	body := interfaceBody(t, src, "CustomerView")
	want := []string{
		"ID() uint",
		"Email() string",
		"Active() bool",
		"Tier() string",
		"Joined() time.Time",
	}
	for _, sig := range want {
		if !strings.Contains(body, sig) {
			t.Errorf("interface CustomerView missing accessor %q; body:\n%s", sig, body)
		}
	}
}

// TestViewAccessorsFollowAuthoredOrder pins that accessor order is the authored
// field order (identity first), which drives nothing user-visible but is part
// of the determinism contract for generated output.
func TestViewAccessorsFollowAuthoredOrder(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Views(g)
	body := interfaceBody(t, viewSource(t, files, "customer"), "CustomerView")

	order := []string{"ID()", "Email()", "Active()", "Tier()", "Joined()"}
	last := -1
	for _, name := range order {
		at := strings.Index(body, name)
		if at == -1 {
			t.Fatalf("accessor %q not found in interface body:\n%s", name, body)
		}
		if at < last {
			t.Errorf("accessor %q is out of authored order", name)
		}
		last = at
	}
}

// TestViewBelongsToExposesForeignKey checks a belongs_to is exposed as its
// foreign-key accessor <Code>ID() uint — the model's stored key — not a nested
// view or the raw association (F0's narrow ABI).
func TestViewBelongsToExposesForeignKey(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Views(g)
	body := interfaceBody(t, viewSource(t, files, "invoice"), "InvoiceView")

	if !strings.Contains(body, "CustomerID() uint") {
		t.Errorf("belongs_to must be exposed as CustomerID() uint; body:\n%s", body)
	}
}

// TestViewDoesNotExposePersistenceModel is the §22 invariant: the ABI surface
// (the interface) must not leak the GORM model type or any ORM detail. The
// construction seam (NewXView) legitimately takes the model, so the check is
// scoped to the interface body.
func TestViewDoesNotExposePersistenceModel(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Views(g)

	for _, tc := range []struct{ pkg, iface, model string }{
		{"customer", "CustomerView", "Customer"},
		{"invoice", "InvoiceView", "Invoice"},
	} {
		body := interfaceBody(t, viewSource(t, files, tc.pkg), tc.iface)
		if strings.Contains(body, "*"+tc.model) {
			t.Errorf("%s interface leaks the persistence model *%s:\n%s", tc.iface, tc.model, body)
		}
		if strings.Contains(body, "gorm") {
			t.Errorf("%s interface leaks a gorm reference:\n%s", tc.iface, body)
		}
	}
}

// TestViewAccessorsSurviveRelabelAndStorageRename is the core §23 guarantee:
// label and storage_name are independent naming domains, so changing them while
// the code symbols are frozen must leave every accessor signature untouched, so
// existing hooks stay source-compatible. The ABI surface is the interface body;
// like model.go, the doc comment echoes the current label and is allowed to
// differ — it is a comment, not part of the contract.
func TestViewAccessorsSurviveRelabelAndStorageRename(t *testing.T) {
	base, _ := buildGraph(t)
	baseFiles, err := Views(base)
	if err != nil {
		t.Fatalf("Views(base): %v", err)
	}

	renamed := relabelAndRenameStorage(t)
	renamedFiles, err := Views(renamed)
	if err != nil {
		t.Fatalf("Views(renamed): %v", err)
	}

	for _, tc := range []struct{ pkg, iface string }{
		{"customer", "CustomerView"},
		{"invoice", "InvoiceView"},
	} {
		before := interfaceBody(t, viewSource(t, baseFiles, tc.pkg), tc.iface)
		after := interfaceBody(t, viewSource(t, renamedFiles, tc.pkg), tc.iface)
		if before != after {
			t.Errorf("accessor signatures for %s changed under relabel/storage rename;\n--- before ---\n%s\n--- after ---\n%s",
				tc.iface, before, after)
		}
	}
}

// TestViewImplBacksModelField checks the generated implementation reads the
// matching struct field, so the view actually reflects persisted values.
func TestViewImplBacksModelField(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Views(g)
	// gofmt aligns the consecutive one-line method bodies, so compare with
	// internal runs of spaces collapsed rather than pinning the padding.
	src := collapseSpaces(viewSource(t, files, "customer"))

	for _, want := range []string{
		"func (v customerView) ID() uint { return v.model.ID }",
		"func (v customerView) Email() string { return v.model.Email }",
		"func NewCustomerView(model *Customer) CustomerView { return customerView{model: model} }",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("view.go missing %q; got:\n%s", want, src)
		}
	}
}

func TestViewsNilGraph(t *testing.T) {
	if _, err := Views(nil); err == nil {
		t.Error("Views(nil) must error")
	}
}

// interfaceBody returns the text between the braces of `type <name> interface {
// ... }` in src.
func interfaceBody(t *testing.T, src, name string) string {
	t.Helper()
	head := "type " + name + " interface {"
	start := strings.Index(src, head)
	if start == -1 {
		t.Fatalf("no interface %s in:\n%s", name, src)
	}
	rest := src[start+len(head):]
	end := strings.Index(rest, "}")
	if end == -1 {
		t.Fatalf("unterminated interface %s in:\n%s", name, src)
	}
	return rest[:end]
}

// relabelAndRenameStorage rebuilds the fixture graph with every label and
// storage_name changed but every code symbol frozen — the visual-rename case
// §23 promises leaves the extension ABI untouched.
func relabelAndRenameStorage(t *testing.T) *graph.Graph {
	t.Helper()
	g, _ := buildGraph(t)
	for _, resource := range g.Resources {
		resource.Spec.Label = "Renamed " + resource.Spec.Label
		resource.Spec.StorageName = resource.Spec.StorageName + "_v2"
		for i, field := range resource.Fields {
			field.Spec.Label = "Renamed field"
			// Distinct new column names so storage stays a valid, unique set.
			field.Spec.StorageName = renamedColumn(field.Spec.StorageName, i)
		}
	}
	if diagnostics := spec.Validate(g.Spec); diagnostics != nil {
		t.Fatalf("renamed fixture is invalid:\n%s", diagnostics.Error())
	}
	rebuilt, err := graph.Build(g.Spec)
	if err != nil {
		t.Fatalf("rebuild renamed graph: %v", err)
	}
	return rebuilt
}

// collapseSpaces replaces every run of spaces with a single space, so a
// substring check ignores gofmt's column alignment.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func renamedColumn(old string, i int) string {
	// A belongs_to column must stay <something>_id for GORM's convention and the
	// generator's derivation; keep the suffix, change the stem.
	if strings.HasSuffix(old, "_id") {
		return "renamed_ref_id"
	}
	return "renamed_col_" + string(rune('a'+i))
}
