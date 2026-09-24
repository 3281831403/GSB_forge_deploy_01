package compiler

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// README generates the exported project's README.md (DESIGN.md §28): a short
// overview and the three commands to run the app with Gombit — gombit db
// migrate, gombit dev, gombit build --embed — so the exported repository is
// self-documenting and needs no Forge involvement (P2, D10/D11). It is
// deterministic: the same spec produces byte-identical Markdown.
func README(s *spec.ProjectSpec) []byte {
	var b strings.Builder

	name := appName(s)
	b.WriteString("# ")
	b.WriteString(name)
	b.WriteString("\n\n")
	b.WriteString("An application built with [Gombit](https://github.com/gombit-dev/gombit) and exported from Gombit Forge. ")
	b.WriteString("It is an ordinary Gombit project with no dependency on Forge — you own the code.\n")

	if resources := resourceLabels(s); len(resources) > 0 {
		b.WriteString("\n## Resources\n\n")
		for _, label := range resources {
			b.WriteString("- ")
			b.WriteString(label)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n## Getting started\n\n")
	b.WriteString("Install the [Gombit CLI](https://github.com/gombit-dev/gombit), then run these from the project root.\n\n")
	b.WriteString("Apply database migrations:\n\n")
	b.WriteString("```sh\ngombit db migrate\n```\n\n")
	b.WriteString("Run the app in development:\n\n")
	b.WriteString("```sh\ngombit dev\n```\n\n")
	b.WriteString("Build a production binary with the frontend embedded:\n\n")
	b.WriteString("```sh\ngombit build --embed\n```\n")

	return []byte(b.String())
}

// WriteReadme writes the generated README to <dir>/README.md, replacing any
// scaffold-provided README. Unlike the generated code roots this is a single
// root-level file, so it is written directly rather than through Materialize.
func WriteReadme(dir string, s *spec.ProjectSpec) error {
	return os.WriteFile(filepath.Join(dir, "README.md"), README(s), 0o644)
}

// appName is the application's display name: the branding app name if set, else
// the project name, else a neutral fallback.
func appName(s *spec.ProjectSpec) string {
	if s.Branding != nil && strings.TrimSpace(s.Branding.AppName) != "" {
		return strings.TrimSpace(s.Branding.AppName)
	}
	if strings.TrimSpace(s.Project.Name) != "" {
		return strings.TrimSpace(s.Project.Name)
	}
	return "Application"
}

// resourceLabels lists the resources' human labels in authored order.
func resourceLabels(s *spec.ProjectSpec) []string {
	labels := make([]string, 0, len(s.Resources))
	for _, r := range s.Resources {
		if r == nil {
			continue
		}
		label := strings.TrimSpace(r.Label)
		if label == "" {
			label = r.CodeName
		}
		labels = append(labels, label)
	}
	return labels
}
