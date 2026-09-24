package compiler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/gombit"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// TestMakeMigrationsGeneratesVersionedSQL is the issue #11 acceptance check:
// a versioned Atlas migration is generated from the compiled models, and adding
// a field produces a new migration.
//
// It scaffolds a real Gombit app, writes the compiled tree in, and drives
// `gombit db makemigrations` — the same delegation a build worker performs. The
// migration diff is computed by Atlas via Gombit, not by Forge (P4). Postgres
// is the managed target (D4); Atlas computes the diff against a throwaway dev
// database Gombit points at docker://postgres, so the test needs docker but not
// a standing server. Applying against a live database is the #12 harness.
//
// Requires installed gombit, atlas, go and docker; skips in -short or when any
// is absent, like the other toolchain integration tests.
func TestMakeMigrationsGeneratesVersionedSQL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration integration test in -short")
	}
	for _, bin := range []string{gombit.DefaultBinary, "atlas", "go", "docker"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: %v", bin, err)
		}
	}

	cli := &gombit.CLI{}
	version, err := cli.Version(context.Background())
	if err != nil {
		t.Fatalf("gombit version: %v", err)
	}
	if err := gombit.CheckSupported(version); err != nil {
		t.Skipf("installed toolchain unsupported: %v", err)
	}

	const module = "example.com/app"
	dir := filepath.Join(t.TempDir(), "app")

	// Scaffold a Postgres app and resolve its module graph.
	scaffold := gombit.ScaffoldRequest{
		Dir: dir, Name: "app", Module: module,
		Database: gombit.DatabasePostgres, Auth: gombit.AuthCookie, UI: gombit.UIMinimal,
		Tidy: true,
	}
	if err := cli.Scaffold(context.Background(), scaffold); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	s := sampleSpec(t)
	writeCompiled(t, dir, s)
	tidy(t, dir)

	models, err := MigrationModelsForSpec(s, module)
	if err != nil {
		t.Fatalf("migration models: %v", err)
	}

	// The scaffold seeds a bootstrap migration when atlas is present, so
	// "a migration was generated" must be observed as a file that did not
	// exist before this call — not merely a non-empty directory.
	beforeInitial := migrationSQLFiles(t, dir)

	if err := cli.MakeMigrations(context.Background(), gombit.MakeMigrationsRequest{
		Dir: dir, Name: "initial", Driver: gombit.DatabasePostgres, Models: models,
	}); err != nil {
		t.Fatalf("makemigrations initial: %v", err)
	}
	initialNew := newFiles(beforeInitial, migrationSQLFiles(t, dir))
	if len(initialNew) != 1 {
		t.Fatalf("initial makemigrations should add exactly one migration, added %v", initialNew)
	}
	initialSQL := readMigration(t, dir, initialNew[0])
	if !strings.Contains(initialSQL, "customers") || !strings.Contains(initialSQL, "invoices") {
		t.Errorf("initial migration must create the resource tables:\n%s", initialSQL)
	}
	if strings.Contains(initialSQL, "nickname") {
		t.Error("the field added later must not appear in the initial migration")
	}

	// Add a field, recompile, and generate again: a new migration referencing
	// the added column must appear.
	s.Resources[0].Fields = append(s.Resources[0].Fields, &spec.Field{
		ID: spec.MustNewID(spec.KindField), Label: "Nickname", Type: spec.TypeString,
		CodeName: "Nickname", StorageName: "nickname",
	})
	if d := spec.Validate(s); d != nil {
		t.Fatalf("mutated spec invalid: %s", d.Error())
	}
	writeCompiled(t, dir, s)

	beforeAdd := migrationSQLFiles(t, dir)
	if err := cli.MakeMigrations(context.Background(), gombit.MakeMigrationsRequest{
		Dir: dir, Name: "add_nickname", Driver: gombit.DatabasePostgres, Models: models,
	}); err != nil {
		t.Fatalf("makemigrations add_nickname: %v", err)
	}
	addNew := newFiles(beforeAdd, migrationSQLFiles(t, dir))
	if len(addNew) != 1 {
		t.Fatalf("adding a field should add exactly one migration, added %v", addNew)
	}
	if addSQL := readMigration(t, dir, addNew[0]); !strings.Contains(addSQL, "nickname") {
		t.Errorf("the new migration must reference the added column:\n%s", addSQL)
	}
}

// newFiles returns the entries in after that were not in before.
func newFiles(before, after []string) []string {
	prior := make(map[string]bool, len(before))
	for _, f := range before {
		prior[f] = true
	}
	var added []string
	for _, f := range after {
		if !prior[f] {
			added = append(added, f)
		}
	}
	return added
}

func readMigration(t *testing.T, dir, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "database", "migrations", name))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return strings.ToLower(string(body))
}

func writeCompiled(t *testing.T, dir string, s *spec.ProjectSpec) {
	t.Helper()
	files, err := Compile(s, testModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, file := range files {
		full := filepath.Join(dir, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, file.Content, 0o644); err != nil {
			t.Fatalf("write %s: %v", file.Path, err)
		}
	}
}

func tidy(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy:\n%s", out)
	}
}

// migrationSQLFiles returns the sorted .sql migration file names in the app.
func migrationSQLFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "database", "migrations"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read migrations dir: %v", err)
	}
	var files []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, entry.Name())
		}
	}
	return files
}
