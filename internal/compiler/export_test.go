package compiler

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writeTree materializes a map of relative path → content under a temp dir,
// creating parent directories, and returns the dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

// exportEntries returns the archive entry names (sorted) and a name→content map.
func exportEntries(t *testing.T, data []byte) ([]string, map[string]string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	names := make([]string, 0, len(zr.File))
	content := map[string]string{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read entry %s: %v", f.Name, err)
		}
		rc.Close()
		content[f.Name] = buf.String()
	}
	sort.Strings(names)
	return names, content
}

// a representative §28 Gombit repo tree, plus excluded dirs.
func sampleRepo() map[string]string {
	return map[string]string{
		"go.mod":             "module example.com/app\n",
		"gombit.yaml":        "name: app\n",
		"README.md":          "# App\n",
		"cmd/server/main.go": "package main\n",
		"internal/forge_generated/customer/model.go": "package customer\n",
		"internal/extensions/customer/hooks.go":      "package customer // user code\n",
		"frontend/src/forge_generated/resources.tsx": "export const x = 1;\n",
		"database/migrations/0001.sql":               "-- sql\n",
		"config/app.yaml":                            "k: v\n",
		"docs/README.md":                             "docs\n",
		// Excluded: VCS and build/dependency output.
		".git/HEAD":                    "ref: refs/heads/main\n",
		"node_modules/dep/index.js":    "module.exports = {}\n",
		"frontend/node_modules/x/i.js": "x\n",
		"frontend/dist/bundle.js":      "bundled\n",
	}
}

func TestExportIncludesFullTreeAndExtensions(t *testing.T) {
	dir := writeTree(t, sampleRepo())
	var buf bytes.Buffer
	if err := Export(dir, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	names, content := exportEntries(t, buf.Bytes())

	// The full §28 tree, including user extensions, is present.
	for _, want := range []string{
		"go.mod", "gombit.yaml", "README.md",
		"cmd/server/main.go",
		"internal/forge_generated/customer/model.go",
		"internal/extensions/customer/hooks.go",
		"frontend/src/forge_generated/resources.tsx",
		"database/migrations/0001.sql",
		"config/app.yaml",
		"docs/README.md",
	} {
		if _, ok := content[want]; !ok {
			t.Errorf("export missing %s; got %v", want, names)
		}
	}
	// User extension content is preserved verbatim (it is user-owned, §72).
	if content["internal/extensions/customer/hooks.go"] != "package customer // user code\n" {
		t.Error("extension content must be exported verbatim")
	}
}

func TestExportExcludesVCSAndBuildOutput(t *testing.T) {
	dir := writeTree(t, sampleRepo())
	var buf bytes.Buffer
	if err := Export(dir, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	names, _ := exportEntries(t, buf.Bytes())
	for _, n := range names {
		for _, bad := range []string{".git/", "node_modules/", "/dist/", "dist/"} {
			if strings.Contains(n, bad) {
				t.Errorf("export must exclude %q; found %s", bad, n)
			}
		}
	}
}

// TestExportExcludesSecrets pins the decision that an export ships source, not
// credentials: .env (which carries the scaffold's GOMBIT_JWT_SECRET) and
// .env.local are excluded, while a blanked .env.example template is kept.
func TestExportExcludesSecrets(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod":       "module example.com/app\n",
		".env":         "GOMBIT_JWT_SECRET=supersecret\n",
		".env.local":   "GOMBIT_JWT_SECRET=local\n",
		".env.example": "GOMBIT_JWT_SECRET=\n",
	})
	var buf bytes.Buffer
	if err := Export(dir, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	_, content := exportEntries(t, buf.Bytes())

	if _, ok := content[".env"]; ok {
		t.Error("export must not ship .env (it carries the scaffold's JWT secret)")
	}
	if _, ok := content[".env.local"]; ok {
		t.Error("export must not ship .env.local")
	}
	if _, ok := content[".env.example"]; !ok {
		t.Error("export must keep the .env.example template")
	}
}

// TestExportIsDeterministic: the same tree exports to byte-identical archives.
func TestExportIsDeterministic(t *testing.T) {
	dir := writeTree(t, sampleRepo())
	var a, b bytes.Buffer
	if err := Export(dir, &a); err != nil {
		t.Fatalf("export a: %v", err)
	}
	if err := Export(dir, &b); err != nil {
		t.Fatalf("export b: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("the same tree must export byte-identically")
	}
}

// TestCollectSanitizedAndSorted: Collect returns the sanitized source set in
// sorted path order, with content, excluding VCS/build output and secrets — the
// shared collection both export targets consume.
func TestCollectSanitizedAndSorted(t *testing.T) {
	dir := writeTree(t, sampleRepo())
	files, err := Collect(dir)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	paths := make([]string, len(files))
	byPath := map[string][]byte{}
	for i, f := range files {
		paths[i] = f.Path
		byPath[f.Path] = f.Content
	}
	if !sort.StringsAreSorted(paths) {
		t.Errorf("Collect must return sorted paths; got %v", paths)
	}
	if _, ok := byPath["internal/extensions/customer/hooks.go"]; !ok {
		t.Error("Collect must include user extensions")
	}
	if got := byPath["internal/extensions/customer/hooks.go"]; string(got) != "package customer // user code\n" {
		t.Errorf("Collect must read content verbatim; got %q", got)
	}
	for _, p := range paths {
		if strings.Contains(p, ".git/") || strings.Contains(p, "node_modules/") || strings.Contains(p, "dist/") {
			t.Errorf("Collect must exclude VCS/build output; found %s", p)
		}
	}
}

// TestCollectPreservesExecutableBit: an executable file is marked Executable so
// every export target can preserve it (parity between ZIP and GitHub).
func TestCollectPreservesExecutableBit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := Collect(dir)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f.Path] = f.Executable
	}
	if !got["run.sh"] {
		t.Error("executable file must be marked Executable")
	}
	if got["go.mod"] {
		t.Error("a 0644 file must not be marked Executable")
	}
}

// TestWriteZipFromSourceFiles: WriteZip archives a SourceFile collection with the
// executable bit preserved and is deterministic.
func TestWriteZipFromSourceFiles(t *testing.T) {
	files := []SourceFile{
		{Path: "go.mod", Content: []byte("module x\n")},
		{Path: "run.sh", Content: []byte("#!/bin/sh\n"), Executable: true},
	}
	var a, b bytes.Buffer
	if err := WriteZip(files, &a); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	if err := WriteZip(files, &b); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("WriteZip of the same collection must be byte-identical")
	}
	zr, err := zip.NewReader(bytes.NewReader(a.Bytes()), int64(a.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	modes := map[string]uint32{}
	for _, f := range zr.File {
		modes[f.Name] = uint32(f.Mode().Perm())
	}
	if modes["run.sh"] != 0o755 {
		t.Errorf("run.sh mode = %o, want 755", modes["run.sh"])
	}
	if modes["go.mod"] != 0o644 {
		t.Errorf("go.mod mode = %o, want 644", modes["go.mod"])
	}
}

func TestExportRejectsNonDir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Export(f, new(bytes.Buffer)); err == nil {
		t.Error("Export must reject a non-directory root")
	}
}
