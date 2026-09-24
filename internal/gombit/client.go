package gombit

import (
	"context"
	"fmt"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// Database is a persistence driver Gombit can scaffold.
type Database string

const (
	DatabasePostgres Database = "postgres"
	DatabaseSQLite   Database = "sqlite"
	DatabaseMySQL    Database = "mysql"
)

// Auth is an authentication mode Gombit can scaffold.
//
// Verified against gombit v0.1.5: scaffold.validAuths is exactly
// {jwt, cookie}, and `--auth none` is refused with "auth must be one of jwt,
// cookie". There is no unmounted-auth flag, and an empty --auth defaults to
// jwt rather than to none. So this type has no None: representing a mode the
// toolchain rejects would only move the failure later.
type Auth string

const (
	AuthCookie Auth = "cookie"
	AuthJWT    Auth = "jwt"
)

// AuthModes lists every mode this package may emit, in a stable order.
//
// ScaffoldRequest.Validate and the parity test both read this list, so it is
// the single source of truth for scaffoldable auth modes: adding one here
// without confirming the toolchain accepts it fails the test rather than
// shipping a request `gombit new` will reject.
func AuthModes() []Auth { return []Auth{AuthCookie, AuthJWT} }

// scaffoldable reports whether mode is one this package may emit.
func scaffoldable(mode Auth) bool {
	for _, candidate := range AuthModes() {
		if candidate == mode {
			return true
		}
	}
	return false
}

// UI is a frontend preset Gombit can scaffold.
type UI string

const (
	UIMinimal UI = "minimal"
	UIMUI     UI = "mui"
)

// ScaffoldRequest describes one whole application to scaffold.
//
// It is deliberately project-level: one request produces one application
// shell, and resources are not part of it. Forge synthesizes resource code
// itself under internal/forge_generated/** (ADR-004 D1).
type ScaffoldRequest struct {
	// Dir is the directory the application is written into.
	Dir string
	// Name is the application name.
	Name string
	// Module is the Go module path of the generated application.
	Module string

	Database Database
	Auth     Auth
	UI       UI

	// Tidy runs `go mod tidy` in the generated tree. It reaches the network,
	// so callers that must stay offline leave it false and resolve modules
	// themselves.
	Tidy bool
	// Force allows writing into a non-empty directory.
	Force bool
}

// Validate reports whether the request is well formed.
func (r ScaffoldRequest) Validate() error {
	if r.Dir == "" {
		return fmt.Errorf("gombit: scaffold request needs a destination directory")
	}
	if r.Name == "" {
		return fmt.Errorf("gombit: scaffold request needs an application name")
	}
	if r.Module == "" {
		return fmt.Errorf("gombit: scaffold request needs a module path")
	}

	switch r.Database {
	case DatabasePostgres, DatabaseSQLite, DatabaseMySQL:
	default:
		return fmt.Errorf("gombit: unsupported database %q", r.Database)
	}
	if !scaffoldable(r.Auth) {
		return fmt.Errorf(
			"gombit: unsupported auth mode %q (the toolchain scaffolds only %v)",
			r.Auth, AuthModes())
	}
	switch r.UI {
	case UIMinimal, UIMUI:
	default:
		return fmt.Errorf("gombit: unsupported ui preset %q", r.UI)
	}
	return nil
}

// Client is Forge's view of the Gombit toolchain.
//
// The interface exists so the compiler can be tested without a toolchain, and
// so the transport (CLI today, in-process packages if Gombit ever exports a
// suitable seam) can change without touching callers.
type Client interface {
	// Version reports the toolchain version.
	Version(ctx context.Context) (Version, error)
	// Scaffold creates one application shell.
	Scaffold(ctx context.Context, request ScaffoldRequest) error
	// MakeMigrations generates a versioned Atlas migration from the declared
	// models.
	MakeMigrations(ctx context.Context, request MakeMigrationsRequest) error
}

// ScaffoldRequestFor derives the scaffold request for a project.
//
// This is the only place ProjectSpec vocabulary is translated into Gombit
// vocabulary. The spec must be valid; an invalid one is rejected rather than
// translated, so a malformed project cannot reach the toolchain.
//
// Spec validity does not by itself imply the toolchain will accept the
// result: the two vocabularies are maintained separately and can drift. Every
// translation below therefore returns an error for anything it cannot map,
// rather than assuming validation has already guaranteed it.
func ScaffoldRequestFor(s *spec.ProjectSpec, dir, module string) (ScaffoldRequest, error) {
	if diagnostics := spec.Validate(s); diagnostics != nil {
		return ScaffoldRequest{}, fmt.Errorf(
			"gombit: refusing to scaffold from an invalid spec: %w", diagnostics)
	}

	database, err := databaseFor(s.Database.Driver)
	if err != nil {
		return ScaffoldRequest{}, err
	}
	auth, err := authFor(s.Auth.Mode)
	if err != nil {
		return ScaffoldRequest{}, err
	}

	return ScaffoldRequest{
		Dir:      dir,
		Name:     s.Project.Slug,
		Module:   module,
		Database: database,
		Auth:     auth,
		// Forge generates its own pages under frontend/src/forge_generated/**,
		// so the scaffold only needs the headless skeleton. Asking for the MUI
		// preset would scaffold CRUD screens Forge immediately supersedes.
		UI: UIMinimal,
	}, nil
}

func databaseFor(driver spec.DatabaseDriver) (Database, error) {
	switch driver {
	case spec.DriverPostgres:
		return DatabasePostgres, nil
	case spec.DriverSQLite:
		return DatabaseSQLite, nil
	case spec.DriverMySQL:
		return DatabaseMySQL, nil
	default:
		return "", fmt.Errorf("gombit: no Gombit driver for spec driver %q", driver)
	}
}

func authFor(mode spec.AuthMode) (Auth, error) {
	switch mode {
	case spec.AuthCookie:
		return AuthCookie, nil
	case spec.AuthJWT:
		return AuthJWT, nil
	default:
		return "", fmt.Errorf(
			"gombit: no Gombit auth mode for spec mode %q (the toolchain scaffolds only %q and %q)",
			mode, AuthJWT, AuthCookie)
	}
}
