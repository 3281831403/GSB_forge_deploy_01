package compiler

import (
	"context"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/gombit"
)

// fakeSourceToolchain stands in for the real Gombit toolchain: Scaffold lays down a
// minimal canonical base tree (including an .env secret, to prove sanitization),
// and Tidy/MakeMigrations record that the assembly drove them.
type fakeSourceToolchain struct {
	scaffolded, tidied, migrated bool
}

func (f *fakeSourceToolchain) Scaffold(_ context.Context, req gombit.ScaffoldRequest) error {
	f.scaffolded = true
	base := map[string]string{
		"go.mod":                    "module " + req.Module + "\n",
		"cmd/server/main.go":        "package main // scaffold placeholder — overwritten by the composition root\n",
		"internal/web/static/.keep": "",
		".env":                      "GOMBIT_JWT_SECRET=scaffold-secret\n",
	}
	for path, content := range base {
		if err := writeRootFile(req.Dir, path, []byte(content)); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSourceToolchain) Tidy(_ context.Context, _ string) error {
	f.tidied = true
	return nil
}

func (f *fakeSourceToolchain) MakeMigrations(_ context.Context, req gombit.MakeMigrationsRequest) error {
	f.migrated = true
	return writeRootFile(req.Dir, "database/migrations/0001_initial.sql", []byte("-- initial migration\n"))
}

func TestBuildApplicationSource(t *testing.T) {
	fake := &fakeSourceToolchain{}
	files, err := BuildApplicationSource(context.Background(), fake, ApplicationSourceRequest{
		Spec:       sampleSpec(t),
		Module:     "example.com/app",
		Name:       "app",
		Provenance: NewProvenance("v0.1.7", "rev_1"),
	})
	if err != nil {
		t.Fatalf("build application source: %v", err)
	}
	if !fake.scaffolded || !fake.tidied || !fake.migrated {
		t.Errorf("assembly must drive the toolchain seam: scaffold=%v tidy=%v migrate=%v",
			fake.scaffolded, fake.tidied, fake.migrated)
	}

	byPath := map[string]SourceFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	// The assembled collection composes every piece.
	for _, want := range []string{
		"go.mod",                    // scaffold base
		"internal/web/static/.keep", // scaffold base
		"internal/forge_generated/customer/model.go", // Compile + Materialize
		"cmd/server/main.go",                         // composition root
		"README.md",                                  // #82
		"forge.json",                                 // #83
		"database/migrations/0001_initial.sql",       // toolchain migration
	} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("assembled source missing %s", want)
		}
	}
	// The composition root overwrote the scaffold placeholder with the real main.
	if root := string(byPath["cmd/server/main.go"].Content); !strings.Contains(root, "forge.RegisterAll(app)") {
		t.Errorf("composition root not written over the scaffold placeholder:\n%s", root)
	}
	// Sanitization still applies to the assembled tree: the scaffold's .env secret
	// is not shipped.
	if _, ok := byPath[".env"]; ok {
		t.Error("assembled source must not ship the scaffold's .env secret")
	}
}

func TestBuildApplicationSourceValidatesRequest(t *testing.T) {
	fake := &fakeSourceToolchain{}
	if _, err := BuildApplicationSource(context.Background(), fake, ApplicationSourceRequest{Module: "example.com/app"}); err == nil {
		t.Error("a nil spec must error")
	}
	if _, err := BuildApplicationSource(context.Background(), fake, ApplicationSourceRequest{Spec: sampleSpec(t)}); err == nil {
		t.Error("an empty module must error")
	}
}
