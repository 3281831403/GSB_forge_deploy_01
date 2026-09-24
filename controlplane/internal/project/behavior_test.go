package project_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
	"github.com/gombit-dev/gombit-forge/internal/compiler"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// compiledOutput compiles a spec and returns its whole file tree concatenated,
// so a test can tell whether a spec change reaches generation.
func compiledOutput(t *testing.T, s *spec.ProjectSpec) string {
	t.Helper()
	files, err := compiler.Compile(s, "example.com/app")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var b strings.Builder
	for _, f := range files {
		b.WriteString(f.Path)
		b.WriteByte('\n')
		b.Write(f.Content)
	}
	return b.String()
}

func TestUpdateBehaviorCommitsAndSerializes(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	ctx := context.Background()
	if _, err := svc.AddField(ctx, projectID, resID, project.FieldInput{Label: "Name", Type: spec.TypeString}, 7); err != nil {
		t.Fatalf("add field: %v", err)
	}
	if _, err := svc.AddField(ctx, projectID, resID, project.FieldInput{Label: "Amount", Type: spec.TypeInteger}, 7); err != nil {
		t.Fatalf("add field: %v", err)
	}
	fields := headSpec(t, svc, projectID).Resources[0].Fields
	fieldID, amountID := fields[0].ID, fields[1].ID

	res, err := svc.UpdateBehavior(ctx, projectID, resID, spec.ResourceBehavior{
		CreateEnabled: false, UpdateEnabled: true, DeleteEnabled: true, AdminVisible: true,
		ListFields: []spec.ID{fieldID}, SearchableFields: []spec.ID{fieldID},
		AggregatableFields: []spec.ID{amountID},
	}, 7)
	if err != nil {
		t.Fatalf("update behavior: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted || res.Class != "neutral" {
		t.Fatalf("behavior update outcome=%s class=%s, want committed/neutral", res.Outcome, res.Class)
	}
	b := headSpec(t, svc, projectID).Resources[0].Behavior
	if b.CreateEnabled || !b.UpdateEnabled || !b.AdminVisible {
		t.Errorf("behavior not serialized: %+v", b)
	}
	if len(b.ListFields) != 1 || b.ListFields[0] != fieldID {
		t.Errorf("list_fields not serialized: %v", b.ListFields)
	}
	if len(b.AggregatableFields) != 1 || b.AggregatableFields[0] != amountID {
		t.Errorf("aggregatable_fields not serialized: %v", b.AggregatableFields)
	}
}

func TestUpdateBehaviorRejectsUnknownField(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	res, err := svc.UpdateBehavior(context.Background(), projectID, resID, spec.ResourceBehavior{
		AdminVisible: true, ListFields: []spec.ID{spec.MustNewID(spec.KindField)},
	}, 7)
	if err != nil {
		t.Fatalf("update behavior: %v", err)
	}
	if res.Outcome != project.OutcomeInvalidSpec {
		t.Errorf("a list_fields entry that isn't a field of the resource must be rejected; got %s", res.Outcome)
	}
}

// TestBehaviorAffectsGeneratedHandlers is acceptance criterion 2: toggling
// create/update/delete changes what the compiler generates.
func TestBehaviorAffectsGeneratedHandlers(t *testing.T) {
	svc, projectID, resID := projectWithResource(t)
	ctx := context.Background()
	if _, err := svc.AddField(ctx, projectID, resID, project.FieldInput{Label: "Name", Type: spec.TypeString, Required: true}, 7); err != nil {
		t.Fatalf("add field: %v", err)
	}

	if _, err := svc.UpdateBehavior(ctx, projectID, resID, spec.ResourceBehavior{
		CreateEnabled: true, UpdateEnabled: true, DeleteEnabled: true, AdminVisible: true,
	}, 7); err != nil {
		t.Fatalf("enable: %v", err)
	}
	withCreate := compiledOutput(t, headSpec(t, svc, projectID))

	if _, err := svc.UpdateBehavior(ctx, projectID, resID, spec.ResourceBehavior{
		CreateEnabled: false, UpdateEnabled: true, DeleteEnabled: true, AdminVisible: true,
	}, 7); err != nil {
		t.Fatalf("disable create: %v", err)
	}
	withoutCreate := compiledOutput(t, headSpec(t, svc, projectID))

	if withCreate == withoutCreate {
		t.Error("toggling create_enabled must change the generated handlers")
	}
}
