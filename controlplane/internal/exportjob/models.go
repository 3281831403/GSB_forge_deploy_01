// Package exportjob is the asynchronous "export a project revision to a GitHub
// repository" queue (#85). A build is too toolchain-heavy and externally
// side-effectful to run in an HTTP request, so the POST enqueues a persisted
// job and a worker runs it out of band (freeze at enqueue, poll for status).
//
// It is deliberately narrow — one export-specific table and its lifecycle, not
// a generic build queue, artifact model, or Cloud-like worker. The runtime
// build/deploy primitives are Gombit Cloud's (ADR-005), not the Forge control
// plane's.
package exportjob

import "time"

// Status is an export job's lifecycle state.
type Status string

const (
	// StatusQueued is a freshly enqueued job awaiting a worker.
	StatusQueued Status = "queued"
	// StatusRunning is a job a worker has claimed and is executing.
	StatusRunning Status = "running"
	// StatusSucceeded is a job whose source was pushed to a new repository.
	StatusSucceeded Status = "succeeded"
	// StatusFailed is a job that errored; Error holds a sanitized reason.
	StatusFailed Status = "failed"
)

// ExportJob is one "export this revision to a GitHub repo" request. It freezes
// the exact revision at enqueue time (RevisionID), so a later head move does not
// change what gets exported, and records the terminal outcome (RepoURL on
// success, a sanitized Error on failure).
//
// ProjectID/RevisionID/UserID are plain indexed columns, not foreign keys: a
// job is an audit-style record of an action, and a revision or project deleted
// out from under a queued job surfaces as a run-time failure the worker records,
// not a database constraint — mirroring the FK-free connection tables.
type ExportJob struct {
	ID         uint `gorm:"primaryKey"`
	ProjectID  uint `gorm:"not null;index"`
	RevisionID uint `gorm:"not null"`
	// UserID is the initiator, whose stored GitHub token the worker uses.
	UserID   uint   `gorm:"not null;index"`
	RepoName string `gorm:"size:100;not null"`
	Private  bool   `gorm:"not null"`
	Status   Status `gorm:"size:20;not null;index"`
	// RepoURL is the created repository's web URL, set on success.
	RepoURL string `gorm:"size:255"`
	// Error is a sanitized failure reason, set on failure — never a raw toolchain
	// error, which can leak paths or tokens.
	Error     string `gorm:"type:text"`
	CreatedAt time.Time
	UpdatedAt time.Time
}
