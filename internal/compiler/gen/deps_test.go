package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	// These blank imports make gorm and shopspring/decimal real dependencies
	// of gombit-forge. The model generator emits code that imports them, so
	// pinning the exact versions the generated code is tested against belongs
	// in this module's go.mod — and it keeps them warm in the module cache so
	// TestGeneratedModelsCompile can build offline instead of skipping.
	_ "github.com/shopspring/decimal"
	_ "gorm.io/gorm"
)

// depDecimal and depGorm are the go.mod require lines the compile test writes
// into its throwaway module. TestDepVersionsMatchGoMod guards them against
// drifting from what this module actually depends on.
const (
	depDecimal = "github.com/shopspring/decimal v1.4.0"
	depGorm    = "gorm.io/gorm v1.31.2"
)

// TestDepVersionsMatchGoMod fails if the pinned strings above diverge from
// this module's go.mod, so a go.mod bump cannot leave the compile test
// pointing at an uncached version.
func TestDepVersionsMatchGoMod(t *testing.T) {
	goMod := readGoMod(t)
	for _, want := range []string{depDecimal, depGorm} {
		// go.mod indents require lines with a tab; match the version token
		// rather than exact whitespace.
		if !strings.Contains(goMod, want) {
			t.Errorf("go.mod does not require %q; the compile test pins a version the module does not build against", want)
		}
	}
}

// readGoMod returns the module's go.mod, found by walking up from the test's
// working directory.
func readGoMod(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		candidate := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(candidate); err == nil {
			return string(data)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the test directory")
		}
		dir = parent
	}
}
