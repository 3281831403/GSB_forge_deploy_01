package cloudbuild

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gombit-dev/gombit/auth"
	"github.com/gombit-dev/gombit/contract"
	"github.com/gombit-dev/gombit/framework"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/cloudclient"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/org"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
)

const cookieSecurityName = "cookieAuth"

// Projects is the subset of project.Service the deploy routes need: load a
// project (for its org and Cloud linkage, to authorize and target) and resolve
// its head revision (to freeze).
type Projects interface {
	GetProject(ctx context.Context, projectID uint) (project.Project, error)
	Head(ctx context.Context, projectID uint) (project.Revision, bool, error)
}

// Authorizer is org.Service.Authorize — the per-org capability check.
type Authorizer interface {
	Authorize(ctx context.Context, orgID, userID uint, capability org.Capability) error
}

// Cloud is the slice of the Gombit Cloud §51 client the deploy routes call. It
// covers reading a build's logs and the deployment surface — list a project's
// environments, deploy a build to one, read a deployment (including a migration
// hold), and roll an environment back. *cloudclient.Client satisfies it. Forge
// relays Cloud's logs and proxies deployment actions; it stores no logs (ADR-005
// §24) and owns no deployment state machine (ADR-005 D2/D6) — Cloud is the source
// of truth for build and deployment lifecycle.
type Cloud interface {
	GetBuildLogs(ctx context.Context, buildID, since string) ([]cloudclient.BuildLog, error)
	ListEnvironments(ctx context.Context, cloudProjectID string) ([]cloudclient.Environment, error)
	CreatePreviewEnvironment(ctx context.Context, cloudProjectID, name string, ttlSeconds int64, sourceType, sourceRef string) (cloudclient.Environment, error)
	CreateDeployment(ctx context.Context, envID, buildID string) (cloudclient.Deployment, error)
	GetDeployment(ctx context.Context, envID, deploymentID string) (cloudclient.Deployment, error)
	GetDeploymentLogs(ctx context.Context, deploymentID, since string) ([]cloudclient.DeploymentLog, error)
	Rollback(ctx context.Context, envID string) (cloudclient.Deployment, error)
}

// Register mounts the cloud-build (deploy) routes on the app behind the cookie
// gate.
func Register(app *framework.App, jobs *Service, projects Projects, authz Authorizer, cloud Cloud) error {
	authSvc, err := auth.NewService(app.DB(), app.Config())
	if err != nil {
		return err
	}
	RegisterRoutes(app.API(), app.Config().API.Prefix,
		huma.Middlewares{authSvc.RequireCookieSession()}, jobs, projects, authz, cloud)
	return nil
}

// RegisterRoutes wires the deploy/build-job operations onto api behind gate.
// Split from Register so the real routes + cookie gate are testable on a
// humatest API without a full framework.App.
func RegisterRoutes(api huma.API, prefix string, gate huma.Middlewares, jobs *Service, projects Projects, authz Authorizer, cloud Cloud) {
	h := &handler{jobs: jobs, projects: projects, authz: authz, cloud: cloud}
	security := []map[string][]string{{cookieSecurityName: {}}}
	tags := []string{"Deploy"}

	huma.Register(api, huma.Operation{
		OperationID:   "deploy-project",
		Method:        http.MethodPost,
		Path:          prefix + "/projects/{projectID}/deploy",
		Summary:       "Deploy the project's current revision to Gombit Cloud",
		Description:   "Freezes the project's head revision and enqueues an asynchronous Cloud build (source is assembled and submitted by a worker; no request performs a build). Returns 202 with a build-job id to poll.",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusAccepted,
	}, h.deploy)

	huma.Register(api, huma.Operation{
		OperationID: "get-build-job",
		Method:      http.MethodGet,
		Path:        prefix + "/build-jobs/{jobID}",
		Summary:     "Get a Cloud build job's status",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.get)

	huma.Register(api, huma.Operation{
		OperationID: "list-project-build-jobs",
		Method:      http.MethodGet,
		Path:        prefix + "/projects/{projectID}/build-jobs",
		Summary:     "List a project's Cloud build jobs (deploy history)",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.list)

	huma.Register(api, huma.Operation{
		OperationID: "get-build-job-logs",
		Method:      http.MethodGet,
		Path:        prefix + "/build-jobs/{jobID}/logs",
		Summary:     "Read a build job's Cloud build logs",
		Description: "Relays the Cloud build's log lines (read from Cloud, not stored by Forge). Pass ?since=<RFC3339> to tail only newer lines. Empty until the worker has created the Cloud build.",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.logsHandler)

	huma.Register(api, huma.Operation{
		OperationID: "list-project-environments",
		Method:      http.MethodGet,
		Path:        prefix + "/projects/{projectID}/environments",
		Summary:     "List the linked Cloud project's environments (deploy targets)",
		Description: "Lists the environments of the project's linked Gombit Cloud project, so a human can pick where to deploy. Cloud owns the environments; Forge only surfaces them. 422 if the project is not linked to Cloud.",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.listEnvironments)

	huma.Register(api, huma.Operation{
		OperationID:   "create-preview-environment",
		Method:        http.MethodPost,
		Path:          prefix + "/projects/{projectID}/preview-environments",
		Summary:       "Create an ephemeral preview environment on the linked Cloud project",
		Description:   "Asks Gombit Cloud to create a throwaway preview environment (with a TTL) for the project's current revision. Deploying a build into it — and redeploying on each preview rebuild — refreshes the preview; Cloud's promotion switches traffic to the new healthy revision atomically (L12/§23). 422 if the project is not linked to Cloud or has no revision to preview.",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusCreated,
	}, h.createPreviewEnvironment)

	huma.Register(api, huma.Operation{
		OperationID:   "deploy-build-to-environment",
		Method:        http.MethodPost,
		Path:          prefix + "/projects/{projectID}/environments/{envID}/deployments",
		Summary:       "Deploy a Cloud build to one of the project's environments",
		Description:   "Asks Gombit Cloud to deploy an already-built artifact (by Cloud build id) to the chosen environment. Cloud runs the migration preflight and owns the deployment lifecycle; a destructive migration awaiting approval comes back as a created deployment in blocked_pending_approval with a block (Forge never approves — L10).",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusAccepted,
	}, h.createDeployment)

	huma.Register(api, huma.Operation{
		OperationID: "get-environment-deployment",
		Method:      http.MethodGet,
		Path:        prefix + "/projects/{projectID}/environments/{envID}/deployments/{deploymentID}",
		Summary:     "Get a Cloud deployment's status (including a migration hold)",
		Description: "Reads a deployment's current Cloud state, pass-through. When held on a destructive migration it carries a block with the Cloud-generated approval_url; the same deployment resumes once a human approves the migration in Cloud.",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.getDeployment)

	huma.Register(api, huma.Operation{
		OperationID: "get-deployment-logs",
		Method:      http.MethodGet,
		Path:        prefix + "/projects/{projectID}/environments/{envID}/deployments/{deploymentID}/logs",
		Summary:     "Read a deployment's application logs from Gombit Cloud",
		Description: "Relays a deployment's application/runtime log lines (read from Cloud, not stored by Forge — ADR-005 §24). Pass ?since=<RFC3339> to tail only newer lines. Empty until the deployment's app has emitted logs.",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.deploymentLogs)

	huma.Register(api, huma.Operation{
		OperationID:   "rollback-environment",
		Method:        http.MethodPost,
		Path:          prefix + "/projects/{projectID}/environments/{envID}/rollback",
		Summary:       "Roll a project's environment back to its previous healthy revision",
		Description:   "Asks Gombit Cloud to roll the environment back. Cloud creates a new forward deployment that re-deploys the earlier build (§92); Forge owns no rollback state. 409 if there is no previous healthy deployment to restore.",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusAccepted,
	}, h.rollback)
}

type handler struct {
	jobs     *Service
	projects Projects
	authz    Authorizer
	cloud    Cloud
}

// jobData is a build job as the API exposes it.
type jobData struct {
	ID             uint   `json:"id"`
	ProjectID      uint   `json:"project_id"`
	Status         string `json:"status"`
	CloudBuildID   string `json:"cloud_build_id,omitempty" doc:"The Gombit Cloud build id, once the worker has created it"`
	CloudStatus    string `json:"cloud_status,omitempty" doc:"The last Cloud build state observed (Cloud's own vocabulary)"`
	ArtifactDigest string `json:"artifact_digest,omitempty" doc:"The content-addressed artifact digest, once the build has succeeded"`
	Error          string `json:"error,omitempty" doc:"A sanitized failure reason, once the job has failed"`
}

func toJobData(j BuildJob) jobData {
	return jobData{
		ID: j.ID, ProjectID: j.ProjectID, Status: string(j.Status),
		CloudBuildID: j.CloudBuildID, CloudStatus: j.CloudStatus,
		ArtifactDigest: j.ArtifactDigest, Error: j.Error,
	}
}

type deployInput struct {
	ProjectID string `path:"projectID" doc:"Project identifier"`
}

type deployOutput struct {
	Status int
	Body   contract.Data[jobData]
}

func (h *handler) deploy(ctx context.Context, in *deployInput) (*deployOutput, error) {
	// Deploying builds and ships the project's source on Cloud — a write action,
	// gated on edit (not view), so a future read-only viewer can't trigger a
	// deploy. A non-member gets NotFound so cross-org project ids can't be probed.
	p, user, err := h.loadAuthorized(ctx, in.ProjectID, org.CapProjectEdit)
	if err != nil {
		return nil, err
	}

	head, ok, err := h.projects.Head(ctx, p.ID)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.Internal("could not resolve the project's head revision"))
	}
	if !ok {
		return nil, contract.WithContext(ctx, contract.Validation("the project has no revision to deploy", map[string][]string{
			"project": {"create a revision before deploying"},
		}))
	}

	cloudProjectID := ""
	if p.CloudProjectID != nil {
		cloudProjectID = *p.CloudProjectID
	}
	job, err := h.jobs.Enqueue(ctx, p.ID, head.ID, user.ID, cloudProjectID)
	if err != nil {
		// A project not linked to Cloud is a precondition failure (422), not a
		// storage fault (500).
		if errors.Is(err, ErrNoCloudProject) {
			return nil, contract.WithContext(ctx, contract.Validation("the project is not linked to a Gombit Cloud project", map[string][]string{
				"project": {"link the project to Gombit Cloud before deploying"},
			}))
		}
		return nil, contract.WithContext(ctx, contract.Internal("could not enqueue the deploy"))
	}
	return &deployOutput{Status: http.StatusAccepted, Body: contract.Data[jobData]{Data: toJobData(job)}}, nil
}

type getInput struct {
	JobID string `path:"jobID" doc:"Build job identifier"`
}

type getOutput struct {
	Body contract.Data[jobData]
}

func (h *handler) get(ctx context.Context, in *getInput) (*getOutput, error) {
	user, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	jobID, err := parseID(in.JobID)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.NotFound("build job not found"))
	}
	job, ok, err := h.jobs.Get(ctx, jobID)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.Internal("could not load the build job"))
	}
	if !ok {
		return nil, contract.WithContext(ctx, contract.NotFound("build job not found"))
	}
	// Deploy history is project-scoped, not initiator-private: any project viewer
	// can poll any of the project's jobs, consistent with the list endpoint (which
	// already exposes the same fields). A non-viewer, or a vanished project, maps
	// to the same build-job NotFound, so a job's existence isn't leaked.
	if _, err := h.resolveAuthorized(ctx, job.ProjectID, user.ID, org.CapProjectView); err != nil {
		return nil, mapAuthErr(ctx, err, "build job")
	}
	return &getOutput{Body: contract.Data[jobData]{Data: toJobData(job)}}, nil
}

type listInput struct {
	ProjectID string `path:"projectID" doc:"Project identifier"`
}

type listOutput struct {
	Body contract.Data[[]jobData]
}

func (h *handler) list(ctx context.Context, in *listInput) (*listOutput, error) {
	// Listing a project's deploy history is a read within the project; gate on
	// view. Tenancy-safe: a non-member sees NotFound.
	p, _, err := h.loadAuthorized(ctx, in.ProjectID, org.CapProjectView)
	if err != nil {
		return nil, err
	}
	jobs, err := h.jobs.ListForProject(ctx, p.ID)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.Internal("could not list the project's build jobs"))
	}
	out := make([]jobData, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toJobData(j))
	}
	return &listOutput{Body: contract.Data[[]jobData]{Data: out}}, nil
}

type logsInput struct {
	JobID string `path:"jobID" doc:"Build job identifier"`
	Since string `query:"since" doc:"Only return log lines after this RFC3339 timestamp (for tailing)"`
}

// logLine is a build log line as the API exposes it — Forge's own shape, not the
// Cloud client's type, so the transport boundary doesn't leak into the API.
type logLine struct {
	Timestamp string `json:"timestamp"`
	Stream    string `json:"stream,omitempty"`
	Message   string `json:"message"`
}

type logsOutput struct {
	Body contract.Data[[]logLine]
}

func (h *handler) logsHandler(ctx context.Context, in *logsInput) (*logsOutput, error) {
	user, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	jobID, err := parseID(in.JobID)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.NotFound("build job not found"))
	}
	job, ok, err := h.jobs.Get(ctx, jobID)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.Internal("could not load the build job"))
	}
	if !ok {
		return nil, contract.WithContext(ctx, contract.NotFound("build job not found"))
	}
	// Project-scoped, like get/list: any project viewer can read the logs.
	if _, err := h.resolveAuthorized(ctx, job.ProjectID, user.ID, org.CapProjectView); err != nil {
		return nil, mapAuthErr(ctx, err, "build job")
	}
	// No Cloud build has been created yet (the worker hasn't run, or the job
	// failed before submitting) — there are simply no logs, not an error.
	if job.CloudBuildID == "" {
		return &logsOutput{Body: contract.Data[[]logLine]{Data: []logLine{}}}, nil
	}
	lines, err := h.cloud.GetBuildLogs(ctx, job.CloudBuildID, in.Since)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.Internal("could not read the build logs from Gombit Cloud"))
	}
	out := make([]logLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, logLine{Timestamp: l.Timestamp, Stream: l.Stream, Message: l.Message})
	}
	return &logsOutput{Body: contract.Data[[]logLine]{Data: out}}, nil
}

// environmentData is a Cloud environment as the deploy API exposes it — a deploy
// target the human picks. Forge's own shape, so the Cloud transport type doesn't
// leak into the API.
type environmentData struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty" doc:"The Cloud environment kind (e.g. persistent, ephemeral)"`
	State     string `json:"state,omitempty" doc:"Reclamation lifecycle state, set only for an ephemeral preview"`
	ExpiresAt string `json:"expires_at,omitempty" doc:"When an ephemeral preview lapses; empty for a persistent environment"`
}

// deploymentBlock mirrors Cloud's structured migration hold: a deployment waiting
// on a human to approve a destructive migration (§32; L9/L10). Forge surfaces it —
// it never approves.
type deploymentBlock struct {
	Code        string `json:"code" doc:"Why the deployment is held (e.g. migration_approval_required)"`
	MigrationID string `json:"migration_id" doc:"The migration a human must approve in Cloud"`
	ApprovalURL string `json:"approval_url,omitempty" doc:"Cloud-generated deep link where a human approves the migration"`
}

// deploymentData is a Cloud deployment as the deploy API exposes it. Pass-through:
// Cloud owns status, promotion, health, rollback and the migration gate; Forge
// mirrors the view and holds no deployment state of its own (ADR-005 D2/D6).
type deploymentData struct {
	ID                   string           `json:"id"`
	EnvironmentID        string           `json:"environment_id"`
	BuildID              string           `json:"build_id"`
	Status               string           `json:"status"`
	ArtifactDigest       string           `json:"artifact_digest,omitempty"`
	RestoresDeploymentID string           `json:"restores_deployment_id,omitempty" doc:"For a rollback, the earlier deployment whose build this restores"`
	RolledBackFromID     string           `json:"rolled_back_from_id,omitempty" doc:"For a rollback, the deployment that was current when the rollback was requested"`
	Block                *deploymentBlock `json:"block,omitempty" doc:"Set only when Status is blocked_pending_approval"`
}

func toDeploymentData(d cloudclient.Deployment) deploymentData {
	out := deploymentData{
		ID: d.ID, EnvironmentID: d.EnvironmentID, BuildID: d.BuildID, Status: d.Status,
		ArtifactDigest: d.ArtifactDigest, RestoresDeploymentID: d.RestoresDeploymentID,
		RolledBackFromID: d.RolledBackFromID,
	}
	if d.Block != nil {
		out.Block = &deploymentBlock{Code: d.Block.Code, MigrationID: d.Block.MigrationID, ApprovalURL: d.Block.ApprovalURL}
	}
	return out
}

type listEnvironmentsInput struct {
	ProjectID string `path:"projectID" doc:"Project identifier"`
}

type listEnvironmentsOutput struct {
	Body contract.Data[[]environmentData]
}

func (h *handler) listEnvironments(ctx context.Context, in *listEnvironmentsInput) (*listEnvironmentsOutput, error) {
	// Listing deploy targets is a read within the project; gate on view.
	p, _, err := h.loadAuthorized(ctx, in.ProjectID, org.CapProjectView)
	if err != nil {
		return nil, err
	}
	cloudProjectID, err := h.cloudProjectID(ctx, p)
	if err != nil {
		return nil, err
	}
	envs, err := h.cloud.ListEnvironments(ctx, cloudProjectID)
	if err != nil {
		return nil, mapCloudErr(ctx, err, "environments")
	}
	out := make([]environmentData, 0, len(envs))
	for _, e := range envs {
		out = append(out, environmentData{ID: e.ID, Name: e.Name, Kind: e.Kind, State: e.State, ExpiresAt: e.ExpiresAt})
	}
	return &listEnvironmentsOutput{Body: contract.Data[[]environmentData]{Data: out}}, nil
}

// defaultPreviewTTLSeconds is the lifetime Forge requests for a preview when the
// caller doesn't specify one — a week, well under Cloud's 30-day cap. Cloud
// validates and bounds it regardless.
const defaultPreviewTTLSeconds = 7 * 24 * 60 * 60

type createPreviewEnvironmentInput struct {
	ProjectID string `path:"projectID" doc:"Project identifier"`
	Body      struct {
		Name       string `json:"name,omitempty" doc:"Optional environment name (lowercase slug); defaults to preview-r<revision> for the current revision"`
		TTLSeconds int64  `json:"ttl_seconds,omitempty" doc:"Optional preview lifetime in seconds; defaults to one week, capped by Cloud"`
	}
}

type createPreviewEnvironmentOutput struct {
	Status int
	Body   contract.Data[environmentData]
}

func (h *handler) createPreviewEnvironment(ctx context.Context, in *createPreviewEnvironmentInput) (*createPreviewEnvironmentOutput, error) {
	// Creating a preview is a write (it provisions a Cloud environment); gate on edit.
	p, _, err := h.loadAuthorized(ctx, in.ProjectID, org.CapProjectEdit)
	if err != nil {
		return nil, err
	}
	cloudProjectID, err := h.cloudProjectID(ctx, p)
	if err != nil {
		return nil, err
	}
	// A preview is of the project's current revision — resolve it for provenance
	// and a sensible default name. No revision means nothing to preview (422).
	head, ok, err := h.projects.Head(ctx, p.ID)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.Internal("could not resolve the project's head revision"))
	}
	if !ok {
		return nil, contract.WithContext(ctx, contract.Validation("the project has no revision to preview", map[string][]string{
			"project": {"create a revision before previewing"},
		}))
	}
	name := in.Body.Name
	if name == "" {
		name = fmt.Sprintf("preview-r%d", head.ID)
	}
	ttl := in.Body.TTLSeconds
	if ttl <= 0 {
		ttl = defaultPreviewTTLSeconds
	}
	// Provenance: mark the preview as Forge-created and pin the revision it previews.
	env, err := h.cloud.CreatePreviewEnvironment(ctx, cloudProjectID, name, ttl, "forge", fmt.Sprintf("r%d", head.ID))
	if err != nil {
		// Cloud enforces (project, name) uniqueness, so a second create for the same
		// revision 409s. A preview of a revision is a single environment you redeploy
		// into to refresh it — so treat create as idempotent: return the existing
		// preview (200) for the operator to reselect and redeploy, rather than a
		// confusing Conflict on a button that looks like it should just work, or a
		// pile of orphaned week-TTL environments for one revision.
		var ce *cloudclient.Error
		if errors.As(err, &ce) && ce.Status == http.StatusConflict {
			if existing, ok, ferr := h.findEnvironmentByName(ctx, cloudProjectID, name); ferr == nil && ok {
				return &createPreviewEnvironmentOutput{Status: http.StatusOK, Body: contract.Data[environmentData]{Data: existing}}, nil
			}
		}
		return nil, mapCloudErr(ctx, err, "preview environment")
	}
	return &createPreviewEnvironmentOutput{
		Status: http.StatusCreated,
		Body:   contract.Data[environmentData]{Data: environmentData{ID: env.ID, Name: env.Name, Kind: env.Kind, State: env.State, ExpiresAt: env.ExpiresAt}},
	}, nil
}

// findEnvironmentByName returns the project's environment with the given name, if
// any. Used to make preview creation idempotent when Cloud reports the name is
// already taken.
func (h *handler) findEnvironmentByName(ctx context.Context, cloudProjectID, name string) (environmentData, bool, error) {
	envs, err := h.cloud.ListEnvironments(ctx, cloudProjectID)
	if err != nil {
		return environmentData{}, false, err
	}
	for _, e := range envs {
		if e.Name == name {
			return environmentData{ID: e.ID, Name: e.Name, Kind: e.Kind, State: e.State, ExpiresAt: e.ExpiresAt}, true, nil
		}
	}
	return environmentData{}, false, nil
}

type createDeploymentInput struct {
	ProjectID string `path:"projectID" doc:"Project identifier"`
	EnvID     string `path:"envID" doc:"Cloud environment identifier (one of the project's environments)"`
	Body      struct {
		BuildID string `json:"build_id" doc:"The Gombit Cloud build id to deploy (from a succeeded build job's cloud_build_id)"`
	}
}

type createDeploymentOutput struct {
	Status int
	Body   contract.Data[deploymentData]
}

func (h *handler) createDeployment(ctx context.Context, in *createDeploymentInput) (*createDeploymentOutput, error) {
	// Deploying is a write; gate on edit so a read-only viewer can't ship. The env
	// must belong to the project (tenancy-safe), never an arbitrary Cloud env id.
	if in.Body.BuildID == "" {
		return nil, contract.WithContext(ctx, contract.Validation("a build id is required", map[string][]string{
			"build_id": {"provide the Cloud build id to deploy"},
		}))
	}
	if _, err := h.authorizedEnv(ctx, in.ProjectID, in.EnvID, org.CapProjectEdit); err != nil {
		return nil, err
	}
	d, err := h.cloud.CreateDeployment(ctx, in.EnvID, in.Body.BuildID)
	if err != nil {
		return nil, mapCloudErr(ctx, err, "deployment")
	}
	return &createDeploymentOutput{Status: http.StatusAccepted, Body: contract.Data[deploymentData]{Data: toDeploymentData(d)}}, nil
}

type getDeploymentInput struct {
	ProjectID    string `path:"projectID" doc:"Project identifier"`
	EnvID        string `path:"envID" doc:"Cloud environment identifier"`
	DeploymentID string `path:"deploymentID" doc:"Cloud deployment identifier"`
}

type getDeploymentOutput struct {
	Body contract.Data[deploymentData]
}

func (h *handler) getDeployment(ctx context.Context, in *getDeploymentInput) (*getDeploymentOutput, error) {
	// Reading deployment status is a project read; gate on view.
	if _, err := h.authorizedEnv(ctx, in.ProjectID, in.EnvID, org.CapProjectView); err != nil {
		return nil, err
	}
	d, err := h.cloud.GetDeployment(ctx, in.EnvID, in.DeploymentID)
	if err != nil {
		return nil, mapCloudErr(ctx, err, "deployment")
	}
	return &getDeploymentOutput{Body: contract.Data[deploymentData]{Data: toDeploymentData(d)}}, nil
}

// appLogLine is a deployment application-log line as the API exposes it — Forge's
// own shape, so the Cloud transport type doesn't leak into the API. Stream is
// Cloud's log channel (stdout/stderr); the UI renders it as the line's level.
type appLogLine struct {
	ID         string `json:"id" doc:"Cloud's stable per-line id, used by the tail to dedup the inclusive-since boundary"`
	Timestamp  string `json:"timestamp"`
	Stream     string `json:"stream,omitempty"`
	Message    string `json:"message"`
	InstanceID string `json:"instance_id,omitempty" doc:"The runtime instance that emitted the line, when Cloud reports it"`
	RequestID  string `json:"request_id,omitempty"`
}

type deploymentLogsInput struct {
	ProjectID    string `path:"projectID" doc:"Project identifier"`
	EnvID        string `path:"envID" doc:"Cloud environment identifier"`
	DeploymentID string `path:"deploymentID" doc:"Cloud deployment identifier"`
	Since        string `query:"since" doc:"Only return log lines after this RFC3339 timestamp (for tailing)"`
}

type deploymentLogsOutput struct {
	Body contract.Data[[]appLogLine]
}

func (h *handler) deploymentLogs(ctx context.Context, in *deploymentLogsInput) (*deploymentLogsOutput, error) {
	// Reading application logs is a project read; gate on view.
	if _, err := h.authorizedEnv(ctx, in.ProjectID, in.EnvID, org.CapProjectView); err != nil {
		return nil, err
	}
	// authorizedEnv proves EnvID is the project's — NOT that this deployment lives
	// in it. The Cloud log endpoint is addressed by deployment id alone (GET
	// /deployments/{id}/logs) and Forge calls Cloud under a global service token
	// that can see every tenant's deployments, so reading a caller-supplied
	// deployment id without binding it to the authorized env would leak another
	// tenant's application logs. GetDeployment is env-scoped by Cloud, so a
	// deployment outside EnvID 404s here before any log is read.
	if _, err := h.cloud.GetDeployment(ctx, in.EnvID, in.DeploymentID); err != nil {
		return nil, mapCloudErr(ctx, err, "deployment")
	}
	lines, err := h.cloud.GetDeploymentLogs(ctx, in.DeploymentID, in.Since)
	if err != nil {
		return nil, mapCloudErr(ctx, err, "deployment")
	}
	out := make([]appLogLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, appLogLine{ID: l.ID, Timestamp: l.Timestamp, Stream: l.Stream, Message: l.Message, InstanceID: l.InstanceID, RequestID: l.RequestID})
	}
	return &deploymentLogsOutput{Body: contract.Data[[]appLogLine]{Data: out}}, nil
}

type rollbackInput struct {
	ProjectID string `path:"projectID" doc:"Project identifier"`
	EnvID     string `path:"envID" doc:"Cloud environment identifier"`
}

type rollbackOutput struct {
	Status int
	Body   contract.Data[deploymentData]
}

func (h *handler) rollback(ctx context.Context, in *rollbackInput) (*rollbackOutput, error) {
	// Rollback is a write; gate on edit.
	if _, err := h.authorizedEnv(ctx, in.ProjectID, in.EnvID, org.CapProjectEdit); err != nil {
		return nil, err
	}
	d, err := h.cloud.Rollback(ctx, in.EnvID)
	if err != nil {
		return nil, mapCloudErr(ctx, err, "deployment")
	}
	return &rollbackOutput{Status: http.StatusAccepted, Body: contract.Data[deploymentData]{Data: toDeploymentData(d)}}, nil
}

// cloudProjectID returns the project's linked Cloud project id, or a 422 when the
// project is not linked — deploying to Cloud has no meaning without a counterpart.
func (h *handler) cloudProjectID(ctx context.Context, p project.Project) (string, error) {
	if p.CloudProjectID == nil || *p.CloudProjectID == "" {
		return "", contract.WithContext(ctx, contract.Validation("the project is not linked to a Gombit Cloud project", map[string][]string{
			"project": {"link the project to Gombit Cloud before deploying"},
		}))
	}
	return *p.CloudProjectID, nil
}

// authorizedEnv authorizes the caller on the project for capability and confirms
// envID is one of that project's linked Cloud environments — so a caller can only
// target an environment that actually belongs to the project they hold the
// capability on, never an arbitrary Cloud env id from another tenant. An env that
// isn't the project's maps to the same NotFound as a missing one (no existence
// leak). Returns the resolved Cloud project id.
func (h *handler) authorizedEnv(ctx context.Context, projectIDParam, envID string, capability org.Capability) (string, error) {
	p, _, err := h.loadAuthorized(ctx, projectIDParam, capability)
	if err != nil {
		return "", err
	}
	cloudProjectID, err := h.cloudProjectID(ctx, p)
	if err != nil {
		return "", err
	}
	envs, err := h.cloud.ListEnvironments(ctx, cloudProjectID)
	if err != nil {
		return "", mapCloudErr(ctx, err, "environments")
	}
	for _, e := range envs {
		if e.ID == envID {
			return cloudProjectID, nil
		}
	}
	return "", contract.WithContext(ctx, contract.NotFound("environment not found"))
}

// mapCloudErr maps a Gombit Cloud client error to a contract error, preserving the
// meaning of Cloud's HTTP status rather than collapsing every remote failure to a
// 500: a 404 stays a NotFound, a 409 a Conflict (e.g. nothing to roll back to),
// other 4xx a Validation. A transport failure or a 5xx is a genuine internal fault.
func mapCloudErr(ctx context.Context, err error, resource string) error {
	var ce *cloudclient.Error
	if errors.As(err, &ce) {
		switch {
		case ce.Status == http.StatusNotFound:
			return contract.WithContext(ctx, contract.NotFound(resource+" not found"))
		case ce.Status == http.StatusConflict:
			return contract.WithContext(ctx, contract.Conflict(ce.Message))
		case ce.Status >= 400 && ce.Status < 500:
			return contract.WithContext(ctx, contract.Validation(ce.Message, nil))
		}
	}
	return contract.WithContext(ctx, contract.Internal("could not reach Gombit Cloud for the "+resource))
}

// loadAuthorized resolves and authorizes the project named in a path param for
// the caller, mapping a non-member and a missing/invalid project alike to
// NotFound (tenancy-safe).
func (h *handler) loadAuthorized(ctx context.Context, projectIDParam string, capability org.Capability) (project.Project, auth.User, error) {
	user, err := caller(ctx)
	if err != nil {
		return project.Project{}, auth.User{}, err
	}
	projectID, err := parseID(projectIDParam)
	if err != nil {
		return project.Project{}, auth.User{}, contract.WithContext(ctx, contract.NotFound("project not found"))
	}
	p, err := h.resolveAuthorized(ctx, projectID, user.ID, capability)
	if err != nil {
		return project.Project{}, auth.User{}, mapAuthErr(ctx, err, "project")
	}
	return p, user, nil
}

// resolveAuthorized loads the project and checks the capability, returning the
// *unmapped* domain error (project.ErrProjectNotFound / org.ErrNotMember /
// org.ErrForbidden) or a genuine fault — so each caller maps it to the right
// resource's NotFound (a project route says "project", the job route says "build
// job"), rather than leaking that a job's project is the thing that was missing.
func (h *handler) resolveAuthorized(ctx context.Context, projectID, userID uint, capability org.Capability) (project.Project, error) {
	p, err := h.projects.GetProject(ctx, projectID)
	if err != nil {
		return project.Project{}, err
	}
	if err := h.authz.Authorize(ctx, p.OrganizationID, userID, capability); err != nil {
		return project.Project{}, err
	}
	return p, nil
}

// mapAuthErr maps a resolveAuthorized error to a contract error: a missing
// project, a non-member, and a forbidden caller all collapse to NotFound on
// resource (tenancy-safe — no existence leak, no absent/forbidden distinction);
// anything else is a genuine fault (500).
func mapAuthErr(ctx context.Context, err error, resource string) error {
	if errors.Is(err, project.ErrProjectNotFound) || errors.Is(err, org.ErrNotMember) || errors.Is(err, org.ErrForbidden) {
		return contract.WithContext(ctx, contract.NotFound(resource+" not found"))
	}
	return contract.WithContext(ctx, contract.Internal("could not authorize the "+resource))
}

func caller(ctx context.Context) (auth.User, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return auth.User{}, contract.WithContext(ctx, contract.Authentication("authentication required"))
	}
	return user, nil
}

func parseID(s string) (uint, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(n), nil
}
