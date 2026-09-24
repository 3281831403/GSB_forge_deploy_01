package githubconnect

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrInvalidState means the callback's state is missing, expired, already
	// used, or bound to a different user — the flow refuses to proceed.
	ErrInvalidState = errors.New("githubconnect: invalid or expired oauth state")
	// ErrNotConnected means the user has no stored GitHub connection.
	ErrNotConnected = errors.New("githubconnect: user has no github connection")
)

// stateTTL bounds how long a pending connect may sit between the redirect and
// the callback.
const stateTTL = 10 * time.Minute

// Exchanger is the GitHub OAuth surface the flow needs, injected so the service
// is tested without real GitHub. The production adapter wraps
// internal/githubexport (AuthorizeURL + ExchangeCode).
type Exchanger interface {
	AuthorizeURL(state string) string
	ExchangeCode(ctx context.Context, code string) (token string, err error)
}

// Service runs the OAuth connect flow and stores connections.
type Service struct {
	db  *gorm.DB
	gh  Exchanger
	now func() time.Time
}

// NewService builds the service over db and the GitHub exchanger.
func NewService(db *gorm.DB, gh Exchanger) *Service {
	return &Service{db: db, gh: gh, now: time.Now}
}

// StartConnect mints a cryptographically-random state bound to userID, stores
// it, and returns the GitHub authorize URL to redirect the user to.
func (s *Service) StartConnect(ctx context.Context, userID uint) (string, error) {
	state, err := randomState()
	if err != nil {
		return "", err
	}
	row := OAuthState{State: state, UserID: userID, ExpiresAt: s.now().Add(stateTTL)}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return "", err
	}
	return s.gh.AuthorizeURL(state), nil
}

// CompleteConnect verifies and consumes the callback state, exchanges the code
// for a token, and stores the connection.
//
// The state must exist, be unexpired, and be bound to userID (the caller the
// cookie session establishes), and it is consumed exactly once — so a missing,
// expired, replayed, or cross-user state is rejected as ErrInvalidState and an
// attacker cannot bind their GitHub account to a victim's session. The code is
// exchanged only after the state is validated and consumed.
func (s *Service) CompleteConnect(ctx context.Context, userID uint, state, code string) error {
	if err := s.consumeState(ctx, userID, state); err != nil {
		return err
	}
	token, err := s.gh.ExchangeCode(ctx, code)
	if err != nil {
		return err
	}
	conn := Connection{UserID: userID, AccessToken: token}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"access_token", "updated_at"}),
	}).Create(&conn).Error
}

// consumeState atomically validates and deletes the state row so it can be used
// only once. The row is locked FOR UPDATE inside the transaction, so two
// concurrent callbacks with the same state cannot both succeed.
func (s *Service) consumeState(ctx context.Context, userID uint, state string) error {
	if state == "" {
		return ErrInvalidState
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var st OAuthState
		res := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("state = ?", state).First(&st)
		if errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return ErrInvalidState
		}
		if res.Error != nil {
			return res.Error
		}
		// A state bound to a different user is a CSRF attempt: reject it, and do
		// not delete it — it is the legitimate user's to consume.
		if st.UserID != userID {
			return ErrInvalidState
		}
		if s.now().After(st.ExpiresAt) {
			// Expired: consume it so it can't linger, and reject.
			if err := tx.Delete(&st).Error; err != nil {
				return err
			}
			return ErrInvalidState
		}
		return tx.Delete(&st).Error
	})
}

// Token returns the user's stored GitHub access token, or ErrNotConnected.
func (s *Service) Token(ctx context.Context, userID uint) (string, error) {
	var conn Connection
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&conn).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrNotConnected
	}
	if err != nil {
		return "", err
	}
	return conn.AccessToken, nil
}

// randomState returns 32 bytes of crypto-random, URL-safe state.
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
