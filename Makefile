# skills-check uses process substitution, which /bin/sh does not provide.
SHELL := /bin/bash

# The control plane is a separate nested module (it imports Gombit; the compiler
# must not). Root `./...` never descends into it, so its build/vet/test are
# explicit targets, folded into `all` and mirrored in CI.
CONTROLPLANE := controlplane

# The Forge editor is a React/TypeScript SPA under controlplane/web (M2). It is
# a separate npm project the Go gates never touch; it has its own install /
# typecheck / build / test targets, mirrored in CI as a distinct job.
WEB := controlplane/web

.PHONY: all test lint fmt fmt-check vet tidy clean skills-check \
	cp-build cp-vet cp-test cp-race \
	web web-install web-check web-build web-test

# `all` is the Go merge gate; it deliberately does not run the web SPA (that
# would put node on every Go dev's critical path). A frontend dev runs `make web`
# separately, and CI's dedicated `web` job gates the SPA on every PR.
all: fmt-check vet cp-vet test cp-test skills-check

test:
	go test ./... -count=1

race:
	go test ./... -race -count=1

# Control-plane module (github.com/gombit-dev/gombit-forge/controlplane).
cp-build:
	cd $(CONTROLPLANE) && go build ./...

cp-vet:
	cd $(CONTROLPLANE) && go vet ./...

cp-test:
	cd $(CONTROLPLANE) && go test ./... -count=1

# CI variant: skips the Docker/Postgres boot test (see boot_test.go), matching
# how the M0 e2e harness stays out of the merge gate.
cp-test-short:
	cd $(CONTROLPLANE) && go test ./... -short -count=1

cp-race:
	cd $(CONTROLPLANE) && go test ./... -race -count=1

# Forge editor SPA (controlplane/web). `make web` is the SPA gate a frontend dev
# runs; web-install uses `npm ci` against the committed lockfile for a
# reproducible install.
web: web-check web-build web-test

web-install:
	cd $(WEB) && npm ci

web-check:
	cd $(WEB) && npm run typecheck

web-build:
	cd $(WEB) && npm run build

web-test:
	cd $(WEB) && npm run test

cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1

fmt:
	gofmt -w .

# CI gate: fail when anything is unformatted.
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "unformatted files:"; echo "$$unformatted"; exit 1; \
	fi

vet:
	go vet ./...

# The Claude and Cursor skill trees are duplicated because Cursor does not
# reliably follow symlinks. Duplication without enforcement is just drift with
# extra steps, so fail when they diverge. The only sanctioned difference is
# each review/SKILL.md's one-line pointer to its counterpart.
skills-check:
	@status=0; \
	for name in feature bugfix review; do \
		if ! diff -r -x SKILL.md ".claude/skills/$$name" ".cursor/skills/$$name" >/dev/null 2>&1; then \
			echo "skills drift in $$name references:"; \
			diff -r -x SKILL.md ".claude/skills/$$name" ".cursor/skills/$$name"; \
			status=1; \
		fi; \
		if ! diff <(grep -v 'counterpart is' ".claude/skills/$$name/SKILL.md") \
		          <(grep -v 'counterpart is' ".cursor/skills/$$name/SKILL.md") >/dev/null 2>&1; then \
			echo "skills drift in $$name/SKILL.md:"; \
			diff <(grep -v 'counterpart is' ".claude/skills/$$name/SKILL.md") \
			     <(grep -v 'counterpart is' ".cursor/skills/$$name/SKILL.md"); \
			status=1; \
		fi; \
	done; \
	if [ $$status -eq 0 ]; then echo "skills: .claude and .cursor in sync"; fi; \
	exit $$status

tidy:
	go mod tidy

# Rewrite the canonical-JSON golden file after a deliberate schema change.
golden:
	UPDATE_GOLDEN=1 go test ./internal/spec/ -run TestGoldenCanonicalJSON

clean:
	rm -f coverage.out
