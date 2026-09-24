package platform

import (
	"github.com/gombit-dev/gombit/auth"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/audit"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/cloudbuild"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/exportjob"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/githubconnect"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/org"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
)

// Models is the control plane's complete schema as GORM model values: Gombit's
// own auth tables plus Forge's control-plane tables. It is the single source of
// truth for what tables the control plane owns.
//
// The auth tables come from auth.Models() rather than a hand-copied list —
// that function is Gombit's public API for its schema (ADR-004 D3: consume
// Gombit's public APIs, never duplicate its infrastructure), so when Gombit
// adds a model this set grows with it instead of silently omitting a table.
// The control plane reuses Gombit's identity and RBAC rather than defining its
// own (D12), so those tables are part of the same managed schema.
//
// Deployment applies this schema through Atlas migrations, never AutoMigrate
// (DESIGN.md §14). The initial migration (#101) is committed under
// database/migrations, and tests apply it — not AutoMigrate — because the
// schema's cyclic foreign keys and the project_revisions append-only trigger
// cannot be expressed through GORM's AutoMigrate (see internal/dbtest).
//
// The set is deliberately the authoring loop only — Organization/Member/
// Invitation (#36), Project/Revision (#37), audit.Event (#36, service #40) and
// the GitHub export connection/OAuth-state tables, the export-job queue (#85)
// and the cloud-build queue that tracks a Cloud build for a deployed revision
// (#103 — a job record mirroring the export queue, not a runtime model). The
// runtime models
// (Environment, Build, Deployment, Domain) are owned by
// Gombit Cloud, not the Forge control plane (ADR-005 D2/D6): Forge compiles a
// revision to an ordinary Gombit application and hands it to Cloud, which owns
// build, deployment, environments, databases, secrets and domains. The link to
// the Cloud side is Project.CloudProjectID, not a table here.
//
// The model set is also the input to `gombit db makemigrations`; when it
// changes, regenerate the migration from these types (see #101 for the exact
// invocation). The append-only trigger is out-of-band — GORM cannot express it,
// so makemigrations will not carry it; keep it across regenerations.
func Models() []any {
	return append(auth.Models(),
		&org.Organization{},
		&org.Member{},
		&org.Invitation{},
		&project.Project{},
		&project.Revision{},
		&audit.Event{},
		&githubconnect.Connection{},
		&githubconnect.OAuthState{},
		&exportjob.ExportJob{},
		&cloudbuild.BuildJob{},
	)
}
