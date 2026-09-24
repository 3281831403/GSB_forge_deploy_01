// Package platform holds the control plane's infrastructure wiring — the
// project-level plumbing (database, and later cache/secrets) that the feature
// packages build on. It is Forge dogfooding Gombit (DESIGN.md §6, D7): the
// control plane is itself an ordinary Gombit application.
package platform

import (
	"github.com/gombit-dev/gombit/config"
	"github.com/gombit-dev/gombit/database"
)

// OpenDatabase opens the SQL database from typed config. The control plane is
// PostgreSQL-backed (DESIGN.md D4); the driver is selected by configuration so
// tests can point it at a throwaway instance.
//
// Schema is applied out of band via Atlas migrations, never AutoMigrate
// (DESIGN.md §14). The bootstrap has no models of its own yet — User,
// Organization and the rest arrive with the M1 model issues — so there is
// nothing to migrate here at startup.
func OpenDatabase(cfg config.DatabaseConfig) (*database.DB, error) {
	return database.Open(cfg)
}
