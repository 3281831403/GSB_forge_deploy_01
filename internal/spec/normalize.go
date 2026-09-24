package spec

import (
	"strings"
	"unicode"
)

// Normalize converts a human label into a candidate exported Go identifier
// (ADR-001 §6). It is the first step of symbol minting (§5): the candidate it
// returns is then checked against the symbol ledger and the reserved-name table
// and disambiguated before it is frozen. Normalize itself performs none of
// those checks — it does not consult the ledger or the reserved set, so
// "created at" normalizes to "CreatedAt" here even though that symbol is
// reserved; catching that collision is minting's job (#17/#18), not this
// function's.
//
// The rules are fixed and deterministic — the same label yields the same
// identifier on every run and every build — because a code symbol is a frozen
// source ABI and must not shift under a project as the toolchain moves. If the
// rules ever change they change behind an explicit version, never silently.
//
// The transformation:
//
//   - fold Latin-1 accented letters to their ASCII base (café → Cafe, Straße →
//     Strasse); any other non-ASCII rune is treated as a separator;
//   - split the label into words on any non-alphanumeric run and at each
//     lowercase→uppercase boundary, so both "created-at" and "createdAt" yield
//     ["created","at"];
//   - upper-case the first letter of each word and concatenate (PascalCase),
//     preserving the rest of each word so acronyms survive (API → API). The
//     cost of preserving case is that a shouted label yields a shouted
//     identifier ("EMAIL ADDRESS" → EMAILADDRESS); distinguishing an acronym
//     from shouting would need a heuristic, and a heuristic here would move
//     frozen symbols when it changed, which ADR-001 forbids;
//   - if the result would start with digits, spell the whole leading digit run
//     so the identifier is legal and the number is preserved ("2 factor" →
//     TwoFactor, "42" → FourTwo).
//
// ok is false when the label cannot form an identifier the author would
// recognise: it has no usable alphanumeric content (empty, whitespace, all
// punctuation), or it contains a letter this normalizer cannot fold to ASCII (a
// Latin Extended-A or non-Latin letter — see foldToASCII). In the latter case
// Normalize fails closed rather than dropping the letter and minting a
// plausible-but-wrong symbol; the minting pipeline then supplies a kind-derived
// fallback. Callers must handle ok=false rather than assume a name.
func Normalize(label string) (ident string, ok bool) {
	var folded strings.Builder
	folded.Grow(len(label))
	droppedLetter := false
	for _, r := range label {
		f := foldToASCII(r)
		if r >= 0x80 && f == " " && unicode.IsLetter(r) {
			// A letter the fold table cannot represent. Keeping the surviving
			// letters would mint a name that looks deliberate ("żółć" → "O") and
			// freeze it forever (ADR-001 D3–D5); fail closed so the kind-derived
			// fallback runs instead (D14).
			droppedLetter = true
		}
		folded.WriteString(f)
	}
	if droppedLetter {
		return "", false
	}

	words := splitWords(folded.String())
	if len(words) == 0 {
		return "", false
	}

	var b strings.Builder
	for _, w := range words {
		b.WriteString(upperFirstASCII(w))
	}
	out := b.String()

	// A Go identifier may not begin with a digit. Spell the entire leading digit
	// run rather than substituting one digit, so the number is preserved rather
	// than silently altered ("42" → FourTwo, not Four2).
	if out[0] >= '0' && out[0] <= '9' {
		end := 0
		for end < len(out) && out[end] >= '0' && out[end] <= '9' {
			end++
		}
		var sb strings.Builder
		for _, d := range []byte(out[:end]) {
			sb.WriteString(digitWords[d-'0'])
		}
		sb.WriteString(out[end:])
		out = sb.String()
	}

	// Constructed to be valid; verify rather than trust, so a future rule change
	// that breaks the invariant fails a test instead of emitting bad source.
	if !IsExportedGoIdent(out) {
		return "", false
	}
	return out, true
}

// digitWords spells single decimal digits for leading-digit repair.
var digitWords = [10]string{
	"Zero", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine",
}

// splitWords breaks an ASCII string into identifier words. A word is a maximal
// run of ASCII alphanumerics; runs are also cut at each lowercase→uppercase
// boundary so camelCase input splits the way separator-delimited input does.
func splitWords(s string) []string {
	var words []string
	var cur []byte
	var prev byte

	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			if prev >= 'a' && prev <= 'z' && c >= 'A' && c <= 'Z' {
				flush() // camelCase boundary
			}
			cur = append(cur, c)
			prev = c
		default:
			flush()
			prev = 0
		}
	}
	flush()
	return words
}

// upperFirstASCII upper-cases the first byte of an ASCII word, leaving the rest
// untouched so an all-caps acronym stays intact.
func upperFirstASCII(w string) string {
	if w == "" {
		return ""
	}
	b := []byte(w)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 'a' - 'A'
	}
	return string(b)
}

// foldToASCII maps one rune to its ASCII identifier contribution: ASCII runes
// pass through; Latin-1 accented letters fold to their base letter (some to two
// letters, e.g. Æ→AE, ß→ss); every other non-ASCII rune becomes a space, which
// splitWords treats as a separator.
//
// The table is Latin-1 only (much of Western European text) and deliberately
// dependency-free. Letters outside it — Latin Extended-A (Polish, Czech,
// Hungarian, Turkish, the Baltics, …) and non-Latin scripts — are not folded;
// Normalize detects that a *letter* rather than punctuation hit this space and
// fails closed, so an unrepresentable label yields a kind-derived fallback
// rather than a mangled fragment. Extending the table is a safe future
// improvement that turns those fallbacks into names.
func foldToASCII(r rune) string {
	if r < 0x80 {
		return string(r)
	}
	switch r {
	case 'À', 'Á', 'Â', 'Ã', 'Ä', 'Å':
		return "A"
	case 'Æ':
		return "AE"
	case 'Ç':
		return "C"
	case 'È', 'É', 'Ê', 'Ë':
		return "E"
	case 'Ì', 'Í', 'Î', 'Ï':
		return "I"
	case 'Ð':
		return "D"
	case 'Ñ':
		return "N"
	case 'Ò', 'Ó', 'Ô', 'Õ', 'Ö', 'Ø':
		return "O"
	case 'Ù', 'Ú', 'Û', 'Ü':
		return "U"
	case 'Ý':
		return "Y"
	case 'Þ':
		return "TH"
	case 'ß':
		return "ss"
	case 'à', 'á', 'â', 'ã', 'ä', 'å':
		return "a"
	case 'æ':
		return "ae"
	case 'ç':
		return "c"
	case 'è', 'é', 'ê', 'ë':
		return "e"
	case 'ì', 'í', 'î', 'ï':
		return "i"
	case 'ð':
		return "d"
	case 'ñ':
		return "n"
	case 'ò', 'ó', 'ô', 'õ', 'ö', 'ø':
		return "o"
	case 'ù', 'ú', 'û', 'ü':
		return "u"
	case 'ý', 'ÿ':
		return "y"
	case 'þ':
		return "th"
	default:
		return " "
	}
}
