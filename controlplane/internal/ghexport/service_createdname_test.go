package ghexport

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/githubexport"
	"github.com/gombit-dev/gombit-forge/internal/compiler"
)

// normalizingPublisher mimics GitHub's repo-name normalization: asked for
// "my app", it creates "my-app" and reports the created name back. It records
// the repo the push and the rollback actually target.
type normalizingPublisher struct {
	pushErr     error
	pushedRepo  string
	deletedRepo string
}

func (p *normalizingPublisher) CreateRepository(_ context.Context, _, name string, _ bool) (githubexport.Repo, error) {
	created := strings.ReplaceAll(name, " ", "-") // GitHub normalizes spaces to hyphens
	return githubexport.Repo{
		Name:          created,
		FullName:      "octo/" + created,
		HTMLURL:       "https://github.com/octo/" + created,
		Owner:         githubexport.Owner{Login: "octo"},
		DefaultBranch: "main",
	}, nil
}

func (p *normalizingPublisher) PushFiles(_ context.Context, _, _, repo, _ string, _ []compiler.SourceFile, _ string) error {
	p.pushedRepo = repo
	return p.pushErr
}

func (p *normalizingPublisher) DeleteRepository(_ context.Context, _, _, repo string) error {
	p.deletedRepo = repo
	return nil
}

// TestExportPushesToCreatedRepoName: when GitHub normalizes the requested name,
// the push must target the repo GitHub actually created — pushing the requested
// name would 404 (or worse, hit a different repo the user owns).
func TestExportPushesToCreatedRepoName(t *testing.T) {
	pub := &normalizingPublisher{}
	svc := NewService(fakeTokens{token: "t"}, fakeSpecs{spec: minimalSpec(), ref: "r"}, fakeToolchain{}, pub, "v")

	res, err := svc.Export(context.Background(), 7, 3, "my app", false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.FullName != "octo/my-app" {
		t.Errorf("result = %+v, want the created repo octo/my-app", res)
	}
	if pub.pushedRepo != "my-app" {
		t.Errorf("push targeted %q, want the created repo name %q", pub.pushedRepo, "my-app")
	}
}

// TestExportRollsBackCreatedRepoName: a failed export must roll back the repo
// GitHub created — deleting the requested name would miss the orphan.
func TestExportRollsBackCreatedRepoName(t *testing.T) {
	pub := &normalizingPublisher{pushErr: errors.New("push boom")}
	svc := NewService(fakeTokens{token: "t"}, fakeSpecs{spec: minimalSpec(), ref: "r"}, fakeToolchain{}, pub, "v")

	if _, err := svc.Export(context.Background(), 7, 3, "my app", false); err == nil {
		t.Fatal("push failure must error")
	}
	if pub.deletedRepo != "my-app" {
		t.Errorf("rollback targeted %q, want the created repo name %q", pub.deletedRepo, "my-app")
	}
}

// fullNameOnlyPublisher reports no Name, only FullName — the fallback path.
type fullNameOnlyPublisher struct {
	pushedRepo string
}

func (p *fullNameOnlyPublisher) CreateRepository(_ context.Context, _, _ string, _ bool) (githubexport.Repo, error) {
	return githubexport.Repo{
		FullName:      "octo/my-app",
		HTMLURL:       "https://github.com/octo/my-app",
		Owner:         githubexport.Owner{Login: "octo"},
		DefaultBranch: "main",
	}, nil
}

func (p *fullNameOnlyPublisher) PushFiles(_ context.Context, _, _, repo, _ string, _ []compiler.SourceFile, _ string) error {
	p.pushedRepo = repo
	return nil
}

func (p *fullNameOnlyPublisher) DeleteRepository(context.Context, string, string, string) error {
	return nil
}

// TestExportDerivesRepoNameFromFullName: a Publisher that reports only FullName
// still pushes to the created repo, derived from the full name.
func TestExportDerivesRepoNameFromFullName(t *testing.T) {
	pub := &fullNameOnlyPublisher{}
	svc := NewService(fakeTokens{token: "t"}, fakeSpecs{spec: minimalSpec(), ref: "r"}, fakeToolchain{}, pub, "v")

	if _, err := svc.Export(context.Background(), 7, 3, "my app", false); err != nil {
		t.Fatalf("export: %v", err)
	}
	if pub.pushedRepo != "my-app" {
		t.Errorf("push targeted %q, want %q derived from the full name", pub.pushedRepo, "my-app")
	}
}
