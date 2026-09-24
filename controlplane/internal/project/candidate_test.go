package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// cloneSpec deep-copies a spec through its canonical encoding, preserving every
// stable ID — so a candidate built from a clone shares identity with the current
// spec, which is what lets classification tell a relabel from a delete-plus-add.
func cloneSpec(t *testing.T, s *spec.ProjectSpec) *spec.ProjectSpec {
	t.Helper()
	data, err := spec.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	clone, err := spec.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return clone
}

// TestSubmitCandidateFirstRevisionCommits: a project's first candidate has no
// prior ABI to break, so a valid spec commits directly.
func TestSubmitCandidateFirstRevisionCommits(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	p, err := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	res, err := svc.SubmitCandidate(ctx, p.ID, validSpec(t, "V1", "v1"), 7)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted {
		t.Fatalf("first candidate outcome = %s, want committed", res.Outcome)
	}
	if res.Revision == nil {
		t.Fatal("committed outcome must carry the revision")
	}
	head, ok, err := svc.Head(ctx, p.ID)
	if err != nil || !ok {
		t.Fatalf("head: ok=%v err=%v", ok, err)
	}
	if head.ID != res.Revision.ID {
		t.Errorf("head = %d, want the committed revision %d", head.ID, res.Revision.ID)
	}
}

// TestSubmitCandidateInvalidSpecRejected: an invalid spec is rejected with
// diagnostics and never becomes a revision.
func TestSubmitCandidateInvalidSpecRejected(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	p, err := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	bad := validSpec(t, "V1", "v1")
	bad.Resources[0].CodeName = "" // invalid: not an exported Go identifier

	res, err := svc.SubmitCandidate(ctx, p.ID, bad, 7)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if res.Outcome != project.OutcomeInvalidSpec {
		t.Fatalf("outcome = %s, want invalid_spec", res.Outcome)
	}
	if len(res.Diagnostics) == 0 {
		t.Error("an invalid-spec rejection must carry diagnostics")
	}
	if _, ok, _ := svc.Head(ctx, p.ID); ok {
		t.Error("an invalid candidate must not create a revision")
	}
}

// TestSubmitCandidateNeutralCommits: a relabel (same code symbols) is ABI-neutral
// and commits a new revision.
func TestSubmitCandidateNeutralCommits(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	p, err := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	base := validSpec(t, "V1", "v1")
	first, err := svc.SubmitCandidate(ctx, p.ID, base, 7)
	if err != nil || first.Outcome != project.OutcomeCommitted {
		t.Fatalf("first submit: outcome=%s err=%v", first.Outcome, err)
	}

	relabel := cloneSpec(t, base)
	relabel.Resources[0].Label = "Client" // label only; code symbol frozen

	res, err := svc.SubmitCandidate(ctx, p.ID, relabel, 9)
	if err != nil {
		t.Fatalf("submit relabel: %v", err)
	}
	if res.Outcome != project.OutcomeCommitted {
		t.Fatalf("relabel outcome = %s, want committed", res.Outcome)
	}
	if res.Class != "neutral" {
		t.Errorf("relabel class = %q, want neutral", res.Class)
	}
	if res.Revision.ParentRevisionID == nil || *res.Revision.ParentRevisionID != first.Revision.ID {
		t.Error("the committed revision must link to the prior head")
	}
}

// TestSubmitCandidateBreakingRejected: an explicit code-symbol change is
// ABI-breaking, so it is returned unresolved with its reasons and the head does
// not move — committing it needs a compatibility build the request path skips.
func TestSubmitCandidateBreakingRejected(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	p, err := svc.CreateProject(ctx, 1, "Acme", "acme", 7)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	base := validSpec(t, "V1", "v1")
	first, err := svc.SubmitCandidate(ctx, p.ID, base, 7)
	if err != nil || first.Outcome != project.OutcomeCommitted {
		t.Fatalf("first submit: outcome=%s err=%v", first.Outcome, err)
	}

	breaking := cloneSpec(t, base)
	breaking.Resources[0].CodeName = "Client" // source rename: breaking
	if d := spec.Validate(breaking); d != nil {
		t.Fatalf("breaking candidate should still be a valid spec:\n%s", d.Error())
	}

	res, err := svc.SubmitCandidate(ctx, p.ID, breaking, 9)
	if err != nil {
		t.Fatalf("submit breaking: %v", err)
	}
	if res.Outcome != project.OutcomeBreaking {
		t.Fatalf("outcome = %s, want breaking", res.Outcome)
	}
	if len(res.Reasons) == 0 {
		t.Error("a breaking outcome must carry reasons")
	}
	// Head must not have advanced.
	head, ok, err := svc.Head(ctx, p.ID)
	if err != nil || !ok {
		t.Fatalf("head: ok=%v err=%v", ok, err)
	}
	if head.ID != first.Revision.ID {
		t.Errorf("a breaking candidate must not move head; head = %d, want %d", head.ID, first.Revision.ID)
	}
}

// TestSubmitCandidateProjectNotFound: submitting to a missing project errors.
func TestSubmitCandidateProjectNotFound(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)

	_, err := svc.SubmitCandidate(context.Background(), 99999, validSpec(t, "V1", "v1"), 7)
	if !errors.Is(err, project.ErrProjectNotFound) {
		t.Errorf("want ErrProjectNotFound, got %v", err)
	}
}

// TestListAndGetProject: created projects are listed in order and fetched by id.
func TestListAndGetProject(t *testing.T) {
	db := dbtest.DB(t)
	seedFKDeps(t, db)
	svc := project.NewService(db)
	ctx := context.Background()

	a, err := svc.CreateProject(ctx, 1, "Alpha", "alpha", 7)
	if err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	b, err := svc.CreateProject(ctx, 1, "Beta", "beta", 7)
	if err != nil {
		t.Fatalf("create beta: %v", err)
	}

	list, err := svc.ListProjects(ctx, 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Fatalf("list = %+v, want [alpha, beta] in order", list)
	}

	got, err := svc.GetProject(ctx, b.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Slug != "beta" {
		t.Errorf("get slug = %q, want beta", got.Slug)
	}

	if _, err := svc.GetProject(ctx, 99999); !errors.Is(err, project.ErrProjectNotFound) {
		t.Errorf("get missing = %v, want ErrProjectNotFound", err)
	}
}
