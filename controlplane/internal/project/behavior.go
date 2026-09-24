package project

import (
	"context"

	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// UpdateBehavior sets a resource's behavior wholesale (DESIGN.md §4.3): the
// create/update/delete toggles, admin visibility, and the list/searchable/
// sortable/filterable/aggregatable field selections. Behavior does not touch the extension
// ABI — it only steers which handlers, admin registration and list columns the
// generators emit — so the transition is ABI-neutral and commits. Field-ID
// selections that don't belong to the resource are caught by spec validation and
// returned as an invalid-spec rejection.
func (s *Service) UpdateBehavior(ctx context.Context, projectID uint, resourceID spec.ID, behavior spec.ResourceBehavior, by uint) (CandidateResult, error) {
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
		r.Behavior = behavior
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}
