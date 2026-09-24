package gen

import (
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// mutationSource returns the generated mutation.go for a resource package name.
func mutationSource(t *testing.T, files []File, pkg string) string {
	t.Helper()
	want := GeneratedRoot + "/" + pkg + "/mutation.go"
	for _, file := range files {
		if file.Path == want {
			return string(file.Content)
		}
	}
	t.Fatalf("no generated file at %s; got %v", want, paths(files))
	return ""
}

func TestMutationsFilePaths(t *testing.T) {
	g, _ := buildGraph(t)
	files, err := Mutations(g)
	if err != nil {
		t.Fatalf("Mutations: %v", err)
	}

	want := []string{
		"internal/forge_generated/customer/mutation.go",
		"internal/forge_generated/invoice/mutation.go",
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

func TestMutationHeaderBannerAndPackage(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Mutations(g)
	src := mutationSource(t, files, "customer")

	if !strings.HasPrefix(src, Banner) {
		t.Errorf("mutation.go must start with the DO-NOT-EDIT banner, got:\n%s", src[:min(len(src), 80)])
	}
	if !strings.Contains(src, "package customer\n") {
		t.Error("mutation.go must declare package customer")
	}
}

// TestCreateDraftAccessorMutatorPairs checks CreateDraft exposes, for each
// writable field, an accessor named by the frozen code symbol and a Set<symbol>
// mutator, both typed to the field's Go type (ADR-001 §25-26).
func TestCreateDraftAccessorMutatorPairs(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Mutations(g)
	src := collapseWS(mutationSource(t, files, "customer"))

	for _, want := range []string{
		"func (d *CustomerCreateDraft) Email() string { return d.email }",
		"func (d *CustomerCreateDraft) SetEmail(v string) { d.email = v }",
		"func (d *CustomerCreateDraft) Active() bool { return d.active }",
		"func (d *CustomerCreateDraft) SetActive(v bool) { d.active = v }",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("CreateDraft missing %q; got:\n%s", want, src)
		}
	}
}

// TestUpdateChangesPresenceSemantics is the §28-29 proof: each accessor returns
// (value, changed) so absence is distinguishable from an explicit zero, and the
// mutator both replaces the value and marks the field changed.
func TestUpdateChangesPresenceSemantics(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Mutations(g)
	src := collapseWS(mutationSource(t, files, "customer"))

	for _, want := range []string{
		// reader yields (value, changed)
		"func (c *CustomerUpdateChanges) Email() (string, bool) { return c.email, c.emailChanged }",
		// writer replaces the value and marks it changed
		"func (c *CustomerUpdateChanges) SetEmail(v string) { c.email = v; c.emailChanged = true }",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("UpdateChanges missing %q; got:\n%s", want, src)
		}
	}
	// The presence bit must be a distinct field, not implied by the value.
	if !strings.Contains(src, "emailChanged bool") {
		t.Errorf("UpdateChanges must carry an explicit presence bit; got:\n%s", src)
	}
}

// TestMutationBelongsToIsWritable checks a belongs_to foreign key is a writable
// draft field (SetCustomerID(uint)) — a create sets the relationship by its key.
func TestMutationBelongsToIsWritable(t *testing.T) {
	g, _ := buildGraph(t)
	files, _ := Mutations(g)
	src := collapseWS(mutationSource(t, files, "invoice"))

	for _, want := range []string{
		"func (d *InvoiceCreateDraft) CustomerID() uint { return d.customerID }",
		"func (d *InvoiceCreateDraft) SetCustomerID(v uint) { d.customerID = v }",
		"func (c *InvoiceUpdateChanges) CustomerID() (uint, bool) { return c.customerID, c.customerIDChanged }",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("Invoice mutation surface missing %q; got:\n%s", want, src)
		}
	}
}

// TestMutationSurvivesRelabelAndStorageRename is the §23 guarantee for the
// mutation ABI: label and storage_name are independent naming domains, so
// changing them while code symbols are frozen leaves every type, field and
// method definition identical. Only the label-bearing doc comments may differ.
func TestMutationSurvivesRelabelAndStorageRename(t *testing.T) {
	base, _ := buildGraph(t)
	baseFiles, err := Mutations(base)
	if err != nil {
		t.Fatalf("Mutations(base): %v", err)
	}

	renamed := renameLabelsAndStorage(t)
	renamedFiles, err := Mutations(renamed)
	if err != nil {
		t.Fatalf("Mutations(renamed): %v", err)
	}

	for _, pkg := range []string{"customer", "invoice"} {
		before := stripComments(mutationSource(t, baseFiles, pkg))
		after := stripComments(mutationSource(t, renamedFiles, pkg))
		if before != after {
			t.Errorf("mutation ABI for %s changed under relabel/storage rename;\n--- before ---\n%s\n--- after ---\n%s",
				pkg, before, after)
		}
	}
}

// TestMutationRejectsMutatorAccessorCollision gives the §12 guard teeth: a field
// code symbol "SetEmail" and a field "Email" would put two SetEmail methods on
// the draft (the first's accessor, the second's mutator), so generation must
// refuse rather than leave it for go build.
func TestMutationRejectsMutatorAccessorCollision(t *testing.T) {
	g := twoScalarGraph(t, "Email", "SetEmail")
	if _, err := Mutations(g); err == nil {
		t.Fatal("Mutations must reject a Set<Field> mutator colliding with another field's accessor")
	}
}

// TestMutationRejectsPresenceBitCollision covers the other class: a field
// "Alice" yields the presence bit "aliceChanged", which a field "AliceChanged"
// folds onto, duplicating a struct field in UpdateChanges.
func TestMutationRejectsPresenceBitCollision(t *testing.T) {
	g := twoScalarGraph(t, "Alice", "AliceChanged")
	if _, err := Mutations(g); err == nil {
		t.Fatal("Mutations must reject a value/presence-bit field-name collision")
	}
}

func TestMutationsNilGraph(t *testing.T) {
	if _, err := Mutations(nil); err == nil {
		t.Error("Mutations(nil) must error")
	}
}

// collapseWS joins the source on whitespace runs so a substring check ignores
// gofmt's column alignment and line breaks.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// stripComments drops every //-comment line, leaving the label-independent
// type/field/method definitions to compare.
func stripComments(src string) string {
	var kept []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// renameLabelsAndStorage rebuilds the fixture graph with every label and
// storage_name changed but every code symbol frozen.
func renameLabelsAndStorage(t *testing.T) *graph.Graph {
	t.Helper()
	g, _ := buildGraph(t)
	for _, resource := range g.Resources {
		resource.Spec.Label = "Renamed " + resource.Spec.Label
		resource.Spec.StorageName = resource.Spec.StorageName + "_v2"
		for i, field := range resource.Fields {
			field.Spec.Label = "Renamed field"
			field.Spec.StorageName = renamedCol(field.Spec.StorageName, i)
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

func renamedCol(old string, i int) string {
	if strings.HasSuffix(old, "_id") {
		return "renamed_ref_id"
	}
	return "renamed_col_" + string(rune('a'+i))
}

// twoScalarGraph builds a one-resource graph with two string fields whose code
// symbols are code1 and code2 — the seam the collision guards are tested on.
func twoScalarGraph(t *testing.T, code1, code2 string) *graph.Graph {
	t.Helper()
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{
				ID: id(spec.KindResource), Label: "Thing",
				CodeName: "Thing", StorageName: "things",
				Fields: []*spec.Field{
					{ID: id(spec.KindField), Label: "One", Type: spec.TypeString, CodeName: code1, StorageName: "one"},
					{ID: id(spec.KindField), Label: "Two", Type: spec.TypeString, CodeName: code2, StorageName: "two"},
				},
			},
		},
	}
	if diagnostics := spec.Validate(s); diagnostics != nil {
		t.Fatalf("fixture spec invalid (%s/%s):\n%s", code1, code2, diagnostics.Error())
	}
	g, err := graph.Build(s)
	if err != nil {
		t.Fatalf("build graph (%s/%s): %v", code1, code2, err)
	}
	return g
}
