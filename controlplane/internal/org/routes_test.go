package org_test

// HTTP-layer tests: exercise the real org operations over a humatest API with
// the real cookie-session gate, so the wiring the service tests bypass — the
// gate attachment and auth.UserFromContext(ctx).Email — is actually covered.
//
// Docker-gated (needs a users table to mint and validate sessions), like the
// service tests. It still pins the single regression most worth pinning on an
// RBAC feature: that every operation is behind the gate. Delete Middlewares
// from any route in RegisterRoutes and TestRoutesEnforceGate fails.

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
)

// httpFixture is a humatest API wired with the real gate over a real DB, plus
// helpers to mint session cookies for seeded users.
type httpFixture struct {
	api     humatest.TestAPI
	authSvc *auth.Service
	svc     *org.Service
	db      *gorm.DB
}

func newHTTPFixture(t *testing.T) *httpFixture {
	t.Helper()
	db := dbtest.DB(t)
	cfg := config.Config{Auth: config.AuthConfig{
		JWTSecret:       "org-routes-test-secret-please-change-0001",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
		Mode:            config.AuthModeCookie,
	}}
	authSvc, err := auth.NewService(db, cfg)
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	svc := org.NewService(db)
	_, api := humatest.New(t)
	org.RegisterRoutes(api, "/api/v1", huma.Middlewares{authSvc.RequireCookieSession()}, svc)
	return &httpFixture{api: api, authSvc: authSvc, svc: svc, db: db}
}

// sessionCookie mints a valid cookie-mode access token for the user, formatted
// as the "Cookie:" header humatest sends verbatim.
func (f *httpFixture) sessionCookie(t *testing.T, userID uint) string {
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

// TestRoutesEnforceGate pins "every operation is behind a gate" and — crucially
// — runs in CI. It uses a sentinel gate that rejects before any handler, so it
// needs no database and is not skipped under -short (unlike the Docker-gated
// tests below). Delete Middlewares from any route in RegisterRoutes and one of
// these operations reaches the (nil) handler instead of the gate, failing here.
// Which gate the production wiring chooses — the real cookie session — is the
// four eyeball-verifiable lines in Register, exercised by TestInviteRouteAuthorizes.
func TestRoutesEnforceGate(t *testing.T) {
	ops := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/organizations"},
		{http.MethodGet, "/api/v1/organizations"},
		{http.MethodGet, "/api/v1/organizations/1/members"},
		{http.MethodPost, "/api/v1/organizations/1/invitations"},
		{http.MethodPost, "/api/v1/invitations/accept"},
	}

	var gated int
	sentinel := func(ctx huma.Context, next func(huma.Context)) {
		gated++
		ctx.SetStatus(http.StatusUnauthorized) // reject; never call next
	}
	_, api := humatest.New(t)
	// svc is nil deliberately: the gate rejects before any handler runs, which
	// is exactly the invariant under test.
	org.RegisterRoutes(api, "/api/v1", huma.Middlewares{sentinel}, nil)

	for _, op := range ops {
		if resp := api.Do(op.method, op.path); resp.Code != http.StatusUnauthorized {
			t.Errorf("%s %s reached the handler; gate not attached (got %d)", op.method, op.path, resp.Code)
		}
	}
	if gated != len(ops) {
		t.Errorf("gate ran %d times, want %d (some operation is ungated)", gated, len(ops))
	}
}

// TestInviteRouteAuthorizes: with a real session, an owner may invite and a
// plain member may not — exercising the handler's auth.UserFromContext wiring
// end to end, not the service in isolation.
func TestInviteRouteAuthorizes(t *testing.T) {
	f := newHTTPFixture(t)
	ctx := context.Background()

	ownerID := seedUser(t, f.db, "owner@example.test")
	o, err := f.svc.CreateOrganization(ctx, "Acme", "acme", ownerID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	invitePath := "/api/v1/organizations/" + strconv.FormatUint(uint64(o.ID), 10) + "/invitations"

	// Owner can invite; the response carries the one-time token, proving the
	// handler ran and the session email flowed through UserFromContext into the
	// authorized path.
	resp := f.api.Post(invitePath, f.sessionCookie(t, ownerID),
		map[string]any{"email": "invitee@example.test", "role": "member"})
	if resp.Code != http.StatusOK {
		t.Fatalf("owner invite = %d, want 200\n%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode invite response: %v", err)
	}
	if body.Data.Token == "" {
		t.Error("invite response missing token")
	}

	// A plain member cannot invite. Make one directly, then try over HTTP.
	memberID := seedUser(t, f.db, "member@example.test")
	if err := f.db.Create(&org.Member{OrganizationID: o.ID, UserID: memberID, Role: org.RoleMember}).Error; err != nil {
		t.Fatalf("make member: %v", err)
	}
	resp = f.api.Post(invitePath, f.sessionCookie(t, memberID),
		map[string]any{"email": "another@example.test", "role": "member"})
	if resp.Code != http.StatusForbidden {
		t.Errorf("member invite = %d, want 403\n%s", resp.Code, resp.Body.String())
	}
}
