package project

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Navigation editing (DESIGN.md §4.5, M3 #55). Navigation is an ordered list of
// entries, each pointing at a page (a dashboard or a resource list) or an
// external URL. It is a frontend concern with no extension contract, so the edit
// is ABI-neutral and commits; a dangling page reference, a non-navigable page
// type or a malformed external URL is caught by spec.Validate and returned as
// invalid_spec.

// ErrInvalidNavEdit means a navigation entry is malformed in a way the input
// alone proves wrong (empty label, unknown target, or a target/field mismatch).
var ErrInvalidNavEdit = errors.New("project: invalid navigation edit")

// NavItemInput is one human-facing navigation entry the editor supplies. The
// stable ID is minted; a page entry sets Page, an external entry sets URL.
type NavItemInput struct {
	Label  string
	Target spec.NavTarget
	Page   spec.ID
	URL    string
}

// SetNavigation replaces the project's whole navigation with the given ordered
// entries (order is meaningful — it is the authored nav order). It is ABI-neutral
// and commits, unless the result is spec-invalid (a dangling or non-navigable
// page, a non-http external URL).
func (s *Service) SetNavigation(ctx context.Context, projectID uint, items []NavItemInput, by uint) (CandidateResult, error) {
	for i, in := range items {
		if err := validateNavInput(in); err != nil {
			return CandidateResult{}, fmt.Errorf("entry %d: %w", i, err)
		}
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
		nav := make([]*spec.NavItem, 0, len(items))
		for _, in := range items {
			item := &spec.NavItem{
				ID:     spec.MustNewID(spec.KindNav),
				Label:  in.Label,
				Target: in.Target,
			}
			// Carry only the field the target uses, so a page entry never leaks a
			// stray URL (which spec.Validate would reject) and vice versa.
			if in.Target == spec.NavExternal {
				item.URL = in.URL
			} else {
				item.Page = in.Page
			}
			nav = append(nav, item)
		}
		candidate.Navigation = nav
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}

// validateNavInput rejects entries the request alone proves malformed. Page
// existence and type, and external-URL scheme, are the spec validator's job and
// surface as invalid_spec.
func validateNavInput(in NavItemInput) error {
	if strings.TrimSpace(in.Label) == "" {
		return fmt.Errorf("%w: a navigation label is required", ErrInvalidNavEdit)
	}
	switch in.Target {
	case spec.NavPage:
		if in.Page == "" {
			return fmt.Errorf("%w: a page entry must reference a page", ErrInvalidNavEdit)
		}
	case spec.NavExternal:
		if strings.TrimSpace(in.URL) == "" {
			return fmt.Errorf("%w: an external entry must set a url", ErrInvalidNavEdit)
		}
	default:
		return fmt.Errorf("%w: unknown navigation target %q", ErrInvalidNavEdit, in.Target)
	}
	return nil
}
