package org_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gombit-dev/gombit/auth"
	"gorm.io/gorm"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/audit"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/org"
)

// seedUser inserts a bare auth user and returns its id. The invitation flow
// keys off the Gombit users table (identity is Gombit's), so tests need real
// user rows.
func seedUser(t *testing.T, db *gorm.DB, email string) uint {
	t.Helper()
	u := auth.User{Email: email, PasswordHash: "x"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return u.ID
}

func TestCreateOrganizationMakesCreatorOwner(t *testing.T) {
	db := dbtest.DB(t)
	svc := org.NewService(db)
	ctx := context.Background()

	creator := seedUser(t, db, "founder@example.test")
	o, err := svc.CreateOrganization(ctx, "Acme", "acme", creator)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	role, ok, err := svc.MemberRole(ctx, o.ID, creator)
	if err != nil || !ok {
		t.Fatalf("creator membership: role=%q ok=%v err=%v", role, ok, err)
	}
	if role != org.RoleOwner {
		t.Errorf("creator role = %q, want owner", role)
	}
}

func TestInviteEmitsAuditEventAndAcceptCreatesMember(t *testing.T) {
	db := dbtest.DB(t)
	svc := org.NewService(db)
	ctx := context.Background()

	owner := seedUser(t, db, "owner@example.test")
	o, err := svc.CreateOrganization(ctx, "Acme", "acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	inv, token, err := svc.InviteMember(ctx, o.ID, owner, "invitee@example.test", org.RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if token == "" {
		t.Fatal("invite must return a raw token")
	}

	// The raw token is not stored; only its hash is. So the persisted row's
	// TokenHash must differ from the raw token.
	var stored org.Invitation
	if err := db.First(&stored, inv.ID).Error; err != nil {
		t.Fatalf("load invitation: %v", err)
	}
	if stored.TokenHash == token {
		t.Error("raw token must not be stored; only its hash")
	}

	// A member.invited audit event was recorded, scoped to the org and actor.
	var ev audit.Event
	if err := db.Where("action = ?", audit.ActionMemberInvited).First(&ev).Error; err != nil {
		t.Fatalf("expected a member.invited audit event: %v", err)
	}
	if ev.OrganizationID == nil || *ev.OrganizationID != o.ID {
		t.Errorf("audit org = %v, want %d", ev.OrganizationID, o.ID)
	}
	if ev.ActorUserID == nil || *ev.ActorUserID != owner {
		t.Errorf("audit actor = %v, want %d", ev.ActorUserID, owner)
	}

	// Accepting the invitation creates the invitee's membership with the
	// invited role.
	invitee := seedUser(t, db, "invitee@example.test")
	m, err := svc.AcceptInvitation(ctx, token, "invitee@example.test", invitee)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if m.Role != org.RoleMember || m.OrganizationID != o.ID || m.UserID != invitee {
		t.Errorf("membership = %+v, want org %d user %d role member", m, o.ID, invitee)
	}

	// The token is single-use: a second accept fails closed.
	if _, err := svc.AcceptInvitation(ctx, token, "invitee@example.test", invitee); err != org.ErrInvitationInvalid {
		t.Errorf("second accept err = %v, want ErrInvitationInvalid", err)
	}
}

// TestAcceptRequiresMatchingEmail proves acceptance is bound to the invited
// identity, not merely to possession of the token: a different logged-in user
// holding the token cannot redeem it, and gets the same opaque error as a bad
// token (no "valid, but not for you" oracle).
func TestAcceptRequiresMatchingEmail(t *testing.T) {
	db := dbtest.DB(t)
	svc := org.NewService(db)
	ctx := context.Background()

	owner := seedUser(t, db, "owner@example.test")
	o, err := svc.CreateOrganization(ctx, "Acme", "acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	_, token, err := svc.InviteMember(ctx, o.ID, owner, "invitee@example.test", org.RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}

	// A different user (with the token but the wrong email) is rejected.
	interloper := seedUser(t, db, "someone-else@example.test")
	if _, err := svc.AcceptInvitation(ctx, token, "someone-else@example.test", interloper); err != org.ErrInvitationInvalid {
		t.Errorf("wrong-email accept err = %v, want ErrInvitationInvalid", err)
	}

	// The intended recipient still can (and email match is case-insensitive).
	invitee := seedUser(t, db, "invitee@example.test")
	if _, err := svc.AcceptInvitation(ctx, token, "Invitee@Example.Test", invitee); err != nil {
		t.Fatalf("intended recipient accept: %v", err)
	}
}

// TestInviteCannotGrantAboveOwnRole proves the second authorization gate: an
// admin may invite (CapMembersInvite) but may not mint a role above its own.
func TestInviteCannotGrantAboveOwnRole(t *testing.T) {
	db := dbtest.DB(t)
	svc := org.NewService(db)
	ctx := context.Background()

	owner := seedUser(t, db, "owner@example.test")
	o, err := svc.CreateOrganization(ctx, "Acme", "acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Owner promotes someone to admin.
	_, adminTok, err := svc.InviteMember(ctx, o.ID, owner, "admin@example.test", org.RoleAdmin)
	if err != nil {
		t.Fatalf("owner invites admin: %v", err)
	}
	adminUser := seedUser(t, db, "admin@example.test")
	if _, err := svc.AcceptInvitation(ctx, adminTok, "admin@example.test", adminUser); err != nil {
		t.Fatalf("admin accepts: %v", err)
	}

	// The admin cannot invite an owner — that would escalate past its own ceiling.
	if _, _, err := svc.InviteMember(ctx, o.ID, adminUser, "puppet@example.test", org.RoleOwner); err != org.ErrRoleExceedsGranter {
		t.Errorf("admin inviting owner err = %v, want ErrRoleExceedsGranter", err)
	}
	// But the admin can invite at or below its own role.
	if _, _, err := svc.InviteMember(ctx, o.ID, adminUser, "peer@example.test", org.RoleAdmin); err != nil {
		t.Errorf("admin inviting admin err = %v, want nil", err)
	}
}

// TestAdminCannotSupersedeOwnerInvitation is the mirror of the create-side
// guard: superseding destroys a grant, so it is bounded by the same hierarchy.
// An admin must not be able to erase an owner's pending owner-level invitation
// by re-inviting the address at a lower role — and, crucially, the owner's
// invitation must remain redeemable afterward (an implementation that refuses
// but still deletes would pass an error-only assertion).
func TestAdminCannotSupersedeOwnerInvitation(t *testing.T) {
	db := dbtest.DB(t)
	svc := org.NewService(db)
	ctx := context.Background()

	owner := seedUser(t, db, "owner@example.test")
	o, err := svc.CreateOrganization(ctx, "Acme", "acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Owner promotes an admin.
	_, adminTok, err := svc.InviteMember(ctx, o.ID, owner, "admin@example.test", org.RoleAdmin)
	if err != nil {
		t.Fatalf("owner invites admin: %v", err)
	}
	adminUser := seedUser(t, db, "admin@example.test")
	if _, err := svc.AcceptInvitation(ctx, adminTok, "admin@example.test", adminUser); err != nil {
		t.Fatalf("admin accepts: %v", err)
	}

	// Owner invites carol as a co-owner.
	_, carolTok, err := svc.InviteMember(ctx, o.ID, owner, "carol@example.test", org.RoleOwner)
	if err != nil {
		t.Fatalf("owner invites carol as owner: %v", err)
	}

	// The admin cannot supersede that owner-level invitation with a lower grant.
	if _, _, err := svc.InviteMember(ctx, o.ID, adminUser, "carol@example.test", org.RoleMember); !errors.Is(err, org.ErrRoleExceedsGranter) {
		t.Errorf("admin superseding owner invite err = %v, want ErrRoleExceedsGranter", err)
	}

	// The owner's invitation must survive: carol can still redeem it, as owner.
	carol := seedUser(t, db, "carol@example.test")
	m, err := svc.AcceptInvitation(ctx, carolTok, "carol@example.test", carol)
	if err != nil {
		t.Fatalf("carol redeems owner invite: %v (was it wrongly deleted?)", err)
	}
	if m.Role != org.RoleOwner {
		t.Errorf("carol joined as %q, want owner", m.Role)
	}
}

func TestInviteRequiresPermission(t *testing.T) {
	db := dbtest.DB(t)
	svc := org.NewService(db)
	ctx := context.Background()

	owner := seedUser(t, db, "owner@example.test")
	o, err := svc.CreateOrganization(ctx, "Acme", "acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// A plain member cannot invite (CapMembersInvite is owner/admin only).
	memberUser := seedUser(t, db, "member@example.test")
	_, token, err := svc.InviteMember(ctx, o.ID, owner, "member@example.test", org.RoleMember)
	if err != nil {
		t.Fatalf("owner invite: %v", err)
	}
	if _, err := svc.AcceptInvitation(ctx, token, "member@example.test", memberUser); err != nil {
		t.Fatalf("accept: %v", err)
	}

	if _, _, err := svc.InviteMember(ctx, o.ID, memberUser, "someone@example.test", org.RoleMember); err != org.ErrForbidden {
		t.Errorf("member invite err = %v, want ErrForbidden", err)
	}

	// A non-member cannot invite either — ErrNotMember, not a leak.
	stranger := seedUser(t, db, "stranger@example.test")
	if _, _, err := svc.InviteMember(ctx, o.ID, stranger, "x@example.test", org.RoleMember); err != org.ErrNotMember {
		t.Errorf("stranger invite err = %v, want ErrNotMember", err)
	}
}

func TestInviteRejectsExistingMemberAndBadRole(t *testing.T) {
	db := dbtest.DB(t)
	svc := org.NewService(db)
	ctx := context.Background()

	owner := seedUser(t, db, "owner@example.test")
	o, err := svc.CreateOrganization(ctx, "Acme", "acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Owner is already a member, so inviting the owner's email is a conflict —
	// and the guard must hold under a different case and surrounding whitespace,
	// because addresses are normalized to Gombit's relation on the way in. (A
	// raw-string comparison would let "Owner@Example.test" slip past.)
	for _, variant := range []string{"owner@example.test", "Owner@Example.test", "  owner@example.test  "} {
		if _, _, err := svc.InviteMember(ctx, o.ID, owner, variant, org.RoleMember); err != org.ErrAlreadyMember {
			t.Errorf("invite existing member as %q err = %v, want ErrAlreadyMember", variant, err)
		}
	}

	// An unknown role is rejected before any write.
	if _, _, err := svc.InviteMember(ctx, o.ID, owner, "new@example.test", org.Role("superuser")); err != org.ErrInvalidRole {
		t.Errorf("invite bad role err = %v, want ErrInvalidRole", err)
	}
}

// TestInviteNormalizesAddressForRedemption is the reviewer's PROBE B: an
// invitation created with surrounding whitespace/casing must still be redeemable
// by the user who owns that (normalized) address — a raw-string store would mint
// an invitation no user row could ever match.
func TestInviteNormalizesAddressForRedemption(t *testing.T) {
	db := dbtest.DB(t)
	svc := org.NewService(db)
	ctx := context.Background()

	owner := seedUser(t, db, "owner@example.test")
	o, err := svc.CreateOrganization(ctx, "Acme", "acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	inv, token, err := svc.InviteMember(ctx, o.ID, owner, "  Invitee@Example.test  ", org.RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if inv.Email != "invitee@example.test" {
		t.Errorf("stored invitation email = %q, want normalized invitee@example.test", inv.Email)
	}

	// The user registered under the plain address (as Gombit would store it) can
	// redeem it.
	invitee := seedUser(t, db, "invitee@example.test")
	if _, err := svc.AcceptInvitation(ctx, token, "invitee@example.test", invitee); err != nil {
		t.Fatalf("normalized recipient accept: %v", err)
	}
}

// countPendingInvites returns how many non-accepted invitations exist for an
// address in an org — the slot the partial unique index guards.
func countPendingInvites(t *testing.T, db *gorm.DB, orgID uint, email string) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&org.Invitation{}).
		Where("organization_id = ? AND email = ? AND accepted_at IS NULL", orgID, email).
		Count(&n).Error; err != nil {
		t.Fatalf("count pending: %v", err)
	}
	return n
}

// TestReinviteSupersedes: re-inviting a still-pending address reissues rather
// than failing — the old token dies, the new one lives, and exactly one pending
// row remains (the partial unique index holds).
func TestReinviteSupersedes(t *testing.T) {
	db := dbtest.DB(t)
	svc := org.NewService(db)
	ctx := context.Background()

	owner := seedUser(t, db, "owner@example.test")
	o, err := svc.CreateOrganization(ctx, "Acme", "acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	_, oldTok, err := svc.InviteMember(ctx, o.ID, owner, "invitee@example.test", org.RoleMember)
	if err != nil {
		t.Fatalf("first invite: %v", err)
	}
	_, newTok, err := svc.InviteMember(ctx, o.ID, owner, "invitee@example.test", org.RoleAdmin)
	if err != nil {
		t.Fatalf("re-invite must supersede, not fail: %v", err)
	}
	if n := countPendingInvites(t, db, o.ID, "invitee@example.test"); n != 1 {
		t.Errorf("pending invitations = %d, want 1 (old superseded)", n)
	}

	invitee := seedUser(t, db, "invitee@example.test")
	// The old token is dead.
	if _, err := svc.AcceptInvitation(ctx, oldTok, "invitee@example.test", invitee); err != org.ErrInvitationInvalid {
		t.Errorf("superseded token accept = %v, want ErrInvitationInvalid", err)
	}
	// The new token works and carries the reissued role (admin).
	m, err := svc.AcceptInvitation(ctx, newTok, "invitee@example.test", invitee)
	if err != nil {
		t.Fatalf("new token accept: %v", err)
	}
	if m.Role != org.RoleAdmin {
		t.Errorf("reissued role = %q, want admin", m.Role)
	}
}

// TestReinviteAfterExpiry is the reviewer's PROBE D: an invitation left to
// expire must not permanently blacklist the address. Re-inviting succeeds.
func TestReinviteAfterExpiry(t *testing.T) {
	db := dbtest.DB(t)
	svc := org.NewService(db)
	ctx := context.Background()

	owner := seedUser(t, db, "owner@example.test")
	o, err := svc.CreateOrganization(ctx, "Acme", "acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	inv, _, err := svc.InviteMember(ctx, o.ID, owner, "invitee@example.test", org.RoleMember)
	if err != nil {
		t.Fatalf("first invite: %v", err)
	}
	// Force the invitation past its TTL, as seven days of inaction would.
	if err := db.Model(&org.Invitation{}).Where("id = ?", inv.ID).
		Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("expire invitation: %v", err)
	}

	if _, _, err := svc.InviteMember(ctx, o.ID, owner, "invitee@example.test", org.RoleMember); err != nil {
		t.Fatalf("re-invite after expiry must succeed, got: %v", err)
	}
}

// TestReinviteExemptsAcceptedRows pins the exempt half of the partial index: an
// accepted invitation does not occupy the slot, so once a member leaves they can
// be invited again. (Membership removal has no API yet, so the row is deleted
// directly to model the leave.)
func TestReinviteExemptsAcceptedRows(t *testing.T) {
	db := dbtest.DB(t)
	svc := org.NewService(db)
	ctx := context.Background()

	owner := seedUser(t, db, "owner@example.test")
	o, err := svc.CreateOrganization(ctx, "Acme", "acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	_, tok, err := svc.InviteMember(ctx, o.ID, owner, "invitee@example.test", org.RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	invitee := seedUser(t, db, "invitee@example.test")
	if _, err := svc.AcceptInvitation(ctx, tok, "invitee@example.test", invitee); err != nil {
		t.Fatalf("accept: %v", err)
	}
	// Model the member leaving.
	if err := db.Where("organization_id = ? AND user_id = ?", o.ID, invitee).
		Delete(&org.Member{}).Error; err != nil {
		t.Fatalf("remove membership: %v", err)
	}
	// The accepted invitation row is exempt from the index, so re-inviting is
	// not blocked by it.
	if _, _, err := svc.InviteMember(ctx, o.ID, owner, "invitee@example.test", org.RoleMember); err != nil {
		t.Fatalf("re-invite after leave must succeed, got: %v", err)
	}
}
