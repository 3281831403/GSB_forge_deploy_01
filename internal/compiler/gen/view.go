package gen

import (
	"fmt"
	"path"
	"strings"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
)

// Views generates one view.go per resource under
// internal/forge_generated/<resource>/ (ADR-001 §21-23).
//
// The file defines the resource's immutable after-hook view: a narrow
// interface whose accessor names derive from frozen code symbols, plus a
// generated implementation that backs it with the persistence model. This is
// the extension ABI that after-hooks (AfterCreate, AfterUpdate, AfterDelete)
// bind to — deliberately not the GORM model itself (§22), so field layout,
// ORM tags and future storage decisions stay private.
//
// Accessor names come from goFieldName, exactly as the model's struct fields
// do, so an accessor signature is a pure function of a field's frozen code
// symbol: a relabel or a storage rename — both independent naming domains
// (ADR-001 D2) — leaves this file byte-identical, and existing hooks stay
// source-compatible (§23).
//
// Files come back in the graph's authored resource order.
func Views(g *graph.Graph) ([]File, error) {
	if g == nil {
		return nil, fmt.Errorf("gen: nil graph")
	}
	if err := validatePackages(g); err != nil {
		return nil, err
	}

	files := make([]File, 0, len(g.Resources))
	for _, resource := range g.Resources {
		file, err := viewFile(resource)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

// viewAccessor is one interface method: an accessor named by a field's frozen
// code symbol, returning the field's Go type, reading the matching model field.
type viewAccessor struct {
	name      string // Email, or CustomerID for a belongs_to — matches goFieldName
	goType    string // string, int64, decimal.Decimal, uint, ...
	modelName string // the struct field it reads; identical to name here
}

func viewFile(resource *graph.Resource) (File, error) {
	if err := validateNames(resource); err != nil {
		return File{}, err
	}

	// Resolve every accessor's type first, collecting imports and failing
	// before any source is emitted if a field type is unmapped.
	accessors := make([]viewAccessor, 0, len(resource.Fields))
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
		accessors = append(accessors, viewAccessor{
			name:      name,
			goType:    mapping.goType,
			modelName: name,
		})
	}

	iface := resource.CodeName() + "View"
	impl := unexport(iface)
	model := resource.CodeName()

	var b strings.Builder
	b.WriteString(Banner)
	b.WriteString("\n\npackage ")
	b.WriteString(PackageName(resource))
	b.WriteString("\n\n")
	b.WriteString(renderImports(imports))
	b.WriteString("\n")

	// The interface: the extension ABI surface. gorm.Model.ID is exposed as
	// the identity accessor; no field may mint the symbol "ID" (it is reserved),
	// so this never collides with a field accessor.
	fmt.Fprintf(&b, "// %s is the immutable extension view of a %q (ADR-001 §21).\n",
		iface, resource.Spec.Label)
	fmt.Fprintf(&b, "//\n")
	fmt.Fprintf(&b, "// After-hooks bind to this interface, not to the %s persistence model\n", model)
	fmt.Fprintf(&b, "// (§22). Accessor names derive from frozen code symbols (§23), so a relabel\n")
	fmt.Fprintf(&b, "// or storage rename never changes a signature here.\n")
	fmt.Fprintf(&b, "type %s interface {\n", iface)
	b.WriteString("\tID() uint\n")
	for _, accessor := range accessors {
		fmt.Fprintf(&b, "\t%s() %s\n", accessor.name, accessor.goType)
	}
	b.WriteString("}\n\n")

	// The implementation: a thin read-only wrapper over the persistence model.
	fmt.Fprintf(&b, "// %s is the generated %s backed by a persistence model.\n", impl, iface)
	fmt.Fprintf(&b, "type %s struct{ model *%s }\n\n", impl, model)

	fmt.Fprintf(&b, "// New%s wraps a persistence model as its immutable extension view. It is the\n", iface)
	fmt.Fprintf(&b, "// construction seam used by generated hook wiring; extensions receive the\n")
	fmt.Fprintf(&b, "// %s interface and never the model.\n", iface)
	fmt.Fprintf(&b, "func New%s(model *%s) %s { return %s{model: model} }\n\n", iface, model, iface, impl)

	fmt.Fprintf(&b, "func (v %s) ID() uint { return v.model.ID }\n", impl)
	for _, accessor := range accessors {
		fmt.Fprintf(&b, "func (v %s) %s() %s { return v.model.%s }\n",
			impl, accessor.name, accessor.goType, accessor.modelName)
	}

	relPath := path.Join(PackageDir(resource), "view.go")
	return formatGo(relPath, b.String())
}

// unexport lowercases the first rune of an exported identifier to derive the
// package-private counterpart (CustomerView -> customerView). Code symbols are
// validated exported Go identifiers, so the result is always a legal
// unexported identifier.
func unexport(name string) string {
	if name == "" {
		return name
	}
	runes := []rune(name)
	runes[0] = toLowerRune(runes[0])
	return string(runes)
}

func toLowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}
