package compiler

import (
	"bytes"
	"fmt"
	"go/format"
	"strings"
	"text/template"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
)

// CompositionRootPath is where the composition root is written in the app tree.
const CompositionRootPath = "cmd/server/main.go"

// CompositionRoot generates the application's composition root — cmd/server/main.go
// (ADR-004: "Forge owns the composition root"). It boots the framework, wires
// every generated resource through forge_generated.RegisterAll, embeds the
// frontend, and closes the database on stop. It deliberately does NOT AutoMigrate:
// migrations are applied out of band (DESIGN.md §14).
//
// It is parameterized only by the application's Go module path and is
// deterministic — gofmt-formatted, no maps/timestamps/randomness — so the same
// module always yields byte-identical output. This is the production generator
// behind the composition root the M0 and export end-to-end tests build and boot.
func CompositionRoot(module string) ([]byte, error) {
	module = strings.TrimSpace(module)
	if module == "" {
		return nil, fmt.Errorf("compiler: composition root needs a module path")
	}
	// A module path is a Go import path: it must not carry characters that would
	// break the generated import strings.
	if strings.ContainsAny(module, "\"`\n\t ") {
		return nil, fmt.Errorf("compiler: invalid module path %q", module)
	}

	var buf bytes.Buffer
	if err := compositionRootTemplate.Execute(&buf, struct{ Banner, Module string }{gen.Banner, module}); err != nil {
		return nil, fmt.Errorf("compiler: composition root template: %w", err)
	}
	// gofmt the result so output is canonical regardless of template spacing.
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("compiler: format composition root: %w", err)
	}
	return formatted, nil
}

var compositionRootTemplate = template.Must(template.New("composition").Parse(`{{.Banner}}
package main

import (
	"context"
	"log"

	"github.com/gombit-dev/gombit/config"
	"github.com/gombit-dev/gombit/framework"

	forge "{{.Module}}/internal/forge_generated"
	"{{.Module}}/internal/platform"
	"{{.Module}}/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	db, err := platform.OpenDatabase(cfg.Database)
	if err != nil {
		log.Fatal(err)
	}
	app, err := framework.New(
		framework.WithConfig(cfg),
		framework.WithDatabase(db),
		framework.WithEmbeddedFrontend(web.FS()),
	)
	if err != nil {
		_ = db.Close()
		log.Fatal(err)
	}
	if err := forge.RegisterAll(app); err != nil {
		log.Fatal(err)
	}
	app.OnStop(func(context.Context) error { return db.Close() })
	if err := framework.Run(app); err != nil {
		log.Fatal(err)
	}
}
`))
