package gen

import (
	"fmt"
	"path"
	"strings"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Mutations generates one mutation.go per resource under
// internal/forge_generated/<resource>/ (ADR-001 §24-31).
//
// The file defines the before-hook mutation surface: two generated types whose
// method names derive from frozen code symbols, so a relabel or storage rename
// never moves a signature (§23).
//
//   - <Type>CreateDraft is the mutable before-create surface (§25-26): an
//     accessor/mutator pair per writable field. BeforeCreate reads, normalizes,
//     computes or sets fields through it — never by touching the persistence
//     model (the locked invariant of §26).
//   - <Type>UpdateChanges is the before-update change set (§28-29): updates
//     carry presence semantics, so a field absent from the update must stay
//     distinguishable from one explicitly set to its zero value. Each accessor
//     returns (value, changed); each mutator sets the value and marks it
//     changed.
//
// Nullable ClearX semantics are deliberately omitted: §26/§29 mark the nullable
// API shape provisional for F0. The locked, non-provisional guarantees —
// mutation through the generated contract, and absence distinguishable from
// zero — are what this stage implements.
//
// Files come back in the graph's authored resource order.
func Mutations(g *graph.Graph) ([]File, error) {
	if g == nil {
		return nil, fmt.Errorf("gen: nil graph")
	}
	if err := validatePackages(g); err != nil {
		return nil, err
	}

	files := make([]File, 0, len(g.Resources))
	for _, resource := range g.Resources {
		file, err := mutationFile(resource)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

// mutationMember is one field's projection into the mutation surface.
type mutationMember struct {
	accessor string // Email, or CustomerID for a belongs_to — the frozen code symbol
	mutator  string // SetEmail
	backing  string // email — the unexported value field
	changed  string // emailChanged — the UpdateChanges presence bit
	goType   string // string, int64, decimal.Decimal, uint, ...
}

func mutationFile(resource *graph.Resource) (File, error) {
	if err := validateNames(resource); err != nil {
		return File{}, err
	}
	if err := validateMutationNames(resource); err != nil {
		return File{}, err
	}

	members := make([]mutationMember, 0, len(resource.Fields))
	imports := map[string]struct{}{}
	for _, field := range resource.Fields {
		mapping, err := resolveType(field)
		if err != nil {
			return File{}, err
		}
		if mapping.importPath != "" {
			imports[mapping.importPath] = struct{}{}
		}
		name := goFieldName(field)
		backing := lowerFirst(name)
		members = append(members, mutationMember{
			accessor: name,
			mutator:  "Set" + name,
			backing:  backing,
			changed:  backing + "Changed",
			goType:   mapping.goType,
		})
	}

	draft := resource.CodeName() + "CreateDraft"
	changes := resource.CodeName() + "UpdateChanges"

	var b strings.Builder
	b.WriteString(Banner)
	b.WriteString("\n\npackage ")
	b.WriteString(PackageName(resource))
	b.WriteString("\n\n")
	b.WriteString(renderImports(imports))
	b.WriteString("\n")

	// CreateDraft: a mutable candidate value before persistence.
	fmt.Fprintf(&b, "// %s is the mutable before-create surface for a %q (ADR-001 §25-26).\n", draft, resource.Spec.Label)
	fmt.Fprintf(&b, "//\n")
	fmt.Fprintf(&b, "// BeforeCreate receives a *%s and reads or sets each writable field through\n", draft)
	fmt.Fprintf(&b, "// these generated accessor/mutator pairs, never through the persistence model.\n")
	fmt.Fprintf(&b, "type %s struct {\n", draft)
	for _, m := range members {
		fmt.Fprintf(&b, "\t%s %s\n", m.backing, m.goType)
	}
	b.WriteString("}\n\n")
	for _, m := range members {
		fmt.Fprintf(&b, "func (d *%s) %s() %s { return d.%s }\n", draft, m.accessor, m.goType, m.backing)
		fmt.Fprintf(&b, "func (d *%s) %s(v %s) { d.%s = v }\n", draft, m.mutator, m.goType, m.backing)
	}
	b.WriteString("\n")

	// UpdateChanges: a change set with presence semantics.
	fmt.Fprintf(&b, "// %s is the mutable before-update change set for a %q (ADR-001 §28-29).\n", changes, resource.Spec.Label)
	fmt.Fprintf(&b, "//\n")
	fmt.Fprintf(&b, "// Each accessor returns (value, changed) so a field absent from the update\n")
	fmt.Fprintf(&b, "// stays distinct from one explicitly set to its zero value; each mutator sets\n")
	fmt.Fprintf(&b, "// the value and marks the field changed.\n")
	fmt.Fprintf(&b, "type %s struct {\n", changes)
	for _, m := range members {
		fmt.Fprintf(&b, "\t%s %s\n", m.backing, m.goType)
		fmt.Fprintf(&b, "\t%s bool\n", m.changed)
	}
	b.WriteString("}\n\n")
	for _, m := range members {
		fmt.Fprintf(&b, "func (c *%s) %s() (%s, bool) { return c.%s, c.%s }\n",
			changes, m.accessor, m.goType, m.backing, m.changed)
		fmt.Fprintf(&b, "func (c *%s) %s(v %s) { c.%s = v; c.%s = true }\n",
			changes, m.mutator, m.goType, m.backing, m.changed)
	}

	relPath := path.Join(PackageDir(resource), "mutation.go")
	return formatGo(relPath, b.String())
}

// validateMutationNames rejects a resource whose mutation surface would emit
// two identifiers that collide, before any source is emitted (ADR-001 §12,
// §36: reserve, don't discover). It covers the identifier classes this stage
// introduces on top of the model's field set:
//
//   - Method names: each field contributes an accessor (its code symbol) and a
//     "Set"+symbol mutator to one method namespace. A field code symbol
//     "SetEmail" and a field "Email" would put two "SetEmail" methods on the
//     draft — the accessor of the first and the mutator of the second.
//   - Struct field names: UpdateChanges carries an unexported value field and a
//     matching "<value>Changed" presence bit per field. A field folding to
//     "aliceChanged" and a field "Alice" would emit that name twice.
//
// go/format parses but does not type-check, so a duplicate method or struct
// field would otherwise reach go build. These are pathological — every case
// needs a hand-picked code symbol — but the guard fails loudly with the two
// offending fields rather than leaving it for the compiler.
func validateMutationNames(resource *graph.Resource) error {
	methods := make(map[string]spec.ID, len(resource.Fields)*2)
	fieldNames := make(map[string]spec.ID, len(resource.Fields)*2)

	claim := func(table map[string]spec.ID, name string, owner spec.ID, kind string) error {
		if prior, taken := table[name]; taken {
			return fmt.Errorf(
				"gen: fields %s and %s both generate the mutation %s %q; rename a code symbol",
				prior, owner, kind, name)
		}
		table[name] = owner
		return nil
	}

	for _, field := range resource.Fields {
		name := goFieldName(field)
		backing := lowerFirst(name)
		if err := claim(methods, name, field.Spec.ID, "method"); err != nil {
			return err
		}
		if err := claim(methods, "Set"+name, field.Spec.ID, "method"); err != nil {
			return err
		}
		if err := claim(fieldNames, backing, field.Spec.ID, "field"); err != nil {
			return err
		}
		if err := claim(fieldNames, backing+"Changed", field.Spec.ID, "field"); err != nil {
			return err
		}
	}
	return nil
}

// lowerFirst folds the first rune of an exported identifier to lower case,
// deriving the unexported backing-field name (Email -> email, CustomerID ->
// customerID). Code symbols are validated exported Go identifiers, so the ASCII
// first rune is always a letter A-Z and the result is a legal unexported
// identifier.
func lowerFirst(name string) string {
	if name == "" {
		return name
	}
	runes := []rune(name)
	if runes[0] >= 'A' && runes[0] <= 'Z' {
		runes[0] += 'a' - 'A'
	}
	return string(runes)
}
