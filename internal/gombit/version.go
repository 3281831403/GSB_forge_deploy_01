// Package gombit is Forge's boundary to the Gombit toolchain.
//
// Gombit owns framework primitives; Forge owns application synthesis
// (ADR-004). This package covers the framework-primitive side: scaffolding an
// application, and later migrations, OpenAPI and client generation.
//
// Every operation here is project-level. Forge never invokes Gombit once per
// resource, page or field (ADR-001 §68–69), and never drives
// `gombit make resource`, which is a convenience for hand-written
// applications (ADR-004 D4).
package gombit

import (
	"fmt"
	"strconv"
	"strings"
)

// MinimumVersion is the oldest Gombit that Forge supports.
//
// v0.1.2 renamed the module from github.com/LAA-Software-Engineering/gombit
// to github.com/gombit-dev/gombit; generated applications import the latter, so
// anything older cannot resolve. The floor advanced to v0.1.11 for the declared
// server-side list filtering/sort/search the generated list handlers consume
// (gombit #260), then to v0.1.12, which added the declared server-side numeric
// aggregate contract (gombit #273) the generated dashboard aggregate cards
// consume; an older toolchain builds a tree whose list pages cannot honor the
// spec's search/filter/sort or reduce a numeric field server-side (ADR-004 D5).
var MinimumVersion = Version{Major: 0, Minor: 1, Patch: 12}

// ModulePath is the Go module generated applications depend on.
const ModulePath = "github.com/gombit-dev/gombit"

// Version is a Gombit release version.
type Version struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion parses a semantic version, with or without a leading "v".
//
// Pre-release and build metadata are rejected rather than silently dropped: a
// pinned toolchain version is recorded in export provenance (ADR-001 §32), so
// quietly reading "v0.2.0-rc1" as "v0.2.0" would misreport what built an
// application.
func ParseVersion(value string) (Version, error) {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(trimmed, "v")
	if trimmed == "" {
		return Version{}, fmt.Errorf("gombit: empty version")
	}

	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("gombit: version %q is not MAJOR.MINOR.PATCH", value)
	}

	numbers := make([]int, 3)
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return Version{}, fmt.Errorf("gombit: version %q has an invalid component %q", value, part)
		}
		numbers[i] = number
	}

	return Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}, nil
}

// String renders the version with a leading "v".
func (v Version) String() string {
	return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Compare returns -1, 0 or 1 as v sorts before, equal to, or after other.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{
		{v.Major, other.Major},
		{v.Minor, other.Minor},
		{v.Patch, other.Patch},
	} {
		switch {
		case pair[0] < pair[1]:
			return -1
		case pair[0] > pair[1]:
			return 1
		}
	}
	return 0
}

// AtLeast reports whether v is not older than other.
func (v Version) AtLeast(other Version) bool { return v.Compare(other) >= 0 }

// CheckSupported reports whether a detected toolchain is usable by Forge.
func CheckSupported(detected Version) error {
	if !detected.AtLeast(MinimumVersion) {
		return fmt.Errorf(
			"gombit: toolchain %s is older than the required %s; "+
				"generated applications import %s and rely on features added by %s "+
				"(declared server-side list filtering #260, numeric aggregates #273)",
			detected, MinimumVersion, ModulePath, MinimumVersion)
	}
	return nil
}

// parseVersionOutput extracts the version from `gombit version` output.
//
// The command prints a block of key/value lines, the first of which is
// "gombit:   v0.1.5". Only that line is authoritative; the "go:" line reports
// the Go toolchain and must not be mistaken for it.
func parseVersionOutput(output string) (Version, error) {
	for line := range strings.SplitSeq(output, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) != "gombit" {
			continue
		}
		return ParseVersion(value)
	}
	return Version{}, fmt.Errorf("gombit: no %q line in version output", "gombit:")
}
