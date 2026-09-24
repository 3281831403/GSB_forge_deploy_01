package cloudbuild

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNoCloudProject is returned by Enqueue when the project has no Cloud project
// linked (Project.CloudProjectID is empty), so there is nowhere to build. It is a
// caller (precondition) error distinct from a storage failure, so the HTTP layer
// can map it to 409/422 rather than 500.
var ErrNoCloudProject = errors.New("cloudbuild: project is not linked to a Gombit Cloud project")

// Service is the cloud-build-job store: enqueue, claim (for the worker), record
// the Cloud build it drives, reflect Cloud's state, and report terminal outcomes.
type Service struct {
	db *gorm.DB
}

// NewService builds the service over db.
func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

// Enqueue creates a queued job that freezes revisionID for projectID, initiated
// by userID, targeting the Cloud project cloudProjectID. The caller (the HTTP
// handler) resolves and authorizes the project and its head revision first, so
// the job is pinned to what the caller saw, not to whatever head becomes when a
// worker runs it. A blank cloudProjectID is ErrNoCloudProject.
func (s *Service) Enqueue(ctx context.Context, projectID, revisionID, userID uint, cloudProjectID string) (BuildJob, error) {
	if strings.TrimSpace(cloudProjectID) == "" {
		return BuildJob{}, ErrNoCloudProject
	}
	job := BuildJob{
		ProjectID:      projectID,
		RevisionID:     revisionID,
		UserID:         userID,
		CloudProjectID: cloudProjectID,
		Status:         StatusQueued,
	}
	if err := s.db.WithContext(ctx).Create(&job).Error; err != nil {
		return BuildJob{}, err
	}
	return job, nil
}

// Get returns the job by id, or ok=false if there is none.
func (s *Service) Get(ctx context.Context, jobID uint) (BuildJob, bool, error) {
	var job BuildJob
	err := s.db.WithContext(ctx).First(&job, jobID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return BuildJob{}, false, nil
	}
	if err != nil {
		return BuildJob{}, false, err
	}
	return job, true, nil
}

// ListForProject returns a project's build jobs, newest first — the deploy
// history surfaced against the project (§103).
func (s *Service) ListForProject(ctx context.Context, projectID uint) ([]BuildJob, error) {
	var jobs []BuildJob
	err := s.db.WithContext(ctx).Where("project_id = ?", projectID).Order("id DESC").Find(&jobs).Error
	return jobs, err
}

// Claim atomically takes the oldest queued job and marks it running, returning
// ok=false when the queue is empty. It locks the row FOR UPDATE SKIP LOCKED so
// concurrent workers never claim the same job and never block on one another.
func (s *Service) Claim(ctx context.Context) (BuildJob, bool, error) {
	var job BuildJob
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ?", StatusQueued).
			Order("id").
			First(&job)
		if res.Error != nil {
			return res.Error
		}
		job.Status = StatusRunning
		return tx.Model(&job).Update("status", StatusRunning).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return BuildJob{}, false, nil
	}
	if err != nil {
		return BuildJob{}, false, err
	}
	return job, true, nil
}

// SetCloudBuild records the Cloud build id a worker created for a running job, so
// a crash after create-build can still find the build (and the reflected state
// has something to point at). Valid only on a running job.
func (s *Service) SetCloudBuild(ctx context.Context, jobID uint, cloudBuildID string) error {
	return s.updateRunning(ctx, jobID, map[string]any{"cloud_build_id": cloudBuildID})
}

// ReflectCloudStatus records the latest Cloud build state the worker observed on
// a running job — how the control plane mirrors Cloud's transitions without
// owning them. Valid only on a running job.
func (s *Service) ReflectCloudStatus(ctx context.Context, jobID uint, cloudStatus string) error {
	return s.updateRunning(ctx, jobID, map[string]any{"cloud_status": cloudStatus})
}

// MarkSucceeded records a job whose Cloud build reached a successful terminal
// state, stamping the final Cloud status and artifact digest. Valid only on a
// running job (see finish).
func (s *Service) MarkSucceeded(ctx context.Context, jobID uint, cloudStatus, artifactDigest string) error {
	return s.finish(ctx, jobID, StatusSucceeded, map[string]any{
		"status":          StatusSucceeded,
		"cloud_status":    cloudStatus,
		"artifact_digest": artifactDigest,
	})
}

// MarkFailed records a failed job with a sanitized reason (and the Cloud status
// if the failure was a Cloud build outcome). Valid only on a running job. The
// caller sanitizes — a raw toolchain/transport error can leak paths or tokens.
func (s *Service) MarkFailed(ctx context.Context, jobID uint, cloudStatus, sanitized string) error {
	return s.finish(ctx, jobID, StatusFailed, map[string]any{
		"status":       StatusFailed,
		"cloud_status": cloudStatus,
		"error":        sanitized,
	})
}

// updateRunning applies a non-terminal field update that matches only a running
// job, so a stray update to a queued or already-terminal job is a no-op error
// rather than a silent clobber.
func (s *Service) updateRunning(ctx context.Context, jobID uint, fields map[string]any) error {
	res := s.db.WithContext(ctx).Model(&BuildJob{}).
		Where("id = ? AND status = ?", jobID, StatusRunning).
		Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("cloudbuild: cannot update job %d: not running (missing or already terminal)", jobID)
	}
	return nil
}

// finish applies a terminal update, enforcing the running → terminal transition:
// the update matches only a row still running, so a never-claimed job, an
// already-terminal job re-marked, or a missing row all fall through as
// RowsAffected == 0 and error rather than clobbering a recorded outcome — what
// keeps a late or duplicate worker from overwriting a job's result.
func (s *Service) finish(ctx context.Context, jobID uint, status Status, fields map[string]any) error {
	res := s.db.WithContext(ctx).Model(&BuildJob{}).
		Where("id = ? AND status = ?", jobID, StatusRunning).
		Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("cloudbuild: cannot mark job %d as %s: not running (missing or already terminal)", jobID, status)
	}
	return nil
}
