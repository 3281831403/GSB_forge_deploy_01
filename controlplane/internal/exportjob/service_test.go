package exportjob_test

// Service tests against the migrated schema (dbtest.DB applies the committed
// export_jobs migration). Docker-gated like the other control-plane DB tests.

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm/clause"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/exportjob"
)

func TestEnqueueCreatesQueuedJob(t *testing.T) {
	db := dbtest.DB(t)
	svc := exportjob.NewService(db)

	job, err := svc.Enqueue(context.Background(), 3, 7, 11, "my-app", true)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if job.ID == 0 || job.Status != exportjob.StatusQueued {
		t.Fatalf("job = %+v, want a persisted queued job", job)
	}
	if job.ProjectID != 3 || job.RevisionID != 7 || job.UserID != 11 || job.RepoName != "my-app" || !job.Private {
		t.Errorf("job fields not frozen as given: %+v", job)
	}

	got, ok, err := svc.Get(context.Background(), job.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.RevisionID != 7 {
		t.Errorf("frozen revision = %d, want 7", got.RevisionID)
	}
}

func TestEnqueueRejectsEmptyRepoName(t *testing.T) {
	db := dbtest.DB(t)
	svc := exportjob.NewService(db)
	_, err := svc.Enqueue(context.Background(), 3, 7, 11, "   ", false)
	if !errors.Is(err, exportjob.ErrBlankRepoName) {
		t.Errorf("blank repo name must return ErrBlankRepoName; got %v", err)
	}
}

func TestGetMissingJob(t *testing.T) {
	db := dbtest.DB(t)
	svc := exportjob.NewService(db)
	if _, ok, err := svc.Get(context.Background(), 999999); err != nil || ok {
		t.Errorf("missing job: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

func TestClaimTakesOldestQueuedAndMarksRunning(t *testing.T) {
	db := dbtest.DB(t)
	svc := exportjob.NewService(db)
	ctx := context.Background()

	first, _ := svc.Enqueue(ctx, 1, 1, 1, "first", false)
	second, _ := svc.Enqueue(ctx, 1, 2, 1, "second", false)

	claimed, ok, err := svc.Claim(ctx)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if claimed.ID != first.ID {
		t.Errorf("claimed job %d, want the oldest %d", claimed.ID, first.ID)
	}
	if claimed.Status != exportjob.StatusRunning {
		t.Errorf("claimed status = %q, want running", claimed.Status)
	}
	// The persisted row is running, so a re-claim can't take it again.
	got, _, _ := svc.Get(ctx, first.ID)
	if got.Status != exportjob.StatusRunning {
		t.Errorf("persisted status = %q, want running", got.Status)
	}

	// Next claim takes the second job; a third finds an empty queue.
	claimed2, ok, _ := svc.Claim(ctx)
	if !ok || claimed2.ID != second.ID {
		t.Errorf("second claim = (%d, ok=%v), want %d", claimed2.ID, ok, second.ID)
	}
	if _, ok, err := svc.Claim(ctx); ok || err != nil {
		t.Errorf("empty-queue claim: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// TestClaimSkipsLockedRow proves the FOR UPDATE SKIP LOCKED semantics: with the
// oldest queued row locked by another transaction, Claim skips it and takes the
// next queued job rather than blocking or double-claiming.
func TestClaimSkipsLockedRow(t *testing.T) {
	db := dbtest.DB(t)
	svc := exportjob.NewService(db)
	ctx := context.Background()

	first, _ := svc.Enqueue(ctx, 1, 1, 1, "first", false)
	second, _ := svc.Enqueue(ctx, 1, 2, 1, "second", false)

	// Hold a lock on the first row in an open transaction on a separate session.
	tx := db.Begin()
	defer tx.Rollback()
	var locked exportjob.ExportJob
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, first.ID).Error; err != nil {
		t.Fatalf("lock first row: %v", err)
	}

	claimed, ok, err := svc.Claim(ctx)
	if err != nil || !ok {
		t.Fatalf("claim past locked row: ok=%v err=%v", ok, err)
	}
	if claimed.ID != second.ID {
		t.Errorf("claim took %d, want the unlocked %d (skip-locked failed)", claimed.ID, second.ID)
	}
}

func TestMarkSucceededRecordsRepoURL(t *testing.T) {
	db := dbtest.DB(t)
	svc := exportjob.NewService(db)
	ctx := context.Background()

	svc.Enqueue(ctx, 1, 1, 1, "app", false)
	job, _, _ := svc.Claim(ctx) // running is the only legal state to mark from
	if err := svc.MarkSucceeded(ctx, job.ID, "https://github.com/octo/app"); err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}
	got, _, _ := svc.Get(ctx, job.ID)
	if got.Status != exportjob.StatusSucceeded || got.RepoURL != "https://github.com/octo/app" {
		t.Errorf("after success: status=%q url=%q", got.Status, got.RepoURL)
	}
}

func TestMarkFailedRecordsError(t *testing.T) {
	db := dbtest.DB(t)
	svc := exportjob.NewService(db)
	ctx := context.Background()

	svc.Enqueue(ctx, 1, 1, 1, "app", false)
	job, _, _ := svc.Claim(ctx)
	if err := svc.MarkFailed(ctx, job.ID, "assemble source failed"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	got, _, _ := svc.Get(ctx, job.ID)
	if got.Status != exportjob.StatusFailed || got.Error != "assemble source failed" {
		t.Errorf("after failure: status=%q error=%q", got.Status, got.Error)
	}
}

func TestMarkMissingJobErrors(t *testing.T) {
	db := dbtest.DB(t)
	svc := exportjob.NewService(db)
	if err := svc.MarkSucceeded(context.Background(), 999999, "x"); err == nil {
		t.Error("marking a missing job must error")
	}
}

// TestMarkRejectsNonRunning pins the running → terminal invariant: a queued job
// (never claimed) can't be marked terminal, and an already-terminal job can't be
// re-marked — both must error and leave the recorded outcome untouched.
func TestMarkRejectsNonRunning(t *testing.T) {
	db := dbtest.DB(t)
	svc := exportjob.NewService(db)
	ctx := context.Background()

	// queued → succeeded is rejected; the job stays queued.
	queued, _ := svc.Enqueue(ctx, 1, 1, 1, "app", false)
	if err := svc.MarkSucceeded(ctx, queued.ID, "https://github.com/octo/app"); err == nil {
		t.Error("marking a queued (unclaimed) job must error")
	}
	if got, _, _ := svc.Get(ctx, queued.ID); got.Status != exportjob.StatusQueued || got.RepoURL != "" {
		t.Errorf("queued job was mutated by a rejected mark: %+v", got)
	}

	// running → succeeded, then a second mark (now terminal) is rejected and does
	// not clobber the recorded success.
	job, _, _ := svc.Claim(ctx)
	if err := svc.MarkSucceeded(ctx, job.ID, "https://github.com/octo/app"); err != nil {
		t.Fatalf("mark running job: %v", err)
	}
	if err := svc.MarkFailed(ctx, job.ID, "late worker"); err == nil {
		t.Error("re-marking an already-terminal job must error")
	}
	got, _, _ := svc.Get(ctx, job.ID)
	if got.Status != exportjob.StatusSucceeded || got.Error != "" {
		t.Errorf("terminal outcome clobbered by a late mark: %+v", got)
	}
}
