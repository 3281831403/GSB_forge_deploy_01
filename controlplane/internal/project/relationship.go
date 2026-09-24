package project

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Relationship operations (DESIGN.md §4.2, ADR-001 §54). A relationship is a
// belongs_to field pointing at another resource; the has_many side is derived by
// the compiler graph from the belongs_to's inverse label, never stored. Creating
// one is the relationship editor's job (belongs_to is rejected by the scalar
// field editor); deleting one reuses the field-delete path, which routes through
// candidate validation like any field removal.

// ErrRelationshipTarget means the relationship's target resource does not exist
// in the project's spec.
var ErrRelationshipTarget = errors.New("project: relationship target not found")

// RelationshipInput describes a belongs_to relationship to create.
type RelationshipInput struct {
	// Label is the human-readable name of the relationship on the owning
	// resource (e.g. "Customer" on Invoice).
	Label string
	// Target is the stable ID of the resource this relationship points at.
	Target spec.ID
	// InverseLabel names the derived has_many side on the target resource
	// (e.g. "Invoices" on Customer). Optional.
	InverseLabel string
	// Required marks the foreign key non-null.
	Required bool
}

// AddRelationship creates a belongs_to field on a resource referencing another
// resource, minting the field symbol and deriving its foreign-key column name.
// Adding a relationship is additive, so it commits; the has_many side falls out
// of the compiler graph from the inverse label.
func (s *Service) AddRelationship(ctx context.Context, projectID uint, resourceID spec.ID, in RelationshipInput, by uint) (CandidateResult, error) {
	if strings.TrimSpace(in.Label) == "" {
		return CandidateResult{}, fmt.Errorf("%w: a relationship label is required", ErrInvalidFieldEdit)
	}
	if in.Target == "" {
		return CandidateResult{}, fmt.Errorf("%w: a target resource is required", ErrRelationshipTarget)
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
		if candidate.FindResource(in.Target) == nil {
			return ErrRelationshipTarget
		}
		ledger := fieldLedger(resourceID, r)
		fieldID := spec.MustNewID(spec.KindField)
		codeName, err := spec.Mint(ledger, spec.FieldNamespace(resourceID), in.Label, fieldID, spec.IsReservedCodeName)
		if err != nil {
			return fmt.Errorf("mint relationship symbol: %w", err)
		}
		r.Fields = append(r.Fields, &spec.Field{
			ID:           fieldID,
			Label:        in.Label,
			Type:         spec.TypeBelongsTo,
			CodeName:     codeName,
			StorageName:  deriveForeignKeyName(in.Label, r),
			Required:     in.Required,
			Target:       in.Target,
			InverseLabel: in.InverseLabel,
		})
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}

// deriveForeignKeyName folds a relationship label into a valid, collision-free
// foreign-key column name (the <name>_id convention).
func deriveForeignKeyName(label string, r *spec.Resource) string {
	base := snakeCase(label)
	if base == "" {
		base = "ref"
	}
	taken := map[string]bool{}
	for _, f := range r.Fields {
		if f != nil {
			taken[f.StorageName] = true
		}
	}
	name := base + "_id"
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s_%d_id", base, n)
	}
	return name
}
