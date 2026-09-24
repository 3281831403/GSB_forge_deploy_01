// Package ghexport orchestrates exporting a project revision to a GitHub
// repository (M7 #85): it loads the user's stored token and the project's head
// spec, assembles the full application source (the shared Revision →
// ApplicationSource artifact), creates the repository, and pushes the tree as
// the initial commit.
//
// It depends only on narrow interfaces — the token store, the spec source, the
// source toolchain, and the GitHub publisher — so the orchestration is unit
// tested with fakes and the real toolchain/GitHub only run in the deployed path.
package ghexport

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/githubexport"
	"github.com/gombit-dev/gombit-forge/internal/compiler"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// TokenStore yields a user's stored GitHub access token (githubconnect.Service
// satisfies it).
type TokenStore interface {
	Token(ctx context.Context, userID uint) (string, error)
}

// SpecSource yields a project's head-revision spec and an opaque reference to
// that revision (for provenance).
type SpecSource interface {
	HeadSpec(ctx context.Context, projectID uint) (s *spec.ProjectSpec, revisionRef string, err error)
}

// Publisher is the GitHub side: create a repository and push a source tree. The
// production implementation wraps internal/githubexport; the interface uses the
// shared compiler.SourceFile so no mapping leaks into the orchestration.
type Publisher interface {
	CreateRepository(ctx context.Context, token, name string, private bool) (githubexport.Repo, error)
	PushFiles(ctx context.Context, token, owner, repo, branch string, files []compiler.SourceFile, message string) error
	// DeleteRepository rolls back a created-but-unpopulated repo when a later
	// export step fails (best-effort).
	DeleteRepository(ctx context.Context, token, owner, repo string) error
}

// Service orchestrates the export.
type Service struct {
	tokens        TokenStore
	specs         SpecSource
	tc            compiler.SourceToolchain
	pub           Publisher
	gombitVersion string
}

// NewService wires the export orchestration. gombitVersion is stamped into the
// exported project's provenance.
func NewService(tokens TokenStore, specs SpecSource, tc compiler.SourceToolchain, pub Publisher, gombitVersion string) *Service {
	return &Service{tokens: tokens, specs: specs, tc: tc, pub: pub, gombitVersion: gombitVersion}
}

// Result is the outcome of a successful export.
type Result struct {
	// RepoURL is the created repository's web URL.
	RepoURL string
	// FullName is owner/repo.
	FullName string
}

// Export exports the project's head revision to a new GitHub repository named
// repoName on the connected user's account. It creates the repo first so the
// generated go.mod's module path matches the repository (github.com/owner/repo),
// assembles the application source for that module, then pushes it as the
// initial commit. A user with no GitHub connection surfaces the token store's
// error (githubconnect.ErrNotConnected).
func (s *Service) Export(ctx context.Context, userID, projectID uint, repoName string, private bool) (Result, error) {
	projectSpec, revisionRef, err := s.specs.HeadSpec(ctx, projectID)
	if err != nil {
		return Result{}, err
	}
	if projectSpec == nil {
		return Result{}, fmt.Errorf("ghexport: project %d has no revision to export", projectID)
	}
	return s.ExportSpec(ctx, userID, repoName, private, projectSpec, revisionRef)
}

// ExportSpec exports an already-resolved spec — a frozen revision — to a new
// GitHub repository, rather than resolving the project's current head. The
// asynchronous export worker uses this so a job exports the exact revision it
// froze at enqueue, immune to a head that moves before the worker runs.
// revisionRef is an opaque reference to that revision, stamped into provenance.
//
// It creates the repo first so the generated go.mod's module path matches the
// repository (github.com/owner/repo), assembles the application source for that
// module, then pushes it as the initial commit. A user with no GitHub connection
// surfaces the token store's error (githubconnect.ErrNotConnected).
func (s *Service) ExportSpec(ctx context.Context, userID uint, repoName string, private bool, projectSpec *spec.ProjectSpec, revisionRef string) (Result, error) {
	if strings.TrimSpace(repoName) == "" {
		return Result{}, fmt.Errorf("ghexport: a repository name is required")
	}
	if projectSpec == nil {
		return Result{}, fmt.Errorf("ghexport: no spec to export")
	}
	token, err := s.tokens.Token(ctx, userID)
	if err != nil {
		return Result{}, err
	}

	repo, err := s.pub.CreateRepository(ctx, token, repoName, private)
	if err != nil {
		return Result{}, fmt.Errorf("ghexport: create repository: %w", err)
	}

	// From here the repo exists but is empty; on any failure roll it back so a
	// retry with the same name isn't blocked by a leftover empty repo.
	owner := repo.Owner.Login
	module := "github.com/" + repo.FullName
	files, err := compiler.BuildApplicationSource(ctx, s.tc, compiler.ApplicationSourceRequest{
		Spec:       projectSpec,
		Module:     module,
		Name:       repoName,
		Provenance: compiler.NewProvenance(s.gombitVersion, revisionRef),
	})
	if err != nil {
		s.rollback(ctx, token, owner, repoName)
		return Result{}, fmt.Errorf("ghexport: assemble source: %w", err)
	}

	branch := repo.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	if err := s.pub.PushFiles(ctx, token, owner, repoName, branch, files, "Initial export from Gombit Forge"); err != nil {
		s.rollback(ctx, token, owner, repoName)
		return Result{}, fmt.Errorf("ghexport: push source: %w", err)
	}

	return Result{RepoURL: repo.HTMLURL, FullName: repo.FullName}, nil
}

// rollback best-effort deletes a repo created earlier in a now-failed export, so
// the user isn't left with an empty repo that also blocks a same-name retry.
//
// The cleanup must not inherit the cancellation that triggered it: a push that
// died because ctx hit its deadline (a network timeout — the commonest way a
// push dies mid-flight) would otherwise hand the delete an already-dead context,
// the delete would no-op, and the orphan this exists to remove would survive. So
// we detach from ctx's cancellation and give the delete its own short budget.
//
// A rollback failure can't fail the export — the caller returns the original
// error, which is what actually went wrong — but it means a genuinely orphaned
// repo, so it is logged rather than silently swallowed.
func (s *Service) rollback(ctx context.Context, token, owner, repo string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := s.pub.DeleteRepository(ctx, token, owner, repo); err != nil {
		slog.Error("ghexport: failed to roll back orphaned repository",
			"owner", owner, "repo", repo, "error", err)
	}
}
