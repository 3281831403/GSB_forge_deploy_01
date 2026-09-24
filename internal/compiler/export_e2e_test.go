package compiler

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gombit-dev/gombit-forge/internal/gombit"
)

// TestExportBuildsAndRunsOutsideForge is the M7 export guarantee (DESIGN.md §28,
// §32; P2/P5, D10/D11): a project exported from Forge is an ordinary Gombit
// repository that builds and runs on its own, with no Forge runtime dependency.
//
// It compiles the sample spec into a scaffolded app, writes the export's README
// and forge.json, then runs Export and extracts the archive into a fresh
// directory outside the Forge module. From that extracted tree alone it asserts
// no gombit-forge reference survives, builds the server, applies the migration on
// a throwaway Postgres, boots it and confirms it serves — proving the exported
// artifact (not just the in-place materialized tree, which TestM0EndToEnd
// covers) stands on its own.
//
// Like the M0 gate it drives the real toolchain and Postgres, so it needs
// gombit, atlas, go and docker and skips in -short or when any is absent.
func TestExportBuildsAndRunsOutsideForge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping export end-to-end harness in -short")
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	const module = "example.com/app"
	s := sampleSpec(t)

	// --- assemble the full application source over the real toolchain --------
	// BuildApplicationSource is the production Revision → ApplicationSource path:
	// scaffold + Compile/Materialize + composition root + README/forge.json +
	// tidy + migration, returned as the shared sanitized SourceFile collection.
	sources, err := BuildApplicationSource(ctx, GombitToolchain{CLI: cli}, ApplicationSourceRequest{
		Spec: s, Module: module, Name: "app",
		Provenance: NewProvenance(version.String(), "e2e-revision"),
	})
	if err != nil {
		t.Fatalf("build application source: %v", err)
	}

	// --- export (ZIP the collection), then extract into a fresh tree ---------
	var archive bytes.Buffer
	if err := WriteZip(sources, &archive); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	exportDir := filepath.Join(t.TempDir(), "exported")
	extractZip(t, archive.Bytes(), exportDir)

	// The export carries its README, provenance and migration, and none of the
	// excluded metadata.
	for _, want := range []string{"README.md", "forge.json", "go.mod", "cmd/server/main.go"} {
		if _, err := os.Stat(filepath.Join(exportDir, want)); err != nil {
			t.Errorf("export missing %s: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(exportDir, ".git")); !os.IsNotExist(err) {
		t.Error("export must not carry .git")
	}
	// The core guarantee: nothing in the extracted tree references Forge.
	assertNoForgeDependency(t, exportDir)

	// --- build + run the extracted app on its own ---------------------------
	pg := startPostgres(t, ctx)
	defer pg.stop()

	env := []string{
		"GOMBIT_DATABASE_DRIVER=postgres",
		"GOMBIT_DATABASE_DSN=" + pg.dsn,
		"GOMBIT_JWT_SECRET=forge-export-e2e-secret-please-change-1",
		"GOMBIT_AUTH_MODE=cookie",
		"GOMBIT_COOKIE_SECURE=false",
		"GOMBIT_COOKIE_SAMESITE=Lax",
	}
	// Migrate, build and boot entirely from the extracted directory.
	runCmd(t, ctx, exportDir, env, gombit.DefaultBinary, "db", "migrate")

	bin := filepath.Join(exportDir, "server")
	runCmd(t, ctx, exportDir, nil, "go", "build", "-o", bin, "./cmd/server")

	port := freePort(t)
	appEnv := append(env, "GOMBIT_HTTP_ADDR=127.0.0.1:"+port)
	server := startServer(t, ctx, exportDir, bin, appEnv)
	defer server.stop()

	base := "http://127.0.0.1:" + port
	waitForHTTP(t, base+"/livez")

	// It serves the generated API — the exported app runs, standalone.
	runCmd(t, ctx, exportDir, appEnv, gombit.DefaultBinary, "createsuperuser",
		"--no-input", "--email", superuserEmail, "--password", superuserPassword)
	session := login(t, base)
	customerID := session.create(t, base+"/api/v1/customers", map[string]any{
		"email":  "export@example.test",
		"active": true,
	})
	got := session.get(t, base+"/api/v1/customers/"+customerID)
	if got["email"] != "export@example.test" {
		t.Errorf("read-back customer email = %v, want export@example.test", got["email"])
	}

	t.Logf("export gate cleared: exported app built and served from %s (customer %s)", exportDir, customerID)
}

// extractZip writes the archive's entries under dest, creating parent dirs and
// preserving each entry's mode. It rejects any entry whose path escapes dest
// (zip-slip), so a malformed archive can't write outside the destination.
func extractZip(t *testing.T, data []byte, dest string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	for _, f := range zr.File {
		target := filepath.Join(dest, f.Name)
		rel, err := filepath.Rel(dest, target)
		if err != nil || rel == ".." || filepath.IsAbs(rel) || hasDotDotPrefix(rel) {
			t.Fatalf("archive entry %q escapes destination", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", f.Name, err)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode())
		if err != nil {
			rc.Close()
			t.Fatalf("create %s: %v", target, err)
		}
		if _, err := io.Copy(out, rc); err != nil {
			rc.Close()
			out.Close()
			t.Fatalf("write %s: %v", target, err)
		}
		rc.Close()
		out.Close()
	}
}

func hasDotDotPrefix(rel string) bool {
	return len(rel) >= 3 && rel[0] == '.' && rel[1] == '.' && (rel[2] == filepath.Separator)
}
