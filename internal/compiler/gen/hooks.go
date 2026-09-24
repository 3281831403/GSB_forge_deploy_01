package gen

import (
	"fmt"
	"path"
	"strings"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// hookMethod is the frozen mapping from a lifecycle event to the generated
// contract method a hook implements. The method name and parameter list derive
// from the event, never from any label or code symbol, so enabling the same
// event always yields the same signature (ADR-001 §34-35, §21-31).
type hookMethod struct {
	name   string // Go method name: AfterCreate, BeforeUpdate, ...
	params string // parameter list after the receiver, e.g. "ctx context.Context, created CustomerView"
}

// hookMethodFor returns the contract method for an event on a resource whose
// generated type is typeName. The before-hooks receive the mutable draft/change
// surfaces (§24-29); the read-only lifecycle points receive the immutable view
// (§21, §30-31).
func hookMethodFor(event spec.HookEvent, typeName string) (hookMethod, bool) {
	view := typeName + "View"
	switch event {
	case spec.HookBeforeCreate:
		return hookMethod{"BeforeCreate", "ctx context.Context, draft *" + typeName + "CreateDraft"}, true
	case spec.HookAfterCreate:
		return hookMethod{"AfterCreate", "ctx context.Context, created " + view}, true
	case spec.HookBeforeUpdate:
		return hookMethod{"BeforeUpdate", "ctx context.Context, current " + view + ", changes *" + typeName + "UpdateChanges"}, true
	case spec.HookAfterUpdate:
		return hookMethod{"AfterUpdate", "ctx context.Context, updated " + view}, true
	case spec.HookBeforeDelete:
		return hookMethod{"BeforeDelete", "ctx context.Context, current " + view}, true
	case spec.HookAfterDelete:
		return hookMethod{"AfterDelete", "ctx context.Context, deleted " + view}, true
	default:
		return hookMethod{}, false
	}
}

// resourceHookMethods resolves a resource's enabled hooks to contract methods in
// authored order. It errors on an unknown event rather than silently dropping
// it — a validated spec never reaches here with one, so this is a defensive
// assertion that keeps a bad event from producing a contract with a missing
// method.
func resourceHookMethods(resource *graph.Resource) ([]hookMethod, error) {
	methods := make([]hookMethod, 0, len(resource.Spec.Hooks))
	for _, hook := range resource.Spec.Hooks {
		method, ok := hookMethodFor(hook.Event, resource.CodeName())
		if !ok {
			return nil, fmt.Errorf("gen: resource %s hook %s has unsupported event %q",
				resource.Spec.ID, hook.ID, hook.Event)
		}
		methods = append(methods, method)
	}
	return methods, nil
}

// Hooks generates one hooks.go per resource that declares lifecycle hooks, under
// internal/forge_generated/<resource>/ (ADR-001 §34-35).
//
// The file is compiler-owned and holds the extension contract: an interface
// with one method per enabled event (typed against the generated view/draft/
// change surfaces), plus a statically-typed registration function and accessor.
// Registration is explicit and reflection-free (§34): the composition root
// (Wiring) hands the user's implementation to Register<Type>Hooks, and the
// compiler proves at build time that the implementation satisfies the contract.
//
// A resource with no hooks emits nothing, so a hook-free spec's generated tree
// is byte-for-byte what it was before this stage existed. Files come back in the
// graph's authored resource order.
func Hooks(g *graph.Graph) ([]File, error) {
	if g == nil {
		return nil, fmt.Errorf("gen: nil graph")
	}
	if err := validatePackages(g); err != nil {
		return nil, err
	}

	files := make([]File, 0)
	for _, resource := range g.Resources {
		if len(resource.Spec.Hooks) == 0 {
			continue
		}
		file, err := hooksFile(resource)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func hooksFile(resource *graph.Resource) (File, error) {
	if err := validateNames(resource); err != nil {
		return File{}, err
	}
	methods, err := resourceHookMethods(resource)
	if err != nil {
		return File{}, err
	}

	iface := resource.CodeName() + "Hooks"
	registrar := "Register" + resource.CodeName() + "Hooks"
	accessor := resource.CodeName() + "HookImpl"
	stored := "registered" + resource.CodeName() + "Hooks"

	var b strings.Builder
	b.WriteString(Banner)
	b.WriteString("\n\npackage ")
	b.WriteString(PackageName(resource))
	b.WriteString("\n\nimport \"context\"\n\n")

	fmt.Fprintf(&b, "// %s is the backend lifecycle-extension contract for the %q resource\n", iface, resource.Spec.Label)
	fmt.Fprintf(&b, "// (ADR-001 §34-35). A hand-written implementation in %s satisfies it;\n", ExtensionPackageDir(resource))
	fmt.Fprintf(&b, "// the composition root registers that implementation through %s.\n", registrar)
	fmt.Fprintf(&b, "type %s interface {\n", iface)
	for _, method := range methods {
		fmt.Fprintf(&b, "\t%s(%s) error\n", method.name, method.params)
	}
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, "var %s %s\n\n", stored, iface)

	fmt.Fprintf(&b, "// %s records the extension implementation of the %q lifecycle hooks. It is\n", registrar, resource.Spec.Label)
	fmt.Fprintf(&b, "// called once from the generated composition root — statically typed, with no\n")
	fmt.Fprintf(&b, "// request-time reflection (ADR-001 §34).\n")
	fmt.Fprintf(&b, "func %s(h %s) { %s = h }\n\n", registrar, iface, stored)

	fmt.Fprintf(&b, "// %s returns the registered hook implementation, or nil if none is wired.\n", accessor)
	fmt.Fprintf(&b, "func %s() %s { return %s }\n", accessor, iface, stored)

	relPath := path.Join(PackageDir(resource), "hooks.go")
	return formatGo(relPath, b.String())
}

// HookStubs generates the one-time, user-owned extension stub for each resource
// that declares hooks: internal/extensions/<resource>/hooks.go (ADR-001 §35).
//
// These files are NOT compiler-owned. They are offered once and, once present,
// belong to the developer permanently — Forge never rewrites them (§90, D8). The
// caller writes them create-if-absent (compiler.WriteStubs), never through the
// generated-tree materialization, which wipes and would clobber user code.
//
// The stub declares `type Hooks struct{}` with a no-op method per enabled event,
// so a freshly-created stub satisfies the generated contract and the app builds;
// the developer then fills in behavior. module is the application module path,
// needed to import the generated package (aliased `generated`) for the view and
// draft/change types.
func HookStubs(g *graph.Graph, module string) ([]File, error) {
	if g == nil {
		return nil, fmt.Errorf("gen: nil graph")
	}
	if module == "" {
		return nil, fmt.Errorf("gen: HookStubs needs a module path")
	}
	if err := validatePackages(g); err != nil {
		return nil, err
	}

	files := make([]File, 0)
	for _, resource := range g.Resources {
		if len(resource.Spec.Hooks) == 0 {
			continue
		}
		file, err := hookStubFile(resource, module)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func hookStubFile(resource *graph.Resource, module string) (File, error) {
	if err := validateNames(resource); err != nil {
		return File{}, err
	}
	methods, err := resourceHookMethods(resource)
	if err != nil {
		return File{}, err
	}

	// The stub references the generated view/draft/change types through the
	// generated package, imported under the alias `generated` (ADR-001 §35). The
	// method signatures are the contract's, with each generated type qualified.
	generatedImport := module + "/" + PackageDir(resource)

	var b strings.Builder
	b.WriteString("// Code generated by Gombit Forge as a one-time stub. Safe to edit.\n")
	b.WriteString("//\n")
	b.WriteString("// Forge created this file once when a lifecycle hook was first enabled and\n")
	b.WriteString("// will never overwrite it (ADR-001 §35). It is yours to implement.\n\n")
	b.WriteString("package ")
	b.WriteString(PackageName(resource))
	b.WriteString("\n\nimport (\n\t\"context\"\n\n")
	fmt.Fprintf(&b, "\tgenerated %q\n)\n\n", generatedImport)

	fmt.Fprintf(&b, "// Hooks implements the %s lifecycle-extension contract.\n", resource.CodeName())
	b.WriteString("type Hooks struct{}\n\n")

	// Assert at compile time that the stub satisfies the generated contract, so a
	// drifted stub fails in this file rather than at the registration call site.
	fmt.Fprintf(&b, "var _ generated.%sHooks = Hooks{}\n\n", resource.CodeName())

	for _, method := range methods {
		qualified := qualifyGenerated(method.params)
		fmt.Fprintf(&b, "func (Hooks) %s(%s) error {\n\treturn nil\n}\n\n", method.name, qualified)
	}

	relPath := path.Join(ExtensionPackageDir(resource), "hooks.go")
	// The stub is hand-editable, so it is gofmt-clean but not banner-marked as
	// DO-NOT-EDIT; formatGo still guarantees it parses and is formatted.
	return formatGo(relPath, b.String())
}

// qualifyGenerated rewrites a contract parameter list (which names generated
// types bare, as they appear inside the generated package) into the form the
// extension package uses, where those types are reached through the `generated`
// import alias. It qualifies the resource's generated type names — the View,
// CreateDraft and UpdateChanges types all share the resource code-symbol prefix
// and are the only non-stdlib identifiers in a contract signature.
func qualifyGenerated(params string) string {
	// Parameters are "ctx context.Context[, name Type][, name *Type]". Only the
	// generated types need the alias; context.Context and the leading names must
	// not. Qualify by rewriting each " Type" / " *Type" occurrence whose type is
	// an exported identifier that is not context.Context.
	fields := strings.Split(params, ", ")
	for i, field := range fields {
		space := strings.LastIndex(field, " ")
		if space < 0 {
			continue
		}
		name, typ := field[:space+1], field[space+1:]
		star := ""
		if strings.HasPrefix(typ, "*") {
			star, typ = "*", typ[1:]
		}
		if typ == "context.Context" || typ == "" {
			continue
		}
		fields[i] = name + star + "generated." + typ
	}
	return strings.Join(fields, ", ")
}
