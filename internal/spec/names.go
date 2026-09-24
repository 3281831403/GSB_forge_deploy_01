package spec

import (
	"fmt"
	"strings"
)

// goKeywords are reserved words that can never be used as a generated symbol.
var goKeywords = map[string]struct{}{
	"break": {}, "case": {}, "chan": {}, "const": {}, "continue": {},
	"default": {}, "defer": {}, "else": {}, "fallthrough": {}, "for": {},
	"func": {}, "go": {}, "goto": {}, "if": {}, "import": {},
	"interface": {}, "map": {}, "package": {}, "range": {}, "return": {},
	"select": {}, "struct": {}, "switch": {}, "type": {}, "var": {},
}

// IsGoKeyword reports whether name is a Go reserved word.
func IsGoKeyword(name string) bool {
	_, found := goKeywords[name]
	return found
}

// reservedModelSymbols is the field/accessor-namespace reservation table
// (ADR-001 §12): exported Go symbols the generated model already defines, which
// a field's code_name therefore may not mint. It is framework- and
// language-level, independent of how the generator lays out packages — the
// generation-specific reservations (the handler type, route entry point and
// import names) belong to the generator, internal/compiler/gen, which owns that
// ABI. This table is the single source both the validator here and the
// generator consult, so a spec can never validate with a code_name the
// generator would then reject at go build.
//
// It is versioned with the model ABI: the gorm.Model promoted fields plus the
// methods the generated model type defines (today, TableName). A stage that
// adds a model field or method — the extension-API accessors (F0 #22), say —
// extends this table, and both consumers pick the change up. Before this table
// was complete the validator accepted Model and TableName while the generator
// rejected them, so a spec could validate yet fail to build; that split is what
// centralizing here closes.
var reservedModelSymbols = map[string]string{
	"Model":     "gorm.Model is embedded on every generated model",
	"ID":        "gorm.Model provides the primary key ID",
	"CreatedAt": "gorm.Model provides CreatedAt",
	"UpdatedAt": "gorm.Model provides UpdatedAt",
	"DeletedAt": "gorm.Model provides DeletedAt (soft delete)",
	"TableName": "the generated model defines a TableName method, and Go forbids a field and method of the same name",
}

// reservedStorageNames is the column-name reservation for the same fields on
// the database side: gorm.Model's four columns, which a field's storage_name
// therefore may not mint. It is deliberately not a mirror of
// reservedModelSymbols — Model and TableName are Go symbol collisions (an
// embedded struct and a method) with no column equivalent, so the two tables
// differ in both shape and content on purpose. It matches the set Gombit's own
// resourcegen refuses.
var reservedStorageNames = map[string]string{
	"id":         "gorm.Model provides the primary key column id",
	"created_at": "gorm.Model provides created_at",
	"updated_at": "gorm.Model provides updated_at",
	"deleted_at": "gorm.Model provides deleted_at (soft delete)",
}

// ReservedModelSymbol reports whether name is reserved in the model's field/
// accessor namespace, and if so why. The reason is actionable feedback the
// allocator and the validator surface (ADR-001 §12: rejected or disambiguated
// at mint time, never left for a go build failure).
func ReservedModelSymbol(name string) (reason string, reserved bool) {
	reason, reserved = reservedModelSymbols[name]
	return reason, reserved
}

// IsReservedCodeName reports whether a field code_name collides with a symbol
// the generated model already defines.
func IsReservedCodeName(name string) bool {
	_, reserved := reservedModelSymbols[name]
	return reserved
}

// GeneratedModelFieldName is the Go struct-field name a field contributes to its
// resource's generated model: the frozen code symbol for a scalar, and the code
// symbol plus "ID" for a belongs_to (GORM's foreign-key convention — the
// association is named by the symbol, its key column by <Symbol>ID). It is the
// identifier whose per-resource uniqueness the model must preserve, so a
// belongs_to "Customer" (key "CustomerID") and a scalar "CustomerID" would emit
// the same struct field. Validation and the model generator share this so the
// two cannot disagree about the name they check for collisions.
func GeneratedModelFieldName(codeName string, fieldType FieldType) string {
	if fieldType == TypeBelongsTo {
		return codeName + "ID"
	}
	return codeName
}

// ReservedStorageName reports whether a column name collides with a gorm.Model
// column, and if so why — the storage-side counterpart to ReservedModelSymbol,
// so both reserved paths surface an actionable reason rather than a generic one.
func ReservedStorageName(name string) (reason string, reserved bool) {
	reason, reserved = reservedStorageNames[name]
	return reason, reserved
}

// IsReservedStorageName reports whether a column name collides with the
// embedded gorm.Model.
func IsReservedStorageName(name string) bool {
	_, reserved := reservedStorageNames[name]
	return reserved
}

// IsExportedGoIdent reports whether name is a legal exported Go identifier,
// which is what every generated code symbol must be (ADR-001 §6).
func IsExportedGoIdent(name string) bool {
	if name == "" || IsGoKeyword(name) {
		return false
	}
	for index, char := range name {
		switch {
		case char >= 'A' && char <= 'Z':
		case char >= 'a' && char <= 'z':
			if index == 0 {
				return false // must start uppercase to be exported
			}
		case char >= '0' && char <= '9':
			if index == 0 {
				return false // may not start with a digit
			}
		case char == '_':
			if index == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// IsStorageName reports whether name is a legal lower_snake_case storage
// identifier suitable for a table or column name.
//
// Go keywords are deliberately allowed: storage names map to the database and
// are a separate naming domain from code symbols (ADR-001 D2), so a column
// may legitimately be called "type".
func IsStorageName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	if name[0] == '_' || strings.HasSuffix(name, "_") || strings.Contains(name, "__") {
		return false
	}
	for index, char := range name {
		switch {
		case char >= 'a' && char <= 'z':
		case char == '_':
		case char >= '0' && char <= '9':
			if index == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isDecimalLiteral reports whether value is a plain decimal number.
//
// This is deliberately narrower than strconv.ParseFloat, which also accepts
// "NaN", "Inf", hexadecimal floats such as "0x1p-2" and exponent notation.
// A default is emitted verbatim into the generated model and the migration,
// so the grammar is restricted to what a column default can actually hold:
// an optional sign, decimal digits, and at most one fractional point.
func isDecimalLiteral(value string) bool {
	if value == "" {
		return false
	}

	index := 0
	if value[0] == '+' || value[0] == '-' {
		index++
	}

	digits := 0
	seenPoint := false
	for ; index < len(value); index++ {
		switch char := value[index]; {
		case char >= '0' && char <= '9':
			digits++
		case char == '.':
			if seenPoint {
				return false
			}
			seenPoint = true
		default:
			return false
		}
	}
	return digits > 0
}

// IsSafeDefaultValue reports whether a string-valued default can be carried
// into generated output, and if not, the first offending rune.
//
// The compiler emits defaults inside a GORM struct tag. Empirically, neither
// Go's struct-tag unquoting nor GORM's ';'-separated tag grammar can
// round-trip a value containing ';', '"', a backtick, a backslash, or a
// control character: the tag either fails to unquote or is truncated at the
// separator. A default that cannot be emitted is not a usable default, so the
// validator rejects it here — keeping "spec-valid" in step with "emittable"
// (the generator relies on this rather than re-deriving it).
func IsSafeDefaultValue(value string) (ok bool, bad string) {
	for _, r := range value {
		switch {
		case r == ';', r == '"', r == '`', r == '\\':
			return false, string(r)
		case r < 0x20: // control characters, including newline and tab
			return false, fmt.Sprintf("\\x%02x", r)
		}
	}
	return true, ""
}

// IsSlug reports whether value is a legal lower kebab-case URL slug.
func IsSlug(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	if strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") || strings.Contains(value, "--") {
		return false
	}
	for index, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char == '-':
		case char >= '0' && char <= '9':
			if index == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
