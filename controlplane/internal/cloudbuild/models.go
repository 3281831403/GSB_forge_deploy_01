// Package cloudbuild is the asynchronous "build this project revision on Gombit
// Cloud" queue — the Forge side of M4 (#103, ADR-005 §4.4). A Deploy enqueues a
// persisted job that freezes the revision; a worker assembles the revision's
// source and submits it to Cloud, which owns build execution (ADR-005 D2). No
// HTTP request performs a build (D8).
//
// It mirrors exportjob's shape — one narrow table and its lifecycle — not a
// generic build system; the build itself is Cloud's. A job records the Cloud
// build it created (CloudBuildID) and the last Cloud state it observed
// (CloudStatus), so the control plane reflects Cloud's build without owning it.
package cloudbuild

import "time"

// Status is the job's local lifecycle in the Forge queue — distinct from the
// Cloud build's own state machine (CloudStatus), which the worker reflects.
type Status string

const (
	// StatusQueued is a freshly enqueued job awaiting a worker.
	StatusQueued Status = "queued"
	// StatusRunning is a job a worker has claimed: assembling source and driving
	// the Cloud build to a terminal state.
	StatusRunning Status = "running"
	// StatusSucceeded is a job whose Cloud build reached a successful terminal
	// state.
	StatusSucceeded Status = "succeeded"
	// StatusFailed is a job that failed to assemble/submit, or whose Cloud build
	// failed; Error holds a sanitized reason.
	StatusFailed Status = "failed"
)

// BuildJob is one "build this revision on Cloud" request. It freezes the exact
// revision at enqueue (RevisionID) so a later head move doesn't change what is
// built, and records the Cloud build it drives.
//
// ProjectID/RevisionID/UserID are plain indexed columns, not foreign keys — a
// job is an audit-style record of an action (mirroring exportjob and the
// FK-free connection tables); a revision or project deleted under a queued job
// surfaces as a worker-run failure, not a database constraint.
//
// UserID is the initiator — the human/org/project that *requested* the deploy.
// It is Forge-local provenance and is deliberately NOT forwarded to Cloud: the
// worker authenticates to Cloud as Forge's own service principal
// (service:forge-build-worker), so Cloud attributes the actor to that service,
// never to an impersonated human (respecting Cloud's human-only L10 boundary).
type BuildJob struct {
	ID         uint `gorm:"primaryKey"`
	ProjectID  uint `gorm:"not null;index"`
	RevisionID uint `gorm:"not null"`
	UserID     uint `gorm:"not null;index"`
	// CloudProjectID is the Cloud project the build targets — Project.CloudProjectID
	// captured at enqueue (#38). Empty until a project is linked to Cloud.
	CloudProjectID string `gorm:"size:120;not null"`
	// CloudBuildID is the id Cloud returned for the created build, set by the
	// worker once it calls Cloud's create-build. Empty until then.
	CloudBuildID string `gorm:"size:120"`
	// CloudStatus is the last build state the worker observed from Cloud (Cloud's
	// own vocabulary: queued/preparing/building/…/succeeded/failed). It is how the
	// control plane reflects Cloud's transitions (§103) without owning them.
	CloudStatus string `gorm:"size:30"`
	// ArtifactDigest is the content-addressed digest Cloud recorded on success.
	ArtifactDigest string `gorm:"size:120"`
	Status         Status `gorm:"size:20;not null;index"`
	// Error is a sanitized failure reason, set on failure — never a raw toolchain
	// or transport error, which can leak paths or token material.
	Error     string `gorm:"type:text"`
	CreatedAt time.Time
	UpdatedAt time.Time
}
