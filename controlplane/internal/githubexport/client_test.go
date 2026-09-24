package githubexport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAuthorizeURL(t *testing.T) {
	cfg := Config{ClientID: "cid", RedirectURL: "https://forge.example/cb", OAuthBaseURL: "https://gh.test"}
	got := AuthorizeURL(cfg, "state-xyz")

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("authorize url not a url: %v", err)
	}
	if u.Host != "gh.test" || u.Path != "/login/oauth/authorize" {
		t.Errorf("authorize endpoint = %s%s, want gh.test/login/oauth/authorize", u.Host, u.Path)
	}
	q := u.Query()
	if q.Get("client_id") != "cid" || q.Get("redirect_uri") != "https://forge.example/cb" ||
		q.Get("scope") != "repo" || q.Get("state") != "state-xyz" {
		t.Errorf("authorize query missing/wrong: %v", q)
	}
}

func TestExchangeCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" || r.Method != http.MethodPost {
			t.Errorf("unexpected token request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.Form.Get("code") != "the-code" || r.Form.Get("client_secret") != "secret" {
			t.Errorf("token exchange form wrong: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "gho_token123"})
	}))
	defer srv.Close()

	cfg := Config{ClientID: "cid", ClientSecret: "secret", RedirectURL: "https://forge.example/cb", OAuthBaseURL: srv.URL}
	token, err := ExchangeCode(context.Background(), srv.Client(), cfg, "the-code")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if token != "gho_token123" {
		t.Errorf("token = %q, want gho_token123", token)
	}
}

func TestExchangeCodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code", "error_description": "expired"})
	}))
	defer srv.Close()
	cfg := Config{OAuthBaseURL: srv.URL}
	if _, err := ExchangeCode(context.Background(), srv.Client(), cfg, "x"); err == nil {
		t.Error("ExchangeCode must error when GitHub returns an error payload")
	}
}

func TestCreateRepository(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/repos" || r.Method != http.MethodPost {
			t.Errorf("unexpected repo request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer token: %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "my-app" || body["auto_init"] != false {
			t.Errorf("create-repo body wrong: %v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"full_name": "octo/my-app", "html_url": "https://gh.test/octo/my-app",
			"owner": map[string]string{"login": "octo"}, "default_branch": "main",
		})
	}))
	defer srv.Close()

	cfg := Config{APIBaseURL: srv.URL}
	repo, err := CreateRepository(context.Background(), srv.Client(), cfg, "tok", "my-app", true)
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if repo.FullName != "octo/my-app" || repo.Owner.Login != "octo" {
		t.Errorf("repo = %+v", repo)
	}
}

// TestDeleteRepository: DELETE /repos/{owner}/{repo} with a 204 succeeds.
func TestDeleteRepository(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotCType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		gotCType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := Config{APIBaseURL: srv.URL}
	if err := DeleteRepository(context.Background(), srv.Client(), cfg, "tok", "octo", "my-app"); err != nil {
		t.Fatalf("delete repo: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/repos/octo/my-app" {
		t.Errorf("delete request = %s %s, want DELETE /repos/octo/my-app", gotMethod, gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("missing bearer token: %q", gotAuth)
	}
	// A DELETE carries no body: no JSON null, no Content-Type.
	if len(gotBody) != 0 || gotCType != "" {
		t.Errorf("DELETE should be bodyless; body=%q content-type=%q", gotBody, gotCType)
	}
}

// TestDeleteRepositoryError: a non-204 surfaces as an error the caller can log.
func TestDeleteRepositoryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
	}))
	defer srv.Close()
	cfg := Config{APIBaseURL: srv.URL}
	if err := DeleteRepository(context.Background(), srv.Client(), cfg, "tok", "octo", "gone"); err == nil {
		t.Error("DeleteRepository must error on a non-204 status")
	}
}

// TestPushFiles drives the whole Git Data API sequence against a fake GitHub and
// asserts blobs → tree → commit → ref, with the tree referencing the pushed
// files and the ref pointed at the created commit.
func TestPushFiles(t *testing.T) {
	var treeEntries []map[string]any
	var refSHA string
	blobContents := map[string]string{} // decoded content -> sha

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/octo/my-app/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content, Encoding string
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Encoding != "base64" {
			t.Errorf("blob encoding = %q, want base64", body.Encoding)
		}
		dec, _ := base64.StdEncoding.DecodeString(body.Content)
		sha := "blob-" + string(dec) // deterministic fake sha keyed on content
		blobContents[string(dec)] = sha
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": sha})
	})
	mux.HandleFunc("/repos/octo/my-app/git/trees", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tree []map[string]any
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		treeEntries = body.Tree
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "tree-sha"})
	})
	mux.HandleFunc("/repos/octo/my-app/git/commits", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["tree"] != "tree-sha" {
			t.Errorf("commit tree = %v, want tree-sha", body["tree"])
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "commit-sha"})
	})
	mux.HandleFunc("/repos/octo/my-app/git/refs", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["ref"] != "refs/heads/main" {
			t.Errorf("ref = %v, want refs/heads/main", body["ref"])
		}
		refSHA, _ = body["sha"].(string)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := Config{APIBaseURL: srv.URL}
	files := []File{
		{Path: "go.mod", Content: []byte("module x")},
		{Path: "cmd/server/main.go", Content: []byte("package main")},
		{Path: "run.sh", Content: []byte("#!/bin/sh"), Executable: true},
	}
	if err := PushFiles(context.Background(), srv.Client(), cfg, "tok", "octo", "my-app", "main", files, "Initial export"); err != nil {
		t.Fatalf("push: %v", err)
	}

	if len(treeEntries) != 3 {
		t.Fatalf("tree has %d entries, want 3", len(treeEntries))
	}
	// The tree references files as blobs, by their created blob shas, with the
	// executable's mode preserved as 100755 and regular files as 100644.
	paths := map[string]string{}
	modes := map[string]string{}
	for _, e := range treeEntries {
		paths[e["path"].(string)] = e["sha"].(string)
		modes[e["path"].(string)] = e["mode"].(string)
		if e["type"] != "blob" {
			t.Errorf("tree entry type wrong: %v", e)
		}
	}
	if modes["go.mod"] != "100644" {
		t.Errorf("go.mod mode = %q, want 100644", modes["go.mod"])
	}
	if modes["run.sh"] != "100755" {
		t.Errorf("executable run.sh mode = %q, want 100755", modes["run.sh"])
	}
	if paths["go.mod"] != "blob-module x" || paths["cmd/server/main.go"] != "blob-package main" {
		t.Errorf("tree entries don't reference the pushed blobs: %v", paths)
	}
	if refSHA != "commit-sha" {
		t.Errorf("branch ref sha = %q, want commit-sha", refSHA)
	}
}

func TestPushFilesRejectsEmpty(t *testing.T) {
	if err := PushFiles(context.Background(), http.DefaultClient, Config{}, "t", "o", "r", "main", nil, "m"); err == nil {
		t.Error("PushFiles must reject an empty file set")
	}
}

// TestDoSurfacesGitHubError: a non-expected status returns GitHub's message.
func TestDoSurfacesGitHubError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"name already exists on this account"}`)
	}))
	defer srv.Close()
	cfg := Config{APIBaseURL: srv.URL}
	_, err := CreateRepository(context.Background(), srv.Client(), cfg, "tok", "dup", false)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error must surface GitHub's message; got %v", err)
	}
}
