package org

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/audit"
)

// Sentinel errors the HTTP layer maps to status codes. Returning typed errors
// keeps the service transport-agnostic.
var (
	// ErrForbidden means the caller's role does not permit the action.
	ErrForbidden = errors.New("org: forbidden")
	// ErrNotMember means the caller has no membership in the organization.
	ErrNotMember = errors.New("org: not a member")
	// ErrInvitationInvalid means the token matched no pending, unexpired
	// invitation.
	ErrInvitationInvalid = errors.New("org: invitation invalid or expired")
	// ErrAlreadyMember means the invitee already belongs to the organization.
	ErrAlreadyMember = errors.New("org: already a member")
	// ErrInvalidRole means a caller-supplied role is not a known Role.
	ErrInvalidRole = errors.New("org: invalid role")
	// ErrRoleExceedsGranter means the inviter tried to grant a role above its
	// own standing (e.g. an admin inviting an owner).
	ErrRoleExceedsGranter = errors.New("org: cannot grant a role above your own")
	// ErrInvitationPending means a non-accepted invitation already exists for
	// this address in this organization. Re-inviting supersedes it in the normal
	// path, so this only surfaces on a concurrent race between two invites to
	// the same address.
	ErrInvitationPending = errors.New("org: invitation already pending")
)

// DefaultInvitationTTL is how long an invitation stays acceptable.
const DefaultInvitationTTL = 7 * 24 * time.Hour

// Service holds the org tenancy operations. The clock and token source are
// injectable so tests are deterministic.
type Service struct {
	db            *gorm.DB
	now           func() time.Time
	newToken      func() (string, error)
	invitationTTL time.Duration
}

// NewService builds a Service over db with production defaults: the system
// clock, cryptographically random invitation tokens, and DefaultInvitationTTL.
func NewService(db *gorm.DB) *Service {
	return &Service{
		db:            db,
		now:           time.Now,
		newToken:      randomToken,
		invitationTTL: DefaultInvitationTTL,
	}
}

// CreateOrganization creates an organization and makes creator its first owner,
// atomically. A half-created org with no owner would be unreachable, so both
// rows commit together or not at all.
func (s *Service) CreateOrganization(ctx context.Context, name, slug string, creatorUserID uint) (Organization, error) {
	o := Organization{Name: name, Slug: slug}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&o).Error; err != nil {
			return err
		}
		owner := Member{OrganizationID: o.ID, UserID: creatorUserID, Role: RoleOwner}
		return tx.Create(&owner).Error
	})
	if err != nil {
		return Organization{}, fmt.Errorf("create organization: %w", err)
	}
	return o, nil
}

// MemberRole returns the caller's role in the organization. The bool is false
// (with a nil error) when the user is not a member.
func (s *Service) MemberRole(ctx context.Context, orgID, userID uint) (Role, bool, error) {
	var m Member
	err := s.db.WithContext(ctx).
		Where("organization_id = ? AND user_id = ?", orgID, userID).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("lookup membership: %w", err)
	}
	return m.Role, true, nil
}

// Authorize resolves the user's role in the org and checks it against the
// capability. It returns ErrNotMember when there is no membership and
// ErrForbidden when the role lacks the capability, so callers can fail closed
// without duplicating the lookup. Every org-scoped decision goes through this
// or its grant-aware sibling AuthorizeGrant — a handler never re-derives the
// role itself.
func (s *Service) Authorize(ctx context.Context, orgID, userID uint, capability Capability) error {
	_, err := s.authorize(ctx, orgID, userID, capability)
	return err
}

// AuthorizeGrant is Authorize for an operation that hands out a role: it checks
// the capability and, additionally, that the caller may grant targetRole (a
// member may only grant a role at or below its own — otherwise an admin could
// mint an owner and escalate past its own ceiling). Inviting goes through here
// rather than composing Can + CanGrant at the call site, so the two gates
// cannot drift apart or be forgotten by the next grant-shaped operation.
//
// It returns the caller's resolved role so a grant-shaped operation can apply
// the same CanGrant bound to an *existing* row it is about to disturb — e.g.
// InviteMember must not supersede a pending invitation whose role it could not
// have issued. Without the role in hand, that second check needs a second
// lookup, which is the duplication authorize was extracted to avoid.
func (s *Service) AuthorizeGrant(ctx context.Context, orgID, userID uint, capability Capability, targetRole Role) (Role, error) {
	role, err := s.authorize(ctx, orgID, userID, capability)
	if err != nil {
		return "", err
	}
	if !CanGrant(role, targetRole) {
		return "", ErrRoleExceedsGranter
	}
	return role, nil
}

// authorize is the shared resolution both public gates build on: it returns the
// caller's role alongside the capability decision so AuthorizeGrant can apply a
// second, role-aware check without a second lookup.
func (s *Service) authorize(ctx context.Context, orgID, userID uint, capability Capability) (Role, error) {
	role, ok, err := s.MemberRole(ctx, orgID, userID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotMember
	}
	if !Can(role, capability) {
		return "", ErrForbidden
	}
	return role, nil
}

// InviteMember creates a pending invitation for email to join the org with the
// given role, authorizing the inviter first, and records a member.invited audit
// event in the same transaction as the invitation — so the event exists iff the
// invitation does. It returns the invitation and the raw token, which is the
// only time the token is available in the clear; only its hash is stored.
func (s *Service) InviteMember(ctx context.Context, orgID, inviterUserID uint, email string, role Role) (Invitation, string, error) {
	if !role.Valid() {
		return Invitation{}, "", ErrInvalidRole
	}
	// Normalize the address once, here at the boundary, exactly as Gombit's
	// auth.createUser does — so an invitation's Email and a users.email row are
	// the same string or neither, and every downstream comparison can be plain
	// equality. Without this, "Owner@x" slips past the already-a-member guard
	// (which compares to the lowercased users row) and a trailing space mints an
	// invitation no user can ever match.
	email = normalizeEmail(email)
	// The inviter must be permitted to invite and not be granting above their
	// own standing; AuthorizeGrant enforces both in one place and hands back the
	// inviter's role for the supersede bound below.
	inviterRole, err := s.AuthorizeGrant(ctx, orgID, inviterUserID, CapMembersInvite, role)
	if err != nil {
		return Invitation{}, "", err
	}

	// A person who is already a member cannot be invited again.
	invited, err := s.userByEmailIsMember(ctx, orgID, email)
	if err != nil {
		return Invitation{}, "", err
	}
	if invited {
		return Invitation{}, "", ErrAlreadyMember
	}

	raw, err := s.newToken()
	if err != nil {
		return Invitation{}, "", fmt.Errorf("mint invitation token: %w", err)
	}
	inv := Invitation{
		OrganizationID:  orgID,
		Email:           email,
		Role:            role,
		TokenHash:       hashToken(raw),
		InvitedByUserID: inviterUserID,
		ExpiresAt:       s.now().Add(s.invitationTTL),
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Re-inviting an address supersedes any non-accepted invitation for it:
		// the old token dies, the new one carries the new role and expiry. This
		// is the only transition out of "expired", and without it the partial
		// unique index on (organization_id, email) would lock the address
		// permanently the moment an invitation went unaccepted for its TTL.
		//
		// Superseding destroys someone else's grant, so it is bounded by the same
		// hierarchy that bounds making one: an admin may not erase (and rewrite)
		// an owner-level invitation any more than it may issue one. Look before
		// deleting.
		var existing Invitation
		switch err := tx.Where("organization_id = ? AND email = ? AND accepted_at IS NULL",
			orgID, email).First(&existing).Error; {
		case errors.Is(err, gorm.ErrRecordNotFound):
			// nothing to supersede
		case err != nil:
			return err
		default:
			if !CanGrant(inviterRole, existing.Role) {
				return ErrRoleExceedsGranter
			}
			if err := tx.Delete(&existing).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&inv).Error; err != nil {
			// After the supersede delete, the only way to collide on the index
			// is a concurrent invite to the same address. Surface it as a typed
			// sentinel rather than leaking gorm.ErrDuplicatedKey (which mapError
			// cannot tell apart from a slug collision).
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return ErrInvitationPending
			}
			return err
		}
		org := orgID
		actor := inviterUserID
		// Record who was invited (the normalized address), not the invitation
		// row id: §23's question is "who was invited to org X, by whom", and the
		// answer must survive deletion of the mutable invitations row. The email
		// is not a secret value, so §23's constraint permits it. (The granted
		// role is the other security-relevant fact; carrying per-action detail
		// like that is the audit schema #40 owns, not this minimal seam.)
		return audit.Record(ctx, tx, audit.Event{
			OrganizationID: &org,
			ActorUserID:    &actor,
			Action:         audit.ActionMemberInvited,
			TargetType:     "member",
			TargetID:       email,
		})
	})
	if err != nil {
		// Preserve the typed sentinels the transaction may surface so callers
		// (and mapError) classify them; wrap only genuinely unexpected errors.
		if errors.Is(err, ErrRoleExceedsGranter) || errors.Is(err, ErrInvitationPending) {
			return Invitation{}, "", err
		}
		return Invitation{}, "", fmt.Errorf("invite member: %w", err)
	}
	return inv, raw, nil
}

// AcceptInvitation redeems a raw invitation token for the accepting user,
// creating their membership and marking the invitation accepted, atomically.
//
// The invitation is addressed to a specific email (§22), and redemption is
// bound to it: the accepting user's email must equal the invitation's. This is
// a misdelivery/typo guard that ties a redemption to the address the invite
// names — it is NOT a defense against a leaked token. Gombit exposes ungated
// self-registration and does not verify email ownership, and there is no
// mailer (InviteMember hands the raw token back to the inviter), so anyone who
// holds the token can register under the invited address and pass this check.
// Treat the token as the credential; this binding is the second factor only in
// the narrow case where the invitee already has an account.
//
// Both strings are normalized (InviteMember stored a normalized address; here
// we normalize the caller's), so the comparison is plain equality. It fails
// closed: unknown, expired, already-accepted, or wrong-recipient are all
// ErrInvitationInvalid, so there is no "valid token, not for you" oracle.
// userEmail is the authenticated caller's email (from the session), not
// caller-supplied.
func (s *Service) AcceptInvitation(ctx context.Context, rawToken, userEmail string, userID uint) (Member, error) {
	var member Member
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var inv Invitation
		lookup := tx.Where("token_hash = ? AND accepted_at IS NULL AND expires_at > ?",
			hashToken(rawToken), s.now()).First(&inv)
		if errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
			return ErrInvitationInvalid
		}
		if lookup.Error != nil {
			return lookup.Error
		}
		// Both sides are normalized, so this is Gombit's own email relation.
		if normalizeEmail(userEmail) != inv.Email {
			return ErrInvitationInvalid
		}

		member = Member{OrganizationID: inv.OrganizationID, UserID: userID, Role: inv.Role}
		if err := tx.Create(&member).Error; err != nil {
			return err
		}
		acceptedAt := s.now()
		return tx.Model(&inv).Update("accepted_at", &acceptedAt).Error
	})
	if err != nil {
		if errors.Is(err, ErrInvitationInvalid) {
			return Member{}, err
		}
		return Member{}, fmt.Errorf("accept invitation: %w", err)
	}
	return member, nil
}

// Members lists an organization's members in a stable order (oldest first).
// OrgsForUser returns the organizations the user is a member of, in creation
// order — the "which orgs can I open?" query the editor's org picker needs. It
// joins memberships to organizations rather than loading associations, so it
// stays a single query and returns only orgs the caller actually belongs to.
func (s *Service) OrgsForUser(ctx context.Context, userID uint) ([]Organization, error) {
	var orgs []Organization
	err := s.db.WithContext(ctx).
		Table("organizations AS o").
		Joins("JOIN organization_members AS m ON m.organization_id = o.id").
		Where("m.user_id = ?", userID).
		Order("o.id").
		Find(&orgs).Error
	if err != nil {
		return nil, fmt.Errorf("list user orgs: %w", err)
	}
	return orgs, nil
}

func (s *Service) Members(ctx context.Context, orgID uint) ([]Member, error) {
	var members []Member
	err := s.db.WithContext(ctx).
		Where("organization_id = ?", orgID).
		Order("id").
		Find(&members).Error
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	return members, nil
}

// userByEmailIsMember reports whether the email belongs to a Gombit user who is
// already a member of the org. It joins on the auth users table by email rather
// than importing the auth model, so org has no compile coupling to auth's
// schema beyond the well-known table and columns. The email is normalized so
// the SQL "=" is Gombit's own email relation (users.email is stored normalized).
func (s *Service) userByEmailIsMember(ctx context.Context, orgID uint, email string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).
		Table("organization_members AS m").
		Joins("JOIN users AS u ON u.id = m.user_id").
		Where("m.organization_id = ? AND u.email = ?", orgID, normalizeEmail(email)).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("check existing membership: %w", err)
	}
	return count > 0, nil
}

// normalizeEmail matches Gombit's auth.createUser (lowercase + trim), so an
// invitation address and a users.email row are the same string or neither. It
// is applied at every boundary where an address enters the package — the
// InviteMember argument, the AcceptInvitation caller email, the
// userByEmailIsMember lookup. It is idempotent, so normalizing twice is
// harmless and no call site has to reason about whether an earlier one already
// did it.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// randomToken returns a 256-bit URL-safe random token.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is the at-rest representation of an invitation token: a hex SHA-256,
// so the database never holds a usable credential.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
