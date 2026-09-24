// Package exportworker drives the asynchronous GitHub export queue (#85): it
// claims queued export jobs and runs each through the existing ghexport stack,
// recording a terminal outcome. It is the out-of-band half of the export flow —
// the HTTP layer only enqueues — so no request performs the toolchain-heavy
// source assembly a build entails.
//
// The worker depends only on narrow interfaces (the job store, a revision-spec
// resolver, and the exporter), so its claim→run→mark logic is unit-tested with
// fakes and no database or real GitHub.
package exportworker

import (
	"context"
	"log/slog"
	"time"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/exportjob"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/ghexport"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Jobs is the subset of exportjob.Service the worker drives.
type Jobs interface {
	Claim(ctx context.Context) (exportjob.ExportJob, bool, error)
	MarkSucceeded(ctx context.Context, jobID uint, repoURL string) error
	MarkFailed(ctx context.Context, jobID uint, sanitized string) error
}

// Revisions resolves a frozen revision's spec (and an opaque provenance ref) by
// id, so the worker exports the exact revision the job froze at enqueue.
type Revisions interface {
	RevisionSpec(ctx context.Context, revisionID uint) (*spec.ProjectSpec, string, error)
}

// Exporter assembles and pushes an already-resolved spec to a new repository —
// ghexport.Service.ExportSpec.
type Exporter interface {
	ExportSpec(ctx context.Context, userID uint, repoName string, private bool, projectSpec *spec.ProjectSpec, revisionRef string) (ghexport.Result, error)
}

// Stored failure reasons. These are deliberately generic categories, not the
// raw error: a toolchain or GitHub error can carry filesystem paths or token
// material, and the job's Error is user-visible via the polling route. The
// detailed error is logged, never stored.
const (
	failLoadRevision = "could not load the revision to export"
	failExport       = "source assembly or push to GitHub failed"
)

// Worker claims and processes export jobs.
type Worker struct {
	jobs Jobs
	revs Revisions
	exp  Exporter
	poll time.Duration
	log  *slog.Logger
}

// New builds a worker. poll is how often Run checks an empty queue; a non-empty
// queue is drained without waiting. A zero poll defaults to 5s; a nil logger
// uses slog.Default.
func New(jobs Jobs, revs Revisions, exp Exporter, poll time.Duration, log *slog.Logger) *Worker {
	if poll <= 0 {
		poll = 5 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Worker{jobs: jobs, revs: revs, exp: exp, poll: poll, log: log}
}

// RunOnce claims at most one job and processes it, reporting whether it did any
// work. A claim error is returned (Run logs and backs off on it); a job that
// fails to export is not an error here — it is recorded as a failed job and
// counts as work done.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	job, ok, err := w.jobs.Claim(ctx)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	w.process(ctx, job)
	return true, nil
}

func (w *Worker) process(ctx context.Context, job exportjob.ExportJob) {
	sp, ref, err := w.revs.RevisionSpec(ctx, job.RevisionID)
	if err != nil {
		w.fail(ctx, job, failLoadRevision, "load revision", err)
		return
	}
	res, err := w.exp.ExportSpec(ctx, job.UserID, job.RepoName, job.Private, sp, ref)
	if err != nil {
		w.fail(ctx, job, failExport, "export", err)
		return
	}
	rc, cancel := w.recordCtx(ctx)
	defer cancel()
	if err := w.jobs.MarkSucceeded(rc, job.ID, res.RepoURL); err != nil {
		w.log.Error("exportworker: export succeeded but recording it failed",
			"job", job.ID, "repo", res.RepoURL, "error", err)
	}
}

// fail logs the detailed error and records the sanitized category on the job.
func (w *Worker) fail(ctx context.Context, job exportjob.ExportJob, stored, stage string, err error) {
	w.log.Error("exportworker: export job failed", "job", job.ID, "stage", stage, "error", err)
	rc, cancel := w.recordCtx(ctx)
	defer cancel()
	if e := w.jobs.MarkFailed(rc, job.ID, stored); e != nil {
		w.log.Error("exportworker: recording a failed job failed", "job", job.ID, "error", e)
	}
}

// recordCtx detaches the terminal-outcome write from the job's context. If the
// worker is shutting down and ctx is cancelled mid-job, the export aborts on
// that cancellation — but the Mark* write must still land, or the job is stuck
// running with no terminal state a poller can see. Same durability fix as the
// ghexport rollback: drop the cancellation, keep a short budget of our own.
func (w *Worker) recordCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
}

// Run drains and then polls the queue until ctx is cancelled. On each tick it
// claims jobs until the queue is empty, so a backlog is worked through without
// waiting a full poll interval between jobs.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		for {
			did, err := w.RunOnce(ctx)
			if err != nil {
				w.log.Error("exportworker: claiming a job failed", "error", err)
				break
			}
			if !did {
				break
			}
			if ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
