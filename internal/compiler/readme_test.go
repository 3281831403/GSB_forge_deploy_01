package compiler

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

func TestREADMEHasCommandsAndOverview(t *testing.T) {
	md := string(README(sampleSpec(t)))

	// The three §28 commands, each in a code block.
	for _, cmd := range []string{"gombit db migrate", "gombit dev", "gombit build --embed"} {
		if !strings.Contains(md, "```sh\n"+cmd+"\n```") {
			t.Errorf("README must document %q in a code block:\n%s", cmd, md)
		}
	}
	// A project overview: the app name as the title and the resources listed.
	if !strings.HasPrefix(md, "# Acme\n") {
		t.Errorf("README title must be the app/project name:\n%s", md)
	}
	for _, label := range []string{"- Customer", "- Invoice"} {
		if !strings.Contains(md, label) {
			t.Errorf("README must list resource %q", label)
		}
	}
	// It states the no-Forge-dependency guarantee (P2).
	if !strings.Contains(md, "no dependency on Forge") {
		t.Error("README must state the exported app has no Forge dependency")
	}
}

// TestREADMEPrefersBrandingName: the branding app name wins over the project name.
func TestREADMEPrefersBrandingName(t *testing.T) {
	s := sampleSpec(t)
	s.Branding = &spec.Branding{AppName: "Shopfront"}
	if !strings.HasPrefix(string(README(s)), "# Shopfront\n") {
		t.Error("README title must prefer the branding app name")
	}
}

func TestREADMEIsDeterministic(t *testing.T) {
	s := sampleSpec(t)
	if !bytes.Equal(README(s), README(s)) {
		t.Error("README must be deterministic")
	}
}

func TestWriteReadme(t *testing.T) {
	dir := t.TempDir()
	if err := WriteReadme(dir, sampleSpec(t)); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("read readme: %v", err)
	}
	if !bytes.Equal(got, README(sampleSpec(t))) {
		t.Error("WriteReadme must write the generated README content")
	}
}
