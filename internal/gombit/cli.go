package gombit

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultBinary is the executable looked up on PATH when none is configured.
const DefaultBinary = "gombit"

// Runner executes one toolchain command and returns its combined output.
//
// It is injectable so command construction can be tested without a Gombit
// installation, and so a build worker can supply a sandboxed executor
// (DESIGN.md §27) instead of running commands directly.
type Runner func(ctx context.Context, dir string, name string, args ...string) ([]byte, error)

// CLI drives Gombit through its command-line interface.
//
// The CLI is the transport, not the contract: Forge issues one coarse
// invocation per project-level operation (ADR-004 D4).
type CLI struct {
	// Binary is the gombit executable; DefaultBinary when empty.
	Binary string
	// Run executes commands; execRunner when nil.
	Run Runner

	// SkipVersionCheck bypasses the minimum-version guard. Tests set it; a
	// production caller should not.
	SkipVersionCheck bool
}

// compile-time proof that the CLI satisfies the boundary contract.
var _ Client = (*CLI)(nil)

func (c *CLI) binary() string {
	if c.Binary == "" {
		return DefaultBinary
	}
	return c.Binary
}

func (c *CLI) runner() Runner {
	if c.Run == nil {
		return execRunner
	}
	return c.Run
}

func execRunner(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	return command.CombinedOutput()
}

// Version reports the toolchain version.
func (c *CLI) Version(ctx context.Context) (Version, error) {
	output, err := c.runner()(ctx, "", c.binary(), "version")
	if err != nil {
		return Version{}, fmt.Errorf("gombit: run version: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return parseVersionOutput(string(output))
}

// Scaffold creates one application shell.
//
// The toolchain version is verified first, so an unsupported Gombit fails
// before any files are written rather than leaving a half-scaffolded tree
// that imports a module path Forge does not support.
func (c *CLI) Scaffold(ctx context.Context, request ScaffoldRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}

	if !c.SkipVersionCheck {
		detected, err := c.Version(ctx)
		if err != nil {
			return err
		}
		if err := CheckSupported(detected); err != nil {
			return err
		}
	}

	parent, name := filepath.Split(filepath.Clean(request.Dir))
	if name == "" {
		return fmt.Errorf("gombit: scaffold destination %q has no final path element", request.Dir)
	}

	// `gombit new` creates <name>/ beneath its working directory, so the
	// command runs in the parent and the destination's final element names
	// the tree. request.Name stays the application name and need not match.
	output, err := c.runner()(ctx, parent, c.binary(), scaffoldArgs(request, name)...)
	if err != nil {
		return fmt.Errorf("gombit: scaffold %s: %w: %s",
			request.Name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// scaffoldArgs builds the argv for `gombit new`.
//
// Kept pure and separate from execution so the command line is testable
// without a toolchain, and so argument order stays deterministic.
func scaffoldArgs(request ScaffoldRequest, dirName string) []string {
	args := []string{
		"new", dirName,
		"--module", request.Module,
		"--database", string(request.Database),
		"--auth", string(request.Auth),
		"--ui", string(request.UI),
	}

	// Tidy reaches the network, so it is opt-in and the flag is inverted:
	// the CLI tidies by default.
	if !request.Tidy {
		args = append(args, "--skip-tidy")
	}
	if request.Force {
		args = append(args, "--force")
	}
	return args
}
