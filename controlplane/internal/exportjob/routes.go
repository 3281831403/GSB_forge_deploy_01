package exportjob

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gombit-dev/gombit/auth"
	"github.com/gombit-dev/gombit/contract"
	"github.com/gombit-dev/gombit/framework"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/org"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
)

const cookieSecurityName = "cookieAuth"

// Projects is the subset of project.Service the export routes need: load a
// project (for its org, to authorize) and resolve its head revision (to freeze).
type Projects interface {
	GetProject(ctx context.Context, projectID uint) (project.Project, error)
	Head(ctx context.Context, projectID uint) (project.Revision, bool, error)
}

// Authorizer is org.Service.Authorize — the per-org capability check.
type Authorizer interface {
	Authorize(ctx context.Context, orgID, userID uint, capability org.Capability) error
}

// Register mounts the export-job routes on the app behind the cookie gate.
func Register(app *framework.App, jobs *Service, projects Projects, authz Authorizer) error {
	authSvc, err := auth.NewService(app.DB(), app.Config())
	if err != nil {
		return err
	}
	RegisterRoutes(app.API(), app.Config().API.Prefix,
		huma.Middlewares{authSvc.RequireCookieSession()}, jobs, projects, authz)
	return nil
}

// RegisterRoutes wires the create/get export-job operations onto api behind
// gate. Split from Register so the real routes + cookie gate are testable on a
// humatest API without a full framework.App.
func RegisterRoutes(api huma.API, prefix string, gate huma.Middlewares, jobs *Service, projects Projects, authz Authorizer) {
	h := &handler{jobs: jobs, projects: projects, authz: authz}
	security := []map[string][]string{{cookieSecurityName: {}}}
	tags := []string{"Export"}

	huma.Register(api, huma.Operation{
		OperationID:   "create-export-job",
		Method:        http.MethodPost,
		Path:          prefix + "/projects/{projectID}/export/github",
		Summary:       "Queue a GitHub export of the project's current revision",
		Description:   "Freezes the project's head revision and enqueues an asynchronous export to a new GitHub repository. Returns 202 with a job id to poll.",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusAccepted,
	}, h.create)

	huma.Register(api, huma.Operation{
		OperationID: "get-export-job",
		Method:      http.MethodGet,
		Path:        prefix + "/export-jobs/{jobID}",
		Summary:     "Get an export job's status",
		Tags:        tags,
		Security:    security,
		Middlewares: gate,
	}, h.get)
}

type handler struct {
	jobs     *Service
	projects Projects
	authz    Authorizer
}

// jobData is the export job as the API exposes it.
type jobData struct {
	ID        uint   `json:"id"`
	ProjectID uint   `json:"project_id"`
	Status    string `json:"status"`
	RepoURL   string `json:"repo_url,omitempty" doc:"The created repository URL, once the export has succeeded"`
	Error     string `json:"error,omitempty" doc:"A sanitized failure reason, once the export has failed"`
}

func toJobData(j ExportJob) jobData {
	return jobData{ID: j.ID, ProjectID: j.ProjectID, Status: string(j.Status), RepoURL: j.RepoURL, Error: j.Error}
}

type createInput struct {
	ProjectID string `path:"projectID" doc:"Project identifier"`
	Body      struct {
		Name string `json:"name" doc:"Name of the GitHub repository to create"`
		// Private is deliberately required, not defaulted: the caller must make an
		// explicit visibility choice so a project's source is never published to a
		// public repository by omission.
		Private bool `json:"private" doc:"Create the repository as private"`
	}
}

type createOutput struct {
	Status int
	Body   contract.Data[jobData]
}

func (h *handler) create(ctx context.Context, in *createInput) (*createOutput, error) {
	// Export copies the entire project source to a repository under the initiating
	// user's own GitHub account — an egress of the source, not a read within the
	// platform. Gate it on the edit capability, not view, so a future read-only
	// viewer role can't export (exfiltrate) source it may only look at. Today all
	// roles that hold View also hold Edit, so this changes no current outcome.
	// A non-member gets NotFound so cross-org project ids can't be probed.
	p, user, err := h.loadAuthorized(ctx, in.ProjectID, org.CapProjectEdit)
	if err != nil {
		return nil, err
	}

	head, ok, err := h.projects.Head(ctx, p.ID)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.Internal("could not resolve the project's head revision"))
	}
	if !ok {
		return nil, contract.WithContext(ctx, contract.Validation("the project has no revision to export", map[string][]string{
			"project": {"create a revision before exporting"},
		}))
	}

	job, err := h.jobs.Enqueue(ctx, p.ID, head.ID, user.ID, in.Body.Name, in.Body.Private)
	if err != nil {
		// A blank name is a client validation error (422); anything else is a
		// storage fault (500) and must not masquerade as "name required".
		if errors.Is(err, ErrBlankRepoName) {
			return nil, contract.WithContext(ctx, contract.Validation("a repository name is required", map[string][]string{
				"name": {"a repository name is required"},
			}))
		}
		return nil, contract.WithContext(ctx, contract.Internal("could not enqueue the export"))
	}
	return &createOutput{Status: http.StatusAccepted, Body: contract.Data[jobData]{Data: toJobData(job)}}, nil
}

type getInput struct {
	JobID string `path:"jobID" doc:"Export job identifier"`
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
		return nil, contract.WithContext(ctx, contract.NotFound("export job not found"))
	}
	job, ok, err := h.jobs.Get(ctx, jobID)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.Internal("could not load the export job"))
	}
	// A job is owned by the user who initiated it. A non-owner (or a missing job)
	// gets the same NotFound — no IDOR, and existence isn't leaked across users.
	if !ok || job.UserID != user.ID {
		return nil, contract.WithContext(ctx, contract.NotFound("export job not found"))
	}
	return &getOutput{Body: contract.Data[jobData]{Data: toJobData(job)}}, nil
}

// loadAuthorized resolves and authorizes the project for the caller, mapping a
// non-member and a missing/invalid project alike to NotFound (tenancy-safe).
func (h *handler) loadAuthorized(ctx context.Context, projectIDParam string, capability org.Capability) (project.Project, auth.User, error) {
	user, err := caller(ctx)
	if err != nil {
		return project.Project{}, auth.User{}, err
	}
	projectID, err := parseID(projectIDParam)
	if err != nil {
		return project.Project{}, auth.User{}, contract.WithContext(ctx, contract.NotFound("project not found"))
	}
	p, err := h.projects.GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, project.ErrProjectNotFound) {
			return project.Project{}, auth.User{}, contract.WithContext(ctx, contract.NotFound("project not found"))
		}
		return project.Project{}, auth.User{}, contract.WithContext(ctx, contract.Internal("could not load the project"))
	}
	if err := h.authz.Authorize(ctx, p.OrganizationID, user.ID, capability); err != nil {
		if errors.Is(err, org.ErrNotMember) || errors.Is(err, org.ErrForbidden) {
			return project.Project{}, auth.User{}, contract.WithContext(ctx, contract.NotFound("project not found"))
		}
		return project.Project{}, auth.User{}, contract.WithContext(ctx, contract.Internal("could not authorize the project"))
	}
	return p, user, nil
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
