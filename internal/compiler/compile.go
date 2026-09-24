// Package compiler is the ProjectSpec-to-source pipeline (DESIGN.md §9). It
// composes the domain graph with the code generators and returns the complete
// compiler-owned file tree.
//
// The pipeline is deterministic by construction: the same compiler version and
// the same spec produce a byte-identical tree (DESIGN.md §32, ADR-001 §83).
// Stages run in a fixed order, each generator preserves the graph's authored
// order and never ranges a map into ordered output, and every generated Go
// file is run through gofmt as it is produced — so the formatter is an
// integral pipeline step, not an afterthought.
package compiler

import (
	"fmt"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// stage is one code-generation step. Stages are ordered; the order is part of
// the determinism contract.
type stage struct {
	name string
	run  func(*graph.Graph) ([]gen.File, error)
}

// fileStages are the per-graph file generators, in emission order. The wiring
// stage is added per compile because it also needs the module path. Migration
// generation is not here: it emits SQL through Gombit/Atlas, driven from the
// gombit boundary with the model set from MigrationModels, rather than files.
var fileStages = []stage{
	{"models", gen.Models},
	{"views", gen.Views},
	{"mutations", gen.Mutations},
	{"extension", gen.Extension},
	{"hooks", gen.Hooks},
	{"handlers", gen.Handlers},
	{"admin", gen.Admin},
	{"frontend", gen.Frontend},
}

// Compile turns a ProjectSpec into the complete compiler-owned file tree,
// including the composition root register.go.
//
// It builds the domain graph (which validates the spec and refuses to build
// over an invalid one, so the generators never see a malformed model) and runs
// every stage. module is the generated application's Go module path, needed so
// register.go can import the resource packages; the returned files are ordered
// stage-by-stage, and within a stage in the graph's authored resource order.
func Compile(s *spec.ProjectSpec, module string) ([]gen.File, error) {
	g, err := graph.Build(s)
	if err != nil {
		return nil, fmt.Errorf("compiler: %w", err)
	}
	return Generate(g, module)
}

// Generate runs every stage over an already-built graph. Compile is the usual
// entry point; Generate exists for callers that already hold a graph.
//
// The wiring stage (register.go) goes through the same path-collision guard as
// every other stage, so the returned tree is the whole compiler-owned output —
// a caller cannot ship an unwired app by using a different entry point.
func Generate(g *graph.Graph, module string) ([]gen.File, error) {
	if g == nil {
		return nil, fmt.Errorf("compiler: nil graph")
	}
	if module == "" {
		return nil, fmt.Errorf("compiler: empty module path")
	}

	stages := append([]stage(nil), fileStages...)
	stages = append(stages, stage{
		name: "fieldrefs",
		run:  func(g *graph.Graph) ([]gen.File, error) { return gen.FieldRefs(g, module) },
	})
	stages = append(stages, stage{
		name: "wiring",
		run:  func(g *graph.Graph) ([]gen.File, error) { return gen.Wiring(g, module) },
	})

	var files []gen.File
	seen := make(map[string]string) // path -> producing stage

	for _, stage := range stages {
		produced, err := stage.run(g)
		if err != nil {
			return nil, fmt.Errorf("compiler: %s: %w", stage.name, err)
		}
		for _, file := range produced {
			if other, clash := seen[file.Path]; clash {
				// Two stages writing the same path would make output depend on
				// append order and silently drop a file downstream. This is a
				// generator bug, surfaced rather than shipped.
				return nil, fmt.Errorf(
					"compiler: stages %s and %s both produced %s", other, stage.name, file.Path)
			}
			seen[file.Path] = stage.name
			files = append(files, file)
		}
	}
	return files, nil
}
