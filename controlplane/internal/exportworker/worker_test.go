package exportworker_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/exportjob"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/exportworker"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/ghexport"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// --- fakes -----------------------------------------------------------------

// fakeJobs is a mutex-guarded in-memory queue so Run (goroutine) and the test
// can touch it concurrently.
type fakeJobs struct {
	mu       sync.Mutex
	queue    []exportjob.ExportJob
	claimErr error

	succeeded    map[uint]string // jobID -> repoURL
	failed       map[uint]string // jobID -> stored reason
	markCanceled bool            // true if any Mark* saw a cancelled ctx
}

func newFakeJobs(jobs ...exportjob.ExportJob) *fakeJobs {
	return &fakeJobs{queue: jobs, succeeded: map[uint]string{}, failed: map[uint]string{}}
}

func (f *fakeJobs) Claim(context.Context) (exportjob.ExportJob, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return exportjob.ExportJob{}, false, f.claimErr
	}
	if len(f.queue) == 0 {
		return exportjob.ExportJob{}, false, nil
	}
	job := f.queue[0]
	f.queue = f.queue[1:]
	return job, true, nil
}

func (f *fakeJobs) MarkSucceeded(ctx context.Context, jobID uint, repoURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		f.markCanceled = true
	}
	f.succeeded[jobID] = repoURL
	return nil
}

func (f *fakeJobs) MarkFailed(ctx context.Context, jobID uint, sanitized string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		f.markCanceled = true
	}
	f.failed[jobID] = sanitized
	return nil
}

func (f *fakeJobs) counts() (queued, done int, sawCanceledMark bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queue), len(f.succeeded) + len(f.failed), f.markCanceled
}

type fakeRevs struct {
	sp  *spec.ProjectSpec
	ref string
	err error
}

func (f fakeRevs) RevisionSpec(context.Context, uint) (*spec.ProjectSpec, string, error) {
	return f.sp, f.ref, f.err
}

type fakeExporter struct {
	res    ghexport.Result
	err    error
	called bool
	gotArg struct {
		userID   uint
		repoName string
		private  bool
		sp       *spec.ProjectSpec
		ref      string
	}
}

func (f *fakeExporter) ExportSpec(_ context.Context, userID uint, repoName string, private bool, sp *spec.ProjectSpec, ref string) (ghexport.Result, error) {
	f.called = true
	f.gotArg.userID, f.gotArg.repoName, f.gotArg.private, f.gotArg.sp, f.gotArg.ref = userID, repoName, private, sp, ref
	if f.err != nil {
		return ghexport.Result{}, f.err
	}
	return f.res, nil
}

func sampleJob() exportjob.ExportJob {
	return exportjob.ExportJob{ID: 42, ProjectID: 3, RevisionID: 7, UserID: 11, RepoName: "my-app", Private: true, Status: exportjob.StatusRunning}
}

// --- tests -----------------------------------------------------------------

func TestRunOnceEmptyQueue(t *testing.T) {
	jobs := newFakeJobs()
	w := exportworker.New(jobs, fakeRevs{}, &fakeExporter{}, 0, nil)
	did, err := w.RunOnce(context.Background())
	if did || err != nil {
		t.Errorf("empty queue: did=%v err=%v, want false,nil", did, err)
	}
}

func TestRunOnceClaimError(t *testing.T) {
	boom := errors.New("claim boom")
	w := exportworker.New(&fakeJobs{claimErr: boom}, fakeRevs{}, &fakeExporter{}, 0, nil)
	if _, err := w.RunOnce(context.Background()); !errors.Is(err, boom) {
		t.Errorf("claim error must propagate; got %v", err)
	}
}

func TestProcessSuccess(t *testing.T) {
	job := sampleJob()
	sp := &spec.ProjectSpec{}
	jobs := newFakeJobs(job)
	exp := &fakeExporter{res: ghexport.Result{RepoURL: "https://github.com/octo/my-app", FullName: "octo/my-app"}}
	w := exportworker.New(jobs, fakeRevs{sp: sp, ref: "rev_abc"}, exp, 0, nil)

	did, err := w.RunOnce(context.Background())
	if !did || err != nil {
		t.Fatalf("RunOnce: did=%v err=%v", did, err)
	}
	if got := jobs.succeeded[job.ID]; got != "https://github.com/octo/my-app" {
		t.Errorf("marked succeeded url = %q", got)
	}
	if len(jobs.failed) != 0 {
		t.Errorf("no job should be failed: %v", jobs.failed)
	}
	// The frozen revision's spec/ref and the job's fields were passed through.
	if !exp.called || exp.gotArg.userID != 11 || exp.gotArg.repoName != "my-app" || !exp.gotArg.private ||
		exp.gotArg.sp != sp || exp.gotArg.ref != "rev_abc" {
		t.Errorf("exporter called with wrong args: %+v", exp.gotArg)
	}
}

func TestProcessRevisionLoadFailureIsFailedNotExported(t *testing.T) {
	job := sampleJob()
	jobs := newFakeJobs(job)
	exp := &fakeExporter{}
	w := exportworker.New(jobs, fakeRevs{err: errors.New("revision gone")}, exp, 0, nil)

	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned error for a job failure: %v", err)
	}
	if exp.called {
		t.Error("exporter must not run when the revision can't be loaded")
	}
	if got := jobs.failed[job.ID]; got != "could not load the revision to export" {
		t.Errorf("stored failure = %q", got)
	}
}

func TestProcessExportFailureIsRecorded(t *testing.T) {
	job := sampleJob()
	jobs := newFakeJobs(job)
	// A raw error carrying a "path" — it must not reach the stored reason.
	exp := &fakeExporter{err: errors.New("assemble source: /tmp/build/x failed")}
	w := exportworker.New(jobs, fakeRevs{sp: &spec.ProjectSpec{}}, exp, 0, nil)

	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned error for a job failure: %v", err)
	}
	got := jobs.failed[job.ID]
	if got != "source assembly or push to GitHub failed" {
		t.Errorf("stored failure = %q, want the sanitized category", got)
	}
	if _, ok := jobs.succeeded[job.ID]; ok {
		t.Error("a failed export must not be marked succeeded")
	}
}

// TestMarkOutcomeSurvivesCancelledContext: even when the job's context is
// already cancelled (a shutdown mid-job), the terminal outcome is still
// recorded, and the Mark* write runs on a detached, non-cancelled context — so
// a poller never sees a job stuck running.
func TestMarkOutcomeSurvivesCancelledContext(t *testing.T) {
	job := sampleJob()
	jobs := newFakeJobs(job)
	exp := &fakeExporter{res: ghexport.Result{RepoURL: "https://github.com/octo/my-app"}}
	w := exportworker.New(jobs, fakeRevs{sp: &spec.ProjectSpec{}, ref: "r"}, exp, 0, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // shutdown before the job is recorded

	w.RunOnce(ctx)

	_, done, sawCanceled := jobs.counts()
	if done != 1 {
		t.Fatalf("outcome not recorded under a cancelled context; done=%d", done)
	}
	if sawCanceled {
		t.Error("Mark* must run on a detached (non-cancelled) context")
	}
	if jobs.succeeded[job.ID] != "https://github.com/octo/my-app" {
		t.Errorf("recorded url = %q", jobs.succeeded[job.ID])
	}
}

// TestRunDrainsThenReturnsOnCancel exercises the Run orchestration: it drains
// the whole backlog, then returns promptly once the context is cancelled.
func TestRunDrainsThenReturnsOnCancel(t *testing.T) {
	jobs := newFakeJobs(
		exportjob.ExportJob{ID: 1, RevisionID: 1, UserID: 1, RepoName: "a"},
		exportjob.ExportJob{ID: 2, RevisionID: 1, UserID: 1, RepoName: "b"},
		exportjob.ExportJob{ID: 3, RevisionID: 1, UserID: 1, RepoName: "c"},
	)
	exp := &fakeExporter{res: ghexport.Result{RepoURL: "https://github.com/octo/x"}}
	w := exportworker.New(jobs, fakeRevs{sp: &spec.ProjectSpec{}}, exp, 5*time.Millisecond, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	// Wait for the backlog to drain.
	deadline := time.After(2 * time.Second)
	for {
		if _, d, _ := jobs.counts(); d == 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run did not drain the queue in time")
		case <-time.After(2 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
