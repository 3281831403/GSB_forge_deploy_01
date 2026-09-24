package gen

import (
	"fmt"
	"path"
	"strings"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
)

// Wiring generates the composition root register.go at the root of the
// compiler-owned tree, exposing RegisterAll (DESIGN.md §9 stages 4/10).
//
// Each resource package exposes Register (routes) and, when admin-visible,
// RegisterAdmin; nothing calls them on its own, because Gombit does not
// discover feature packages by reflection (ADR-001 §34). RegisterAll is the
// single, statically-typed entry point the application's main wires once. It
// belongs to Forge's application synthesis, not the framework (ADR-004 D1), and
// consumes only framework.App plus the generated packages.
//
// module is the generated application's Go module path, needed to import the
// resource packages by their full path.
func Wiring(g *graph.Graph, module string) ([]File, error) {
	if g == nil {
		return nil, fmt.Errorf("gen: nil graph")
	}
	if module == "" {
		return nil, fmt.Errorf("gen: Wiring needs a module path")
	}
	if err := validatePackages(g); err != nil {
		return nil, err
	}
	for _, resource := range g.Resources {
		if err := validateNames(resource); err != nil {
			return nil, err
		}
	}

	var b strings.Builder
	b.WriteString(Banner)
	b.WriteString("\n\npackage forge_generated\n\n")

	// Imports: framework, each resource's generated package, and — for a
	// resource with lifecycle hooks — its user-owned extension package, aliased
	// <pkg>ext because it shares the generated package's name. Generated code
	// referencing user code is intended here (ADR-001 §34): the composition root
	// is where the two ownership domains meet, by an ordinary import, never a
	// rewrite of user code.
	b.WriteString("import (\n")
	fmt.Fprintf(&b, "\t%q\n\n", "github.com/gombit-dev/gombit/framework")
	for _, resource := range g.Resources {
		fmt.Fprintf(&b, "\t%q\n", module+"/"+PackageDir(resource))
	}
	for _, resource := range g.Resources {
		if len(resource.Spec.Hooks) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\t%sext %q\n", PackageName(resource), module+"/"+ExtensionPackageDir(resource))
	}
	b.WriteString(")\n\n")

	b.WriteString("// RegisterAll registers lifecycle hooks, then mounts every generated\n")
	b.WriteString("// resource's routes and admin registration onto the application. main calls\n")
	b.WriteString("// this once. Hook registration is static and reflection-free (ADR-001 §34).\n")
	b.WriteString("func RegisterAll(app *framework.App) error {\n")
	for _, resource := range g.Resources {
		if len(resource.Spec.Hooks) == 0 {
			continue
		}
		pkg := PackageName(resource)
		fmt.Fprintf(&b, "\t%s.Register%sHooks(%sext.Hooks{})\n", pkg, resource.CodeName(), pkg)
	}
	for _, resource := range g.Resources {
		pkg := PackageName(resource)
		fmt.Fprintf(&b, "\t%s.Register(app)\n", pkg)
		if resource.Spec.Behavior.AdminVisible {
			fmt.Fprintf(&b, "\tif err := %s.RegisterAdmin(app); err != nil {\n", pkg)
			b.WriteString("\t\treturn err\n\t}\n")
		}
	}
	b.WriteString("\treturn nil\n}\n")

	file, err := formatGo(path.Join(GeneratedRoot, "register.go"), b.String())
	if err != nil {
		return nil, err
	}
	return []File{file}, nil
}
