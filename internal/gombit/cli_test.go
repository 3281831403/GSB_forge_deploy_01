package gombit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// recorder captures invocations so command construction can be asserted
// without a Gombit installation.
type recorder struct {
	calls   [][]string
	dirs    []string
	outputs map[string]string
	err     error
}

func (r *recorder) run(_ context.Context, dir string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	r.dirs = append(r.dirs, dir)
	if r.err != nil {
		return []byte("boom"), r.err
	}
	if len(args) > 0 {
		if output, found := r.outputs[args[0]]; found {
			return []byte(output), nil
		}
	}
	return nil, nil
}

func newCLI(rec *recorder) *CLI {
	return &CLI{Binary: "gombit", Run: rec.run, SkipVersionCheck: true}
}

func validRequest(dir string) ScaffoldRequest {
	return ScaffoldRequest{
		Dir:      dir,
		Name:     "acme-crm",
		Module:   "example.com/acme",
		Database: DatabasePostgres,
		Auth:     AuthCookie,
		UI:       UIMinimal,
	}
}

func TestScaffoldArgs(t *testing.T) {
	rec := &recorder{}
	cli := newCLI(rec)

	if err := cli.Scaffold(context.Background(), validRequest("/tmp/work/acme")); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("expected exactly one coarse invocation, got %d", len(rec.calls))
	}

	got := strings.Join(rec.calls[0], " ")
	want := "gombit new acme --module example.com/acme " +
		"--database postgres --auth cookie --ui minimal --skip-tidy"
	if got != want {
		t.Errorf("argv:\n got: %s\nwant: %s", got, want)
	}

	// `gombit new` creates <name>/ under its working directory, so the
	// command must run in the parent of the destination.
	if want := filepath.Clean("/tmp/work"); filepath.Clean(rec.dirs[0]) != want {
		t.Errorf("working dir: got %q want %q", rec.dirs[0], want)
	}
}

func TestScaffoldArgsTidyAndForce(t *testing.T) {
	rec := &recorder{}
	cli := newCLI(rec)

	request := validRequest("/tmp/work/acme")
	request.Tidy = true
	request.Force = true

	if err := cli.Scaffold(context.Background(), request); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	got := strings.Join(rec.calls[0], " ")
	// Tidy inverts to the absence of --skip-tidy, since the CLI tidies by default.
	if strings.Contains(got, "--skip-tidy") {
		t.Errorf("Tidy=true must not pass --skip-tidy: %s", got)
	}
	if !strings.Contains(got, "--force") {
		t.Errorf("Force=true must pass --force: %s", got)
	}
}

func TestScaffoldValidatesBeforeRunning(t *testing.T) {
	tests := map[string]func(*ScaffoldRequest){
		"no dir":           func(r *ScaffoldRequest) { r.Dir = "" },
		"no name":          func(r *ScaffoldRequest) { r.Name = "" },
		"no module":        func(r *ScaffoldRequest) { r.Module = "" },
		"unknown database": func(r *ScaffoldRequest) { r.Database = "oracle" },
		"unknown auth":     func(r *ScaffoldRequest) { r.Auth = "saml" },
		"unknown ui":       func(r *ScaffoldRequest) { r.UI = "tailwind" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			cli := newCLI(rec)

			request := validRequest("/tmp/work/acme")
			mutate(&request)

			if err := cli.Scaffold(context.Background(), request); err == nil {
				t.Fatal("expected validation to reject the request")
			}
			if len(rec.calls) != 0 {
				t.Errorf("a rejected request must not reach the toolchain, got %v", rec.calls)
			}
		})
	}
}

// TestScaffoldChecksVersionBeforeWriting is the fail-early guard: an
// unsupported toolchain must be caught before any files exist, not after a
// tree importing the wrong module path has been written.
func TestScaffoldChecksVersionBeforeWriting(t *testing.T) {
	t.Run("rejects a pre-rename toolchain", func(t *testing.T) {
		rec := &recorder{outputs: map[string]string{"version": "gombit:   v0.1.0\n"}}
		cli := &CLI{Binary: "gombit", Run: rec.run}

		err := cli.Scaffold(context.Background(), validRequest("/tmp/work/acme"))
		if err == nil {
			t.Fatal("expected v0.1.0 to be rejected")
		}
		if !strings.Contains(err.Error(), ModulePath) {
			t.Errorf("error should explain the module-path rename: %v", err)
		}

		for _, call := range rec.calls {
			if len(call) > 1 && call[1] == "new" {
				t.Error("scaffold ran despite an unsupported toolchain")
			}
		}
	})

	t.Run("accepts a supported toolchain", func(t *testing.T) {
		rec := &recorder{outputs: map[string]string{"version": "gombit:   v0.1.12\n"}}
		cli := &CLI{Binary: "gombit", Run: rec.run}

		if err := cli.Scaffold(context.Background(), validRequest("/tmp/work/acme")); err != nil {
			t.Fatalf("scaffold: %v", err)
		}
		if len(rec.calls) != 2 {
			t.Fatalf("expected version then new, got %v", rec.calls)
		}
	})
}

func TestScaffoldSurfacesToolchainOutput(t *testing.T) {
	rec := &recorder{err: errors.New("exit status 1")}
	cli := newCLI(rec)

	err := cli.Scaffold(context.Background(), validRequest("/tmp/work/acme"))
	if err == nil {
		t.Fatal("expected the failure to propagate")
	}
	// The toolchain's own message is the useful half of a build failure.
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should include toolchain output: %v", err)
	}
}

func TestScaffoldRejectsDegenerateDestination(t *testing.T) {
	rec := &recorder{}
	cli := newCLI(rec)

	if err := cli.Scaffold(context.Background(), validRequest("/")); err == nil {
		t.Error("expected a destination with no final path element to be rejected")
	}
}

// TestScaffoldAgainstInstalledToolchain drives the real CLI end to end.
//
// The unit tests above prove the argv is what we intend; only this proves the
// argv is what Gombit accepts. Skipped in -short and when no supported
// toolchain is installed. Tidy stays off so the test does not reach the
// network.
func TestScaffoldAgainstInstalledToolchain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping toolchain integration test in -short")
	}
	if _, err := exec.LookPath(DefaultBinary); err != nil {
		t.Skipf("gombit not on PATH: %v", err)
	}

	cli := &CLI{}
	version, err := cli.Version(context.Background())
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if err := CheckSupported(version); err != nil {
		t.Skipf("installed toolchain is unsupported: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "acme-crm")
	request := validRequest(dir)
	request.Module = "example.com/acme"

	if err := cli.Scaffold(context.Background(), request); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	// The shell Forge builds on must actually exist where we said it would.
	for _, rel := range []string{
		"go.mod",
		"gombit.yaml",
		"cmd/server/main.go",
		"internal/platform/database.go",
		"frontend/package.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("scaffolded tree is missing %s: %v", rel, err)
		}
	}

	// ADR-004 D5: generated applications must depend on the renamed module.
	goMod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !strings.Contains(string(goMod), ModulePath) {
		t.Errorf("go.mod does not require %s:\n%s", ModulePath, goMod)
	}
	if strings.Contains(string(goMod), "LAA-Software-Engineering") {
		t.Errorf("go.mod still uses the pre-rename module path:\n%s", goMod)
	}
}

// TestVersionAgainstInstalledToolchain exercises the real binary when one is
// present, so the parser is checked against actual output rather than only
// against a fixture that could drift.
func TestVersionAgainstInstalledToolchain(t *testing.T) {
	if _, err := exec.LookPath(DefaultBinary); err != nil {
		t.Skipf("gombit not on PATH: %v", err)
	}

	cli := &CLI{}
	version, err := cli.Version(context.Background())
	if err != nil {
		t.Fatalf("version against the installed toolchain: %v", err)
	}
	if version == (Version{}) {
		t.Error("parsed a zero version from the installed toolchain")
	}
	t.Logf("installed gombit: %s (supported: %v)", version, CheckSupported(version) == nil)
}
