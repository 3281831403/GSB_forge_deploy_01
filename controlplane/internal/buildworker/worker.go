// Package buildworker drives the asynchronous Cloud-build queue (#103): it
// claims queued build jobs, assembles the frozen revision's source, submits it
// to Gombit Cloud, and reflects the Cloud build's state onto the job until it
// settles. It is the out-of-band half of Deploy — the HTTP layer only enqueues —
// so no request performs the toolchain-heavy source assembly (D8), and Cloud,
// not Forge, executes the build (ADR-005 D2).
//
// The worker authenticates to Cloud as Forge's own service principal (the
// injected Cloud client carries that token); it never forwards the initiating
// user's identity, so Cloud attributes the actor to the service, never an
// impersonated human. It depends only on narrow interfaces (job store, revision
// resolver, source assembler, Cloud client), so claim→submit→track→mark is
// unit-tested with fakes and no database, real toolchain, or real Cloud.
package buildworker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/cloudbuild"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/cloudclient"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Jobs is the subset of cloudbuild.Service the worker drives.
type Jobs interface {
	Claim(ctx context.Context) (cloudbuild.BuildJob, bool, error)
	SetCloudBuild(ctx context.Context, jobID uint, cloudBuildID string) error
	ReflectCloudStatus(ctx context.Context, jobID uint, cloudStatus string) error
	MarkSucceeded(ctx context.Context, jobID uint, cloudStatus, artifactDigest string) error
	MarkFailed(ctx context.Context, jobID uint, cloudStatus, sanitized string) error
}

// Revisions resolves a frozen revision's spec (and an opaque provenance ref) by
// id, so the worker builds the exact revision the job froze at enqueue.
// projectspec.Source satisfies it.
type Revisions interface {
	RevisionSpec(ctx context.Context, revisionID uint) (*spec.ProjectSpec, string, error)
}

// Assembler turns a resolved spec into a source `.tar.gz` written to w — the
// Cloud build's input (compiler.BuildApplicationSource + compiler.WriteTarGz).
type Assembler interface {
	AssembleTarGz(ctx context.Context, sp *spec.ProjectSpec, cloudProjectID, revisionRef string, w io.Writer) error
}

// Cloud is the Gombit Cloud build API — the injected client abstraction.
// *cloudclient.Client satisfies it; a test injects a fake. The client carries
// Forge's service-principal token.
type Cloud interface {
	CreateBuild(ctx context.Context, cloudProjectID, sourceCommit string) (cloudclient.Build, error)
	UploadSource(ctx context.Context, cloudProjectID, buildID string, archive io.Reader) error
	GetBuild(ctx context.Context, cloudProjectID, buildID string) (cloudclient.Build, error)
}

// Stored failure reasons. Deliberately generic categories, not the raw error: a
// toolchain or transport error can carry filesystem paths or token material, and
// the job's Error is user-visible via the polling route. The detailed error is
// logged, never stored.
const (
	failLoadRevision = "could not load the revision to build"
	failAssemble     = "source assembly failed"
	failSubmit       = "submitting the build to Gombit Cloud failed"
	failTrack        = "tracking the Cloud build failed"
	failCloudBuild   = "the Gombit Cloud build did not succeed"
)

// cloudTerminal reports whether a Cloud build status is terminal (Cloud's §22
// build state machine). "succeeded" is the only success; the rest are failures.
//
// This set is a cross-service coupling: it must track Cloud's terminal statuses.
// A Cloud-side terminal status not listed here reads as non-terminal and is only
// caught by track's trackTimeout — i.e. it surfaces as a 20-minute timeout rather
// than an obvious error — so keep this in sync with gombit-cloud's build states.
func cloudTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled", "timed_out":
		return true
	default:
		return false
	}
}

// Worker claims and processes Cloud-build jobs.
type Worker struct {
	jobs         Jobs
	revs         Revisions
	asm          Assembler
	cloud        Cloud
	poll         time.Duration // idle-queue poll interval
	trackPoll    time.Duration // Cloud-build status poll interval
	trackTimeout time.Duration // give-up budget for a single build
	log          *slog.Logger
}

// Options configure a Worker's timing. Zero values fall back to sane defaults.
type Options struct {
	Poll         time.Duration
	TrackPoll    time.Duration
	TrackTimeout time.Duration
}

// New builds a worker. A zero Poll defaults to 5s, TrackPoll to 3s, TrackTimeout
// to 20m; a nil logger uses slog.Default.
func New(jobs Jobs, revs Revisions, asm Assembler, cloud Cloud, opts Options, log *slog.Logger) *Worker {
	if opts.Poll <= 0 {
		opts.Poll = 5 * time.Second
	}
	if opts.TrackPoll <= 0 {
		opts.TrackPoll = 3 * time.Second
	}
	if opts.TrackTimeout <= 0 {
		opts.TrackTimeout = 20 * time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	return &Worker{jobs: jobs, revs: revs, asm: asm, cloud: cloud, poll: opts.Poll, trackPoll: opts.TrackPoll, trackTimeout: opts.TrackTimeout, log: log}
}

// RunOnce claims at most one job and processes it, reporting whether it did any
// work. A claim error is returned (Run logs and backs off); a job that fails to
// build is recorded as failed and counts as work done, not an error.
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

func (w *Worker) process(ctx context.Context, job cloudbuild.BuildJob) {
	sp, ref, err := w.revs.RevisionSpec(ctx, job.RevisionID)
	if err != nil {
		w.fail(ctx, job, "", failLoadRevision, "load revision", err)
		return
	}

	// Assemble the frozen revision's source into an in-memory archive. A project
	// source tree is small (BuildApplicationSource already holds it in memory), and
	// buffering keeps assembly and submission as distinct, cleanly-attributed steps.
	var archive bytes.Buffer
	if err := w.asm.AssembleTarGz(ctx, sp, job.CloudProjectID, ref, &archive); err != nil {
		w.fail(ctx, job, "", failAssemble, "assemble source", err)
		return
	}

	// Create the Cloud build, then upload the source to start it. source_commit is
	// the revision's provenance ref (opaque to Cloud).
	build, err := w.cloud.CreateBuild(ctx, job.CloudProjectID, ref)
	if err != nil {
		w.fail(ctx, job, "", failSubmit, "create build", err)
		return
	}
	// Record the Cloud build id before uploading, so a crash mid-upload still
	// leaves a pointer to the build. Best-effort — a running job accepts it.
	if err := w.jobs.SetCloudBuild(ctx, job.ID, build.ID); err != nil {
		w.log.Error("buildworker: recording the cloud build id failed", "job", job.ID, "cloud_build", build.ID, "error", err)
	}
	if err := w.cloud.UploadSource(ctx, job.CloudProjectID, build.ID, &archive); err != nil {
		w.fail(ctx, job, build.Status, failSubmit, "upload source", err)
		return
	}

	// Poll the Cloud build to a terminal state, reflecting each status onto the
	// job so the control plane mirrors Cloud's transitions.
	final, err := w.track(ctx, job, build.ID)
	if err != nil {
		w.fail(ctx, job, final, failTrack, "track build", err)
		return
	}
	if final == "succeeded" {
		digest := ""
		if b, err := w.cloud.GetBuild(ctx, job.CloudProjectID, build.ID); err == nil {
			digest = b.ArtifactDigest
		} else {
			// The build succeeded, so recording success is still right — but log why
			// the digest is empty rather than leave it a silent gap.
			w.log.Error("buildworker: build succeeded but fetching its artifact digest failed",
				"job", job.ID, "cloud_build", build.ID, "error", err)
		}
		w.succeed(ctx, job, final, digest)
		return
	}
	w.fail(ctx, job, final, failCloudBuild, "cloud build", fmt.Errorf("cloud build ended in %q", final))
}

// track polls the Cloud build until it reaches a terminal state (or ctx/timeout),
// reflecting each observed status onto the job. It returns the last status seen.
func (w *Worker) track(ctx context.Context, job cloudbuild.BuildJob, cloudBuildID string) (string, error) {
	deadline := time.Now().Add(w.trackTimeout)
	ticker := time.NewTicker(w.trackPoll)
	defer ticker.Stop()

	last := ""
	for {
		b, err := w.cloud.GetBuild(ctx, job.CloudProjectID, cloudBuildID)
		if err != nil {
			return last, err
		}
		if b.Status != last {
			last = b.Status
			if err := w.jobs.ReflectCloudStatus(ctx, job.ID, b.Status); err != nil {
				w.log.Error("buildworker: reflecting cloud status failed", "job", job.ID, "status", b.Status, "error", err)
			}
		}
		if cloudTerminal(b.Status) {
			return b.Status, nil
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("cloud build did not settle within %s", w.trackTimeout)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-ticker.C:
		}
	}
}

// succeed records a successful build. The write is detached from the job's ctx
// (recordCtx) so a shutdown mid-track still lands a terminal state a poller sees.
func (w *Worker) succeed(ctx context.Context, job cloudbuild.BuildJob, cloudStatus, artifactDigest string) {
	rc, cancel := w.recordCtx(ctx)
	defer cancel()
	if err := w.jobs.MarkSucceeded(rc, job.ID, cloudStatus, artifactDigest); err != nil {
		w.log.Error("buildworker: build succeeded but recording it failed", "job", job.ID, "error", err)
	}
}

// fail logs the detailed error and records the sanitized category (plus the last
// Cloud status, if any) on the job.
func (w *Worker) fail(ctx context.Context, job cloudbuild.BuildJob, cloudStatus, stored, stage string, err error) {
	w.log.Error("buildworker: build job failed", "job", job.ID, "stage", stage, "error", err)
	rc, cancel := w.recordCtx(ctx)
	defer cancel()
	if e := w.jobs.MarkFailed(rc, job.ID, cloudStatus, stored); e != nil {
		w.log.Error("buildworker: recording a failed job failed", "job", job.ID, "error", e)
	}
}

// recordCtx detaches the terminal-outcome write from the job's context: if the
// worker is shutting down and ctx is cancelled mid-build, the build aborts on
// that cancellation but the Mark* write must still land, or the job is stuck
// running with no terminal state a poller can see.
func (w *Worker) recordCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
}

// Run drains and then polls the queue until ctx is cancelled. On each tick it
// claims jobs until the queue is empty, so a backlog is worked without waiting a
// full poll interval between jobs.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		for {
			did, err := w.RunOnce(ctx)
			if err != nil {
				w.log.Error("buildworker: claiming a job failed", "error", err)
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
