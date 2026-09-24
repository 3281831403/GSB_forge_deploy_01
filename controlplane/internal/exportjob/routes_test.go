package exportjob_test

// HTTP-layer tests for the export routes: real cookie gate + migrated schema
// for the job store, with fakes for the project lookup and authorization so the
// handler's authz/IDOR/freeze wiring is exercised without standing up projects.
//
// TestExportRoutesEnforceGate needs no database and runs in CI; the rest are
// Docker-gated like the other DB tests.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/gombit-dev/gombit/auth"
	"github.com/gombit-dev/gombit/config"
	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/exportjob"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/org"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
)

type fakeProjects struct {
	proj    project.Project
	projErr error
	head    project.Revision
	headOK  bool
	headErr error
}

func (f fakeProjects) GetProject(context.Context, uint) (project.Project, error) {
	return f.proj, f.projErr
}
func (f fakeProjects) Head(context.Context, uint) (project.Revision, bool, error) {
	return f.head, f.headOK, f.headErr
}

type fakeAuthz struct{ err error }

func (f fakeAuthz) Authorize(context.Context, uint, uint, org.Capability) error { return f.err }

type routesFixture struct {
	api     humatest.TestAPI
	authSvc *auth.Service
	jobs    *exportjob.Service
	db      *gorm.DB
}

func newRoutesFixture(t *testing.T, projects exportjob.Projects, authz exportjob.Authorizer) *routesFixture {
	t.Helper()
	db := dbtest.DB(t)
	cfg := config.Config{Auth: config.AuthConfig{
		JWTSecret:       "export-routes-test-secret-please-change-01",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
		Mode:            config.AuthModeCookie,
	}}
	authSvc, err := auth.NewService(db, cfg)
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	jobs := exportjob.NewService(db)
	_, api := humatest.New(t)
	exportjob.RegisterRoutes(api, "/api/v1", huma.Middlewares{authSvc.RequireCookieSession()}, jobs, projects, authz)
	return &routesFixture{api: api, authSvc: authSvc, jobs: jobs, db: db}
}

func seedUser(t *testing.T, db *gorm.DB, email string) uint {
	t.Helper()
	u := auth.User{Email: email, PasswordHash: "x"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return u.ID
}

func (f *routesFixture) cookie(t *testing.T, userID uint) string {
	t.Helper()
	var u auth.User
	if err := f.db.First(&u, userID).Error; err != nil {
		t.Fatalf("load user %d: %v", userID, err)
	}
	pair, err := f.authSvc.IssueTokens(context.Background(), u)
	if err != nil {
		t.Fatalf("issue tokens: %v", err)
	}
	return "Cookie: " + auth.AccessCookieName + "=" + pair.AccessToken
}

func TestExportRoutesEnforceGate(t *testing.T) {
	ops := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/projects/1/export/github"},
		{http.MethodGet, "/api/v1/export-jobs/1"},
	}
	var gated int
	sentinel := func(ctx huma.Context, next func(huma.Context)) {
		gated++
		ctx.SetStatus(http.StatusUnauthorized)
	}
	_, api := humatest.New(t)
	exportjob.RegisterRoutes(api, "/api/v1", huma.Middlewares{sentinel}, nil, nil, nil)
	for _, op := range ops {
		if resp := api.Do(op.method, op.path); resp.Code != http.StatusUnauthorized {
			t.Errorf("%s %s reached the handler; gate not attached (got %d)", op.method, op.path, resp.Code)
		}
	}
	if gated != len(ops) {
		t.Errorf("gate ran %d times, want %d", gated, len(ops))
	}
}

// TestCreateEnqueuesFrozenHead: an authorized member exporting a project with a
// head revision gets a 202 and a queued job frozen to that head revision.
func TestCreateEnqueuesFrozenHead(t *testing.T) {
	projects := fakeProjects{
		proj: project.Project{ID: 3, OrganizationID: 8},
		head: project.Revision{ID: 77, ProjectID: 3}, headOK: true,
	}
	f := newRoutesFixture(t, projects, fakeAuthz{})
	userID := seedUser(t, f.db, "exporter@example.test")

	resp := f.api.Post("/api/v1/projects/3/export/github", f.cookie(t, userID),
		map[string]any{"name": "my-app", "private": true})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("create = %d, want 202\n%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Data struct {
			ID        uint   `json:"id"`
			ProjectID uint   `json:"project_id"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.Status != "queued" || body.Data.ProjectID != 3 {
		t.Errorf("job data = %+v", body.Data)
	}
	// The persisted job froze the head revision and the caller.
	job, ok, _ := f.jobs.Get(context.Background(), body.Data.ID)
	if !ok || job.RevisionID != 77 || job.UserID != userID || job.RepoName != "my-app" || !job.Private {
		t.Errorf("persisted job = %+v (ok=%v)", job, ok)
	}
}

// TestCreateBlankNameIsUnprocessable: an empty repo name (which huma's required
// permits) is the enqueue validation error mapped to 422, not a 500.
func TestCreateBlankNameIsUnprocessable(t *testing.T) {
	projects := fakeProjects{
		proj: project.Project{ID: 3, OrganizationID: 8},
		head: project.Revision{ID: 77}, headOK: true,
	}
	f := newRoutesFixture(t, projects, fakeAuthz{})
	userID := seedUser(t, f.db, "blank@example.test")

	resp := f.api.Post("/api/v1/projects/3/export/github", f.cookie(t, userID),
		map[string]any{"name": "  ", "private": false})
	if resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("blank name create = %d, want 422", resp.Code)
	}
}

func TestCreateNoRevisionIsUnprocessable(t *testing.T) {
	projects := fakeProjects{proj: project.Project{ID: 3, OrganizationID: 8}, headOK: false}
	f := newRoutesFixture(t, projects, fakeAuthz{})
	userID := seedUser(t, f.db, "norev@example.test")

	resp := f.api.Post("/api/v1/projects/3/export/github", f.cookie(t, userID),
		map[string]any{"name": "my-app", "private": false})
	if resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("no-revision create = %d, want 422", resp.Code)
	}
}

// TestCreateNonMemberIsNotFound: an authorization failure is masked as NotFound,
// so a cross-org project id can't be probed for existence.
func TestCreateNonMemberIsNotFound(t *testing.T) {
	projects := fakeProjects{
		proj: project.Project{ID: 3, OrganizationID: 8},
		head: project.Revision{ID: 77}, headOK: true,
	}
	f := newRoutesFixture(t, projects, fakeAuthz{err: org.ErrNotMember})
	userID := seedUser(t, f.db, "outsider@example.test")

	resp := f.api.Post("/api/v1/projects/3/export/github", f.cookie(t, userID),
		map[string]any{"name": "my-app", "private": false})
	if resp.Code != http.StatusNotFound {
		t.Errorf("non-member create = %d, want 404", resp.Code)
	}
}

func TestGetOwnJob(t *testing.T) {
	f := newRoutesFixture(t, fakeProjects{}, fakeAuthz{})
	userID := seedUser(t, f.db, "owner@example.test")
	job, _ := f.jobs.Enqueue(context.Background(), 3, 77, userID, "my-app", false)

	resp := f.api.Get("/api/v1/export-jobs/"+itoa(job.ID), f.cookie(t, userID))
	if resp.Code != http.StatusOK {
		t.Fatalf("get own job = %d, want 200\n%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	json.Unmarshal(resp.Body.Bytes(), &body)
	if body.Data.Status != "queued" {
		t.Errorf("status = %q", body.Data.Status)
	}
}

// TestGetOthersJobIsNotFound is the IDOR guard: a job belonging to another user
// is indistinguishable from a missing one.
func TestGetOthersJobIsNotFound(t *testing.T) {
	f := newRoutesFixture(t, fakeProjects{}, fakeAuthz{})
	owner := seedUser(t, f.db, "jobowner@example.test")
	other := seedUser(t, f.db, "snooper@example.test")
	job, _ := f.jobs.Enqueue(context.Background(), 3, 77, owner, "my-app", false)

	resp := f.api.Get("/api/v1/export-jobs/"+itoa(job.ID), f.cookie(t, other))
	if resp.Code != http.StatusNotFound {
		t.Errorf("other user's job = %d, want 404 (IDOR guard)", resp.Code)
	}
}

func TestGetMissingJobIsNotFound(t *testing.T) {
	f := newRoutesFixture(t, fakeProjects{}, fakeAuthz{})
	userID := seedUser(t, f.db, "poller@example.test")
	resp := f.api.Get("/api/v1/export-jobs/999999", f.cookie(t, userID))
	if resp.Code != http.StatusNotFound {
		t.Errorf("missing job = %d, want 404", resp.Code)
	}
}

func itoa(u uint) string { return strconv.FormatUint(uint64(u), 10) }
