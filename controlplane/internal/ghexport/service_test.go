package ghexport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/githubexport"
	"github.com/gombit-dev/gombit-forge/internal/compiler"
	"github.com/gombit-dev/gombit-forge/internal/gombit"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// --- fakes -----------------------------------------------------------------

type fakeTokens struct {
	token string
	err   error
}

func (f fakeTokens) Token(context.Context, uint) (string, error) { return f.token, f.err }

type fakeSpecs struct {
	spec *spec.ProjectSpec
	ref  string
	err  error
}

func (f fakeSpecs) HeadSpec(context.Context, uint) (*spec.ProjectSpec, string, error) {
	return f.spec, f.ref, f.err
}

// fakeToolchain writes a minimal canonical scaffold so BuildApplicationSource
// produces a realistic tree, and records nothing else.
type fakeToolchain struct{}

func (fakeToolchain) Scaffold(_ context.Context, req gombit.ScaffoldRequest) error {
	base := map[string]string{
		"go.mod":                    "module " + req.Module + "\n",
		"cmd/server/main.go":        "package main // placeholder\n",
		"internal/web/static/.keep": "",
	}
	for path, content := range base {
		full := filepath.Join(req.Dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (fakeToolchain) Tidy(context.Context, string) error { return nil }

func (fakeToolchain) MakeMigrations(_ context.Context, req gombit.MakeMigrationsRequest) error {
	full := filepath.Join(req.Dir, "database", "migrations", "0001_initial.sql")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte("-- migration\n"), 0o644)
}

type fakePublisher struct {
	createdName string
	private     bool
	pushed      struct {
		owner, repo, branch, message string
		files                        []compiler.SourceFile
	}
	deleted            string
	deletedCtxErr      error // ctx.Err() observed inside DeleteRepository
	createErr, pushErr error
}

func (f *fakePublisher) CreateRepository(_ context.Context, _, name string, private bool) (githubexport.Repo, error) {
	f.createdName, f.private = name, private
	if f.createErr != nil {
		return githubexport.Repo{}, f.createErr
	}
	return githubexport.Repo{
		FullName:      "octo/" + name,
		HTMLURL:       "https://github.com/octo/" + name,
		Owner:         githubexport.Owner{Login: "octo"},
		DefaultBranch: "main",
	}, nil
}

func (f *fakePublisher) PushFiles(_ context.Context, _, owner, repo, branch string, files []compiler.SourceFile, message string) error {
	f.pushed.owner, f.pushed.repo, f.pushed.branch, f.pushed.message, f.pushed.files = owner, repo, branch, message, files
	return f.pushErr
}

func (f *fakePublisher) DeleteRepository(ctx context.Context, _, _, repo string) error {
	f.deleted = repo
	f.deletedCtxErr = ctx.Err()
	return nil
}

// errToolchain fails at scaffold, so BuildApplicationSource errors after the repo
// is created — exercising rollback on an assembly failure.
type errToolchain struct{}

func (errToolchain) Scaffold(context.Context, gombit.ScaffoldRequest) error {
	return errors.New("scaffold boom")
}
func (errToolchain) Tidy(context.Context, string) error                                 { return nil }
func (errToolchain) MakeMigrations(context.Context, gombit.MakeMigrationsRequest) error { return nil }

// minimalSpec is a valid one-resource spec the compiler accepts.
func minimalSpec() *spec.ProjectSpec {
	id := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	return &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: id(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{
			{ID: id(spec.KindResource), Label: "Customer", CodeName: "Customer", StorageName: "customers",
				Behavior: spec.ResourceBehavior{CreateEnabled: true},
				Fields: []*spec.Field{
					{ID: id(spec.KindField), Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"},
				}},
		},
	}
}

// --- tests -----------------------------------------------------------------

func TestExport(t *testing.T) {
	pub := &fakePublisher{}
	svc := NewService(
		fakeTokens{token: "gho_tok"},
		fakeSpecs{spec: minimalSpec(), ref: "rev_abc"},
		fakeToolchain{},
		pub,
		"v0.1.7",
	)

	res, err := svc.Export(context.Background(), 7, 3, "my-app", true)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.FullName != "octo/my-app" || res.RepoURL != "https://github.com/octo/my-app" {
		t.Errorf("result = %+v", res)
	}
	// The repo was created (private as requested) before the push.
	if pub.createdName != "my-app" || !pub.private {
		t.Errorf("create repo name=%q private=%v", pub.createdName, pub.private)
	}
	// A successful export must never roll back — rollback is strictly a
	// failure-path action.
	if pub.deleted != "" {
		t.Errorf("successful export must not roll back; deleted=%q", pub.deleted)
	}
	// The push targeted the created repo's owner/branch with a commit message.
	if pub.pushed.owner != "octo" || pub.pushed.repo != "my-app" || pub.pushed.branch != "main" {
		t.Errorf("push target = %+v", pub.pushed)
	}
	if !strings.Contains(pub.pushed.message, "Gombit Forge") {
		t.Errorf("push message = %q", pub.pushed.message)
	}

	byPath := map[string]string{}
	for _, f := range pub.pushed.files {
		byPath[f.Path] = string(f.Content)
	}
	for _, want := range []string{"go.mod", "cmd/server/main.go", "README.md", "forge.json"} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("pushed tree missing %s", want)
		}
	}
	// The module path matches the created repo (github.com/octo/my-app), so the
	// composition root and go.mod are consistent with the repository.
	if !strings.Contains(byPath["go.mod"], "github.com/octo/my-app") {
		t.Errorf("go.mod module must match the repo; got %q", byPath["go.mod"])
	}
	if !strings.Contains(byPath["cmd/server/main.go"], "github.com/octo/my-app/internal/forge_generated") {
		t.Error("composition root must import the repo-matched module")
	}
}

// TestExportRollsBackOnPushError: a push failure after the repo is created must
// delete the orphaned empty repo and return the error.
func TestExportRollsBackOnPushError(t *testing.T) {
	pub := &fakePublisher{pushErr: errors.New("push boom")}
	svc := NewService(fakeTokens{token: "t"}, fakeSpecs{spec: minimalSpec(), ref: "r"}, fakeToolchain{}, pub, "v")
	if _, err := svc.Export(context.Background(), 7, 3, "my-app", false); err == nil {
		t.Fatal("push failure must error")
	}
	if pub.createdName != "my-app" {
		t.Error("repo should have been created")
	}
	if pub.deleted != "my-app" {
		t.Errorf("orphaned repo must be rolled back; deleted=%q", pub.deleted)
	}
}

// TestExportRollsBackOnAssembleError: an assembly failure after repo creation
// must also roll the repo back.
func TestExportRollsBackOnAssembleError(t *testing.T) {
	pub := &fakePublisher{}
	svc := NewService(fakeTokens{token: "t"}, fakeSpecs{spec: minimalSpec(), ref: "r"}, errToolchain{}, pub, "v")
	if _, err := svc.Export(context.Background(), 7, 3, "my-app", false); err == nil {
		t.Fatal("assembly failure must error")
	}
	if pub.deleted != "my-app" {
		t.Errorf("orphaned repo must be rolled back after an assembly failure; deleted=%q", pub.deleted)
	}
}

// TestExportRollsBackUnderCancelledContext pins the WithoutCancel fix: when the
// export's own context is already cancelled (a timed-out push, the commonest
// orphan cause), rollback must still run the delete on a live, non-cancelled
// context — not inherit the cancellation and no-op.
func TestExportRollsBackUnderCancelledContext(t *testing.T) {
	pub := &fakePublisher{pushErr: errors.New("push boom")}
	svc := NewService(fakeTokens{token: "t"}, fakeSpecs{spec: minimalSpec(), ref: "r"}, fakeToolchain{}, pub, "v")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // export runs under an already-dead context

	if _, err := svc.Export(ctx, 7, 3, "my-app", false); err == nil {
		t.Fatal("push failure must error")
	}
	if pub.deleted != "my-app" {
		t.Fatalf("rollback must run even under a cancelled export context; deleted=%q", pub.deleted)
	}
	if pub.deletedCtxErr != nil {
		t.Errorf("rollback must detach from the cancelled context; delete saw ctx.Err()=%v", pub.deletedCtxErr)
	}
}

func TestExportRequiresRepoName(t *testing.T) {
	svc := NewService(fakeTokens{token: "t"}, fakeSpecs{spec: minimalSpec()}, fakeToolchain{}, &fakePublisher{}, "v")
	if _, err := svc.Export(context.Background(), 7, 3, "  ", false); err == nil {
		t.Error("empty repo name must error")
	}
}

func TestExportPropagatesTokenError(t *testing.T) {
	notConnected := errors.New("not connected")
	pub := &fakePublisher{}
	svc := NewService(fakeTokens{err: notConnected}, fakeSpecs{spec: minimalSpec()}, fakeToolchain{}, pub, "v")
	if _, err := svc.Export(context.Background(), 7, 3, "my-app", false); !errors.Is(err, notConnected) {
		t.Errorf("token error must propagate; got %v", err)
	}
	if pub.createdName != "" {
		t.Error("must not create a repo when the user has no token")
	}
}
