package buildworker

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/cloudbuild"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/cloudclient"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

type terminal struct {
	status string
	reason string
	digest string
}

type fakeJobs struct {
	job       cloudbuild.BuildJob
	claimed   bool
	cloudID   string
	reflected []string
	succeeded *terminal
	failed    *terminal
}

func (f *fakeJobs) Claim(context.Context) (cloudbuild.BuildJob, bool, error) {
	if f.claimed {
		return cloudbuild.BuildJob{}, false, nil
	}
	f.claimed = true
	return f.job, true, nil
}
func (f *fakeJobs) SetCloudBuild(_ context.Context, _ uint, id string) error {
	f.cloudID = id
	return nil
}
func (f *fakeJobs) ReflectCloudStatus(_ context.Context, _ uint, s string) error {
	f.reflected = append(f.reflected, s)
	return nil
}
func (f *fakeJobs) MarkSucceeded(_ context.Context, _ uint, status, digest string) error {
	f.succeeded = &terminal{status: status, digest: digest}
	return nil
}
func (f *fakeJobs) MarkFailed(_ context.Context, _ uint, status, reason string) error {
	f.failed = &terminal{status: status, reason: reason}
	return nil
}

type fakeRevs struct{ err error }

func (f fakeRevs) RevisionSpec(context.Context, uint) (*spec.ProjectSpec, string, error) {
	if f.err != nil {
		return nil, "", f.err
	}
	return &spec.ProjectSpec{}, "rev-abc", nil
}

type fakeAsm struct{ err error }

func (f fakeAsm) AssembleTarGz(_ context.Context, _ *spec.ProjectSpec, _, _ string, w io.Writer) error {
	if f.err != nil {
		return f.err
	}
	_, err := io.WriteString(w, "tar-gz")
	return err
}

type fakeCloud struct {
	createErr error
	statuses  []string // returned by GetBuild in sequence; the last repeats
	digest    string
	idx       int
	uploads   int
}

func (f *fakeCloud) CreateBuild(context.Context, string, string) (cloudclient.Build, error) {
	if f.createErr != nil {
		return cloudclient.Build{}, f.createErr
	}
	return cloudclient.Build{ID: "bld_1", Status: "queued"}, nil
}
func (f *fakeCloud) UploadSource(_ context.Context, _, _ string, r io.Reader) error {
	f.uploads++
	_, _ = io.Copy(io.Discard, r)
	return nil
}
func (f *fakeCloud) GetBuild(context.Context, string, string) (cloudclient.Build, error) {
	s := f.statuses[min(f.idx, len(f.statuses)-1)]
	f.idx++
	digest := ""
	if s == "succeeded" {
		digest = f.digest
	}
	return cloudclient.Build{ID: "bld_1", Status: s, ArtifactDigest: digest}, nil
}

func fastWorker(jobs Jobs, revs Revisions, asm Assembler, cloud Cloud) *Worker {
	return New(jobs, revs, asm, cloud, Options{Poll: time.Millisecond, TrackPoll: time.Millisecond, TrackTimeout: 5 * time.Second}, nil)
}

func TestWorkerHappyPath(t *testing.T) {
	jobs := &fakeJobs{job: cloudbuild.BuildJob{ID: 1, RevisionID: 42, CloudProjectID: "prj_cloud"}}
	cloud := &fakeCloud{statuses: []string{"building", "succeeded"}, digest: "sha256:cafe"}
	w := fastWorker(jobs, fakeRevs{}, fakeAsm{}, cloud)

	did, err := w.RunOnce(context.Background())
	if err != nil || !did {
		t.Fatalf("RunOnce = %v, %v", did, err)
	}
	if jobs.cloudID != "bld_1" {
		t.Fatalf("cloud build id not recorded: %q", jobs.cloudID)
	}
	if cloud.uploads != 1 {
		t.Fatalf("uploads = %d, want 1", cloud.uploads)
	}
	if jobs.succeeded == nil || jobs.succeeded.status != "succeeded" || jobs.succeeded.digest != "sha256:cafe" {
		t.Fatalf("succeeded = %+v", jobs.succeeded)
	}
	// Cloud transitions were reflected onto the job (building, then succeeded).
	if len(jobs.reflected) < 2 || jobs.reflected[0] != "building" {
		t.Fatalf("reflected = %v", jobs.reflected)
	}
	if jobs.failed != nil {
		t.Fatalf("job also marked failed: %+v", jobs.failed)
	}
}

func TestWorkerCloudBuildFailed(t *testing.T) {
	jobs := &fakeJobs{job: cloudbuild.BuildJob{ID: 1, CloudProjectID: "prj_cloud"}}
	cloud := &fakeCloud{statuses: []string{"building", "failed"}}
	w := fastWorker(jobs, fakeRevs{}, fakeAsm{}, cloud)

	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if jobs.failed == nil || jobs.failed.status != "failed" || jobs.failed.reason != failCloudBuild {
		t.Fatalf("failed = %+v, want cloud-build failure", jobs.failed)
	}
	if jobs.succeeded != nil {
		t.Fatal("a failed cloud build must not be marked succeeded")
	}
}

func TestWorkerAssemblyFailure(t *testing.T) {
	jobs := &fakeJobs{job: cloudbuild.BuildJob{ID: 1, CloudProjectID: "prj_cloud"}}
	cloud := &fakeCloud{statuses: []string{"succeeded"}}
	w := fastWorker(jobs, fakeRevs{}, fakeAsm{err: errors.New("scaffold blew up")}, cloud)

	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Assembly failed before any Cloud call: recorded failure, no build created.
	if jobs.failed == nil || jobs.failed.reason != failAssemble {
		t.Fatalf("failed = %+v, want assembly failure", jobs.failed)
	}
	if jobs.cloudID != "" || cloud.uploads != 0 {
		t.Fatalf("should not have touched Cloud: cloudID=%q uploads=%d", jobs.cloudID, cloud.uploads)
	}
}

func TestWorkerCreateBuildFailure(t *testing.T) {
	jobs := &fakeJobs{job: cloudbuild.BuildJob{ID: 1, CloudProjectID: "prj_cloud"}}
	cloud := &fakeCloud{createErr: errors.New("cloud down"), statuses: []string{"queued"}}
	w := fastWorker(jobs, fakeRevs{}, fakeAsm{}, cloud)

	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if jobs.failed == nil || jobs.failed.reason != failSubmit {
		t.Fatalf("failed = %+v, want submit failure", jobs.failed)
	}
}

func TestWorkerEmptyQueue(t *testing.T) {
	jobs := &fakeJobs{claimed: true} // nothing to claim
	w := fastWorker(jobs, fakeRevs{}, fakeAsm{}, &fakeCloud{statuses: []string{"queued"}})
	did, err := w.RunOnce(context.Background())
	if err != nil || did {
		t.Fatalf("RunOnce on empty queue = %v, %v, want false, nil", did, err)
	}
}
