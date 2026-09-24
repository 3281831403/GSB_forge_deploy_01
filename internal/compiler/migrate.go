package compiler

import (
	"fmt"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/gombit"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// MigrationModels returns the generated model types for migration generation,
// in the graph's authored resource order.
//
// Each resource's model lives at module/internal/forge_generated/<package> and
// is named by the resource's frozen code symbol, matching what the model
// generator emits. module is the generated application's Go module path.
func MigrationModels(g *graph.Graph, module string) ([]gombit.Model, error) {
	if g == nil {
		return nil, fmt.Errorf("compiler: nil graph")
	}
	if module == "" {
		return nil, fmt.Errorf("compiler: empty module path")
	}
	if err := validateForMigration(g); err != nil {
		return nil, err
	}

	models := make([]gombit.Model, 0, len(g.Resources))
	for _, resource := range g.Resources {
		models = append(models, gombit.Model{
			ImportPath: module + "/" + gen.PackageDir(resource),
			TypeName:   resource.CodeName(),
		})
	}
	return models, nil
}

// validateForMigration runs the same package/name guards the generators
// enforce before deriving model import paths, so a package name the generator
// would refuse cannot leak into a migration argument.
func validateForMigration(g *graph.Graph) error {
	return gen.Validate(g)
}

// MigrationModelsForSpec is MigrationModels starting from a spec: it builds and
// validates the graph first.
func MigrationModelsForSpec(s *spec.ProjectSpec, module string) ([]gombit.Model, error) {
	g, err := graph.Build(s)
	if err != nil {
		return nil, fmt.Errorf("compiler: %w", err)
	}
	return MigrationModels(g, module)
}
