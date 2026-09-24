package compiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// hookedSpec is the sample spec with an AfterCreate hook on the customer.
func hookedSpec(t *testing.T) *spec.ProjectSpec {
	t.Helper()
	s := sampleSpec(t)
	s.Resources[0].Hooks = []*spec.Hook{
		{ID: spec.MustNewID(spec.KindHook), Event: spec.HookAfterCreate},
	}
	if d := spec.Validate(s); d != nil {
		t.Fatalf("hooked spec invalid: %s", d.Error())
	}
	return s
}

func TestStubFilesForHookedSpec(t *testing.T) {
	stubs, err := StubFiles(hookedSpec(t), testModule)
	if err != nil {
		t.Fatalf("StubFiles: %v", err)
	}
	if len(stubs) != 1 || stubs[0].Path != "internal/extensions/customer/hooks.go" {
		t.Fatalf("expected one customer stub; got %v", stubPaths(stubs))
	}
}

func TestStubFilesHooklessEmpty(t *testing.T) {
	stubs, err := StubFiles(sampleSpec(t), testModule)
	if err != nil {
		t.Fatalf("StubFiles: %v", err)
	}
	if len(stubs) != 0 {
		t.Errorf("a hook-free spec must produce no stubs; got %v", stubPaths(stubs))
	}
}

func TestWriteStubsCreatesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	stubs := []gen.File{{Path: "internal/extensions/customer/hooks.go", Content: []byte("package customer\n")}}

	created, err := WriteStubs(dir, stubs)
	if err != nil {
		t.Fatalf("WriteStubs: %v", err)
	}
	if len(created) != 1 || created[0] != stubs[0].Path {
		t.Fatalf("expected to create the stub; created %v", created)
	}
	got, err := os.ReadFile(filepath.Join(dir, "internal/extensions/customer/hooks.go"))
	if err != nil || string(got) != "package customer\n" {
		t.Fatalf("stub not written correctly: %q (err %v)", got, err)
	}
}

// TestWriteStubsNeverOverwrites is the §35/§90 guarantee: once the file exists
// it is user-owned and left untouched, and WriteStubs reports it as not created.
func TestWriteStubsNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "internal/extensions/customer/hooks.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	const userCode = "package customer\n// hand-written by the developer\n"
	if err := os.WriteFile(target, []byte(userCode), 0o644); err != nil {
		t.Fatal(err)
	}

	stubs := []gen.File{{Path: "internal/extensions/customer/hooks.go", Content: []byte("package customer\n// GENERATED STUB\n")}}
	created, err := WriteStubs(dir, stubs)
	if err != nil {
		t.Fatalf("WriteStubs: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("an existing stub must not be recreated; created %v", created)
	}
	got, _ := os.ReadFile(target)
	if string(got) != userCode {
		t.Errorf("WriteStubs overwrote user-owned code:\n%s", got)
	}
}

// TestWriteStubsRejectsNonExtensionPath: WriteStubs refuses anything outside the
// extensions root, so a bad path cannot reach the generated tree or escape the
// project — and it writes nothing when it refuses.
func TestWriteStubsRejectsNonExtensionPath(t *testing.T) {
	for _, bad := range []string{
		"internal/forge_generated/customer/hooks.go", // generated tree
		"../escape.go",                      // out of tree
		"internal/extensions/../secrets.go", // non-clean
		"/abs/path.go",                      // absolute
	} {
		dir := t.TempDir()
		_, err := WriteStubs(dir, []gen.File{{Path: bad, Content: []byte("x")}})
		if err == nil {
			t.Errorf("WriteStubs must reject path %q", bad)
		}
	}
}

func TestWriteStubsEmptyDir(t *testing.T) {
	if _, err := WriteStubs("", nil); err == nil {
		t.Error("WriteStubs(\"\", …) must error")
	}
}

func stubPaths(files []gen.File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}
