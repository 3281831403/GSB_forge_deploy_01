package gombit

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func validMigrateRequest(dir string) MakeMigrationsRequest {
	return MakeMigrationsRequest{
		Dir:    dir,
		Name:   "initial",
		Driver: DatabasePostgres,
		Models: []Model{
			{ImportPath: "example.com/app/internal/forge_generated/customer", TypeName: "Customer"},
			{ImportPath: "example.com/app/internal/forge_generated/invoice", TypeName: "Invoice"},
		},
	}
}

func TestMakeMigrationsArgs(t *testing.T) {
	rec := &recorder{}
	cli := &CLI{Binary: "gombit", Run: rec.run}

	if err := cli.MakeMigrations(context.Background(), validMigrateRequest("/tmp/app")); err != nil {
		t.Fatalf("make migrations: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected one coarse invocation, got %d", len(rec.calls))
	}

	got := strings.Join(rec.calls[0], " ")
	want := "gombit db makemigrations initial --driver postgres " +
		"--model example.com/app/internal/forge_generated/customer.Customer " +
		"--model example.com/app/internal/forge_generated/invoice.Invoice"
	if got != want {
		t.Errorf("argv:\n got: %s\nwant: %s", got, want)
	}
	if rec.dirs[0] != "/tmp/app" {
		t.Errorf("command must run in the app dir, got %q", rec.dirs[0])
	}
}

func TestMakeMigrationsCustomDir(t *testing.T) {
	rec := &recorder{}
	cli := &CLI{Binary: "gombit", Run: rec.run}

	req := validMigrateRequest("/tmp/app")
	req.MigrationDir = "db/migrations"
	if err := cli.MakeMigrations(context.Background(), req); err != nil {
		t.Fatalf("make migrations: %v", err)
	}
	if !strings.Contains(strings.Join(rec.calls[0], " "), "--dir db/migrations") {
		t.Errorf("custom migration dir not passed: %v", rec.calls[0])
	}
}

func TestMakeMigrationsForgetModels(t *testing.T) {
	rec := &recorder{}
	cli := &CLI{Binary: "gombit", Run: rec.run}

	req := validMigrateRequest("/tmp/app")
	req.ForgetModels = []Model{
		{ImportPath: "example.com/app/internal/forge_generated/legacy", TypeName: "Legacy"},
	}
	if err := cli.MakeMigrations(context.Background(), req); err != nil {
		t.Fatalf("make migrations: %v", err)
	}
	got := strings.Join(rec.calls[0], " ")
	if !strings.Contains(got, "--forget-model example.com/app/internal/forge_generated/legacy.Legacy") {
		t.Errorf("forget-model not passed: %s", got)
	}
	// A request that only forgets is valid.
	rec2 := &recorder{}
	cli2 := &CLI{Binary: "gombit", Run: rec2.run}
	onlyForget := MakeMigrationsRequest{
		Dir: "/tmp/app", Name: "drop", Driver: DatabasePostgres,
		ForgetModels: req.ForgetModels,
	}
	if err := cli2.MakeMigrations(context.Background(), onlyForget); err != nil {
		t.Errorf("a forget-only request should be valid: %v", err)
	}
}

func TestMakeMigrationsValidates(t *testing.T) {
	tests := map[string]func(*MakeMigrationsRequest){
		"no dir":            func(r *MakeMigrationsRequest) { r.Dir = "" },
		"no name":           func(r *MakeMigrationsRequest) { r.Name = "" },
		"bad driver":        func(r *MakeMigrationsRequest) { r.Driver = "oracle" },
		"no models":         func(r *MakeMigrationsRequest) { r.Models = nil },
		"model no import":   func(r *MakeMigrationsRequest) { r.Models[0].ImportPath = "" },
		"model bad type":    func(r *MakeMigrationsRequest) { r.Models[0].TypeName = "lowercase" },
		"import whitespace": func(r *MakeMigrationsRequest) { r.Models[0].ImportPath = "bad path" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			cli := &CLI{Binary: "gombit", Run: rec.run}

			req := validMigrateRequest("/tmp/app")
			mutate(&req)
			if err := cli.MakeMigrations(context.Background(), req); err == nil {
				t.Fatal("expected validation to reject the request")
			}
			if len(rec.calls) != 0 {
				t.Errorf("a rejected request must not reach the toolchain, got %v", rec.calls)
			}
		})
	}
}

func TestMakeMigrationsSurfacesToolchainOutput(t *testing.T) {
	rec := &recorder{err: errors.New("exit status 1")}
	cli := &CLI{Binary: "gombit", Run: rec.run}

	err := cli.MakeMigrations(context.Background(), validMigrateRequest("/tmp/app"))
	if err == nil {
		t.Fatal("expected the failure to propagate")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should include toolchain output: %v", err)
	}
}

func TestModelSpec(t *testing.T) {
	m := Model{ImportPath: "example.com/app/internal/forge_generated/order_line", TypeName: "OrderLine"}
	if got, want := m.spec(), "example.com/app/internal/forge_generated/order_line.OrderLine"; got != want {
		t.Errorf("spec(): got %q want %q", got, want)
	}
}
