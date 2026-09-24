package org

// Role is a member's Forge-level role within one organization (DESIGN.md §22).
//
// Forge tenancy is per-organization: the same user can be an owner of one org
// and a plain member of another. Gombit's users/groups/permissions are global
// and cannot express that scoping on their own, so the org-scoped authorization
// decision lives here — as a fixed capability matrix keyed by role, not a
// second identity or permission store. It reuses Gombit's identity (auth.User)
// for *who* the caller is and its permission keys' convention for *what*
// actions are named; it does not reimplement group/permission storage (D12).
type Role string

const (
	// RoleOwner has every capability, including destroying the org and managing
	// other owners. The member who creates an org is its first owner.
	RoleOwner Role = "owner"
	// RoleAdmin manages members and projects but cannot destroy the org.
	RoleAdmin Role = "admin"
	// RoleMember can see the org and create projects in it.
	RoleMember Role = "member"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleMember:
		return true
	default:
		return false
	}
}

// Capability is a Forge-level action a role may or may not permit. The keys
// follow Gombit's dotted permission-key convention (§4.6) so the vocabulary is
// consistent with the permissions the generated apps use, even though these are
// control-plane capabilities checked against the caller's org role.
//
// This set is intentionally only what the code enforces today, not an
// aspirational catalogue. A capability listed here has a reader; when a new
// operation lands (member removal, project create/delete in #37+), its
// capability is added alongside the operation and the gate that checks it — so
// the matrix never asserts a policy no code reads.
type Capability string

const (
	CapMembersView   Capability = "org.members.view"   // read: listMembers
	CapMembersInvite Capability = "org.members.invite" // write: InviteMember

	CapProjectView   Capability = "project.view"   // read: list/get projects (#39)
	CapProjectCreate Capability = "project.create" // write: create a project (#39)
	CapProjectEdit   Capability = "project.edit"   // write: submit a candidate revision (#39)
)

// capabilities is the role → allowed-capability matrix. It is the single source
// of truth for org-scoped capability checks; Can is the only reader. The role
// *hierarchy* (owner > admin > member) is expressed separately, by rank, and is
// what bounds role grants (see CanGrant) — capabilities alone do not distinguish
// owner from admin here, rank does.
// Authoring — viewing, creating and revising projects — is the core member
// activity (DESIGN.md §22: a member "can see the org and create projects in
// it"), so every role holds the project capabilities. Owner/admin are separated
// from member by rank (org destruction, member management), not by project
// access.
var capabilities = map[Role]map[Capability]bool{
	RoleOwner: {
		CapMembersView:   true,
		CapMembersInvite: true,
		CapProjectView:   true,
		CapProjectCreate: true,
		CapProjectEdit:   true,
	},
	RoleAdmin: {
		CapMembersView:   true,
		CapMembersInvite: true,
		CapProjectView:   true,
		CapProjectCreate: true,
		CapProjectEdit:   true,
	},
	RoleMember: {
		CapMembersView:   true,
		CapProjectView:   true,
		CapProjectCreate: true,
		CapProjectEdit:   true,
	},
}

// Can reports whether role permits capability. An unknown role permits nothing
// (fail closed).
func Can(role Role, capability Capability) bool {
	return capabilities[role][capability]
}

// rank orders roles by privilege so grants can be bounded by the granter's own
// standing. An unknown role ranks 0, below every real role, so it can grant
// nothing (fail closed).
func (r Role) rank() int {
	switch r {
	case RoleOwner:
		return 3
	case RoleAdmin:
		return 2
	case RoleMember:
		return 1
	default:
		return 0
	}
}

// CanGrant reports whether a member holding granter may hand out target. A
// member may only grant a role it outranks or equals — otherwise an admin,
// which cannot manage the org, could mint an owner, which can, and escalate
// past its own ceiling through a sock puppet. Holding CapMembersInvite is
// necessary but not sufficient; this is the second gate.
func CanGrant(granter, target Role) bool {
	if !granter.Valid() || !target.Valid() {
		return false
	}
	return target.rank() <= granter.rank()
}
