package githubconnect

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
)

type fakeExchanger struct {
	lastState string
	token     string
	err       error
}

func (f *fakeExchanger) AuthorizeURL(state string) string {
	f.lastState = state
	return "https://github.test/login/oauth/authorize?state=" + state
}

func (f *fakeExchanger) ExchangeCode(_ context.Context, _ string) (string, error) {
	return f.token, f.err
}

// newService builds a service over a fresh test DB with the connect tables
// migrated (they are not yet in the committed control-plane migration; the
// wiring PR adds that), a controllable clock, and a fake GitHub exchanger.
func newService(t *testing.T) (*Service, *fakeExchanger, *gorm.DB, *time.Time) {
	t.Helper()
	// dbtest.DB applies the committed migration set, which now includes the
	// connections and o_auth_states tables (#85) — no in-test AutoMigrate.
	db := dbtest.DB(t)
	gh := &fakeExchanger{token: "gho_token"}
	svc := NewService(db, gh)
	now := time.Now()
	clock := &now
	svc.now = func() time.Time { return *clock }
	return svc, gh, db, clock
}

func TestStartConnectMintsAndStoresState(t *testing.T) {
	svc, gh, db, _ := newService(t)
	ctx := context.Background()

	url, err := svc.StartConnect(ctx, 42)
	if err != nil {
		t.Fatalf("start connect: %v", err)
	}
	if gh.lastState == "" || !strings.Contains(url, gh.lastState) {
		t.Errorf("authorize url %q must carry the minted state %q", url, gh.lastState)
	}
	var st OAuthState
	if err := db.Where("state = ?", gh.lastState).First(&st).Error; err != nil {
		t.Fatalf("state not stored: %v", err)
	}
	if st.UserID != 42 {
		t.Errorf("stored state user = %d, want 42", st.UserID)
	}
	// A second call mints a distinct state (random, not fixed).
	if _, err := svc.StartConnect(ctx, 42); err != nil {
		t.Fatalf("second start: %v", err)
	}
	first := gh.lastState
	if _, err := svc.StartConnect(ctx, 42); err != nil {
		t.Fatalf("third start: %v", err)
	}
	if gh.lastState == first {
		t.Error("state must be random per connect, not fixed")
	}
}

func TestCompleteConnectStoresTokenAndConsumesState(t *testing.T) {
	svc, gh, db, _ := newService(t)
	ctx := context.Background()

	if _, err := svc.StartConnect(ctx, 7); err != nil {
		t.Fatalf("start: %v", err)
	}
	state := gh.lastState

	if err := svc.CompleteConnect(ctx, 7, state, "the-code"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	// The token is stored.
	token, err := svc.Token(ctx, 7)
	if err != nil || token != "gho_token" {
		t.Errorf("stored token = %q err=%v, want gho_token", token, err)
	}
	// The state is consumed: replay fails.
	if err := svc.CompleteConnect(ctx, 7, state, "the-code"); !errors.Is(err, ErrInvalidState) {
		t.Errorf("replayed state = %v, want ErrInvalidState", err)
	}
	// And the row is gone.
	var count int64
	db.Model(&OAuthState{}).Where("state = ?", state).Count(&count)
	if count != 0 {
		t.Error("consumed state row must be deleted")
	}
}

func TestCompleteConnectRejectsUnknownState(t *testing.T) {
	svc, _, _, _ := newService(t)
	if err := svc.CompleteConnect(context.Background(), 7, "nope", "code"); !errors.Is(err, ErrInvalidState) {
		t.Errorf("unknown state = %v, want ErrInvalidState", err)
	}
}

// TestCompleteConnectRejectsCrossUserState is the CSRF guard: a state minted for
// one user can't be completed by another, and the victim's state survives.
func TestCompleteConnectRejectsCrossUserState(t *testing.T) {
	svc, gh, db, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.StartConnect(ctx, 1); err != nil { // victim
		t.Fatalf("start: %v", err)
	}
	victimState := gh.lastState

	if err := svc.CompleteConnect(ctx, 2, victimState, "code"); !errors.Is(err, ErrInvalidState) {
		t.Errorf("cross-user state = %v, want ErrInvalidState", err)
	}
	// The victim's state must not be consumed by the attacker's attempt.
	var count int64
	db.Model(&OAuthState{}).Where("state = ?", victimState).Count(&count)
	if count != 1 {
		t.Error("a cross-user attempt must not delete the victim's state")
	}
	// The victim can still complete their own connect.
	if err := svc.CompleteConnect(ctx, 1, victimState, "code"); err != nil {
		t.Errorf("victim complete: %v", err)
	}
}

func TestCompleteConnectRejectsExpiredState(t *testing.T) {
	svc, gh, _, clock := newService(t)
	ctx := context.Background()
	if _, err := svc.StartConnect(ctx, 5); err != nil {
		t.Fatalf("start: %v", err)
	}
	state := gh.lastState
	*clock = clock.Add(stateTTL + time.Minute) // advance past expiry

	if err := svc.CompleteConnect(ctx, 5, state, "code"); !errors.Is(err, ErrInvalidState) {
		t.Errorf("expired state = %v, want ErrInvalidState", err)
	}
}

func TestCompleteConnectUpsertsConnection(t *testing.T) {
	svc, gh, db, _ := newService(t)
	ctx := context.Background()

	connect := func(token string) {
		gh.token = token
		if _, err := svc.StartConnect(ctx, 9); err != nil {
			t.Fatalf("start: %v", err)
		}
		if err := svc.CompleteConnect(ctx, 9, gh.lastState, "code"); err != nil {
			t.Fatalf("complete: %v", err)
		}
	}
	connect("token-1")
	connect("token-2")

	var count int64
	db.Model(&Connection{}).Where("user_id = ?", 9).Count(&count)
	if count != 1 {
		t.Errorf("want one connection per user, got %d", count)
	}
	if token, _ := svc.Token(ctx, 9); token != "token-2" {
		t.Errorf("re-connect must update the token; got %q, want token-2", token)
	}
}

func TestTokenNotConnected(t *testing.T) {
	svc, _, _, _ := newService(t)
	if _, err := svc.Token(context.Background(), 123); !errors.Is(err, ErrNotConnected) {
		t.Errorf("no connection = %v, want ErrNotConnected", err)
	}
}
