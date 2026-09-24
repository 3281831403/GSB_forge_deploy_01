package project

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/internal/compiler"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Resource-tree operations (DESIGN.md §17 Data, ADR-001 §5, §45-46). These are
// the authoritative resource mutations: the caller supplies human-readable
// labels only, and the backend mints the frozen code symbol and derives the
// storage name (symbol allocation is not the editor's job).
//
// Each op builds its candidate from the head loaded *inside* the project lock
// (withLockedSpec) and commits it in the same transaction, so a concurrent edit
// that committed first is reflected in the candidate rather than silently lost —
// there is no read-modify-write window, and every revision genuinely descends
// from its recorded parent (the append-only lineage of ADR-001 §60).

var (
	// ErrResourceNotFound means the project's current spec has no resource with
	// the given stable ID.
	ErrResourceNotFound = errors.New("project: resource not found")
	// ErrNoSpec means the operation needs an existing spec (rename, delete) but
	// the project has no revisions yet.
	ErrNoSpec = errors.New("project: project has no spec yet")
	// ErrInvalidResourceEdit means the edit's inputs are malformed (e.g. an empty
	// label).
	ErrInvalidResourceEdit = errors.New("project: invalid resource edit")
)

// AddResource creates a resource from a label, minting its code symbol and
// deriving its storage name, then submits the result as a candidate. For a
// project with no revisions it bootstraps an initial spec (postgres + cookie —
// the control plane's locked defaults D4/D5) carrying the new resource.
func (s *Service) AddResource(ctx context.Context, projectID uint, label, labelPlural string, by uint) (CandidateResult, error) {
	if strings.TrimSpace(label) == "" {
		return CandidateResult{}, fmt.Errorf("%w: a resource label is required", ErrInvalidResourceEdit)
	}
	var result CandidateResult
	err := s.withLockedSpec(ctx, projectID, func(tx *gorm.DB, p Project, current *spec.ProjectSpec) error {
		// Build the candidate from the locked head (or a bootstrap for an empty
		// project). Because current is read inside the lock, a concurrent edit that
		// committed first is already reflected here and cannot be lost.
		candidate := bootstrapSpec(p)
		if current != nil {
			c, err := current.Clone()
			if err != nil {
				return err
			}
			candidate = c
		}

		ledger := resourceLedger(candidate)
		resID := spec.MustNewID(spec.KindResource)
		codeName, err := spec.Mint(ledger, spec.NamespaceResource, label, resID, nil)
		if err != nil {
			return fmt.Errorf("mint resource symbol: %w", err)
		}
		candidate.Resources = append(candidate.Resources, &spec.Resource{
			ID:          resID,
			Label:       label,
			LabelPlural: labelPlural,
			CodeName:    codeName,
			StorageName: deriveStorageName(labelPlural, label, candidate),
			Behavior: spec.ResourceBehavior{
				CreateEnabled: true, UpdateEnabled: true, DeleteEnabled: true, AdminVisible: true,
			},
		})

		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}

// RenameResource changes a resource's human-facing labels only (DESIGN.md §4.3).
// The frozen code symbol and storage name are untouched, so the transition is
// ABI-neutral (ADR-001 §55) and commits without a compatibility build.
func (s *Service) RenameResource(ctx context.Context, projectID uint, resourceID spec.ID, label, labelPlural string, by uint) (CandidateResult, error) {
	if strings.TrimSpace(label) == "" {
		return CandidateResult{}, fmt.Errorf("%w: a resource label is required", ErrInvalidResourceEdit)
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
		r.Label = label
		r.LabelPlural = labelPlural
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, by)
		return err
	})
	return result, err
}

// ResourceDeletion is the result of DeleteResource: either the delete committed
// (Committed with the new Revision), or it was Blocked by hard dependencies that
// must be resolved first (ADR-001 §45). HadExtension flags that the deleted
// resource carried lifecycle hooks whose source is archived at build time
// (§46) — surfaced so the editor can warn, though the archival itself is Gombit
// Cloud's, not the control plane's (ADR-005).
type ResourceDeletion struct {
	Committed    bool
	Revision     *Revision
	Blocked      bool
	Blockers     []compiler.DeletionBlocker
	HadExtension bool
	// Diagnostics is set when the resulting candidate was spec-invalid for a
	// reason other than a dependency block.
	Diagnostics spec.Diagnostics
}

// DeleteResource removes a resource after analyzing its dependencies (ADR-001
// §45). If anything in the candidate still references it the delete is blocked
// and nothing is committed; otherwise the candidate is submitted.
func (s *Service) DeleteResource(ctx context.Context, projectID uint, resourceID spec.ID, by uint) (ResourceDeletion, error) {
	var del ResourceDeletion
	err := s.withLockedSpec(ctx, projectID, func(tx *gorm.DB, p Project, current *spec.ProjectSpec) error {
		if current == nil {
			return ErrNoSpec
		}
		target := current.FindResource(resourceID)
		if target == nil {
			return ErrResourceNotFound
		}
		candidate, err := current.Clone()
		if err != nil {
			return err
		}
		candidate.Resources = removeResource(candidate.Resources, resourceID)

		// Deletion-centric dependency analysis before generation (§45): report what
		// still binds the resource rather than a bare dangling-reference diagnostic.
		deletions := compiler.AnalyzeDeletions(current, candidate)
		if blocked := compiler.BlockedDeletions(deletions); len(blocked) > 0 {
			del = ResourceDeletion{Blocked: true, Blockers: blocked[0].Blockers, HadExtension: blocked[0].HadExtension}
			return nil // commit the no-op locking read; nothing appended
		}

		// A deletion is inherently ABI-breaking — the resource's generated contracts
		// vanish — but it does not go through the candidate ABI gate: once
		// dependencies are cleared, §45-46 make the delete an authorized operation
		// whose orphaned extension code is archived at build time (Gombit Cloud),
		// not a candidate that must prove its user code still compiles. So it commits
		// directly, after a validity check that catches a delete which leaves the
		// spec malformed (e.g. removing the project's last resource). Building the
		// candidate from the locked head means the revision genuinely descends from
		// its recorded parent — no forged lineage under a concurrent edit.
		if d := spec.Validate(candidate); d != nil {
			del = ResourceDeletion{Diagnostics: d}
			return nil
		}
		canonical, err := spec.Marshal(candidate)
		if err != nil {
			return fmt.Errorf("canonicalize spec: %w", err)
		}
		hash, err := spec.Hash(candidate)
		if err != nil {
			return fmt.Errorf("hash spec: %w", err)
		}
		rev, err := s.insertRevisionLocked(ctx, tx, p, candidate.SpecVersion, canonical, hash, by)
		if err != nil {
			return err
		}
		del = ResourceDeletion{Committed: true, Revision: &rev, HadExtension: len(target.Hooks) > 0}
		return nil
	})
	return del, err
}

// bootstrapSpec builds a project's initial spec from its row when it has no
// revisions yet: the control plane's locked defaults, postgres (D4) and cookie
// auth (D5), and no resources — the first AddResource fills that in.
func bootstrapSpec(p Project) *spec.ProjectSpec {
	return &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: spec.MustNewID(spec.KindProject), Name: p.Name, Slug: p.Slug},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
	}
}

// resourceLedger reconstructs the resource-symbol ledger from a spec's live
// resource code names, so Mint disambiguates a new symbol against them. It sees
// only live symbols, not tombstones from resources deleted in earlier revisions
// — persisting the full ledger across revisions is ADR-001 §70, not yet built
// here — so it prevents collisions with existing resources but not reuse of a
// long-deleted resource's symbol.
func resourceLedger(s *spec.ProjectSpec) *spec.Ledger {
	l := spec.NewLedger()
	for _, r := range s.Resources {
		if r == nil {
			continue
		}
		_ = l.Record(spec.NamespaceResource, r.CodeName, r.ID)
	}
	return l
}

// deriveStorageName folds a label (plural preferred — it names a table) into a
// valid, collision-free storage identifier (spec.IsStorageName).
func deriveStorageName(labelPlural, label string, s *spec.ProjectSpec) string {
	base := snakeCase(labelPlural)
	if base == "" {
		base = snakeCase(label)
	}
	if base == "" {
		base = "resource"
	}
	taken := map[string]bool{}
	for _, r := range s.Resources {
		if r != nil {
			taken[r.StorageName] = true
		}
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = base + "_" + strconv.Itoa(n)
	}
	return name
}

// snakeCase folds arbitrary text into a lower_snake_case identifier that
// satisfies spec.IsStorageName: lowercase [a-z0-9_], single underscores, no
// leading digit or underscore, no trailing underscore. It returns "" when the
// text has no usable alphanumeric content, so callers can fall back.
func snakeCase(text string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if b.Len() > 0 && !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	// A leading digit is illegal for a storage name; prefix it.
	if out != "" && out[0] >= '0' && out[0] <= '9' {
		out = "r_" + out
	}
	return out
}

func removeResource(resources []*spec.Resource, id spec.ID) []*spec.Resource {
	out := resources[:0:0]
	for _, r := range resources {
		if r == nil || r.ID != id {
			out = append(out, r)
		}
	}
	return out
}
