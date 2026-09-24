// Package cloudclient is the control plane's HTTP client for the Gombit Cloud
// §51 API: create a build for a Cloud project, upload the project's compiled
// source, read a build's state and logs, and — the deploy surface — list a
// project's environments, deploy a build to one, read a deployment (including a
// migration hold), and roll an environment back. It is the Forge side of the
// M4–M5 Cloud integration (ADR-005 §4.4/D2/D6) — Forge hands Cloud source and
// observes; it never builds, and it owns no deployment state machine. Cloud owns
// build execution, deployment lifecycle, health, rollback and the migration gate.
//
// The client is deliberately narrow — one call per concrete §51 endpoint — and
// carries no retry/queue policy of its own; the build worker owns build
// orchestration and the deploy routes are a thin pass-through. Every call
// presents an API token as `Authorization: Bearer` and unwraps Cloud's D10
// envelope ({"data": …} / {"error": {code, message}}).
package cloudclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client talks to a Gombit Cloud control plane. The zero value is not usable;
// BaseURL and Token are required.
type Client struct {
	// BaseURL is the Cloud API root (scheme + host, no trailing slash), e.g.
	// "https://cloud.gombit.dev". The client appends the /api/v1 path itself.
	BaseURL string
	// Token is a Cloud API token, presented as `Authorization: Bearer <token>`.
	Token string
	// HTTP is the transport. Nil uses a client with a sane default timeout.
	HTTP *http.Client
}

// Build is the subset of a Cloud build's state the control plane tracks.
type Build struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	ArtifactDigest string `json:"artifact_digest,omitempty"`
}

// BuildLog is one build log line as Cloud returns it (§40). Forge reads these
// from Cloud and relays them; it never stores them (ADR-005 §24).
type BuildLog struct {
	Timestamp string `json:"timestamp"`
	Stream    string `json:"stream,omitempty"`
	Message   string `json:"message"`
}

// Environment is the subset of a Cloud environment Forge surfaces as a deploy
// target. Cloud owns the environment lifecycle; Forge only lists the ones on the
// linked Cloud project and lets a human pick one to deploy a build to. Kind
// distinguishes a persistent environment from an ephemeral preview; State and
// ExpiresAt are set only for a preview (§46/§89), so the UI can show that a
// preview is throwaway and when it lapses.
type Environment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	State     string `json:"state,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// DeploymentBlock is Cloud's structured hold on a deployment that is waiting on a
// human to approve a destructive migration (§32; L9/L10). It is a legitimate
// lifecycle state, not an error: the deployment exists and will resume once the
// exact migration is approved in Cloud. Forge is a service principal and can
// never approve — it surfaces Code, MigrationID and Cloud's own ApprovalURL so a
// human resolves it in Cloud, then observes the same deployment resume.
type DeploymentBlock struct {
	Code        string `json:"code"`
	MigrationID string `json:"migration_id"`
	ApprovalURL string `json:"approval_url,omitempty"`
}

// Deployment is the subset of a Cloud deployment's state Forge tracks. Forge owns
// no deployment state machine (ADR-005 D2/D6): Cloud owns status, promotion,
// health, rollback and the migration gate; Forge holds only a pass-through view
// and a lightweight build_id→environment/deployment linkage. Block is set only
// when Status is "blocked_pending_approval".
type Deployment struct {
	ID                   string           `json:"id"`
	EnvironmentID        string           `json:"environment_id"`
	BuildID              string           `json:"build_id"`
	Status               string           `json:"status"`
	ArtifactDigest       string           `json:"artifact_digest,omitempty"`
	RestoresDeploymentID string           `json:"restores_deployment_id,omitempty"`
	RolledBackFromID     string           `json:"rolled_back_from_id,omitempty"`
	Block                *DeploymentBlock `json:"block,omitempty"`
}

// DeploymentLog is one application/runtime log line for a deployment (§40).
// Forge reads these from Cloud and relays them; it never collects or stores them
// (ADR-005 §24). Stream is Cloud's log channel (e.g. stdout/stderr); RequestID
// correlates a line to a request when the app emits it.
type DeploymentLog struct {
	// ID is Cloud's stable per-line id (§40). A client tailing with ?since (which
	// Cloud filters inclusively) uses it to drop the boundary line it already has,
	// rather than deduping on a non-unique wall-clock timestamp.
	ID         string `json:"id"`
	Timestamp  string `json:"timestamp"`
	Stream     string `json:"stream,omitempty"`
	Message    string `json:"message"`
	InstanceID string `json:"instance_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

// Error is a non-2xx Cloud response, carrying the D10 error envelope's code and
// message alongside the HTTP status, so a caller can branch on the code (e.g.
// "build_not_queued") rather than string-matching.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("cloud: %d %s: %s", e.Status, e.Code, e.Message)
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// CreateBuild creates a build for cloudProjectID referencing sourceCommit
// (opaque provenance) and returns it in the "queued" state. §51
// POST /projects/{projectID}/builds.
func (c *Client) CreateBuild(ctx context.Context, cloudProjectID, sourceCommit string) (Build, error) {
	body, err := json.Marshal(map[string]string{"source_commit": sourceCommit})
	if err != nil {
		return Build{}, fmt.Errorf("cloud: encode create build: %w", err)
	}
	var b Build
	if err := c.do(ctx, http.MethodPost, "/projects/"+cloudProjectID+"/builds", "application/json", bytes.NewReader(body), &b); err != nil {
		return Build{}, err
	}
	return b, nil
}

// UploadSource uploads a `.tar.gz` source archive for a queued build, which
// starts the build asynchronously on Cloud. §51
// PUT /projects/{projectID}/builds/{buildID}/source. A build that is no longer
// queued yields an *Error with Code "build_not_queued".
func (c *Client) UploadSource(ctx context.Context, cloudProjectID, buildID string, archive io.Reader) error {
	return c.do(ctx, http.MethodPut, "/projects/"+cloudProjectID+"/builds/"+buildID+"/source", "application/gzip", archive, nil)
}

// GetBuild reads a build's current state. §51
// GET /projects/{projectID}/builds/{buildID}.
func (c *Client) GetBuild(ctx context.Context, cloudProjectID, buildID string) (Build, error) {
	var b Build
	if err := c.do(ctx, http.MethodGet, "/projects/"+cloudProjectID+"/builds/"+buildID, "", nil, &b); err != nil {
		return Build{}, err
	}
	return b, nil
}

// GetBuildLogs reads a build's log lines from Cloud, optionally only those after
// `since` (an RFC3339 timestamp) for tailing. §51 GET /builds/{buildID}/logs.
// The build id addresses the log owner directly, so no Cloud project id is
// needed. An empty `since` returns all lines.
func (c *Client) GetBuildLogs(ctx context.Context, buildID, since string) ([]BuildLog, error) {
	path := "/builds/" + buildID + "/logs"
	if since != "" {
		path += "?since=" + url.QueryEscape(since)
	}
	var wrap struct {
		Logs []BuildLog `json:"logs"`
	}
	if err := c.do(ctx, http.MethodGet, path, "", nil, &wrap); err != nil {
		return nil, err
	}
	return wrap.Logs, nil
}

// ListEnvironments lists the deploy targets on a Cloud project. §51
// GET /projects/{projectID}/environments. The linked Cloud project owns them;
// Forge surfaces them so a human picks where a build deploys.
func (c *Client) ListEnvironments(ctx context.Context, cloudProjectID string) ([]Environment, error) {
	var wrap struct {
		Environments []Environment `json:"environments"`
	}
	if err := c.do(ctx, http.MethodGet, "/projects/"+cloudProjectID+"/environments", "", nil, &wrap); err != nil {
		return nil, err
	}
	return wrap.Environments, nil
}

// CreatePreviewEnvironment creates an ephemeral preview environment (§46/§89) on
// the Cloud project — a throwaway target with a TTL. §51
// POST /projects/{projectID}/preview-environments. sourceType/sourceRef are
// optional provenance (e.g. "forge"/<revision id>); ttlSeconds is the preview's
// lifetime (Cloud caps it). Deploying a build into the returned environment, and
// redeploying on each rebuild, is how a preview is refreshed — Cloud's promotion
// switches traffic atomically to the new healthy revision (L12/§23).
func (c *Client) CreatePreviewEnvironment(ctx context.Context, cloudProjectID, name string, ttlSeconds int64, sourceType, sourceRef string) (Environment, error) {
	body := map[string]any{"name": name, "ttl_seconds": ttlSeconds}
	if sourceType != "" || sourceRef != "" {
		body["source"] = map[string]string{"type": sourceType, "ref": sourceRef}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Environment{}, fmt.Errorf("cloud: encode create preview environment: %w", err)
	}
	var e Environment
	if err := c.do(ctx, http.MethodPost, "/projects/"+cloudProjectID+"/preview-environments", "application/json", bytes.NewReader(raw), &e); err != nil {
		return Environment{}, err
	}
	return e, nil
}

// CreateDeployment deploys a build to an environment and returns the created
// deployment. §51 POST /environments/{envID}/deployments. Cloud runs the C3
// migration preflight as part of creation: an unapproved destructive migration
// does not fail the call — Cloud returns a created deployment in
// "blocked_pending_approval" carrying a Block, and the same deployment resumes
// once a human approves the migration in Cloud (L10).
func (c *Client) CreateDeployment(ctx context.Context, envID, buildID string) (Deployment, error) {
	body, err := json.Marshal(map[string]string{"build_id": buildID})
	if err != nil {
		return Deployment{}, fmt.Errorf("cloud: encode create deployment: %w", err)
	}
	var d Deployment
	if err := c.do(ctx, http.MethodPost, "/environments/"+envID+"/deployments", "application/json", bytes.NewReader(body), &d); err != nil {
		return Deployment{}, err
	}
	return d, nil
}

// GetDeployment reads a deployment's current state, including its Block when it is
// held on a migration. §51 GET /environments/{envID}/deployments/{id}.
func (c *Client) GetDeployment(ctx context.Context, envID, deploymentID string) (Deployment, error) {
	var d Deployment
	if err := c.do(ctx, http.MethodGet, "/environments/"+envID+"/deployments/"+deploymentID, "", nil, &d); err != nil {
		return Deployment{}, err
	}
	return d, nil
}

// GetDeploymentLogs reads a deployment's application logs from Cloud, optionally
// only those after `since` (an RFC3339 timestamp) for tailing. §51
// GET /deployments/{deploymentID}/logs. The deployment id addresses the log owner
// directly, so no environment id is needed on the wire. An empty `since` returns
// all lines.
func (c *Client) GetDeploymentLogs(ctx context.Context, deploymentID, since string) ([]DeploymentLog, error) {
	path := "/deployments/" + deploymentID + "/logs"
	if since != "" {
		path += "?since=" + url.QueryEscape(since)
	}
	var wrap struct {
		Logs []DeploymentLog `json:"logs"`
	}
	if err := c.do(ctx, http.MethodGet, path, "", nil, &wrap); err != nil {
		return nil, err
	}
	return wrap.Logs, nil
}

// Rollback rolls an environment back to its previous healthy revision, returning
// the new forward deployment that re-deploys the earlier build (§92 — rollback is
// a new deployment, not a backward pointer move). §51
// POST /environments/{envID}/rollback. An environment with nothing to roll back
// to yields an *Error with Code "conflict".
func (c *Client) Rollback(ctx context.Context, envID string) (Deployment, error) {
	var d Deployment
	if err := c.do(ctx, http.MethodPost, "/environments/"+envID+"/rollback", "", nil, &d); err != nil {
		return Deployment{}, err
	}
	return d, nil
}

// do issues a request against the §51 API and unwraps the D10 envelope. On a
// 2xx it decodes {"data": …} into out (when out is non-nil); on a non-2xx it
// decodes {"error": {code, message}} into an *Error. contentType is set only when
// a body is sent.
func (c *Client) do(ctx context.Context, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api/v1"+path, body)
	if err != nil {
		return fmt.Errorf("cloud: build request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("cloud: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("cloud: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeError(resp.StatusCode, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("cloud: decode response: %w", err)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("cloud: decode data: %w", err)
	}
	return nil
}

// decodeError turns a non-2xx response into an *Error, falling back to a generic
// code/message when the body is not the expected error envelope.
func decodeError(status int, raw []byte) error {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Error.Code == "" {
		return &Error{Status: status, Code: "unexpected", Message: http.StatusText(status)}
	}
	return &Error{Status: status, Code: env.Error.Code, Message: env.Error.Message}
}
