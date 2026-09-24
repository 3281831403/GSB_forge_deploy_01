package spec

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"
)

// ID is an immutable, opaque, prefixed stable identifier (ADR-001 D1).
//
// Identity is carried by the ID and never by a name: relabeling a field or
// renaming its column does not change its ID, and two entities that merely
// share a name are not the same entity (ADR-001 §4).
type ID string

// Kind is the entity class encoded in an ID prefix.
type Kind string

const (
	KindProject  Kind = "prj"
	KindResource Kind = "res"
	KindField    Kind = "fld"
	KindRelation Kind = "rel"
	KindPage     Kind = "pag"
	KindAction   Kind = "act"
	KindHook     Kind = "hok"
	KindNav      Kind = "nav"
)

// Kinds lists every known entity kind in a stable order.
func Kinds() []Kind {
	return []Kind{
		KindProject, KindResource, KindField, KindRelation,
		KindPage, KindAction, KindHook, KindNav,
	}
}

// crockford is the Crockford base32 alphabet used by ULIDs: it excludes
// I, L, O and U so identifiers resist transcription errors.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ulidLen is the encoded length of a 128-bit ULID in Crockford base32.
const ulidLen = 26

// NewID mints a new stable ID for the given kind.
//
// The body is a ULID: a 48-bit big-endian millisecond timestamp followed by
// 80 bits of randomness, so IDs sort lexicographically by creation time while
// remaining collision-resistant. Minting is the only way an ID is created;
// IDs are never derived from labels.
func NewID(kind Kind) (ID, error) {
	return newIDAt(kind, time.Now(), rand.Read)
}

// MustNewID is NewID that panics on entropy failure.
func MustNewID(kind Kind) ID {
	id, err := NewID(kind)
	if err != nil {
		panic(err)
	}
	return id
}

func newIDAt(kind Kind, now time.Time, readRand func([]byte) (int, error)) (ID, error) {
	if kind == "" {
		return "", fmt.Errorf("spec: empty ID kind")
	}

	var raw [16]byte
	millis := uint64(now.UTC().UnixMilli())
	for i := range 6 {
		raw[5-i] = byte(millis >> (8 * uint(i)))
	}
	if _, err := readRand(raw[6:]); err != nil {
		return "", fmt.Errorf("spec: read entropy: %w", err)
	}

	return ID(string(kind) + "_" + encodeCrockford(raw)), nil
}

// encodeCrockford renders 128 bits as 26 Crockford base32 characters. The
// first character carries only the top 2 bits, matching the ULID layout.
func encodeCrockford(raw [16]byte) string {
	var value uint64
	out := make([]byte, ulidLen)

	// Process as two 64-bit halves to avoid big-int arithmetic: emit the low
	// 25 characters from a rolling bit buffer, then the leading character.
	bits := 0
	pos := ulidLen - 1
	for i := len(raw) - 1; i >= 0; i-- {
		value |= uint64(raw[i]) << bits
		bits += 8
		for bits >= 5 {
			out[pos] = crockford[value&0x1f]
			pos--
			value >>= 5
			bits -= 5
		}
	}
	for pos >= 0 {
		out[pos] = crockford[value&0x1f]
		pos--
		value >>= 5
	}
	return string(out)
}

// Kind returns the entity kind encoded in the ID prefix.
func (id ID) Kind() Kind {
	prefix, _, ok := strings.Cut(string(id), "_")
	if !ok {
		return ""
	}
	return Kind(prefix)
}

// Valid reports whether the ID is well-formed for the given kind.
func (id ID) Valid(kind Kind) bool {
	prefix, body, ok := strings.Cut(string(id), "_")
	if !ok || Kind(prefix) != kind || len(body) != ulidLen {
		return false
	}
	for _, char := range body {
		if !strings.ContainsRune(crockford, char) {
			return false
		}
	}
	return true
}

// String renders the ID.
func (id ID) String() string { return string(id) }
