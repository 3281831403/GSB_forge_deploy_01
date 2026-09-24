package ghexport

import (
	"context"
	"net/http"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/githubexport"
	"github.com/gombit-dev/gombit-forge/internal/compiler"
)

// gitHubPublisher is the production Publisher: it drives internal/githubexport
// over an HTTP client, mapping the shared compiler.SourceFile (with its exec
// bit) to githubexport.File so the mode survives the push — the same exec-bit
// handling the ZIP export has.
type gitHubPublisher struct {
	cfg    githubexport.Config
	client *http.Client
}

// NewPublisher builds the production Publisher over the GitHub API config. A nil
// client uses http.DefaultClient.
func NewPublisher(cfg githubexport.Config, client *http.Client) Publisher {
	if client == nil {
		client = http.DefaultClient
	}
	return gitHubPublisher{cfg: cfg, client: client}
}

func (p gitHubPublisher) CreateRepository(ctx context.Context, token, name string, private bool) (githubexport.Repo, error) {
	return githubexport.CreateRepository(ctx, p.client, p.cfg, token, name, private)
}

func (p gitHubPublisher) DeleteRepository(ctx context.Context, token, owner, repo string) error {
	return githubexport.DeleteRepository(ctx, p.client, p.cfg, token, owner, repo)
}

func (p gitHubPublisher) PushFiles(ctx context.Context, token, owner, repo, branch string, files []compiler.SourceFile, message string) error {
	ghFiles := make([]githubexport.File, len(files))
	for i, f := range files {
		ghFiles[i] = githubexport.File{Path: f.Path, Content: f.Content, Executable: f.Executable}
	}
	return githubexport.PushFiles(ctx, p.client, p.cfg, token, owner, repo, branch, ghFiles, message)
}
