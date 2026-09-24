package org

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gombit-dev/gombit/auth"
	"github.com/gombit-dev/gombit/contract"
	"gorm.io/gorm"
)

// Handler serves the org tenancy HTTP operations. Every operation is mounted
// behind the cookie-session gate (see Register), so auth.UserFromContext always
// yields the caller.
type Handler struct {
	svc *Service
}

// --- wire types ------------------------------------------------------------

type organizationData struct {
	ID   uint   `json:"id" doc:"Organization identifier"`
	Name string `json:"name" doc:"Human-readable organization name"`
	Slug string `json:"slug" doc:"URL-safe unique organization key"`
}

type memberData struct {
	ID     uint   `json:"id" doc:"Membership identifier"`
	UserID uint   `json:"user_id" doc:"Gombit user identifier"`
	Role   string `json:"role" doc:"Forge-level role in the organization"`
}

type invitationData struct {
	ID    uint   `json:"id" doc:"Invitation identifier"`
	Email string `json:"email" doc:"Invited email address"`
	Role  string `json:"role" doc:"Forge-level role the invitee will receive"`
	// Token is the raw invitation token, returned once at creation so the
	// caller can build the invite link. It is never stored or returned again.
	Token     string    `json:"token" doc:"One-time invitation token (shown only here)"`
	ExpiresAt time.Time `json:"expires_at" doc:"When the invitation stops being acceptable"`
}

// --- create organization ---------------------------------------------------

type createOrganizationInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"120" doc:"Human-readable organization name"`
		Slug string `json:"slug" minLength:"1" maxLength:"120" pattern:"^[a-z0-9]+(-[a-z0-9]+)*$" doc:"URL-safe unique key"`
	}
}

type createOrganizationOutput struct {
	Body contract.Data[organizationData]
}

func (h *Handler) createOrganization(ctx context.Context, in *createOrganizationInput) (*createOrganizationOutput, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, contract.WithContext(ctx, contract.Authentication("authentication required"))
	}
	o, err := h.svc.CreateOrganization(ctx, in.Body.Name, in.Body.Slug, user.ID)
	if err != nil {
		return nil, mapError(ctx, err, "create organization")
	}
	return &createOrganizationOutput{Body: contract.Data[organizationData]{
		Data: organizationData{ID: o.ID, Name: o.Name, Slug: o.Slug},
	}}, nil
}

// --- list my organizations -------------------------------------------------

type listOrganizationsInput struct{}

type listOrganizationsOutput struct {
	Body contract.Data[[]organizationData]
}

func (h *Handler) listOrganizations(ctx context.Context, _ *listOrganizationsInput) (*listOrganizationsOutput, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, contract.WithContext(ctx, contract.Authentication("authentication required"))
	}
	orgs, err := h.svc.OrgsForUser(ctx, user.ID)
	if err != nil {
		return nil, mapError(ctx, err, "list organizations")
	}
	rows := make([]organizationData, 0, len(orgs))
	for _, o := range orgs {
		rows = append(rows, organizationData{ID: o.ID, Name: o.Name, Slug: o.Slug})
	}
	return &listOrganizationsOutput{Body: contract.Data[[]organizationData]{Data: rows}}, nil
}

// --- list members ----------------------------------------------------------

type listMembersInput struct {
	OrgID string `path:"orgID" doc:"Organization identifier"`
}

type listMembersOutput struct {
	Body contract.Data[[]memberData]
}

func (h *Handler) listMembers(ctx context.Context, in *listMembersInput) (*listMembersOutput, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, contract.WithContext(ctx, contract.Authentication("authentication required"))
	}
	orgID, err := parseID(in.OrgID)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.NotFound("organization not found"))
	}
	if err := h.svc.Authorize(ctx, orgID, user.ID, CapMembersView); err != nil {
		return nil, mapError(ctx, err, "list members")
	}
	members, err := h.svc.Members(ctx, orgID)
	if err != nil {
		return nil, mapError(ctx, err, "list members")
	}
	rows := make([]memberData, 0, len(members))
	for _, m := range members {
		rows = append(rows, memberData{ID: m.ID, UserID: m.UserID, Role: string(m.Role)})
	}
	return &listMembersOutput{Body: contract.Data[[]memberData]{Data: rows}}, nil
}

// --- invite member ---------------------------------------------------------

type inviteMemberInput struct {
	OrgID string `path:"orgID" doc:"Organization identifier"`
	Body  struct {
		Email string `json:"email" format:"email" doc:"Email address to invite"`
		Role  string `json:"role" enum:"owner,admin,member" doc:"Forge-level role to grant"`
	}
}

type inviteMemberOutput struct {
	Body contract.Data[invitationData]
}

func (h *Handler) inviteMember(ctx context.Context, in *inviteMemberInput) (*inviteMemberOutput, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, contract.WithContext(ctx, contract.Authentication("authentication required"))
	}
	orgID, err := parseID(in.OrgID)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.NotFound("organization not found"))
	}
	inv, token, err := h.svc.InviteMember(ctx, orgID, user.ID, in.Body.Email, Role(in.Body.Role))
	if err != nil {
		return nil, mapError(ctx, err, "invite member")
	}
	return &inviteMemberOutput{Body: contract.Data[invitationData]{
		Data: invitationData{
			ID: inv.ID, Email: inv.Email, Role: string(inv.Role),
			Token: token, ExpiresAt: inv.ExpiresAt,
		},
	}}, nil
}

// --- accept invitation -----------------------------------------------------

type acceptInvitationInput struct {
	Body struct {
		Token string `json:"token" minLength:"1" doc:"Invitation token from the invite link"`
	}
}

type acceptInvitationOutput struct {
	Body contract.Data[memberData]
}

func (h *Handler) acceptInvitation(ctx context.Context, in *acceptInvitationInput) (*acceptInvitationOutput, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, contract.WithContext(ctx, contract.Authentication("authentication required"))
	}
	m, err := h.svc.AcceptInvitation(ctx, in.Body.Token, user.Email, user.ID)
	if err != nil {
		return nil, mapError(ctx, err, "accept invitation")
	}
	return &acceptInvitationOutput{Body: contract.Data[memberData]{
		Data: memberData{ID: m.ID, UserID: m.UserID, Role: string(m.Role)},
	}}, nil
}

// --- helpers ---------------------------------------------------------------

func parseID(s string) (uint, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(n), nil
}

// mapError translates a service error into the matching contract envelope. The
// default is a 500 that does not leak the underlying message.
func mapError(ctx context.Context, err error, action string) error {
	switch {
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrNotMember), errors.Is(err, ErrRoleExceedsGranter):
		return contract.WithContext(ctx, contract.Authorization("not permitted"))
	case errors.Is(err, ErrAlreadyMember):
		return contract.WithContext(ctx, contract.Conflict("already a member"))
	case errors.Is(err, ErrInvitationPending):
		return contract.WithContext(ctx, contract.Conflict("an invitation is already pending for this address"))
	case errors.Is(err, ErrInvitationInvalid):
		return contract.WithContext(ctx, contract.NotFound("invitation invalid or expired"))
	case errors.Is(err, ErrInvalidRole):
		return contract.WithContext(ctx, contract.Validation("invalid role", map[string][]string{
			"role": {"must be one of owner, admin, member"},
		}))
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return contract.WithContext(ctx, contract.Conflict("already exists"))
	default:
		return contract.WithContext(ctx, contract.Internal(action))
	}
}
