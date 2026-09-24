package compiler

import (
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

func TestMigrationModelsForSpec(t *testing.T) {
	models, err := MigrationModelsForSpec(sampleSpec(t), "example.com/app")
	if err != nil {
		t.Fatalf("migration models: %v", err)
	}

	want := []struct{ importPath, typeName string }{
		{"example.com/app/internal/forge_generated/customer", "Customer"},
		{"example.com/app/internal/forge_generated/invoice", "Invoice"},
	}
	if len(models) != len(want) {
		t.Fatalf("model count: got %d want %d", len(models), len(want))
	}
	for i, w := range want {
		if models[i].ImportPath != w.importPath || models[i].TypeName != w.typeName {
			t.Errorf("model %d: got %s.%s want %s.%s",
				i, models[i].ImportPath, models[i].TypeName, w.importPath, w.typeName)
		}
	}
}

// TestMigrationModelsPreserveAuthoredOrder pins that models track the graph's
// authored resource order, so the migration argument list is deterministic.
func TestMigrationModelsPreserveAuthoredOrder(t *testing.T) {
	s := sampleSpec(t)
	// Reverse the resource order; models must follow.
	s.Resources[0], s.Resources[1] = s.Resources[1], s.Resources[0]

	models, err := MigrationModelsForSpec(s, "example.com/app")
	if err != nil {
		t.Fatalf("migration models: %v", err)
	}
	if models[0].TypeName != "Invoice" || models[1].TypeName != "Customer" {
		t.Errorf("models did not follow authored order: %+v", models)
	}
}

func TestMigrationModelsValidates(t *testing.T) {
	if _, err := MigrationModelsForSpec(sampleSpec(t), ""); err == nil {
		t.Error("empty module path must error")
	}
	if _, err := MigrationModels(nil, "example.com/app"); err == nil {
		t.Error("nil graph must error")
	}
}

// TestMigrationModelsRejectUngeneratableSpec confirms the same guards the
// generators apply gate model derivation: a resource folding to an illegal
// package must not yield a migration import path.
func TestMigrationModelsRejectUngeneratableSpec(t *testing.T) {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	s := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			// "Main" folds to package main, which the generator rejects.
			{
				ID: id(spec.KindResource), Label: "Main", CodeName: "Main", StorageName: "mains",
				Fields: []*spec.Field{
					{ID: id(spec.KindField), Label: "Name", Type: spec.TypeString, CodeName: "Name", StorageName: "name"},
				},
			},
		},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("fixture should be spec-valid: %s", d.Error())
	}
	if _, err := MigrationModelsForSpec(s, "example.com/app"); err == nil {
		t.Fatal("migration models must reject a resource the generator would refuse")
	}
}
