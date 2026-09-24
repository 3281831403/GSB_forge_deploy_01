package compiler

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

func deletedResource(codeName string) DeletedResource {
	return DeletedResource{ID: spec.MustNewID(spec.KindResource), CodeName: codeName}
}

func readManifest(t *testing.T, dir, key string) ArchiveManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".forge", "orphaned", key, ManifestName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m ArchiveManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return m
}

// TestArchiveMovesExtensionOutOfBuildTree is the core of §46-48: the extension
// code is moved (not copied) into .forge/orphaned/<key>/**, the source no longer
// exists under internal/extensions, the bytes are preserved verbatim, and the
// manifest records the original stable ID.
func TestArchiveMovesExtensionOutOfBuildTree(t *testing.T) {
	dir := t.TempDir()
	src := gen.ExtensionPackageDirForCodeName("Customer") // internal/extensions/customer
	const body = "package customer\n\n// hand-written; must survive archival verbatim\n"
	writeFile(t, dir, src+"/hooks.go", body)

	del := deletedResource("Customer")
	archived, err := ArchiveExtensions(dir, "rev-1", []DeletedResource{del})
	if err != nil {
		t.Fatalf("ArchiveExtensions: %v", err)
	}
	if len(archived) != 1 {
		t.Fatalf("want 1 archived extension, got %d", len(archived))
	}

	// Source is gone (moved, not copied) — no in-place orphan remains.
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(src))); !os.IsNotExist(err) {
		t.Errorf("source extension dir must be gone after archival; stat err = %v", err)
	}
	// Destination holds the verbatim bytes under the dot-prefixed archive root.
	wantDest := ".forge/orphaned/rev-1/customer/hooks.go"
	got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(wantDest)))
	if err != nil {
		t.Fatalf("archived file missing: %v", err)
	}
	if string(got) != body {
		t.Errorf("archived bytes not preserved verbatim:\n got %q\nwant %q", got, body)
	}
	if archived[0].ArchivedPath != ".forge/orphaned/rev-1/customer" {
		t.Errorf("archived path = %q", archived[0].ArchivedPath)
	}
	// Manifest associates the archive with the original stable ID (§47).
	m := readManifest(t, dir, "rev-1")
	if len(m.Extensions) != 1 || m.Extensions[0].ResourceID != del.ID {
		t.Errorf("manifest must record the original resource id %s; got %+v", del.ID, m.Extensions)
	}
}

// TestArchiveLeavesNothingInBuildTree is acceptance criterion 3 (§48): after
// archival, no extension source remains under internal/extensions for the
// deleted resource, and the archive lives under a dot-prefixed root that the Go
// toolchain does not traverse.
func TestArchiveLeavesNothingInBuildTree(t *testing.T) {
	dir := t.TempDir()
	src := gen.ExtensionPackageDirForCodeName("Customer")
	writeFile(t, dir, src+"/hooks.go", "package customer\n")
	writeFile(t, dir, src+"/helpers.go", "package customer\n\nfunc h() {}\n")

	if _, err := ArchiveExtensions(dir, "rev-1", []DeletedResource{deletedResource("Customer")}); err != nil {
		t.Fatalf("ArchiveExtensions: %v", err)
	}

	extRoot := filepath.Join(dir, filepath.FromSlash(gen.ExtensionsRoot))
	if entries, err := os.ReadDir(extRoot); err == nil {
		for _, e := range entries {
			if strings.EqualFold(e.Name(), "customer") {
				t.Errorf("customer extension dir must not remain under %s", gen.ExtensionsRoot)
			}
		}
	}
	if !strings.HasPrefix(ArchiveRoot, ".") {
		t.Errorf("archive root %q must be dot-prefixed so go tooling skips it", ArchiveRoot)
	}
}

// TestArchiveGoBuildSucceedsAfterDeletion is the §96 mandatory proof, run for
// real: an extension importing a now-deleted generated package makes
// `go build ./...` fail; archiving the extension out of the build graph makes it
// pass again — without rewriting the source.
func TestArchiveGoBuildSucceedsAfterDeletion(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.25\n")
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, dir, "internal/forge_generated/customer/model.go",
		"package customer\n\ntype Customer struct{}\n")
	writeFile(t, dir, gen.ExtensionPackageDirForCodeName("Customer")+"/hooks.go",
		"package customer\n\nimport fg \"example.com/app/internal/forge_generated/customer\"\n\nvar _ = fg.Customer{}\n")

	tc := GoToolchain{}
	// Baseline: the whole app builds.
	if err := tc.Typecheck(context.Background(), dir); err != nil {
		t.Fatalf("baseline build should pass: %v", err)
	}

	// Delete the generated dependency, as recompiling the candidate without the
	// resource would (Materialize wipes its generated package).
	if err := os.RemoveAll(filepath.Join(dir, "internal/forge_generated/customer")); err != nil {
		t.Fatalf("remove generated dir: %v", err)
	}
	// Now the extension has a dangling import: the build must fail (§48).
	if err := tc.Typecheck(context.Background(), dir); err == nil {
		t.Fatal("expected the dangling-import build to fail before archival")
	}

	// Archive the orphaned extension out of the build graph.
	if _, err := ArchiveExtensions(dir, "rev-2", []DeletedResource{deletedResource("Customer")}); err != nil {
		t.Fatalf("ArchiveExtensions: %v", err)
	}
	// §96: the archived source must not poison the active module.
	if err := tc.Typecheck(context.Background(), dir); err != nil {
		t.Fatalf("build must pass after archival: %v", err)
	}
}

// TestArchiveSkipsResourceWithoutExtension: a deleted resource that never had
// hand-written code archives nothing and is silently skipped.
func TestArchiveSkipsResourceWithoutExtension(t *testing.T) {
	dir := t.TempDir()
	archived, err := ArchiveExtensions(dir, "rev-1", []DeletedResource{deletedResource("Ghost")})
	if err != nil {
		t.Fatalf("ArchiveExtensions: %v", err)
	}
	if len(archived) != 0 {
		t.Errorf("a resource with no extension dir must archive nothing; got %v", archived)
	}
	// No manifest is written when nothing was archived.
	if _, err := os.Stat(filepath.Join(dir, ".forge", "orphaned", "rev-1", ManifestName)); !os.IsNotExist(err) {
		t.Errorf("no manifest expected when nothing archived; stat err = %v", err)
	}
}

// TestArchiveRefusesToOverwrite: archival must never clobber an existing archive
// (§47 "must not silently delete custom source").
func TestArchiveRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := gen.ExtensionPackageDirForCodeName("Customer")
	writeFile(t, dir, src+"/hooks.go", "package customer\n")
	// Pre-existing archive at the same key+package.
	writeFile(t, dir, ".forge/orphaned/rev-1/customer/old.go", "package customer\n")

	if _, err := ArchiveExtensions(dir, "rev-1", []DeletedResource{deletedResource("Customer")}); err == nil {
		t.Fatal("archiving over an existing destination must error")
	}
	// The source must be left untouched on refusal.
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(src), "hooks.go")); err != nil {
		t.Errorf("source must remain after a refused archival: %v", err)
	}
}

// TestArchiveManifestMerges: archiving two resources into the same key across
// two calls keeps both associations.
func TestArchiveManifestMerges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, gen.ExtensionPackageDirForCodeName("Customer")+"/hooks.go", "package customer\n")
	writeFile(t, dir, gen.ExtensionPackageDirForCodeName("Invoice")+"/hooks.go", "package invoice\n")

	c := deletedResource("Customer")
	i := deletedResource("Invoice")
	if _, err := ArchiveExtensions(dir, "rev-1", []DeletedResource{c}); err != nil {
		t.Fatalf("first archive: %v", err)
	}
	if _, err := ArchiveExtensions(dir, "rev-1", []DeletedResource{i}); err != nil {
		t.Fatalf("second archive: %v", err)
	}
	m := readManifest(t, dir, "rev-1")
	if len(m.Extensions) != 2 {
		t.Fatalf("manifest must merge both archivals; got %d entries", len(m.Extensions))
	}
	ids := map[spec.ID]bool{}
	for _, e := range m.Extensions {
		ids[e.ResourceID] = true
	}
	if !ids[c.ID] || !ids[i.ID] {
		t.Errorf("manifest must retain both resource ids; got %+v", m.Extensions)
	}
}

// TestArchivePartialBatchPersistsMovedAssociations is the §47 durability
// guarantee under a mid-batch failure: when a later resource's move is refused,
// the resources already moved must still be recorded in the manifest — their
// source is gone, so a retry cannot re-derive the association, and losing it
// would strand archived code with no record of what it was.
func TestArchivePartialBatchPersistsMovedAssociations(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, gen.ExtensionPackageDirForCodeName("Alpha")+"/hooks.go", "package alpha\n")
	writeFile(t, dir, gen.ExtensionPackageDirForCodeName("Beta")+"/hooks.go", "package beta\n")
	// Pre-existing archive for Beta blocks its move mid-batch.
	writeFile(t, dir, ".forge/orphaned/rev-1/beta/old.go", "package beta\n")

	a := deletedResource("Alpha")
	b := deletedResource("Beta")
	archived, err := ArchiveExtensions(dir, "rev-1", []DeletedResource{a, b})
	if err == nil {
		t.Fatal("expected the batch to fail on Beta's blocked destination")
	}
	// Alpha moved; Beta did not.
	if len(archived) != 1 || archived[0].ResourceID != a.ID {
		t.Fatalf("only Alpha should be reported moved; got %+v", archived)
	}
	// Alpha's source is gone but its association is already on disk.
	if _, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(gen.ExtensionPackageDirForCodeName("Alpha")))); !os.IsNotExist(statErr) {
		t.Errorf("Alpha source should be moved; stat err = %v", statErr)
	}
	m := readManifest(t, dir, "rev-1")
	if len(m.Extensions) != 1 || m.Extensions[0].ResourceID != a.ID {
		t.Fatalf("Alpha's association must survive the mid-batch failure; manifest = %+v", m.Extensions)
	}

	// Recovery: clear Beta's blocker and retry. Alpha is skipped (already moved),
	// Beta moves, and the manifest ends with BOTH associations — Alpha's was not
	// erased by the retry.
	if err := os.RemoveAll(filepath.Join(dir, ".forge", "orphaned", "rev-1", "beta")); err != nil {
		t.Fatalf("clear blocker: %v", err)
	}
	if _, err := ArchiveExtensions(dir, "rev-1", []DeletedResource{a, b}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	m = readManifest(t, dir, "rev-1")
	ids := map[spec.ID]bool{}
	for _, e := range m.Extensions {
		ids[e.ResourceID] = true
	}
	if !ids[a.ID] || !ids[b.ID] {
		t.Errorf("after recovery the manifest must hold both associations; got %+v", m.Extensions)
	}
}

func TestArchiveRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	del := []DeletedResource{deletedResource("Customer")}
	cases := []struct {
		name string
		dir  string
		key  string
	}{
		{"empty dir", "", "rev-1"},
		{"empty key", dir, ""},
		{"key with slash", dir, "a/b"},
		{"key dotdot", dir, ".."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ArchiveExtensions(tc.dir, tc.key, del); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}
