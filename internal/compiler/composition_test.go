package compiler

import (
	"bytes"
	"go/format"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
)

func TestCompositionRoot(t *testing.T) {
	src, err := CompositionRoot("example.com/app")
	if err != nil {
		t.Fatalf("composition root: %v", err)
	}
	got := string(src)

	for _, want := range []string{
		gen.Banner,
		"package main",
		`forge "example.com/app/internal/forge_generated"`,
		`"example.com/app/internal/platform"`,
		`"example.com/app/internal/web"`,
		"forge.RegisterAll(app)",
		"framework.WithEmbeddedFrontend(web.FS())",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("composition root missing %q\n%s", want, got)
		}
	}
	// It must never AutoMigrate (§14): migrations are applied out of band.
	if strings.Contains(got, "AutoMigrate") {
		t.Error("composition root must not AutoMigrate")
	}
	// It is gofmt-clean.
	formatted, err := format.Source(src)
	if err != nil {
		t.Fatalf("generated source does not parse: %v", err)
	}
	if !bytes.Equal(formatted, src) {
		t.Error("composition root must be gofmt-clean")
	}
}

func TestCompositionRootIsDeterministic(t *testing.T) {
	a, _ := CompositionRoot("example.com/app")
	b, _ := CompositionRoot("example.com/app")
	if !bytes.Equal(a, b) {
		t.Error("composition root must be deterministic")
	}
}

func TestCompositionRootRejectsBadModule(t *testing.T) {
	for _, bad := range []string{"", "   ", "has space", "has\"quote"} {
		if _, err := CompositionRoot(bad); err == nil {
			t.Errorf("CompositionRoot(%q) must error", bad)
		}
	}
}
