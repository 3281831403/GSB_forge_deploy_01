package gombit

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input   string
		want    Version
		wantErr bool
	}{
		{input: "v0.1.5", want: Version{0, 1, 5}},
		{input: "0.1.5", want: Version{0, 1, 5}},
		{input: "  v1.20.300  ", want: Version{1, 20, 300}},
		{input: "v0.0.0", want: Version{0, 0, 0}},

		{input: "", wantErr: true},
		{input: "v", wantErr: true},
		{input: "v0.1", wantErr: true},
		{input: "v0.1.5.1", wantErr: true},
		{input: "v0.1.x", wantErr: true},
		{input: "v-1.0.0", wantErr: true},
		// Pre-release and build metadata are refused rather than truncated:
		// the pinned version is recorded as export provenance.
		{input: "v0.2.0-rc1", wantErr: true},
		{input: "v0.2.0+build7", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := ParseVersion(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Errorf("got %v want %v", got, test.want)
			}
		})
	}
}

func TestVersionCompare(t *testing.T) {
	tests := []struct {
		left, right Version
		want        int
	}{
		{Version{0, 1, 2}, Version{0, 1, 2}, 0},
		{Version{0, 1, 1}, Version{0, 1, 2}, -1},
		{Version{0, 1, 3}, Version{0, 1, 2}, 1},
		{Version{0, 2, 0}, Version{0, 1, 9}, 1},
		{Version{1, 0, 0}, Version{0, 9, 9}, 1},
		// Ordering is component-wise, not lexicographic on the rendered string.
		{Version{0, 1, 10}, Version{0, 1, 9}, 1},
		{Version{0, 10, 0}, Version{0, 9, 0}, 1},
	}

	for _, test := range tests {
		name := test.left.String() + " vs " + test.right.String()
		t.Run(name, func(t *testing.T) {
			if got := test.left.Compare(test.right); got != test.want {
				t.Errorf("Compare: got %d want %d", got, test.want)
			}
			if got := test.right.Compare(test.left); got != -test.want {
				t.Errorf("Compare is not antisymmetric: got %d", got)
			}
		})
	}
}

// TestCheckSupported pins the v0.1.12 floor: older toolchains lack the declared
// server-side numeric aggregate contract (#273) the generated dashboard
// aggregate cards consume (v0.1.11 added only list filtering/sort/search, #260),
// so a tree they scaffold cannot reduce a numeric field server-side.
func TestCheckSupported(t *testing.T) {
	supported := []Version{{0, 1, 12}, {0, 2, 0}, {1, 0, 0}}
	for _, version := range supported {
		if err := CheckSupported(version); err != nil {
			t.Errorf("%s should be supported: %v", version, err)
		}
	}

	// Everything below the floor, including the previously-supported v0.1.2–v0.1.11.
	unsupported := []Version{{0, 1, 11}, {0, 1, 10}, {0, 1, 2}, {0, 1, 1}, {0, 0, 9}}
	for _, version := range unsupported {
		if err := CheckSupported(version); err == nil {
			t.Errorf("%s should be rejected", version)
		}
	}
}

func TestParseVersionOutput(t *testing.T) {
	// The real shape of `gombit version` output.
	output := `gombit:   v0.1.5
commit:   unknown
built:    unknown
go:       go1.25.13
platform: linux/amd64`

	got, err := parseVersionOutput(output)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := (Version{0, 1, 5}); got != want {
		t.Errorf("got %v want %v", got, want)
	}
}

// TestParseVersionOutputIgnoresGoLine guards the obvious misread: the "go:"
// line is a different toolchain and must never be taken as Gombit's version.
func TestParseVersionOutputIgnoresGoLine(t *testing.T) {
	if _, err := parseVersionOutput("go:       go1.25.13\nplatform: linux/amd64"); err == nil {
		t.Error("expected an error when no gombit: line is present")
	}
}

func TestParseVersionOutputRejectsGarbage(t *testing.T) {
	for _, output := range []string{"", "not a version", "gombit:", "gombit:   dev"} {
		if _, err := parseVersionOutput(output); err == nil {
			t.Errorf("expected an error for %q", output)
		}
	}
}
