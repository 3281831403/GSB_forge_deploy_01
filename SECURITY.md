# Security Policy

## Reporting a vulnerability

**Do not open a public issue for a security vulnerability.**

Report privately through GitHub Security Advisories:

> [Report a vulnerability](https://github.com/gombit-dev/gombit-forge/security/advisories/new)

If you can't use that form, email **leonardo.aa88@gmail.com** with `gombit-forge
security` in the subject.

Please include:

- affected commit or version;
- the component — the compiler/generators, the generated application code, or the
  control plane (auth, org tenancy, revisions, GitHub export);
- reproduction steps or a proof of concept;
- impact as you see it.

### What to expect

| Stage | Target |
| --- | --- |
| Acknowledgement | 3 business days |
| Initial assessment | 10 business days |
| Fix or mitigation plan | depends on severity; communicated in the assessment |
| Public disclosure | coordinated, up to 90 days from the report |

We'll credit you in the advisory and release notes unless you'd rather stay
anonymous.

## Supported versions

Forge is pre-alpha. Fixes land on `main`; there are no long-term support
branches, and interfaces may change without notice before a first tagged
release.

## Scope

**In scope** — vulnerabilities in this repository:

- the **compiler and generators** (`internal/spec`, `internal/compiler/**`,
  `internal/gombit`), **including the code they emit** — an insecure default in
  generated output is a vulnerability in Forge, not just the generated app;
- the **control plane** (`controlplane/**`): cookie/session auth, org tenancy and
  the per-org role/capability matrix, invitation flow, project-revision
  immutability and lineage, and the GitHub export / OAuth-connect path;
- the editor SPA (`controlplane/web`);
- anything that lets Forge become a runtime dependency of, or exfiltrate source
  from, a generated application.

**Out of scope:**

- misconfiguration of an application *you* generated — for example a weak or
  shared `GOMBIT_JWT_SECRET`, disabling CSRF, or exposing `/docs` in production;
- **`VITE_*` environment variables** — by design everything under `VITE_*` is
  compiled into the frontend bundle and is public; putting a secret there is a
  configuration error, not a Forge vulnerability;
- vulnerabilities in the **Gombit framework** itself — report those to
  [gombit-dev/gombit](https://github.com/gombit-dev/gombit/security) — and in the
  **Gombit Cloud** runtime (build, deploy, managed database, secrets, domains),
  which is not part of this repository;
- third-party dependency issues without a demonstrated impact on Forge — report
  those upstream, though we're glad to hear about them;
- findings from automated scanners with no accompanying exploitability analysis.

## Hardening notes

The properties most likely to matter are enforced and documented rather than
implicit:

- **Generated apps carry no Forge runtime dependency** (locked decision D2): a
  deployed app keeps working if Forge disappears, so a Forge compromise cannot
  reach through a generated app at runtime.
- **Identity is the stable ID, never the name, and symbols are frozen once
  minted** (ADR-001): a relabel can never become a source-symbol change or a
  silent rename.
- **Determinism**: the same compiler version and spec produce byte-identical
  output, so generated artifacts are reproducible and reviewable.
- **Tenancy and export authorization** live in the control plane's org
  capability matrix; GitHub export is gated on the project-edit capability so a
  view-only role cannot exfiltrate source.
