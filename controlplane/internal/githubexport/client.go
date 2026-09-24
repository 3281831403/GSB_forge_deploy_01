// Package githubexport is the GitHub side of Forge's optional "export to a
// GitHub repository" target (DESIGN.md §4.9, §32; M7 #85). It is a thin,
// well-scoped client over GitHub's OAuth and REST APIs: build the OAuth
// authorize URL, exchange the callback code for a token, create a repository,
// and push the exported project tree as a single commit via the Git Data API.
//
// Every network call takes an injectable *http.Client and reads its base URLs
// from Config, so the whole integration is exercised against an httptest server
// with no real GitHub credentials. The control-plane wiring (the OAuth connect
// flow, token storage and the export endpoint) consumes this package.
package githubexport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Config carries the OAuth app credentials and the API/OAuth base URLs. The base
// URLs default to public GitHub when empty and are overridable for tests and for
// GitHub Enterprise.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// OAuthBaseURL is the OAuth host (default https://github.com); APIBaseURL is
	// the REST host (default https://api.github.com).
	OAuthBaseURL string
	APIBaseURL   string
}

func (c Config) oauthBase() string {
	if c.OAuthBaseURL != "" {
		return strings.TrimRight(c.OAuthBaseURL, "/")
	}
	return "https://github.com"
}

func (c Config) apiBase() string {
	if c.APIBaseURL != "" {
		return strings.TrimRight(c.APIBaseURL, "/")
	}
	return "https://api.github.com"
}

// File is one file to push: a forward-slash repo-relative path, its content, and
// whether it is executable (so an exec bit survives the push, matching the ZIP
// export's mode handling).
type File struct {
	Path       string
	Content    []byte
	Executable bool
}

// Repo is the created repository, as much of it as callers need.
type Repo struct {
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	Owner         Owner  `json:"owner"`
	DefaultBranch string `json:"default_branch"`
}

// Owner is a repository owner (the authenticated user), as much as callers need.
type Owner struct {
	Login string `json:"login"`
}

// AuthorizeURL builds the GitHub OAuth authorize URL for the connect flow. The
// state is an opaque anti-CSRF token the caller mints and later verifies on the
// callback; scope is "repo" so the token can create and push to a repository.
func AuthorizeURL(cfg Config, state string) string {
	q := url.Values{
		"client_id":    {cfg.ClientID},
		"redirect_uri": {cfg.RedirectURL},
		"scope":        {"repo"},
		"state":        {state},
	}
	return cfg.oauthBase() + "/login/oauth/authorize?" + q.Encode()
}

// ExchangeCode exchanges the OAuth callback code for an access token.
func ExchangeCode(ctx context.Context, client *http.Client, cfg Config, code string) (string, error) {
	form := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURL},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cfg.oauthBase()+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := do(client, req, http.StatusOK, &body); err != nil {
		return "", err
	}
	if body.Error != "" {
		return "", fmt.Errorf("githubexport: token exchange failed: %s (%s)", body.Error, body.ErrorDesc)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("githubexport: token exchange returned no access token")
	}
	return body.AccessToken, nil
}

// CreateRepository creates a repository for the authenticated user. It is
// created empty (auto_init false) so PushFiles can lay down the exported tree as
// the initial commit rather than merging onto a generated README.
func CreateRepository(ctx context.Context, client *http.Client, cfg Config, token, name string, private bool) (Repo, error) {
	payload := map[string]any{"name": name, "private": private, "auto_init": false}
	req, err := jsonRequest(ctx, http.MethodPost, cfg.apiBase()+"/user/repos", token, payload)
	if err != nil {
		return Repo{}, err
	}
	var repo Repo
	if err := do(client, req, http.StatusCreated, &repo); err != nil {
		return Repo{}, err
	}
	return repo, nil
}

// DeleteRepository deletes owner/repo (GitHub returns 204). It rolls back a
// repository that was created but never populated when a later export step
// fails, so a retry isn't blocked by a leftover empty repo. Callers use it
// best-effort — a non-204 (e.g. the repo is already gone) surfaces as an error
// for the caller to log rather than fail the original operation on.
func DeleteRepository(ctx context.Context, client *http.Client, cfg Config, token, owner, repo string) error {
	req, err := jsonRequest(ctx, http.MethodDelete, cfg.apiBase()+"/repos/"+owner+"/"+repo, token, nil)
	if err != nil {
		return err
	}
	return do(client, req, http.StatusNoContent, nil)
}

// PushFiles pushes files to owner/repo as a single commit on branch via the Git
// Data API: a blob per file, one tree, one commit with no parent, then the
// branch ref pointed at it. Pushing to an empty repository is the expected path
// (CreateRepository makes an empty repo), so the ref is created, not updated.
func PushFiles(ctx context.Context, client *http.Client, cfg Config, token, ownerName, repo, branch string, files []File, message string) error {
	if len(files) == 0 {
		return fmt.Errorf("githubexport: refusing to push an empty file set")
	}
	base := cfg.apiBase() + "/repos/" + ownerName + "/" + repo + "/git"

	type treeEntry struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
		Type string `json:"type"`
		SHA  string `json:"sha"`
	}
	entries := make([]treeEntry, 0, len(files))
	for _, f := range files {
		var blob struct {
			SHA string `json:"sha"`
		}
		req, err := jsonRequest(ctx, http.MethodPost, base+"/blobs", token, map[string]any{
			"content":  base64.StdEncoding.EncodeToString(f.Content),
			"encoding": "base64",
		})
		if err != nil {
			return err
		}
		if err := do(client, req, http.StatusCreated, &blob); err != nil {
			return fmt.Errorf("githubexport: create blob %s: %w", f.Path, err)
		}
		mode := "100644"
		if f.Executable {
			mode = "100755"
		}
		entries = append(entries, treeEntry{Path: f.Path, Mode: mode, Type: "blob", SHA: blob.SHA})
	}

	var tree struct {
		SHA string `json:"sha"`
	}
	req, err := jsonRequest(ctx, http.MethodPost, base+"/trees", token, map[string]any{"tree": entries})
	if err != nil {
		return err
	}
	if err := do(client, req, http.StatusCreated, &tree); err != nil {
		return fmt.Errorf("githubexport: create tree: %w", err)
	}

	var commit struct {
		SHA string `json:"sha"`
	}
	req, err = jsonRequest(ctx, http.MethodPost, base+"/commits", token, map[string]any{
		"message": message,
		"tree":    tree.SHA,
		"parents": []string{},
	})
	if err != nil {
		return err
	}
	if err := do(client, req, http.StatusCreated, &commit); err != nil {
		return fmt.Errorf("githubexport: create commit: %w", err)
	}

	req, err = jsonRequest(ctx, http.MethodPost, base+"/refs", token, map[string]any{
		"ref": "refs/heads/" + branch,
		"sha": commit.SHA,
	})
	if err != nil {
		return err
	}
	if err := do(client, req, http.StatusCreated, nil); err != nil {
		return fmt.Errorf("githubexport: create ref: %w", err)
	}
	return nil
}

// jsonRequest builds a request with the GitHub auth headers. A nil payload sends
// no body and no Content-Type (for a bodyless DELETE); otherwise the payload is
// JSON-encoded.
func jsonRequest(ctx context.Context, method, u, token string, payload any) (*http.Request, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	return req, nil
}

// do sends req, checks the status, and decodes the JSON body into out (nil to
// discard). A non-expected status returns an error carrying GitHub's message.
func do(client *http.Client, req *http.Request, wantStatus int, out any) error {
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("githubexport: %s %s: unexpected status %d: %s",
			req.Method, req.URL.Path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("githubexport: decode response: %w", err)
	}
	return nil
}
