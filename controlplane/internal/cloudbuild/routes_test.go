package cloudbuild_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/gombit-dev/gombit/auth"
	"github.com/gombit-dev/gombit/config"
	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/cloudbuild"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/cloudclient"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/org"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
)

// fakeProjects resolves a fixed project + head revision — the routes' project
// dependency, so the handler logic (auth, head resolution, Cloud linkage) is
// tested without the full project service.
type fakeProjects struct {
	proj    project.Project
	head    project.Revision
	hasHead bool
}

func (f fakeProjects) GetProject(context.Context, uint) (project.Project, error) { return f.proj, nil }
func (f fakeProjects) Head(context.Context, uint) (project.Revision, bool, error) {
	return f.head, f.hasHead, nil
}

// fakeAuthz authorizes (or not) with a fixed verdict.
type fakeAuthz struct{ err error }

func (f fakeAuthz) Authorize(context.Context, uint, uint, org.Capability) error { return f.err }

// fakeLogs is the test double for the Cloud client the deploy routes call. It
// returns fixed log lines, environments and deployments and records the args it
// was called with, so the route logic (auth, env-ownership, error mapping,
// pass-through) is tested without a live Cloud.
type fakeLogs struct {
	lines     []cloudclient.BuildLog
	err       error
	gotBuild  string
	gotSince  string
	callCount int

	// Deploy surface.
	envs            []cloudclient.Environment
	envsErr         error
	deployment      cloudclient.Deployment
	deployErr       error
	gotEnvID        string
	gotBuildID      string
	gotDeployID     string
	rollbackErr     error
	didRollback     bool
	appLogs         []cloudclient.DeploymentLog
	appLogsErr      error
	gotLogsDeployID string

	// Preview environment creation.
	previewEnv           cloudclient.Environment
	previewErr           error
	gotPreviewName       string
	gotPreviewTTL        int64
	gotPreviewSourceType string
	gotPreviewSourceRef  string
}

func (f *fakeLogs) GetBuildLogs(_ context.Context, buildID, since string) ([]cloudclient.BuildLog, error) {
	f.callCount++
	f.gotBuild, f.gotSince = buildID, since
	return f.lines, f.err
}

func (f *fakeLogs) GetDeploymentLogs(_ context.Context, deploymentID, since string) ([]cloudclient.DeploymentLog, error) {
	f.gotLogsDeployID, f.gotSince = deploymentID, since
	return f.appLogs, f.appLogsErr
}

func (f *fakeLogs) ListEnvironments(_ context.Context, _ string) ([]cloudclient.Environment, error) {
	return f.envs, f.envsErr
}

func (f *fakeLogs) CreatePreviewEnvironment(_ context.Context, _, name string, ttlSeconds int64, sourceType, sourceRef string) (cloudclient.Environment, error) {
	f.gotPreviewName, f.gotPreviewTTL = name, ttlSeconds
	f.gotPreviewSourceType, f.gotPreviewSourceRef = sourceType, sourceRef
	return f.previewEnv, f.previewErr
}

func (f *fakeLogs) CreateDeployment(_ context.Context, envID, buildID string) (cloudclient.Deployment, error) {
	f.gotEnvID, f.gotBuildID = envID, buildID
	return f.deployment, f.deployErr
}

func (f *fakeLogs) GetDeployment(_ context.Context, envID, deploymentID string) (cloudclient.Deployment, error) {
	f.gotEnvID, f.gotDeployID = envID, deploymentID
	return f.deployment, f.deployErr
}

func (f *fakeLogs) Rollback(_ context.Context, envID string) (cloudclient.Deployment, error) {
	f.gotEnvID, f.didRollback = envID, true
	return f.deployment, f.rollbackErr
}

type routesFixture struct {
	api     humatest.TestAPI
	authSvc *auth.Service
	jobs    *cloudbuild.Service
	db      *gorm.DB
}

func newRoutesFixture(t *testing.T, projects cloudbuild.Projects, authz cloudbuild.Authorizer) *routesFixture {
	return newRoutesFixtureWithLogs(t, projects, authz, &fakeLogs{})
}

func newRoutesFixtureWithLogs(t *testing.T, projects cloudbuild.Projects, authz cloudbuild.Authorizer, logs cloudbuild.Cloud) *routesFixture {
	t.Helper()
	db := dbtest.DB(t)
	cfg := config.Config{Auth: config.AuthConfig{
		JWTSecret:       "cloudbuild-routes-test-secret-please-change-01",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
		Mode:            config.AuthModeCookie,
	}}
	authSvc, err := auth.NewService(db, cfg)
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	jobs := cloudbuild.NewService(db)
	_, api := humatest.New(t)
	cloudbuild.RegisterRoutes(api, "/api/v1", huma.Middlewares{authSvc.RequireCookieSession()}, jobs, projects, authz, logs)
	return &routesFixture{api: api, authSvc: authSvc, jobs: jobs, db: db}
}

func (f *routesFixture) seedUser(t *testing.T, email string) uint {
	t.Helper()
	u := auth.User{Email: email, PasswordHash: "x"}
	if err := f.db.Create(&u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u.ID
}

func (f *routesFixture) cookie(t *testing.T, userID uint) string {
	t.Helper()
	var u auth.User
	if err := f.db.First(&u, userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	pair, err := f.authSvc.IssueTokens(context.Background(), u)
	if err != nil {
		t.Fatalf("issue tokens: %v", err)
	}
	return "Cookie: " + auth.AccessCookieName + "=" + pair.AccessToken
}

func cloudLinked(id string) project.Project {
	return project.Project{ID: 1, OrganizationID: 7, CloudProjectID: &id}
}

func TestDeployEnqueuesForLinkedProject(t *testing.T) {
	fx := newRoutesFixture(t,
		fakeProjects{proj: cloudLinked("prj_cloud"), head: project.Revision{ID: 42}, hasHead: true},
		fakeAuthz{})
	user := fx.seedUser(t, "dev@example.test")

	resp := fx.api.Post("/api/v1/projects/1/deploy", fx.cookie(t, user))
	if resp.Code != http.StatusAccepted {
		t.Fatalf("deploy → %d, want 202: %s", resp.Code, resp.Body.String())
	}

	// The job was persisted against the project, queued, frozen to the head.
	list, err := fx.jobs.ListForProject(context.Background(), 1)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if list[0].RevisionID != 42 || list[0].UserID != user || list[0].CloudProjectID != "prj_cloud" ||
		list[0].Status != cloudbuild.StatusQueued {
		t.Fatalf("enqueued job = %+v", list[0])
	}
}

func TestDeployRejectsUnlinkedProject(t *testing.T) {
	// A project with no Cloud link (CloudProjectID nil) is a 422, not a 500.
	fx := newRoutesFixture(t,
		fakeProjects{proj: project.Project{ID: 1, OrganizationID: 7}, head: project.Revision{ID: 42}, hasHead: true},
		fakeAuthz{})
	user := fx.seedUser(t, "dev@example.test")

	resp := fx.api.Post("/api/v1/projects/1/deploy", fx.cookie(t, user))
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("deploy unlinked → %d, want 422: %s", resp.Code, resp.Body.String())
	}
}

func TestGetBuildJobIsProjectScoped(t *testing.T) {
	// Deploy history is project-scoped: a project viewer who did NOT initiate a
	// job can still poll it (the regression the get/list mismatch would cause).
	fx := newRoutesFixture(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{})
	viewer := fx.seedUser(t, "viewer@example.test")

	// A job initiated by a different user (id 999).
	job, err := fx.jobs.Enqueue(context.Background(), 1, 42, 999, "prj_cloud")
	if err != nil {
		t.Fatal(err)
	}

	resp := fx.api.Get("/api/v1/build-jobs/"+strconv.FormatUint(uint64(job.ID), 10), fx.cookie(t, viewer))
	if resp.Code != http.StatusOK {
		t.Fatalf("viewer (non-initiator) get → %d, want 200: %s", resp.Code, resp.Body.String())
	}
}

func TestBuildJobLogsRelayedFromCloud(t *testing.T) {
	logs := &fakeLogs{lines: []cloudclient.BuildLog{{Timestamp: "2026-01-01T00:00:00Z", Stream: "build", Message: "cloning repo"}}}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, logs)
	user := fx.seedUser(t, "viewer@example.test")
	ctx := context.Background()

	job, err := fx.jobs.Enqueue(ctx, 1, 42, user, "prj_cloud")
	if err != nil {
		t.Fatal(err)
	}
	// Give the job a Cloud build id: claim (→running), then record it.
	if _, _, err := fx.jobs.Claim(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fx.jobs.SetCloudBuild(ctx, job.ID, "bld_9"); err != nil {
		t.Fatal(err)
	}

	path := "/api/v1/build-jobs/" + strconv.FormatUint(uint64(job.ID), 10) + "/logs?since=2026-01-01T00:00:00Z"
	resp := fx.api.Get(path, fx.cookie(t, user))
	if resp.Code != http.StatusOK {
		t.Fatalf("logs → %d: %s", resp.Code, resp.Body.String())
	}
	// The job's Cloud build id and the ?since are forwarded to Cloud.
	if logs.gotBuild != "bld_9" || logs.gotSince != "2026-01-01T00:00:00Z" {
		t.Fatalf("GetBuildLogs(build=%q, since=%q)", logs.gotBuild, logs.gotSince)
	}
	if !strings.Contains(resp.Body.String(), "cloning repo") {
		t.Fatalf("logs body missing the relayed line: %s", resp.Body.String())
	}
}

func TestBuildJobLogsEmptyBeforeCloudBuild(t *testing.T) {
	logs := &fakeLogs{}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, logs)
	user := fx.seedUser(t, "viewer@example.test")

	// A freshly queued job has no Cloud build yet.
	job, err := fx.jobs.Enqueue(context.Background(), 1, 42, user, "prj_cloud")
	if err != nil {
		t.Fatal(err)
	}
	resp := fx.api.Get("/api/v1/build-jobs/"+strconv.FormatUint(uint64(job.ID), 10)+"/logs", fx.cookie(t, user))
	if resp.Code != http.StatusOK {
		t.Fatalf("logs → %d", resp.Code)
	}
	// Cloud is not called when there's no build id — empty logs, not an error.
	if logs.callCount != 0 {
		t.Fatalf("GetBuildLogs called %d times before a cloud build exists", logs.callCount)
	}
}

func TestDeployRequiresAuth(t *testing.T) {
	fx := newRoutesFixture(t,
		fakeProjects{proj: cloudLinked("prj_cloud"), head: project.Revision{ID: 42}, hasHead: true},
		fakeAuthz{})
	// No cookie → the gate rejects before the handler.
	resp := fx.api.Post("/api/v1/projects/1/deploy")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated deploy → %d, want 401", resp.Code)
	}
}

// --- Deploy surface (#105): environments, deploy-to-env, status, rollback ---

func TestListEnvironments(t *testing.T) {
	cloud := &fakeLogs{envs: []cloudclient.Environment{
		{ID: "env_prod", Name: "production", Kind: "persistent"},
		{ID: "env_stg", Name: "staging", Kind: "persistent"},
	}}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "viewer@example.test")

	resp := fx.api.Get("/api/v1/projects/1/environments", fx.cookie(t, user))
	if resp.Code != http.StatusOK {
		t.Fatalf("list environments → %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"env_prod"`) || !strings.Contains(resp.Body.String(), `"staging"`) {
		t.Fatalf("environments body = %s", resp.Body.String())
	}
}

func TestListEnvironmentsUnlinkedProject(t *testing.T) {
	// A project with no Cloud link has no environments to list — 422, not a 500.
	fx := newRoutesFixtureWithLogs(t,
		fakeProjects{proj: project.Project{ID: 1, OrganizationID: 7}}, fakeAuthz{}, &fakeLogs{})
	user := fx.seedUser(t, "viewer@example.test")

	resp := fx.api.Get("/api/v1/projects/1/environments", fx.cookie(t, user))
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("list environments unlinked → %d, want 422: %s", resp.Code, resp.Body.String())
	}
}

func TestDeployBuildToEnvironmentSurfacesBlock(t *testing.T) {
	// Cloud creates the deployment and returns it held on a destructive migration
	// (a legitimate lifecycle state, not an error). Forge passes the block through —
	// it never approves (L10).
	cloud := &fakeLogs{
		envs: []cloudclient.Environment{{ID: "env_prod", Name: "production"}},
		deployment: cloudclient.Deployment{
			ID: "dep_1", EnvironmentID: "env_prod", BuildID: "bld_1", Status: "blocked_pending_approval",
			Block: &cloudclient.DeploymentBlock{Code: "migration_approval_required", MigrationID: "mig_7", ApprovalURL: "https://console.example/databases/db_2/migrations"},
		},
	}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "dev@example.test")

	resp := fx.api.Post("/api/v1/projects/1/environments/env_prod/deployments", fx.cookie(t, user),
		map[string]any{"build_id": "bld_1"})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("deploy → %d, want 202: %s", resp.Code, resp.Body.String())
	}
	// The chosen env + build were forwarded to Cloud.
	if cloud.gotEnvID != "env_prod" || cloud.gotBuildID != "bld_1" {
		t.Fatalf("cloud got env=%q build=%q", cloud.gotEnvID, cloud.gotBuildID)
	}
	// The structured block is surfaced with Cloud's approval_url.
	body := resp.Body.String()
	if !strings.Contains(body, `"blocked_pending_approval"`) || !strings.Contains(body, `"migration_approval_required"`) ||
		!strings.Contains(body, `"mig_7"`) || !strings.Contains(body, "databases/db_2/migrations") {
		t.Fatalf("deployment body = %s", body)
	}
}

func TestDeployToForeignEnvironmentIsNotFound(t *testing.T) {
	// An env id that is NOT one of the project's environments must not be deployable
	// to — tenancy-safe NotFound, and Cloud's CreateDeployment is never called.
	cloud := &fakeLogs{envs: []cloudclient.Environment{{ID: "env_prod", Name: "production"}}}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "dev@example.test")

	resp := fx.api.Post("/api/v1/projects/1/environments/env_someone_else/deployments", fx.cookie(t, user),
		map[string]any{"build_id": "bld_1"})
	if resp.Code != http.StatusNotFound {
		t.Fatalf("deploy to foreign env → %d, want 404: %s", resp.Code, resp.Body.String())
	}
	if cloud.gotBuildID != "" {
		t.Fatalf("CreateDeployment must not be called for a foreign env (got build %q)", cloud.gotBuildID)
	}
}

func TestDeployMissingBuildIDIsValidation(t *testing.T) {
	cloud := &fakeLogs{envs: []cloudclient.Environment{{ID: "env_prod"}}}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "dev@example.test")

	resp := fx.api.Post("/api/v1/projects/1/environments/env_prod/deployments", fx.cookie(t, user),
		map[string]any{"build_id": ""})
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("deploy without build id → %d, want 422: %s", resp.Code, resp.Body.String())
	}
}

func TestGetDeploymentPassthrough(t *testing.T) {
	cloud := &fakeLogs{
		envs:       []cloudclient.Environment{{ID: "env_prod"}},
		deployment: cloudclient.Deployment{ID: "dep_1", EnvironmentID: "env_prod", BuildID: "bld_1", Status: "running", ArtifactDigest: "sha256:cafe"},
	}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "viewer@example.test")

	resp := fx.api.Get("/api/v1/projects/1/environments/env_prod/deployments/dep_1", fx.cookie(t, user))
	if resp.Code != http.StatusOK {
		t.Fatalf("get deployment → %d: %s", resp.Code, resp.Body.String())
	}
	if cloud.gotEnvID != "env_prod" || cloud.gotDeployID != "dep_1" {
		t.Fatalf("cloud got env=%q dep=%q", cloud.gotEnvID, cloud.gotDeployID)
	}
	if !strings.Contains(resp.Body.String(), `"running"`) {
		t.Fatalf("deployment body = %s", resp.Body.String())
	}
}

func TestRollbackEnvironment(t *testing.T) {
	cloud := &fakeLogs{
		envs:       []cloudclient.Environment{{ID: "env_prod"}},
		deployment: cloudclient.Deployment{ID: "dep_2", EnvironmentID: "env_prod", Status: "pending", RestoresDeploymentID: "dep_0", RolledBackFromID: "dep_1"},
	}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "dev@example.test")

	resp := fx.api.Post("/api/v1/projects/1/environments/env_prod/rollback", fx.cookie(t, user))
	if resp.Code != http.StatusAccepted {
		t.Fatalf("rollback → %d, want 202: %s", resp.Code, resp.Body.String())
	}
	if !cloud.didRollback || cloud.gotEnvID != "env_prod" {
		t.Fatalf("cloud rollback called=%v env=%q", cloud.didRollback, cloud.gotEnvID)
	}
	if !strings.Contains(resp.Body.String(), `"dep_0"`) {
		t.Fatalf("rollback body = %s", resp.Body.String())
	}
}

func TestRollbackConflictIsMapped(t *testing.T) {
	// Cloud's 409 (nothing to roll back to) is preserved as a 409, not a 500.
	cloud := &fakeLogs{
		envs:        []cloudclient.Environment{{ID: "env_prod"}},
		rollbackErr: &cloudclient.Error{Status: http.StatusConflict, Code: "conflict", Message: "no previous healthy deployment to roll back to"},
	}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "dev@example.test")

	resp := fx.api.Post("/api/v1/projects/1/environments/env_prod/rollback", fx.cookie(t, user))
	if resp.Code != http.StatusConflict {
		t.Fatalf("rollback conflict → %d, want 409: %s", resp.Code, resp.Body.String())
	}
}

func TestDeploymentLogsRelayedFromCloud(t *testing.T) {
	cloud := &fakeLogs{
		envs:    []cloudclient.Environment{{ID: "env_prod"}},
		appLogs: []cloudclient.DeploymentLog{{Timestamp: "2026-01-01T00:00:01Z", Stream: "stdout", Message: "listening", RequestID: "req_9"}},
	}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "viewer@example.test")

	resp := fx.api.Get("/api/v1/projects/1/environments/env_prod/deployments/dep_1/logs?since=2026-01-01T00:00:00Z", fx.cookie(t, user))
	if resp.Code != http.StatusOK {
		t.Fatalf("deployment logs → %d: %s", resp.Code, resp.Body.String())
	}
	// The deployment id + ?since are forwarded to Cloud's log endpoint; relayed.
	if cloud.gotLogsDeployID != "dep_1" || cloud.gotSince != "2026-01-01T00:00:00Z" {
		t.Fatalf("cloud got logs dep=%q since=%q", cloud.gotLogsDeployID, cloud.gotSince)
	}
	body := resp.Body.String()
	if !strings.Contains(body, `"listening"`) || !strings.Contains(body, `"req_9"`) || !strings.Contains(body, `"stdout"`) {
		t.Fatalf("logs body = %s", body)
	}
}

func TestDeploymentLogsForeignDeploymentIsNotFound(t *testing.T) {
	// The env is the caller's, but the deployment id belongs to another tenant.
	// Binding the deployment to the env (GetDeployment is Cloud-env-scoped) must
	// 404 BEFORE any log is read — otherwise Forge's global service token would
	// relay another tenant's application logs (cross-tenant IDOR).
	cloud := &fakeLogs{
		envs:      []cloudclient.Environment{{ID: "env_prod"}},
		deployErr: &cloudclient.Error{Status: http.StatusNotFound, Code: "not_found", Message: "deployment not found"},
	}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "viewer@example.test")

	resp := fx.api.Get("/api/v1/projects/1/environments/env_prod/deployments/dep_from_other_tenant/logs", fx.cookie(t, user))
	if resp.Code != http.StatusNotFound {
		t.Fatalf("foreign-deployment logs → %d, want 404: %s", resp.Code, resp.Body.String())
	}
	if cloud.gotLogsDeployID != "" {
		t.Fatalf("the log fetch must never be reached for a deployment outside the env (got %q)", cloud.gotLogsDeployID)
	}
}

func TestDeploymentLogsForeignEnvIsNotFound(t *testing.T) {
	// The env-ownership guard bounds log reads too: a foreign env is NotFound and
	// Cloud's log API is never called.
	cloud := &fakeLogs{envs: []cloudclient.Environment{{ID: "env_prod"}}}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "viewer@example.test")

	resp := fx.api.Get("/api/v1/projects/1/environments/env_foreign/deployments/dep_1/logs", fx.cookie(t, user))
	if resp.Code != http.StatusNotFound {
		t.Fatalf("foreign-env logs → %d, want 404: %s", resp.Code, resp.Body.String())
	}
	if cloud.gotLogsDeployID != "" {
		t.Fatalf("GetDeploymentLogs must not be called for a foreign env")
	}
}

func TestCreatePreviewEnvironment(t *testing.T) {
	cloud := &fakeLogs{
		previewEnv: cloudclient.Environment{ID: "env_pr42", Name: "preview-r42", Kind: "ephemeral", State: "active", ExpiresAt: "2026-01-08T00:00:00Z"},
	}
	fx := newRoutesFixtureWithLogs(t,
		fakeProjects{proj: cloudLinked("prj_cloud"), head: project.Revision{ID: 42}, hasHead: true}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "dev@example.test")

	resp := fx.api.Post("/api/v1/projects/1/preview-environments", fx.cookie(t, user), map[string]any{})
	if resp.Code != http.StatusCreated {
		t.Fatalf("create preview → %d, want 201: %s", resp.Code, resp.Body.String())
	}
	// The default name is derived from the head revision, the default TTL is a week,
	// and the preview is tagged with Forge provenance pinning the revision.
	if cloud.gotPreviewName != "preview-r42" || cloud.gotPreviewTTL != 7*24*60*60 {
		t.Fatalf("preview name=%q ttl=%d", cloud.gotPreviewName, cloud.gotPreviewTTL)
	}
	if cloud.gotPreviewSourceType != "forge" || cloud.gotPreviewSourceRef != "r42" {
		t.Fatalf("preview source type=%q ref=%q", cloud.gotPreviewSourceType, cloud.gotPreviewSourceRef)
	}
	if !strings.Contains(resp.Body.String(), `"ephemeral"`) {
		t.Fatalf("preview body = %s", resp.Body.String())
	}
}

func TestCreatePreviewEnvironmentIdempotentOnConflict(t *testing.T) {
	// Cloud enforces (project, name) uniqueness, so a second create for the same
	// revision 409s. The route must return the EXISTING preview (200) so repeated
	// clicks just reselect it — not a confusing Conflict, not a pile of orphans.
	cloud := &fakeLogs{
		previewErr: &cloudclient.Error{Status: http.StatusConflict, Code: "conflict", Message: "environment name already in use"},
		envs: []cloudclient.Environment{
			{ID: "env_prod", Name: "production", Kind: "persistent"},
			{ID: "env_pr42", Name: "preview-r42", Kind: "ephemeral", State: "active", ExpiresAt: "2026-01-08T00:00:00Z"},
		},
	}
	fx := newRoutesFixtureWithLogs(t,
		fakeProjects{proj: cloudLinked("prj_cloud"), head: project.Revision{ID: 42}, hasHead: true}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "dev@example.test")

	resp := fx.api.Post("/api/v1/projects/1/preview-environments", fx.cookie(t, user), map[string]any{})
	if resp.Code != http.StatusOK {
		t.Fatalf("duplicate preview → %d, want 200 (return existing): %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"env_pr42"`) {
		t.Fatalf("expected the existing preview env, got %s", resp.Body.String())
	}
}

func TestCreatePreviewEnvironmentUnlinkedProject(t *testing.T) {
	fx := newRoutesFixtureWithLogs(t,
		fakeProjects{proj: project.Project{ID: 1, OrganizationID: 7}, head: project.Revision{ID: 42}, hasHead: true}, fakeAuthz{}, &fakeLogs{})
	user := fx.seedUser(t, "dev@example.test")

	resp := fx.api.Post("/api/v1/projects/1/preview-environments", fx.cookie(t, user), map[string]any{})
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("preview unlinked → %d, want 422: %s", resp.Code, resp.Body.String())
	}
}

func TestCreatePreviewEnvironmentNoRevision(t *testing.T) {
	// A linked project with no revision has nothing to preview — 422, not a 500.
	fx := newRoutesFixtureWithLogs(t,
		fakeProjects{proj: cloudLinked("prj_cloud"), hasHead: false}, fakeAuthz{}, &fakeLogs{})
	user := fx.seedUser(t, "dev@example.test")

	resp := fx.api.Post("/api/v1/projects/1/preview-environments", fx.cookie(t, user), map[string]any{})
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("preview no-revision → %d, want 422: %s", resp.Code, resp.Body.String())
	}
}

func TestListEnvironmentsSurfacesPreviewFields(t *testing.T) {
	cloud := &fakeLogs{envs: []cloudclient.Environment{
		{ID: "env_prod", Name: "production", Kind: "persistent"},
		{ID: "env_pr12", Name: "preview-pr-12", Kind: "ephemeral", State: "active", ExpiresAt: "2026-01-02T00:00:00Z"},
	}}
	fx := newRoutesFixtureWithLogs(t, fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{}, cloud)
	user := fx.seedUser(t, "viewer@example.test")

	resp := fx.api.Get("/api/v1/projects/1/environments", fx.cookie(t, user))
	if resp.Code != http.StatusOK {
		t.Fatalf("list environments → %d: %s", resp.Code, resp.Body.String())
	}
	// The preview's kind, state and expiry are surfaced so the UI can mark it throwaway.
	body := resp.Body.String()
	if !strings.Contains(body, `"ephemeral"`) || !strings.Contains(body, `"2026-01-02T00:00:00Z"`) {
		t.Fatalf("environments body = %s", body)
	}
}

func TestDeployToEnvironmentDeniedIsNotFound(t *testing.T) {
	// An authorization failure collapses to NotFound (tenancy-safe), and Cloud is
	// never called.
	cloud := &fakeLogs{envs: []cloudclient.Environment{{ID: "env_prod"}}}
	fx := newRoutesFixtureWithLogs(t,
		fakeProjects{proj: cloudLinked("prj_cloud")}, fakeAuthz{err: org.ErrForbidden}, cloud)
	user := fx.seedUser(t, "outsider@example.test")

	resp := fx.api.Post("/api/v1/projects/1/environments/env_prod/deployments", fx.cookie(t, user),
		map[string]any{"build_id": "bld_1"})
	if resp.Code != http.StatusNotFound {
		t.Fatalf("forbidden deploy → %d, want 404: %s", resp.Code, resp.Body.String())
	}
	if cloud.gotBuildID != "" {
		t.Fatalf("CreateDeployment must not be called when authorization fails")
	}
}
