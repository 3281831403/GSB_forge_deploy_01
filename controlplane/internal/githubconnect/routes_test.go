package githubconnect_test

// HTTP-layer tests: exercise the real connect operations over a humatest API
// with the real cookie-session gate and the migrated schema, so the wiring the
// service tests bypass — the gate, auth.UserFromContext, the 302 redirects and
// the query binding — is actually covered. A fake Exchanger stands in for
// GitHub so no network is touched.
//
// TestRoutesEnforceGate needs no database and runs in CI (not -short-skipped);
// the end-to-end tests are Docker-gated like the service tests.

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/gombit-dev/gombit/auth"
	"github.com/gombit-dev/gombit/config"
	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/githubconnect"
)

// fakeExchanger is a deterministic GitHub OAuth stand-in: it embeds the state in
// the authorize URL (so a test can recover it) and returns a fixed token.
type fakeExchanger struct{ token string }

func (fakeExchanger) AuthorizeURL(state string) string {
	return "https://github.test/login/oauth/authorize?state=" + url.QueryEscape(state)
}
func (f fakeExchanger) ExchangeCode(context.Context, string) (string, error) {
	return f.token, nil
}

const successRedirect = "/integrations"

type httpFixture struct {
	api     humatest.TestAPI
	authSvc *auth.Service
	db      *gorm.DB
}

func newHTTPFixture(t *testing.T) *httpFixture {
	t.Helper()
	db := dbtest.DB(t)
	cfg := config.Config{Auth: config.AuthConfig{
		JWTSecret:       "connect-routes-test-secret-please-change-01",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
		Mode:            config.AuthModeCookie,
	}}
	authSvc, err := auth.NewService(db, cfg)
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	svc := githubconnect.NewService(db, fakeExchanger{token: "gho_fake_token"})
	_, api := humatest.New(t)
	githubconnect.RegisterRoutes(api, "/api/v1",
		huma.Middlewares{authSvc.RequireCookieSession()}, svc, successRedirect)
	return &httpFixture{api: api, authSvc: authSvc, db: db}
}

func seedUser(t *testing.T, db *gorm.DB, email string) uint {
	t.Helper()
	u := auth.User{Email: email, PasswordHash: "x"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return u.ID
}

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

const (
	connectPath  = "/api/v1/integrations/github/connect"
	callbackPath = "/api/v1/integrations/github/callback"
)

// TestRoutesEnforceGate pins "every operation is behind a gate" and runs in CI:
// a sentinel gate rejects before any handler, so it needs no database. Remove
// Middlewares from either route and it reaches the (nil) handler and fails here.
func TestRoutesEnforceGate(t *testing.T) {
	ops := []struct{ method, path string }{
		{http.MethodGet, connectPath},
		{http.MethodGet, callbackPath},
	}
	var gated int
	sentinel := func(ctx huma.Context, next func(huma.Context)) {
		gated++
		ctx.SetStatus(http.StatusUnauthorized) // reject; never call next
	}
	_, api := humatest.New(t)
	githubconnect.RegisterRoutes(api, "/api/v1", huma.Middlewares{sentinel}, nil, successRedirect)

	for _, op := range ops {
		if resp := api.Do(op.method, op.path); resp.Code != http.StatusUnauthorized {
			t.Errorf("%s %s reached the handler; gate not attached (got %d)", op.method, op.path, resp.Code)
		}
	}
	if gated != len(ops) {
		t.Errorf("gate ran %d times, want %d (some operation is ungated)", gated, len(ops))
	}
}

// TestConnectStartRedirects: an authenticated start mints and stores a state and
// 302-redirects to GitHub's authorize URL carrying that state.
func TestConnectStartRedirects(t *testing.T) {
	f := newHTTPFixture(t)
	userID := seedUser(t, f.db, "connector@example.test")

	resp := f.api.Get(connectPath, f.sessionCookie(t, userID))
	if resp.Code != http.StatusFound {
		t.Fatalf("start = %d, want 302\n%s", resp.Code, resp.Body.String())
	}
	loc := resp.Header().Get("Location")
	if loc == "" {
		t.Fatal("start must set a Location header")
	}
	u, err := url.Parse(loc)
	if err != nil || u.Host != "github.test" || u.Query().Get("state") == "" {
		t.Fatalf("Location = %q, want a github.test authorize URL with a state", loc)
	}
	// The state was persisted bound to the user.
	var count int64
	f.db.Table("o_auth_states").Where("user_id = ?", userID).Count(&count)
	if count != 1 {
		t.Errorf("stored oauth states for user = %d, want 1", count)
	}
}

// TestConnectCallbackStoresConnection drives the whole flow: start to mint a
// real state, then the callback with that state and a code stores the exchanged
// token and redirects to the success page.
func TestConnectCallbackStoresConnection(t *testing.T) {
	f := newHTTPFixture(t)
	userID := seedUser(t, f.db, "callback@example.test")
	cookie := f.sessionCookie(t, userID)

	start := f.api.Get(connectPath, cookie)
	u, _ := url.Parse(start.Header().Get("Location"))
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("no state minted by start")
	}

	resp := f.api.Get(callbackPath+"?code=the-code&state="+url.QueryEscape(state), cookie)
	if resp.Code != http.StatusFound {
		t.Fatalf("callback = %d, want 302\n%s", resp.Code, resp.Body.String())
	}
	if loc := resp.Header().Get("Location"); loc != successRedirect {
		t.Errorf("callback Location = %q, want %q", loc, successRedirect)
	}
	// The connection was stored with the exchanged token, and the state consumed.
	token, err := githubconnect.NewService(f.db, fakeExchanger{}).Token(context.Background(), userID)
	if err != nil || token != "gho_fake_token" {
		t.Errorf("stored token = %q, err = %v; want gho_fake_token", token, err)
	}
	var states int64
	f.db.Table("o_auth_states").Where("user_id = ?", userID).Count(&states)
	if states != 0 {
		t.Errorf("state must be consumed; remaining = %d", states)
	}
}

// TestConnectCallbackRejectsBadState: an unknown/forged state is a validation
// error and stores no connection.
func TestConnectCallbackRejectsBadState(t *testing.T) {
	f := newHTTPFixture(t)
	userID := seedUser(t, f.db, "forger@example.test")

	resp := f.api.Get(callbackPath+"?code=x&state=never-minted", f.sessionCookie(t, userID))
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("callback with bad state = %d, want 422\n%s", resp.Code, resp.Body.String())
	}
	if _, err := githubconnect.NewService(f.db, fakeExchanger{}).Token(context.Background(), userID); err == nil {
		t.Error("no connection should be stored for a rejected callback")
	}
}

// TestConnectCallbackRejectsCrossUserSession pins the security-critical wiring
// at the HTTP layer: the callback binds the connection to the *session's* user,
// not the state's minter. User A mints a state; a callback replaying A's state
// under B's cookie is rejected (the state is A-bound), storing nothing for B —
// and A's state survives, so the legitimate user can still complete.
func TestConnectCallbackRejectsCrossUserSession(t *testing.T) {
	f := newHTTPFixture(t)
	userA := seedUser(t, f.db, "victim@example.test")
	userB := seedUser(t, f.db, "attacker@example.test")

	start := f.api.Get(connectPath, f.sessionCookie(t, userA))
	u, _ := url.Parse(start.Header().Get("Location"))
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("no state minted by start")
	}

	resp := f.api.Get(callbackPath+"?code=the-code&state="+url.QueryEscape(state), f.sessionCookie(t, userB))
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross-user callback = %d, want 422\n%s", resp.Code, resp.Body.String())
	}
	svc := githubconnect.NewService(f.db, fakeExchanger{})
	if _, err := svc.Token(context.Background(), userB); err == nil {
		t.Error("attacker B must not have a stored connection")
	}
	// A's state was not consumed by the cross-user attempt: A can still complete.
	var states int64
	f.db.Table("o_auth_states").Where("user_id = ?", userA).Count(&states)
	if states != 1 {
		t.Errorf("victim's state must survive a cross-user attempt; remaining = %d", states)
	}
}

// TestConnectCallbackRequiresParams: a callback missing code or state is a 422,
// not a 500 or a redirect.
func TestConnectCallbackRequiresParams(t *testing.T) {
	f := newHTTPFixture(t)
	userID := seedUser(t, f.db, "noparams@example.test")

	resp := f.api.Get(callbackPath+"?state=only-state", f.sessionCookie(t, userID))
	if resp.Code != http.StatusUnprocessableEntity {
		t.Errorf("callback missing code = %d, want 422", resp.Code)
	}
}
