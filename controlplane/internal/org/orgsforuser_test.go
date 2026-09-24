package org_test

import (
	"context"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/org"
)

// TestOrgsForUser: the picker query returns only the orgs the user belongs to,
// in creation order, never orgs they were never invited to.
func TestOrgsForUser(t *testing.T) {
	db := dbtest.DB(t)
	svc := org.NewService(db)
	ctx := context.Background()

	alice := seedUser(t, db, "alice@example.test")
	bob := seedUser(t, db, "bob@example.test")

	// Alice founds two orgs (owner of both); Bob founds a third.
	a1, err := svc.CreateOrganization(ctx, "Acme", "acme", alice)
	if err != nil {
		t.Fatalf("create acme: %v", err)
	}
	a2, err := svc.CreateOrganization(ctx, "Beta", "beta", alice)
	if err != nil {
		t.Fatalf("create beta: %v", err)
	}
	if _, err := svc.CreateOrganization(ctx, "Ceta", "ceta", bob); err != nil {
		t.Fatalf("create ceta: %v", err)
	}

	orgs, err := svc.OrgsForUser(ctx, alice)
	if err != nil {
		t.Fatalf("orgs for user: %v", err)
	}
	if len(orgs) != 2 || orgs[0].ID != a1.ID || orgs[1].ID != a2.ID {
		t.Fatalf("alice's orgs = %+v, want [acme, beta] in order", orgs)
	}
	// Bob's org must not leak into Alice's list.
	for _, o := range orgs {
		if o.Slug == "ceta" {
			t.Error("OrgsForUser returned an org the user does not belong to")
		}
	}
}
