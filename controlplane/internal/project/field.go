package project

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Field operations (DESIGN.md §4.2, ADR-001 §5, §56). Like the resource ops the
// caller supplies human-readable input and the backend mints the field's frozen
// code symbol and derives its storage name; every edit is built inside the
// project lock and routed through the candidate pipeline, so a field type change
// is classified ABI-breaking (§56) and returned for validation rather than
// silently committed, while a label or constraint change commits as neutral.

var (
	// ErrFieldNotFound means the resource has no field with the given stable ID.
	ErrFieldNotFound = errors.New("project: field not found")
	// ErrInvalidFieldEdit means the field input is malformed (empty label,
	// unknown type, or a belongs_to which belongs to the relationship editor).
	ErrInvalidFieldEdit = errors.New("project: invalid field edit")
)

// FieldInput is the human-facing description of a field the editor supplies.
// Target/relationship fields (belongs_to) are the relationship editor's job
// (#46) and are rejected here.
type FieldInput struct {
	Label      string
	Type       spec.FieldType
	Required   bool
	Unique     bool
	Default    *string
	EnumValues []spec.EnumValue
}

// AddField mints a new field on a resource from the input, deriving its storage
// name, then submits the candidate. Adding a field is additive, so it commits.
func (s *Service) AddField(ctx context.Context, projectID uint, resourceID spec.ID, in FieldInput, by uint) (CandidateResult, error) {
	if err := validateFieldInput(in); err != nil {
		return CandidateResult{}, err
	}
	var result CandidateResult
	err := s.withLockedSpec(ctx, projectID, func(tx *gorm.DB, p Project, current *spec.ProjectSpec) error {
		if current == nil {
			return ErrNoSpec
		}
		candidate, err := current.Clone()
		if err != nil {
			return err
		}
		r := candidate.FindResource(resourceID)
		if r == nil {
			return ErrResourceNotFound
		}
		ledger := fieldLedger(resourceID, r)
		fieldID := spec.MustNewID(spec.KindField)
		codeName, err := spec.Mint(ledger, spec.FieldNamespace(resourceID), in.Label, fieldID, spec.IsReservedCodeName)
		if err != nil {
			return fmt.Errorf("mint field symbol: %w", err)
		}
		r.Fields = append(r.Fields, &spec.Field{
			ID:          fieldID,
			Label:       in.Label,
			Type:        in.Type,
			CodeName:    codeName,
			StorageName: deriveFieldStorageName(in.Label, r),
			Required:    in.Required,
			Unique:      in.Unique,
			Default:     in.Default,
			EnumValues:  in.EnumValues,
		})
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}

// UpdateField changes a field's label, type and constraints. The frozen code
// symbol and storage name are preserved (identity, D2), so a label or constraint
// change is ABI-neutral and commits; a type change is extension-visible and
// classifies breaking (§56, §93), returned for validation rather than committed.
func (s *Service) UpdateField(ctx context.Context, projectID uint, resourceID, fieldID spec.ID, in FieldInput, by uint) (CandidateResult, error) {
	if err := validateFieldInput(in); err != nil {
		return CandidateResult{}, err
	}
	var result CandidateResult
	err := s.withLockedSpec(ctx, projectID, func(tx *gorm.DB, p Project, current *spec.ProjectSpec) error {
		if current == nil {
			return ErrNoSpec
		}
		candidate, err := current.Clone()
		if err != nil {
			return err
		}
		r := candidate.FindResource(resourceID)
		if r == nil {
			return ErrResourceNotFound
		}
		f := r.FindField(fieldID)
		if f == nil {
			return ErrFieldNotFound
		}
		// A relationship field is the #46 editor's job; editing it here (with no
		// Target in FieldInput) would strip its target and fail validation with a
		// baffling message, so reject it up front — symmetric with AddField.
		if f.Type == spec.TypeBelongsTo {
			return fmt.Errorf("%w: edit belongs_to fields in the relationship editor", ErrInvalidFieldEdit)
		}
		f.Label = in.Label
		f.Type = in.Type
		f.Required = in.Required
		f.Unique = in.Unique
		f.Default = in.Default
		f.EnumValues = in.EnumValues
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}

// DeleteField removes a field. Removing a field drops its extension accessor, so
// the transition is ABI-breaking and is returned for validation rather than
// committed (a field has no per-resource archival path); a field still
// referenced by behavior lists or pages fails validation instead.
func (s *Service) DeleteField(ctx context.Context, projectID uint, resourceID, fieldID spec.ID, by uint) (CandidateResult, error) {
	var result CandidateResult
	err := s.withLockedSpec(ctx, projectID, func(tx *gorm.DB, p Project, current *spec.ProjectSpec) error {
		if current == nil {
			return ErrNoSpec
		}
		candidate, err := current.Clone()
		if err != nil {
			return err
		}
		r := candidate.FindResource(resourceID)
		if r == nil {
			return ErrResourceNotFound
		}
		if r.FindField(fieldID) == nil {
			return ErrFieldNotFound
		}
		r.Fields = removeField(r.Fields, fieldID)
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}

// validateFieldInput rejects malformed field input before any spec is built.
func validateFieldInput(in FieldInput) error {
	if strings.TrimSpace(in.Label) == "" {
		return fmt.Errorf("%w: a field label is required", ErrInvalidFieldEdit)
	}
	switch in.Type {
	case spec.TypeString, spec.TypeText, spec.TypeInteger, spec.TypeDecimal,
		spec.TypeBoolean, spec.TypeDatetime, spec.TypeDate, spec.TypeEnum:
	case spec.TypeBelongsTo:
		return fmt.Errorf("%w: belongs_to fields are created with the relationship editor", ErrInvalidFieldEdit)
	default:
		return fmt.Errorf("%w: unknown field type %q", ErrInvalidFieldEdit, in.Type)
	}
	return nil
}

// fieldLedger reconstructs a resource's field-symbol ledger from its live field
// code names, so Mint disambiguates a new field symbol against them (the same
// live-only limitation as resourceLedger, §70).
func fieldLedger(resourceID spec.ID, r *spec.Resource) *spec.Ledger {
	l := spec.NewLedger()
	ns := spec.FieldNamespace(resourceID)
	for _, f := range r.Fields {
		if f != nil {
			_ = l.Record(ns, f.CodeName, f.ID)
		}
	}
	return l
}

// deriveFieldStorageName folds a field label into a valid, collision-free
// storage column name within its resource.
func deriveFieldStorageName(label string, r *spec.Resource) string {
	base := snakeCase(label)
	if base == "" {
		base = "field"
	}
	taken := map[string]bool{}
	for _, f := range r.Fields {
		if f != nil {
			taken[f.StorageName] = true
		}
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s_%d", base, n)
	}
	return name
}

func removeField(fields []*spec.Field, id spec.ID) []*spec.Field {
	out := fields[:0:0]
	for _, f := range fields {
		if f == nil || f.ID != id {
			out = append(out, f)
		}
	}
	return out
}
