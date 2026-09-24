package project

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/audit"
	"github.com/gombit-dev/gombit-forge/internal/compiler"
	"github.com/gombit-dev/gombit-forge/internal/compiler/gen"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Sentinel errors the HTTP layer (issue #39) maps to status codes.
var (
	// ErrProjectNotFound means no project has the given id.
	ErrProjectNotFound = errors.New("project: not found")
	// ErrInvalidSpec means the candidate spec failed semantic validation; the
	// wrapped error carries the diagnostics.
	ErrInvalidSpec = errors.New("project: invalid spec")
	// ErrCorruptLineage means a project's HeadRevisionID points at a revision
	// that does not exist — an impossible state, distinct from "no revisions
	// yet". The API layer maps it to a 500, not a 404.
	ErrCorruptLineage = errors.New("project: head revision missing")
)

// Service holds project and revision operations.
//
// now is a seam for a future timestamp-ordering test to replace; today it is
// always the system clock — there is no option or setter yet, so the comment
// promises nothing the package cannot do.
type Service struct {
	db  *gorm.DB
	now func() time.Time
}

// NewService builds a Service over db with the system clock.
func NewService(db *gorm.DB) *Service {
	return &Service{db: db, now: time.Now}
}

// CreateProject creates an empty project in an organization. It has no revisions
// until CreateRevision is called, so HeadRevisionID is nil.
//
// Authorization (the caller's org role) is applied by the API layer (#39); this
// is the data operation.
func (s *Service) CreateProject(ctx context.Context, orgID uint, name, slug string, createdBy uint) (Project, error) {
	p := Project{OrganizationID: orgID, Name: name, Slug: slug, CreatedBy: createdBy}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&p).Error; err != nil {
			return err
		}
		// Record project.created atomically with the row it describes (§23): if
		// the audit write fails the project does not exist either.
		return audit.Record(ctx, tx, audit.Event{
			OrganizationID: &orgID,
			ActorUserID:    &createdBy,
			Action:         audit.ActionProjectCreated,
			TargetType:     "project",
			TargetID:       strconv.FormatUint(uint64(p.ID), 10),
		})
	})
	if err != nil {
		return Project{}, fmt.Errorf("create project: %w", err)
	}
	return p, nil
}

// ListProjects returns an organization's projects in creation order.
func (s *Service) ListProjects(ctx context.Context, orgID uint) ([]Project, error) {
	var projects []Project
	if err := s.db.WithContext(ctx).
		Where("organization_id = ?", orgID).Order("id").Find(&projects).Error; err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	return projects, nil
}

// GetProject returns one project by id, or ErrProjectNotFound.
func (s *Service) GetProject(ctx context.Context, projectID uint) (Project, error) {
	var p Project
	err := s.db.WithContext(ctx).First(&p, projectID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Project{}, ErrProjectNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("load project: %w", err)
	}
	return p, nil
}

// CandidateOutcome is how a submitted candidate spec was resolved.
type CandidateOutcome string

const (
	// OutcomeCommitted means the candidate was accepted and a new revision was
	// recorded.
	OutcomeCommitted CandidateOutcome = "committed"
	// OutcomeInvalidSpec means the candidate failed semantic validation and was
	// not committed; Diagnostics carries why.
	OutcomeInvalidSpec CandidateOutcome = "invalid_spec"
	// OutcomeBreaking means the candidate is an ABI-breaking transition. It cannot
	// commit until user extension code is proven compatible (ADR-001 §41), which
	// is a build the request path must not run (DESIGN.md §26, D8), so it is
	// returned unresolved with its reasons rather than committed here.
	OutcomeBreaking CandidateOutcome = "breaking"
)

// CandidateResult is the outcome of SubmitCandidate.
type CandidateResult struct {
	Outcome  CandidateOutcome
	Revision *Revision // set when committed
	// Diagnostics is set when the spec was invalid.
	Diagnostics spec.Diagnostics
	// Class is the ABI classification of the transition ("neutral", "additive" or
	// "breaking"); empty for a project's first revision, which has no prior ABI.
	Class string
	// Reasons carries the concrete ABI reasons behind an additive or breaking
	// verdict.
	Reasons []string
}

// SubmitCandidate is the candidate-edit → revision flow (DESIGN.md §8, ADR-001
// §37-41). It accepts a candidate spec, validates it and classifies the ABI
// transition against the project's current head, and — when the transition is
// compatible — records it as a new revision. Both steps are pure functions of
// the specs: it never builds, so the control-plane request path stays
// build-free (D8).
//
// Classification runs inside the same transaction that locks the project row and
// (on acceptance) appends the revision, so the candidate is always classified
// against the exact parent it commits onto — a concurrent revision cannot slip
// between a stale classification and the commit.
//
// A breaking transition is not committed: proving user code still compiles is a
// build, which belongs out of band (ADR-005 Cloud; the compiler's
// ValidateCandidate). It is returned as OutcomeBreaking with its reasons so the
// editor can surface the incompatibility and offer the compatibility path.
func (s *Service) SubmitCandidate(ctx context.Context, projectID uint, candidate *spec.ProjectSpec, submittedBy uint) (CandidateResult, error) {
	var result CandidateResult
	err := s.withLockedSpec(ctx, projectID, func(tx *gorm.DB, p Project, current *spec.ProjectSpec) error {
		var err error
		result, err = s.classifyAndInsertLocked(ctx, tx, p, current, candidate, submittedBy)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrProjectNotFound) {
			return CandidateResult{}, err
		}
		return CandidateResult{}, fmt.Errorf("submit candidate: %w", err)
	}
	return result, nil
}

// withLockedSpec runs fn inside a transaction that locks the project row FOR
// UPDATE and loads the project's current head spec — nil when there are no
// revisions yet. Loading the current spec inside the lock is what lets the
// resource operations build a candidate from the true head and commit it without
// a read-modify-write window: no concurrent revision can slip between the read
// and the append (the append-only lineage of ADR-001 §60 depends on this).
func (s *Service) withLockedSpec(ctx context.Context, projectID uint, fn func(tx *gorm.DB, p Project, current *spec.ProjectSpec) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var p Project
		lock := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, projectID)
		if errors.Is(lock.Error, gorm.ErrRecordNotFound) {
			return ErrProjectNotFound
		}
		if lock.Error != nil {
			return lock.Error
		}
		var current *spec.ProjectSpec
		if p.HeadRevisionID != nil {
			var head Revision
			if err := tx.First(&head, *p.HeadRevisionID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf("%w: project %d head %d", ErrCorruptLineage, projectID, *p.HeadRevisionID)
				}
				return err
			}
			s2, err := spec.Unmarshal([]byte(head.SpecJSON))
			if err != nil {
				return fmt.Errorf("decode head spec: %w", err)
			}
			current = s2
		}
		return fn(tx, p, current)
	})
}

// classifyAndInsertLocked validates the candidate, classifies it against the
// already-loaded current spec, and appends a revision for a neutral or additive
// transition — all on the caller's locked transaction. A breaking transition is
// classified but not committed (§41); an invalid spec is reported, not inserted.
// current is nil for a project's first revision.
func (s *Service) classifyAndInsertLocked(ctx context.Context, tx *gorm.DB, p Project, current, candidate *spec.ProjectSpec, by uint) (CandidateResult, error) {
	if d := spec.Validate(candidate); d != nil {
		return CandidateResult{Outcome: OutcomeInvalidSpec, Diagnostics: d}, nil
	}
	canonical, err := spec.Marshal(candidate)
	if err != nil {
		return CandidateResult{}, fmt.Errorf("canonicalize spec: %w", err)
	}
	hash, err := spec.Hash(candidate)
	if err != nil {
		return CandidateResult{}, fmt.Errorf("hash spec: %w", err)
	}

	var class string
	var reasons []string
	if current != nil {
		transition, err := compiler.ClassifyEdit(current, candidate)
		if err != nil {
			return CandidateResult{}, fmt.Errorf("classify candidate: %w", err)
		}
		class = transition.Class.String()
		reasons = transition.Reasons
		if transition.Class == gen.ClassBreaking {
			return CandidateResult{Outcome: OutcomeBreaking, Class: class, Reasons: reasons}, nil
		}
	}
	rev, err := s.insertRevisionLocked(ctx, tx, p, candidate.SpecVersion, canonical, hash, by)
	if err != nil {
		return CandidateResult{}, err
	}
	return CandidateResult{Outcome: OutcomeCommitted, Revision: &rev, Class: class, Reasons: reasons}, nil
}

// CreateRevision records an accepted candidate spec as a new immutable revision
// of the project and advances the project's head to it.
//
// The spec is validated (an invalid candidate never becomes a revision),
// canonicalized and hashed through the compiler's internal/spec, so the stored
// bytes and hash are the compiler's own. Lineage is set by pointing the new
// revision's parent at the project's current head, then moving head forward —
// all within one transaction that locks the project row, so concurrent
// revisions of the same project serialize into a linear chain rather than
// forking off a shared parent.
func (s *Service) CreateRevision(ctx context.Context, projectID uint, sp *spec.ProjectSpec, createdBy uint) (Revision, error) {
	if d := spec.Validate(sp); d != nil {
		return Revision{}, fmt.Errorf("%w: %s", ErrInvalidSpec, d.Error())
	}
	canonical, err := spec.Marshal(sp)
	if err != nil {
		return Revision{}, fmt.Errorf("canonicalize spec: %w", err)
	}
	hash, err := spec.Hash(sp)
	if err != nil {
		return Revision{}, fmt.Errorf("hash spec: %w", err)
	}

	var rev Revision
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock the project row so two concurrent revisions cannot both read the
		// same head and fork the chain.
		var p Project
		lock := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, projectID)
		if errors.Is(lock.Error, gorm.ErrRecordNotFound) {
			return ErrProjectNotFound
		}
		if lock.Error != nil {
			return lock.Error
		}
		var insertErr error
		rev, insertErr = s.insertRevisionLocked(ctx, tx, p, sp.SpecVersion, canonical, hash, createdBy)
		return insertErr
	})
	if err != nil {
		if errors.Is(err, ErrProjectNotFound) {
			return Revision{}, err
		}
		return Revision{}, fmt.Errorf("create revision: %w", err)
	}
	return rev, nil
}

// insertRevisionLocked appends a revision to a project whose row is already
// locked FOR UPDATE by the caller's transaction, advances the head to it, and
// records the audit event — all on tx. Both CreateRevision and SubmitCandidate
// share it so the append-and-advance is written once; the lock is the caller's
// responsibility, and the shared lock is what serializes concurrent revisions
// into a linear chain rather than a fork.
func (s *Service) insertRevisionLocked(ctx context.Context, tx *gorm.DB, p Project, specVersion int, canonical []byte, hash string, createdBy uint) (Revision, error) {
	rev := Revision{
		ProjectID:        p.ID,
		SpecVersion:      specVersion,
		SpecJSON:         string(canonical),
		SpecHash:         hash,
		ParentRevisionID: p.HeadRevisionID, // nil for the first revision
		CreatedBy:        createdBy,
		CreatedAt:        s.now(),
	}
	if err := tx.Create(&rev).Error; err != nil {
		return Revision{}, err
	}
	// Advance head. Update the column explicitly rather than saving the struct so
	// nothing else on the project row is touched.
	if err := tx.Model(&Project{}).Where("id = ?", p.ID).
		Update("head_revision_id", rev.ID).Error; err != nil {
		return Revision{}, err
	}
	// spec.revision.created, in the same transaction as the revision and the head
	// move — the org comes from the locked project row.
	if err := audit.Record(ctx, tx, audit.Event{
		OrganizationID: &p.OrganizationID,
		ActorUserID:    &createdBy,
		Action:         audit.ActionSpecRevised,
		TargetType:     "revision",
		TargetID:       strconv.FormatUint(uint64(rev.ID), 10),
	}); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

// Head returns a project's current revision. It returns ErrProjectNotFound if
// the project does not exist, (Revision{}, false, nil) if the project exists but
// has no revisions yet, and ErrCorruptLineage if the project's head points at a
// revision that is gone.
func (s *Service) Head(ctx context.Context, projectID uint) (Revision, bool, error) {
	var p Project
	err := s.db.WithContext(ctx).First(&p, projectID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Revision{}, false, ErrProjectNotFound
	}
	if err != nil {
		return Revision{}, false, fmt.Errorf("load project: %w", err)
	}
	if p.HeadRevisionID == nil {
		return Revision{}, false, nil
	}
	rev, ok, err := s.Revision(ctx, *p.HeadRevisionID)
	if err != nil {
		return Revision{}, false, err
	}
	if !ok {
		// The project claims a head, but that revision is gone: the chain is
		// corrupt. Surface it loudly rather than disguising it as a fresh
		// project with no revisions.
		return Revision{}, false, fmt.Errorf("%w: project %d head %d",
			ErrCorruptLineage, projectID, *p.HeadRevisionID)
	}
	return rev, true, nil
}

// Revision returns a single revision by id.
func (s *Service) Revision(ctx context.Context, id uint) (Revision, bool, error) {
	var rev Revision
	err := s.db.WithContext(ctx).First(&rev, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Revision{}, false, nil
	}
	if err != nil {
		return Revision{}, false, fmt.Errorf("load revision: %w", err)
	}
	return rev, true, nil
}
