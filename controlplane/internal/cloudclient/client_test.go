package cloudclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cloudStub is a minimal stand-in for the §51 build endpoints. Each handler
// asserts the request shape the client is supposed to send.
func cloudStub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/projects/{projectID}/builds", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok-123" {
			t.Errorf("create build auth = %q, want Bearer tok-123", got)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("create build content-type = %q", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"source_commit":"rev-abc"`) {
			t.Errorf("create build body = %s", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"bld_1","status":"queued"}}`))
	})

	mux.HandleFunc("PUT /api/v1/projects/{projectID}/builds/{id}/source", func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/gzip" {
			t.Errorf("upload content-type = %q, want application/gzip", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "tar-gz-bytes" {
			t.Errorf("upload body = %q", body)
		}
		if r.PathValue("id") != "bld_1" {
			t.Errorf("upload build id = %q", r.PathValue("id"))
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"id":"bld_1","status":"queued"}}`))
	})

	mux.HandleFunc("GET /api/v1/projects/{projectID}/builds/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"bld_1","status":"succeeded","artifact_digest":"sha256:cafe"}}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestClientBuildLifecycle(t *testing.T) {
	srv := cloudStub(t)
	c := &Client{BaseURL: srv.URL, Token: "tok-123"}
	ctx := context.Background()

	b, err := c.CreateBuild(ctx, "prj_9", "rev-abc")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}
	if b.ID != "bld_1" || b.Status != "queued" {
		t.Fatalf("created build = %+v", b)
	}

	if err := c.UploadSource(ctx, "prj_9", b.ID, strings.NewReader("tar-gz-bytes")); err != nil {
		t.Fatalf("UploadSource: %v", err)
	}

	got, err := c.GetBuild(ctx, "prj_9", b.ID)
	if err != nil {
		t.Fatalf("GetBuild: %v", err)
	}
	if got.Status != "succeeded" || got.ArtifactDigest != "sha256:cafe" {
		t.Fatalf("fetched build = %+v", got)
	}
}

func TestClientGetBuildLogs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/builds/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "bld_1" {
			t.Errorf("build id = %q", r.PathValue("id"))
		}
		// The since filter is forwarded for tailing.
		if got := r.URL.Query().Get("since"); got != "2026-01-01T00:00:00Z" {
			t.Errorf("since = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":{"logs":[{"timestamp":"2026-01-01T00:00:01Z","stream":"build","message":"cloning"}]}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, Token: "tok"}
	logs, err := c.GetBuildLogs(context.Background(), "bld_1", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("GetBuildLogs: %v", err)
	}
	if len(logs) != 1 || logs[0].Message != "cloning" || logs[0].Stream != "build" {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestClientListEnvironments(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{projectID}/environments", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("projectID") != "prj_9" {
			t.Errorf("project id = %q", r.PathValue("projectID"))
		}
		_, _ = w.Write([]byte(`{"data":{"environments":[{"id":"env_prod","name":"production","kind":"persistent"},{"id":"env_stg","name":"staging","kind":"persistent"}]}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, Token: "t"}
	envs, err := c.ListEnvironments(context.Background(), "prj_9")
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	if len(envs) != 2 || envs[0].ID != "env_prod" || envs[0].Name != "production" || envs[1].ID != "env_stg" {
		t.Fatalf("envs = %+v", envs)
	}
}

func TestClientCreateDeploymentBlocked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/environments/{envID}/deployments", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("envID") != "env_prod" {
			t.Errorf("env id = %q", r.PathValue("envID"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"build_id":"bld_1"`) {
			t.Errorf("create deployment body = %s", body)
		}
		w.WriteHeader(http.StatusCreated)
		// A destructive migration awaiting approval: a created deployment carrying a block.
		_, _ = w.Write([]byte(`{"data":{"id":"dep_1","environment_id":"env_prod","build_id":"bld_1","status":"blocked_pending_approval","block":{"code":"migration_approval_required","migration_id":"mig_7","approval_url":"https://console.example/databases/db_2/migrations"}}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, Token: "t"}
	d, err := c.CreateDeployment(context.Background(), "env_prod", "bld_1")
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if d.ID != "dep_1" || d.Status != "blocked_pending_approval" {
		t.Fatalf("deployment = %+v", d)
	}
	if d.Block == nil {
		t.Fatalf("expected a block, got none: %+v", d)
	}
	if d.Block.Code != "migration_approval_required" || d.Block.MigrationID != "mig_7" || d.Block.ApprovalURL == "" {
		t.Fatalf("block = %+v", d.Block)
	}
}

func TestClientGetDeploymentUnblocked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/environments/{envID}/deployments/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("envID") != "env_prod" || r.PathValue("id") != "dep_1" {
			t.Errorf("env=%q dep=%q", r.PathValue("envID"), r.PathValue("id"))
		}
		_, _ = w.Write([]byte(`{"data":{"id":"dep_1","environment_id":"env_prod","build_id":"bld_1","status":"running","artifact_digest":"sha256:cafe"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, Token: "t"}
	d, err := c.GetDeployment(context.Background(), "env_prod", "dep_1")
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if d.Status != "running" || d.ArtifactDigest != "sha256:cafe" || d.Block != nil {
		t.Fatalf("deployment = %+v (block should be nil)", d)
	}
}

func TestClientCreatePreviewEnvironment(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{projectID}/preview-environments", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("projectID") != "prj_9" {
			t.Errorf("project id = %q", r.PathValue("projectID"))
		}
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		if !strings.Contains(s, `"name":"preview-r42"`) || !strings.Contains(s, `"ttl_seconds":604800`) {
			t.Errorf("create preview body = %s", s)
		}
		if !strings.Contains(s, `"type":"forge"`) || !strings.Contains(s, `"ref":"r42"`) {
			t.Errorf("create preview source = %s", s)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"env_pr42","name":"preview-r42","kind":"ephemeral","state":"active","expires_at":"2026-01-08T00:00:00Z"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, Token: "t"}
	env, err := c.CreatePreviewEnvironment(context.Background(), "prj_9", "preview-r42", 604800, "forge", "r42")
	if err != nil {
		t.Fatalf("CreatePreviewEnvironment: %v", err)
	}
	if env.ID != "env_pr42" || env.Kind != "ephemeral" || env.ExpiresAt == "" {
		t.Fatalf("preview env = %+v", env)
	}
}

func TestClientGetDeploymentLogs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/deployments/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "dep_1" {
			t.Errorf("deployment id = %q", r.PathValue("id"))
		}
		if got := r.URL.Query().Get("since"); got != "2026-01-01T00:00:00Z" {
			t.Errorf("since = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":{"logs":[{"timestamp":"2026-01-01T00:00:01Z","stream":"stdout","message":"listening on :8080","request_id":"req_9"}]}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, Token: "t"}
	logs, err := c.GetDeploymentLogs(context.Background(), "dep_1", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("GetDeploymentLogs: %v", err)
	}
	if len(logs) != 1 || logs[0].Message != "listening on :8080" || logs[0].Stream != "stdout" || logs[0].RequestID != "req_9" {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestClientRollback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/environments/{envID}/rollback", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("envID") != "env_prod" {
			t.Errorf("env id = %q", r.PathValue("envID"))
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"dep_2","environment_id":"env_prod","build_id":"bld_0","status":"pending","restores_deployment_id":"dep_0","rolled_back_from_id":"dep_1"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, Token: "t"}
	d, err := c.Rollback(context.Background(), "env_prod")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if d.ID != "dep_2" || d.RestoresDeploymentID != "dep_0" || d.RolledBackFromID != "dep_1" {
		t.Fatalf("rollback deployment = %+v", d)
	}
}

func TestClientRollbackConflict(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/environments/{envID}/rollback", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"no previous healthy deployment to roll back to"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, Token: "t"}
	_, err := c.Rollback(context.Background(), "env_prod")
	var ce *Error
	if !errors.As(err, &ce) || ce.Status != http.StatusConflict || ce.Code != "conflict" {
		t.Fatalf("error = %v, want 409 conflict", err)
	}
}

func TestClientErrorEnvelope(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/builds/{id}/source", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"build_not_queued","message":"source can only be submitted to a queued build"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, Token: "t"}
	err := c.UploadSource(context.Background(), "p", "b", strings.NewReader("x"))
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v, want *cloudclient.Error", err)
	}
	if ce.Status != http.StatusConflict || ce.Code != "build_not_queued" {
		t.Fatalf("error = %+v, want 409 build_not_queued", ce)
	}
}

func TestClientNonEnvelopeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := &Client{BaseURL: srv.URL, Token: "t"}
	_, err := c.GetBuild(context.Background(), "p", "b")
	var ce *Error
	if !errors.As(err, &ce) || ce.Status != http.StatusInternalServerError || ce.Code != "unexpected" {
		t.Fatalf("error = %v, want 500 unexpected", err)
	}
}
