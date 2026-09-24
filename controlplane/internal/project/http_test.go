package project_test

// End-to-end HTTP test over a humatest API with the real cookie gate and a real
// DB: exercises the wiring the service tests bypass — session → handler →
// org authorization → RawBody spec decode → outcome-to-status mapping.
// Docker-gated, like the service tests.

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
	"github.com/gombit-dev/gombit-forge/controlplane/internal/org"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

type httpFixture struct {
	api     humatest.TestAPI
	authSvc *auth.Service
	orgSvc  *org.Service
	db      *gorm.DB
}

func newHTTPFixture(t *testing.T) *httpFixture {
	t.Helper()
	db := dbtest.DB(t)
	cfg := config.Config{Auth: config.AuthConfig{
		JWTSecret:       "project-routes-test-secret-please-change-01",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
		Mode:            config.AuthModeCookie,
	}}
	authSvc, err := auth.NewService(db, cfg)
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	orgSvc := org.NewService(db)
	_, api := humatest.New(t)
	project.RegisterRoutes(api, "/api/v1",
		huma.Middlewares{authSvc.RequireCookieSession()}, project.NewService(db), orgSvc)
	return &httpFixture{api: api, authSvc: authSvc, orgSvc: orgSvc, db: db}
}

func (f *httpFixture) user(t *testing.T, email string) uint {
	t.Helper()
	u := auth.User{Email: email, PasswordHash: "x"}
	if err := f.db.Create(&u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u.ID
}

func (f *httpFixture) cookie(t *testing.T, userID uint) string {
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

// specBody re-encodes a spec as a generic JSON object so humatest sends it as the
// request body, which the handler reads back as its RawBody.
func specBody(t *testing.T, s *spec.ProjectSpec) map[string]any {
	t.Helper()
	data, err := spec.Marshal(s)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("to map: %v", err)
	}
	return body
}

// TestProjectAPIEndToEnd walks the whole authoring loop over HTTP: create a
// project, commit a first revision, and see a breaking candidate rejected with
// 409 while the head stays put.
func TestProjectAPIEndToEnd(t *testing.T) {
	f := newHTTPFixture(t)
	ctx := context.Background()

	ownerID := f.user(t, "owner@example.test")
	o, err := f.orgSvc.CreateOrganization(ctx, "Acme", "acme", ownerID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	cookie := f.cookie(t, ownerID)
	orgPath := "/api/v1/organizations/" + strconv.FormatUint(uint64(o.ID), 10) + "/projects"

	// Create a project.
	resp := f.api.Post(orgPath, cookie, map[string]any{"name": "Acme CRM", "slug": "acme-crm"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("create project = %d, want 201\n%s", resp.Code, resp.Body.String())
	}
	var created struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	revPath := "/api/v1/projects/" + strconv.FormatUint(uint64(created.Data.ID), 10) + "/revisions"

	// First candidate commits.
	base := validSpec(t, "V1", "v1")
	resp = f.api.Post(revPath, cookie, specBody(t, base))
	if resp.Code != http.StatusCreated {
		t.Fatalf("first revision = %d, want 201\n%s", resp.Code, resp.Body.String())
	}

	// A breaking candidate is rejected 409, not committed.
	breaking := cloneSpec(t, base)
	breaking.Resources[0].CodeName = "Client"
	resp = f.api.Post(revPath, cookie, specBody(t, breaking))
	if resp.Code != http.StatusConflict {
		t.Fatalf("breaking candidate = %d, want 409\n%s", resp.Code, resp.Body.String())
	}

	// An invalid candidate is a 422, separate from the ABI conflict above.
	invalid := cloneSpec(t, base)
	invalid.Resources[0].StorageName = "1_not_valid"
	resp = f.api.Post(revPath, cookie, specBody(t, invalid))
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid candidate = %d, want 422\n%s", resp.Code, resp.Body.String())
	}
}

// TestGetProjectSpec returns the head spec once a revision exists, and null
// before any revision is committed.
func TestGetProjectSpec(t *testing.T) {
	f := newHTTPFixture(t)
	ctx := context.Background()

	ownerID := f.user(t, "spec-owner@example.test")
	o, err := f.orgSvc.CreateOrganization(ctx, "Acme", "acme", ownerID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	p, err := project.NewService(f.db).CreateProject(ctx, o.ID, "Acme CRM", "acme-crm", ownerID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	cookie := f.cookie(t, ownerID)
	specPath := "/api/v1/projects/" + strconv.FormatUint(uint64(p.ID), 10) + "/spec"

	// No revisions yet: spec is null.
	resp := f.api.Get(specPath, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("get spec (no head) = %d, want 200\n%s", resp.Code, resp.Body.String())
	}
	var empty struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(empty.Data) != "null" {
		t.Errorf("spec before any revision = %s, want null", empty.Data)
	}

	// Commit a revision, then the spec comes back as the exact canonical bytes.
	base := validSpec(t, "V1", "v1")
	if _, err := project.NewService(f.db).SubmitCandidate(ctx, p.ID, base, ownerID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	resp = f.api.Get(specPath, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("get spec = %d, want 200\n%s", resp.Code, resp.Body.String())
	}
	var got struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want, _ := spec.Marshal(base)
	roundtrip, err := spec.Unmarshal(got.Data)
	if err != nil {
		t.Fatalf("returned spec is not decodable: %v", err)
	}
	back, _ := spec.Marshal(roundtrip)
	if string(back) != string(want) {
		t.Errorf("returned spec does not round-trip to the committed one")
	}
}

// TestAddResourceOverHTTP: adding a resource from a label over HTTP mints the
// symbol server-side and lands it in the project's spec.
func TestAddResourceOverHTTP(t *testing.T) {
	f := newHTTPFixture(t)
	ctx := context.Background()

	ownerID := f.user(t, "res-owner@example.test")
	o, err := f.orgSvc.CreateOrganization(ctx, "Acme", "acme", ownerID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	p, err := project.NewService(f.db).CreateProject(ctx, o.ID, "Acme CRM", "acme-crm", ownerID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	cookie := f.cookie(t, ownerID)
	base := "/api/v1/projects/" + strconv.FormatUint(uint64(p.ID), 10)

	resp := f.api.Post(base+"/resources", cookie, map[string]any{"label": "Order", "label_plural": "Orders"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("add resource = %d, want 201\n%s", resp.Code, resp.Body.String())
	}

	// The spec now carries the resource with a backend-minted symbol.
	specResp := f.api.Get(base+"/spec", cookie)
	var got struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(specResp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	s, err := spec.Unmarshal(got.Data)
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	if len(s.Resources) != 1 || s.Resources[0].Label != "Order" || s.Resources[0].CodeName == "" {
		t.Errorf("spec resources = %+v, want one Order with a minted symbol", s.Resources)
	}
}

// TestGetProjectHealth surfaces the three-state health: unknown before any
// revision, and Spec ok / Extension ABI ok / Runtime Build unknown once a valid
// spec is committed (the deployed build is Cloud's, not the control plane's).
func TestGetProjectHealth(t *testing.T) {
	f := newHTTPFixture(t)
	ctx := context.Background()

	ownerID := f.user(t, "health-owner@example.test")
	o, err := f.orgSvc.CreateOrganization(ctx, "Acme", "acme", ownerID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	svc := project.NewService(f.db)
	p, err := svc.CreateProject(ctx, o.ID, "Acme CRM", "acme-crm", ownerID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	cookie := f.cookie(t, ownerID)
	healthPath := "/api/v1/projects/" + strconv.FormatUint(uint64(p.ID), 10) + "/health"

	type facet struct {
		Name, Status, Summary string
	}
	decode := func(resp interface{ Bytes() []byte }) []facet {
		var body struct {
			Data struct {
				Facets []facet `json:"facets"`
			} `json:"data"`
		}
		if err := json.Unmarshal(resp.Bytes(), &body); err != nil {
			t.Fatalf("decode health: %v", err)
		}
		return body.Data.Facets
	}

	// No revisions: all three unknown.
	resp := f.api.Get(healthPath, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("health (no head) = %d, want 200\n%s", resp.Code, resp.Body.String())
	}
	facets := decode(resp.Body)
	if len(facets) != 3 {
		t.Fatalf("want 3 facets, got %d", len(facets))
	}
	for _, fc := range facets {
		if fc.Status != "unknown" {
			t.Errorf("before any revision facet %s = %s, want unknown", fc.Name, fc.Status)
		}
	}

	// Commit a valid spec.
	if _, err := svc.SubmitCandidate(ctx, p.ID, validSpec(t, "V1", "v1"), ownerID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	facets = decode(f.api.Get(healthPath, cookie).Body)
	got := map[string]string{}
	for _, fc := range facets {
		got[fc.Name] = fc.Status
	}
	if got["Spec"] != "ok" || got["Extension ABI"] != "ok" {
		t.Errorf("committed spec health: Spec=%s ABI=%s, want ok/ok", got["Spec"], got["Extension ABI"])
	}
	if got["Runtime Build"] != "unknown" {
		t.Errorf("Runtime Build = %s, want unknown (Cloud owns the build)", got["Runtime Build"])
	}
}

// TestProjectAPIHidesOtherOrgProjects: a user who is not a member of a project's
// org gets 404, so project ids cannot be probed across the tenancy boundary.
func TestProjectAPIHidesOtherOrgProjects(t *testing.T) {
	f := newHTTPFixture(t)
	ctx := context.Background()

	ownerID := f.user(t, "owner2@example.test")
	o, err := f.orgSvc.CreateOrganization(ctx, "Acme", "acme", ownerID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	p, err := project.NewService(f.db).CreateProject(ctx, o.ID, "Secret", "secret", ownerID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	outsiderID := f.user(t, "outsider@example.test")
	getPath := "/api/v1/projects/" + strconv.FormatUint(uint64(p.ID), 10)
	resp := f.api.Get(getPath, f.cookie(t, outsiderID))
	if resp.Code != http.StatusNotFound {
		t.Errorf("outsider get = %d, want 404 (not 403, to avoid leaking existence)\n%s", resp.Code, resp.Body.String())
	}
}
