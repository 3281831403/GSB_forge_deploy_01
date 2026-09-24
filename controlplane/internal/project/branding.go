package project

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Branding editing (DESIGN.md §19, M3 #56). Branding — application name, logo,
// accent color and appearance mode — is a frontend concern with no extension
// contract, so the edit is ABI-neutral and commits; a malformed accent color or
// appearance is caught by spec.Validate and returned as invalid_spec.

// BrandingInput is the human-facing branding the editor supplies. Every field is
// optional; an all-empty input clears branding back to the compiler defaults.
type BrandingInput struct {
	AppName     string
	LogoRef     string
	AccentColor string
	Appearance  string
}

// SetBranding replaces the project's branding. It is ABI-neutral and commits,
// unless the result is spec-invalid (a non-hex accent color or an unsupported
// appearance). An all-empty input drops the branding block so the generated app
// falls back to its defaults rather than pinning empty values.
func (s *Service) SetBranding(ctx context.Context, projectID uint, in BrandingInput, by uint) (CandidateResult, error) {
	var result CandidateResult
	err := s.withLockedSpec(ctx, projectID, func(tx *gorm.DB, p Project, current *spec.ProjectSpec) error {
		if current == nil {
			return ErrNoSpec
		}
		candidate, err := current.Clone()
		if err != nil {
			return err
		}
		candidate.Branding = brandingOrNil(in)
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}

// brandingOrNil builds a Branding from the input, or nil when nothing is set (so
// an all-empty save falls back to the generated defaults rather than serializing
// an empty block).
func brandingOrNil(in BrandingInput) *spec.Branding {
	appName := strings.TrimSpace(in.AppName)
	logo := strings.TrimSpace(in.LogoRef)
	accent := strings.TrimSpace(in.AccentColor)
	appearance := strings.TrimSpace(in.Appearance)
	if appName == "" && logo == "" && accent == "" && appearance == "" {
		return nil
	}
	return &spec.Branding{
		AppName:     appName,
		LogoRef:     logo,
		AccentColor: accent,
		Appearance:  appearance,
	}
}
