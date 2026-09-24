package spec

import "testing"

// TestNormalizeADRExamples pins the four examples ADR-001 §6 gives by name, so
// the documented behaviour and the code cannot drift apart.
func TestNormalizeADRExamples(t *testing.T) {
	cases := []struct{ label, want string }{
		{"email", "Email"},
		{"contact email", "ContactEmail"},
		{"created-at", "CreatedAt"},
		{"2 factor enabled", "TwoFactorEnabled"},
	}
	for _, c := range cases {
		got, ok := Normalize(c.label)
		if !ok || got != c.want {
			t.Errorf("Normalize(%q) = (%q, %v), want (%q, true)", c.label, got, ok, c.want)
		}
	}
}

func TestNormalizeExact(t *testing.T) {
	cases := []struct{ label, want string }{
		// separators of every kind collapse into word boundaries
		{"first name", "FirstName"},
		{"first_name", "FirstName"},
		{"first-name", "FirstName"},
		{"hello, world!", "HelloWorld"},
		{"a.b.c", "ABC"},
		{"  spaced  out  ", "SpacedOut"},
		// camelCase input splits the same as separated input
		{"createdAt", "CreatedAt"},
		{"firstName", "FirstName"},
		// acronyms (all-caps runs) are preserved, not lower-cased
		{"API key", "APIKey"},
		{"EMAIL", "EMAIL"},
		// single letters and already-valid identifiers
		{"a", "A"},
		{"Customer", "Customer"},
		// leading digits are spelled (whole run) so the identifier is legal and
		// the number is preserved, not altered
		{"2", "Two"},
		{"2 factor", "TwoFactor"},
		{"42", "FourTwo"},
		{"42 units", "FourTwoUnits"},
		{"8ball", "Eightball"},
		{"version 2", "Version2"}, // trailing digit is fine as-is
		// Go keywords are only lower-case; PascalCase never collides
		{"type", "Type"},
		{"for", "For"},
		// diacritics fold to ASCII
		{"café", "Cafe"},
		{"naïve", "Naive"},
		{"Straße", "Strasse"},
		{"Ölfarbe", "Olfarbe"},
	}
	for _, c := range cases {
		got, ok := Normalize(c.label)
		if !ok || got != c.want {
			t.Errorf("Normalize(%q) = (%q, %v), want (%q, true)", c.label, got, ok, c.want)
		}
	}
}

// TestNormalizeFailsClosedOnUnrepresentableLetters: a label containing a letter
// the Latin-1 fold table cannot represent must fail closed, not drop the letter
// and mint a plausible-but-wrong symbol from the survivors (ADR-001 D14). These
// are the common case — one diacritic in an otherwise-ASCII word — not exotic.
func TestNormalizeFailsClosedOnUnrepresentableLetters(t *testing.T) {
	for _, label := range []string{
		"żółć", "Łódź", "čeština", "Ürün adı", "Đoàn", "π value", "µservice",
	} {
		if got, ok := Normalize(label); ok {
			t.Errorf("Normalize(%q) = (%q, true), want fail-closed (\"\", false)", label, got)
		}
	}
	// Latin-1 diacritics still fold and succeed — the guard fires on dropped
	// letters, not on letters the table handles.
	for _, c := range []struct{ label, want string }{
		{"Kraków", "Krakow"},
		{"café société", "CafeSociete"},
		{"Straße", "Strasse"},
	} {
		if got, ok := Normalize(c.label); !ok || got != c.want {
			t.Errorf("Normalize(%q) = (%q, %v), want (%q, true)", c.label, got, ok, c.want)
		}
	}
}

// TestNormalizeDegenerate: a label with no usable alphanumeric content yields
// (\"\", false) so the caller (minting) supplies a fallback rather than being
// handed an empty or invalid identifier.
func TestNormalizeDegenerate(t *testing.T) {
	for _, label := range []string{"", "   ", "!!!", "___", "-", ".", "—", "😀", "→", "\t\n"} {
		if got, ok := Normalize(label); ok {
			t.Errorf("Normalize(%q) = (%q, true), want (\"\", false)", label, got)
		}
	}
}

// TestNormalizeAlwaysValidWhenOK is the load-bearing invariant behind §5's
// pipeline: whatever Normalize accepts, it returns a legal exported Go
// identifier — including the ugly inputs (glued leading digits, mixed
// punctuation, stray unicode) that the exact-match table does not enumerate.
func TestNormalizeAlwaysValidWhenOK(t *testing.T) {
	labels := []string{
		"3d model", "2nd place", "42", "123abc", "v2 payload",
		"the “quoted” thing", "a—b—c", "µservice name", "ID", "id",
		"snake_case_and-kebab mixed", "TrailingDigits99", "8ball",
		"café société", "π value", "one two three four five",
	}
	for _, label := range labels {
		got, ok := Normalize(label)
		if !ok {
			continue // degenerate is allowed; this test only constrains the ok case
		}
		if !IsExportedGoIdent(got) {
			t.Errorf("Normalize(%q) = %q, which is not a valid exported Go identifier", label, got)
		}
	}
}

// TestNormalizeIsDeterministic: the same label must always produce the same
// identifier — a code symbol is a frozen ABI and cannot shift between runs.
func TestNormalizeIsDeterministic(t *testing.T) {
	for _, label := range []string{"contact email", "2 factor enabled", "café", "3d model", ""} {
		first, ok1 := Normalize(label)
		for i := 0; i < 5; i++ {
			got, ok := Normalize(label)
			if got != first || ok != ok1 {
				t.Errorf("Normalize(%q) not deterministic: got (%q,%v) then (%q,%v)", label, first, ok1, got, ok)
			}
		}
	}
}

// TestNormalizeDoesNotConsultReservedTable documents the boundary in §5:
// normalization produces a candidate; ledger and reserved-name checks are a
// later, separate stage (#17/#18). "created at" normalizes to the reserved
// symbol CreatedAt rather than being rejected or altered here.
func TestNormalizeDoesNotConsultReservedTable(t *testing.T) {
	got, ok := Normalize("created at")
	if !ok || got != "CreatedAt" {
		t.Fatalf("Normalize(%q) = (%q,%v), want (\"CreatedAt\", true)", "created at", got, ok)
	}
	if !IsReservedCodeName(got) {
		t.Fatal("precondition: CreatedAt should be reserved, so this test proves Normalize ignores the reserved set")
	}
}
