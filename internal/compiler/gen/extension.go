package gen

import (
	"fmt"
	"path"
	"strings"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
)

// ExtensionPackage is the compiler-owned leaf package that holds the shared
// extension-ABI rejection types. Resource packages import it for the FieldRef
// type; it imports nothing app-specific, so no import cycle forms with the
// composition root (which imports the resource packages).
const ExtensionPackage = "extension"

// extensionDir is the directory of the shared extension package.
func extensionDir() string { return path.Join(GeneratedRoot, ExtensionPackage) }

// Extension generates the shared extension package under
// internal/forge_generated/extension/ (ADR-001 §27).
//
// It defines the structured, field-scoped rejection surface a before-hook uses:
// a FieldRef that names a semantic field by stable identity, a FieldError that
// pairs a FieldRef with a message, and InvalidField to build one. Rejection is
// still an ordinary Go error (§27), so a hook may reject with a plain error too;
// FieldError merely lets a rejection stay associated with a field so the API/UI
// can surface it against the right control.
//
// The package is Forge-runtime-free (D2): it is the generated application's own
// code and imports only the standard library. It does not reimplement Gombit's
// validation — it is the hook-authoring value that the request path (hook
// wiring, #25) maps onto Gombit's error response.
func Extension(g *graph.Graph) ([]File, error) {
	if g == nil {
		return nil, fmt.Errorf("gen: nil graph")
	}

	var b strings.Builder
	b.WriteString(Banner)
	b.WriteString("\n\npackage ")
	b.WriteString(ExtensionPackage)
	b.WriteString("\n\n")

	b.WriteString(`// FieldRef identifies a semantic field by its stable identity, independent of
// the field's label or storage name (ADR-001 §27). Generated code mints one per
// field; a before-hook names it to scope a rejection to that field, and the
// reference stays valid across relabels and storage renames.
type FieldRef struct {
	// Resource and Field are the stable opaque IDs of the field and its owning
	// resource (ADR-001 D1) — the field's identity.
	Resource string
	Field    string
	// Name is the field's current API name (its storage name, the JSON key a
	// client sends), for associating an error with a form control. It tracks the
	// storage name and is refreshed on every compile; identity lives in Field.
	Name string
}

// FieldError is a structured, field-scoped rejection. A before-hook returns one
// to reject an operation and tie the reason to a semantic field (ADR-001 §27).
// It is an ordinary error, so it composes with errors.As at the request
// boundary and with plain-error rejection.
type FieldError struct {
	Field   FieldRef
	Message string
}

func (e *FieldError) Error() string {
	return e.Field.Name + ": " + e.Message
}

// InvalidField builds a field-scoped rejection for the given field.
func InvalidField(field FieldRef, message string) error {
	return &FieldError{Field: field, Message: message}
}
`)

	file, err := formatGo(path.Join(extensionDir(), "extension.go"), b.String())
	if err != nil {
		return nil, err
	}
	return []File{file}, nil
}

// FieldRefs generates one fields.go per resource that declares a stable FieldRef
// for each field (ADR-001 §27), under internal/forge_generated/<resource>/.
//
// The var name is Field + the field's frozen code symbol, so the identifier a
// hook writes (generated.FieldEmail) never moves under a relabel or storage
// rename (§23). Each ref embeds the field's stable IDs — its identity — and its
// current API name. A resource with no fields emits no file, so the extension
// import is never unused.
//
// module is the generated application's Go module path, needed to import the
// shared extension package by its full path.
func FieldRefs(g *graph.Graph, module string) ([]File, error) {
	if g == nil {
		return nil, fmt.Errorf("gen: nil graph")
	}
	if module == "" {
		return nil, fmt.Errorf("gen: FieldRefs needs a module path")
	}
	if err := validatePackages(g); err != nil {
		return nil, err
	}

	files := make([]File, 0, len(g.Resources))
	for _, resource := range g.Resources {
		if len(resource.Fields) == 0 {
			continue
		}
		file, err := fieldRefsFile(resource, module)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func fieldRefsFile(resource *graph.Resource, module string) (File, error) {
	if err := validateNames(resource); err != nil {
		return File{}, err
	}
	if err := validateFieldRefNames(resource); err != nil {
		return File{}, err
	}

	var b strings.Builder
	b.WriteString(Banner)
	b.WriteString("\n\npackage ")
	b.WriteString(PackageName(resource))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "import %q\n\n", module+"/"+extensionDir())

	fmt.Fprintf(&b, "// Field references for the %q resource (ADR-001 §27): each names a semantic\n", resource.Spec.Label)
	b.WriteString("// field by stable identity, so a before-hook rejection stays associated with\n")
	b.WriteString("// the field across relabels and storage renames.\n")
	b.WriteString("var (\n")
	for _, field := range resource.Fields {
		fmt.Fprintf(&b, "\tField%s = extension.FieldRef{Resource: %q, Field: %q, Name: %q}\n",
			goFieldName(field), resource.Spec.ID, field.Spec.ID, field.Spec.StorageName)
	}
	b.WriteString(")\n")

	relPath := path.Join(PackageDir(resource), "fields.go")
	return formatGo(relPath, b.String())
}

// validateFieldRefNames rejects a resource whose generated FieldRef var would
// collide with any other exported package-level symbol in its package, before
// any source is emitted (ADR-001 §12, §36: reserve, don't discover).
//
// The var names are Field+<code symbol>. Field code symbols are unique
// (validateNames), so two refs never collide; but the resource package holds
// symbols from every stage — the model type <Code>, the view interface
// <Code>View, the view constructor New<Code>View, the handler/registration
// funcs — and a Field-prefixed var can land on one of them (e.g. resource
// "Field" + field "View" mints both `type FieldView` and `var FieldView`).
// packageLevelSymbols is the single reservation set all of those live in, so
// this guard stays complete as stages are added rather than re-deriving a
// partial list that the next <Code>Xxx type would silently defeat.
func validateFieldRefNames(resource *graph.Resource) error {
	reserved := packageLevelSymbols(resource)
	for _, field := range resource.Fields {
		name := "Field" + goFieldName(field)
		if what, taken := reserved[name]; taken {
			return fmt.Errorf(
				"gen: field %s generates the reference %q, which collides with %s in package %s; rename a code symbol",
				field.Spec.ID, name, what, PackageName(resource))
		}
	}
	return nil
}
