package gen

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
)

// Admin generates admin.go per admin-visible resource under
// internal/forge_generated/<resource>/ (DESIGN.md §9 stage 6, §4.3).
//
// The generated code registers the model with Gombit's admin through the
// public admin.Register entry point (ADR-004 D3); it declares no admin data
// plane, permission model or UI of its own. Managed apps use cookie auth
// (DESIGN.md D5), which is what admin.Register requires.
//
// Only resources whose behavior marks them admin-visible produce a file, and
// admin create/update/delete follow the same CRUD toggles as the API; list and
// detail are always enabled so a visible resource can be browsed. Files come
// back in the graph's authored resource order.
func Admin(g *graph.Graph) ([]File, error) {
	if g == nil {
		return nil, fmt.Errorf("gen: nil graph")
	}
	if err := validatePackages(g); err != nil {
		return nil, err
	}

	var files []File
	for _, resource := range g.Resources {
		if err := validateNames(resource); err != nil {
			return nil, err
		}
		if !resource.Spec.Behavior.AdminVisible {
			continue
		}

		view := newAdminView(resource)
		src, err := renderTemplate(adminTemplate, view)
		if err != nil {
			return nil, err
		}
		file, err := formatGo(path.Join(PackageDir(resource), "admin.go"), src)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

// adminView augments resourceView with the admin registration fields.
//
// Slug, Singular and Plural are Go string literals (already quoted), not raw
// values: labels are unconstrained human text (ADR-001 D2) and would otherwise
// break the generated composite literal or be reinterpreted through Go's
// escape rules — the same representation rule the model defaults follow.
type adminView struct {
	resourceView
	Slug     string // quoted, e.g. `"customers"`
	Singular string // quoted, e.g. `"Customer"`
	Plural   string // quoted, or "" meaning "let Gombit derive it"
}

func newAdminView(resource *graph.Resource) adminView {
	// A specified plural is a label even when it equals the singular, so it is
	// emitted; only an empty plural is left for Gombit to derive (ADR-001 D2).
	plural := ""
	if strings.TrimSpace(resource.Spec.LabelPlural) != "" {
		plural = strconv.Quote(resource.Spec.LabelPlural)
	}
	return adminView{
		resourceView: newResourceView(resource),
		Slug:         strconv.Quote(resource.Spec.StorageName),
		Singular:     strconv.Quote(resource.Spec.Label),
		Plural:       plural,
	}
}
