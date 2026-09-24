// Command server boots the Forge control plane.
//
// The control plane is itself a Gombit application (DESIGN.md §6, D7): Forge
// dogfoods Gombit rather than building a bespoke backend. It runs with
// cookie/session auth (DESIGN.md §20, D5) and is PostgreSQL-backed (D4).
//
// In cookie mode framework.New mounts the admin surface automatically, with no
// explicit wiring here: admin.Mount serves the gated data plane at
// /api/v1/admin/*, and the framework-owned admin SPA (gombit's internal/adminui)
// is served at /admin/. The admin catalog is empty until a model registers with
// it — the M1 model issues (User, Organization, Project, …) fill it in.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gombit-dev/gombit/config"
	"github.com/gombit-dev/gombit/framework"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/buildworker"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/cloudbuild"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/cloudclient"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/exportjob"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/exportworker"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/ghexport"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/githubconnect"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/githubexport"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/org"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/platform"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/projectspec"
	"github.com/gombit-dev/gombit-forge/internal/compiler"
	"github.com/gombit-dev/gombit-forge/internal/gombit"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	db, err := platform.OpenDatabase(cfg.Database)
	if err != nil {
		log.Fatal(err)
	}

	app, err := framework.New(
		framework.WithConfig(cfg),
		framework.WithDatabase(db),
	)
	if err != nil {
		_ = db.Close()
		log.Fatal(err)
	}

	// Feature packages register explicitly (Gombit does not discover them by
	// reflection). Tenancy is the first; the project API (#39) is the second.
	if err := org.Register(app); err != nil {
		_ = db.Close()
		log.Fatal(err)
	}
	if err := project.Register(app); err != nil {
		_ = db.Close()
		log.Fatal(err)
	}
	// GitHub repository export (#85) is optional: it registers only when the
	// OAuth app credentials are configured, so the control plane runs fine
	// without them. GitHub OAuth is not part of Gombit's typed config, so its
	// settings are read from the environment here at the composition root, not
	// inside a runtime package.
	// stopWorker cancels the background export worker on shutdown; it stays a
	// no-op unless the export feature registers below.
	stopWorker := func() {}
	if ghCfg, successRedirect, ok := githubOAuthConfig(); ok {
		if err := githubconnect.Register(app, ghCfg, successRedirect); err != nil {
			_ = db.Close()
			log.Fatal(err)
		}
		if err := registerGitHubExport(app, ghCfg, &stopWorker); err != nil {
			_ = db.Close()
			log.Fatal(err)
		}
	}

	// Cloud build/deploy (#103) is optional the same way: it registers only when
	// the Gombit Cloud API URL + service token are configured, so the control
	// plane runs fine without them (deploys are simply unavailable). Unset config
	// means the feature is disabled — never a localhost fallback or a fallback
	// identity.
	stopBuildWorker := func() {}
	if baseURL, token, ok := cloudConfig(); ok {
		if err := registerCloudBuild(app, baseURL, token, &stopBuildWorker); err != nil {
			_ = db.Close()
			log.Fatal(err)
		}
	}

	app.OnStop(func(context.Context) error {
		stopWorker()      // before closing the DB the workers use
		stopBuildWorker() //
		return db.Close()
	})

	if err := framework.Run(app); err != nil {
		log.Fatal(err)
	}
}

// githubOAuthConfig reads the GitHub OAuth app settings from the environment.
// It reports ok=false (and the feature stays unregistered) unless the client
// id, secret and redirect URL are all set — the three the OAuth handshake
// cannot run without. The success redirect defaults to the app root.
func githubOAuthConfig() (cfg githubexport.Config, successRedirect string, ok bool) {
	clientID := os.Getenv("GITHUB_OAUTH_CLIENT_ID")
	clientSecret := os.Getenv("GITHUB_OAUTH_CLIENT_SECRET")
	redirectURL := os.Getenv("GITHUB_OAUTH_REDIRECT_URL")
	set := 0
	for _, v := range []string{clientID, clientSecret, redirectURL} {
		if v != "" {
			set++
		}
	}
	if set < 3 {
		// All-empty is the intentional "feature off" path and stays silent; a
		// partial config is almost always a typo (a wrong var name, a missing
		// secret) that would otherwise fail as a mystery 404 on the connect
		// route, so surface it.
		if set > 0 {
			log.Printf("github oauth: partially configured (%d/3 of CLIENT_ID/CLIENT_SECRET/REDIRECT_URL set); connect flow disabled", set)
		}
		return githubexport.Config{}, "", false
	}
	successRedirect = os.Getenv("GITHUB_OAUTH_SUCCESS_REDIRECT")
	if successRedirect == "" {
		successRedirect = "/"
	}
	return githubexport.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		// OAuthBaseURL and APIBaseURL default to public GitHub; override via
		// GITHUB_OAUTH_BASE_URL / GITHUB_API_BASE_URL for GitHub Enterprise.
		OAuthBaseURL: os.Getenv("GITHUB_OAUTH_BASE_URL"),
		APIBaseURL:   os.Getenv("GITHUB_API_BASE_URL"),
	}, successRedirect, true
}

// registerGitHubExport wires the asynchronous GitHub export (#85): the job
// routes plus the background worker that runs them. Enqueue happens in the HTTP
// request; the toolchain-heavy source assembly (scaffold, tidy, migrations)
// runs only in the worker, so no request performs a build.
//
// It sets *stopWorker to the worker's cancel func so main can stop it on
// shutdown before closing the database the worker uses. The export stack shares
// one projectspec.Source both as the worker's frozen-revision resolver and as
// ghexport's head-spec source.
func registerGitHubExport(app *framework.App, ghCfg githubexport.Config, stopWorker *func()) error {
	db := app.DB()
	projectSvc := project.NewService(db)
	src := projectspec.NewSource(projectSvc)
	tokens := githubconnect.NewService(db, githubconnect.NewExchanger(ghCfg, nil))
	pub := ghexport.NewPublisher(ghCfg, nil)

	// Query the gombit toolchain version once for export provenance. Export needs
	// the toolchain anyway; if it's unavailable the feature still wires and
	// provenance records "unknown" rather than failing startup.
	cli := &gombit.CLI{}
	gombitVersion := "unknown"
	if v, err := cli.Version(context.Background()); err == nil {
		gombitVersion = v.String()
	} else {
		log.Printf("github export: gombit toolchain version unavailable (%v); provenance will record %q", err, gombitVersion)
	}
	exporter := ghexport.NewService(tokens, src, compiler.GombitToolchain{CLI: cli}, pub, gombitVersion)

	jobs := exportjob.NewService(db)
	if err := exportjob.Register(app, jobs, projectSvc, org.NewService(db)); err != nil {
		return err
	}

	worker := exportworker.New(jobs, src, exporter, 0, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()
	// stopWorker signals cancellation and then waits (bounded) for Run to return,
	// so shutdown actually joins the worker before the DB it writes to is closed,
	// rather than racing it. A worker mid-export past the deadline is abandoned —
	// a full in-flight assembly can run for minutes and shutdown can't block on it.
	*stopWorker = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			log.Printf("github export: worker did not stop within 10s; proceeding with shutdown")
		}
	}
	return nil
}

// cloudConfig reads the Gombit Cloud API settings from the environment. It
// reports ok=false (feature disabled) unless BOTH the base URL and the service
// token are set — the two the worker cannot call Cloud without. The token is
// Forge's own service-principal credential (service:forge-build-worker), a
// deployment secret, never committed. Unset means deploy is disabled — never a
// localhost fallback or a fallback identity.
func cloudConfig() (baseURL, token string, ok bool) {
	baseURL = strings.TrimRight(os.Getenv("GOMBIT_CLOUD_API_URL"), "/")
	token = os.Getenv("GOMBIT_CLOUD_API_TOKEN")
	if baseURL == "" || token == "" {
		// A partial config is almost always a typo (a wrong var name, a missing
		// secret) that would otherwise fail as a mystery at deploy time; surface it.
		if baseURL != "" || token != "" {
			log.Printf("gombit cloud: partially configured (need both GOMBIT_CLOUD_API_URL and GOMBIT_CLOUD_API_TOKEN); deploy disabled")
		}
		return "", "", false
	}
	return baseURL, token, true
}

// registerCloudBuild wires the asynchronous Cloud build/deploy (#103): the deploy
// routes plus the background worker that assembles a revision's source, submits
// it to Cloud, and reflects the build's state. Enqueue happens in the HTTP
// request; the toolchain-heavy assembly + submission runs only in the worker
// (D8). The worker's Cloud client carries Forge's service-principal token, so
// Cloud attributes the actor to the service, never the initiating user.
//
// It sets *stopWorker to the worker's cancel func so main can stop it on shutdown
// before closing the database the worker uses.
func registerCloudBuild(app *framework.App, baseURL, token string, stopWorker *func()) error {
	db := app.DB()
	jobs := cloudbuild.NewService(db)
	projectSvc := project.NewService(db)

	// A generous whole-request timeout so a large source upload over a slow link
	// isn't cut off by a short default (the upload's duration scales with archive
	// size); per-request cancellation still rides on ctx. The same client serves
	// both the worker (submit/track) and the routes' build-log relay.
	cloud := &cloudclient.Client{BaseURL: baseURL, Token: token, HTTP: &http.Client{Timeout: 10 * time.Minute}}
	if err := cloudbuild.Register(app, jobs, projectSvc, org.NewService(db), cloud); err != nil {
		return err
	}

	// Query the gombit toolchain version once for build provenance (the assembler
	// needs the toolchain anyway); if unavailable, provenance records "unknown"
	// rather than failing startup.
	cli := &gombit.CLI{}
	gombitVersion := "unknown"
	if v, err := cli.Version(context.Background()); err == nil {
		gombitVersion = v.String()
	} else {
		log.Printf("gombit cloud: gombit toolchain version unavailable (%v); provenance will record %q", err, gombitVersion)
	}
	asm := buildworker.NewSourceAssembler(compiler.GombitToolchain{CLI: cli}, gombitVersion)
	worker := buildworker.New(jobs, projectspec.NewSource(projectSvc), asm, cloud, buildworker.Options{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()
	*stopWorker = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			log.Printf("gombit cloud: build worker did not stop within 10s; proceeding with shutdown")
		}
	}
	return nil
}
