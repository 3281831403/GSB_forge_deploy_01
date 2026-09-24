// Package projectspec adapts the project revision store to the spec-resolving
// interfaces the export stack consumes: ghexport's SpecSource (the project's
// head revision) and the export worker's Revisions (a specific frozen revision).
// Both decode a stored revision's canonical bytes back into a ProjectSpec and
// use its hash as the opaque provenance reference.
package projectspec

import (
	"context"
	"fmt"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Revisions is the subset of project.Service this adapter needs.
type Revisions interface {
	Head(ctx context.Context, projectID uint) (project.Revision, bool, error)
	Revision(ctx context.Context, id uint) (project.Revision, bool, error)
}

// Source resolves specs from the project revision store.
type Source struct {
	revs Revisions
}

// NewSource builds the adapter over a revision store (project.Service).
func NewSource(revs Revisions) *Source {
	return &Source{revs: revs}
}

// HeadSpec returns the project's head-revision spec and its hash, or a nil spec
// when the project has no revision yet (which ghexport reports as "nothing to
// export"). It satisfies ghexport.SpecSource.
func (s *Source) HeadSpec(ctx context.Context, projectID uint) (*spec.ProjectSpec, string, error) {
	rev, ok, err := s.revs.Head(ctx, projectID)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", nil
	}
	return decode(rev)
}

// RevisionSpec returns a specific revision's spec and its hash — the frozen
// revision an export job pinned at enqueue. A missing revision is an error, not
// an empty result: the job referenced a revision that should exist. It satisfies
// the export worker's Revisions interface.
func (s *Source) RevisionSpec(ctx context.Context, revisionID uint) (*spec.ProjectSpec, string, error) {
	rev, ok, err := s.revs.Revision(ctx, revisionID)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", fmt.Errorf("projectspec: revision %d not found", revisionID)
	}
	return decode(rev)
}

// decode turns a stored revision's canonical bytes back into a ProjectSpec,
// pairing it with the revision hash as the provenance reference.
func decode(rev project.Revision) (*spec.ProjectSpec, string, error) {
	sp, err := spec.Unmarshal([]byte(rev.SpecJSON))
	if err != nil {
		return nil, "", fmt.Errorf("projectspec: decode revision %d: %w", rev.ID, err)
	}
	return sp, rev.SpecHash, nil
}
