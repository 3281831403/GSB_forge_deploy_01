# Gombit Forge — MVP Design Document v1

**Status:** Design-ready
**Product:** Gombit Forge
**Purpose:** No-code/low-code SaaS application builder powered by Gombit
**MVP goal:** Let a non-Go developer visually define a CRUD-oriented web application, preview it, deploy it, and export the resulting ordinary Gombit source code.

---

## 1. Product thesis

Gombit Forge is a visual application builder that compiles a declarative project specification into a real Gombit application.

The core promise is:

> **Build visually. Ship normally. Own the code.**

Unlike conventional no-code platforms, Forge does not require applications to remain inside a proprietary runtime. A Forge project can be exported as a normal Go + React repository using Gombit, Gin, Huma, GORM, Atlas, OpenAPI, TypeScript, and React.

Forge is therefore best understood as:

> **A visual compiler and managed deployment platform for Gombit applications.**

The MVP is intentionally focused on structured business applications: internal tools, CRUD SaaS products, portals, inventory systems, lightweight CRMs, admin systems, and similar data-driven applications.

Forge is not initially a replacement for Webflow, Bubble, Retool, or a general-purpose visual programming environment.

---

# 2. Product principles

## P1 — The project specification is the source of truth

A Forge application is represented by a versioned declarative `ProjectSpec`.

The visual editor edits this specification.

Generation is:

```text
ProjectSpec
    ↓
Forge compiler
    ↓
Gombit application
    ↓
build
    ↓
deployable artifact
```

The generated source tree is not treated as an editable round-trip representation while the project remains managed by Forge.

---

## P2 — No code captivity

A user may export the generated application at any time.

Export produces an ordinary Gombit project containing:

* Go source
* GORM models
* Huma handlers
* React frontend
* TypeScript client
* Atlas migrations
* authentication configuration
* admin registration
* deployment configuration

Once exported and modified outside Forge, Forge does not promise to reverse-engineer arbitrary source changes back into the visual model.

This boundary is intentional.

---

## P3 — Structured before arbitrary

The MVP supports structured application building:

* resources
* fields
* relationships
* CRUD pages
* tables
* forms
* authentication
* permissions
* navigation
* basic dashboards
* branding

The MVP does not provide arbitrary pixel-positioned page design.

Most business applications do not need a freeform canvas to become useful.

---

## P4 — Use Gombit instead of reimplementing Gombit

Forge must invoke or reuse existing Gombit capabilities wherever possible.

Forge must not independently implement:

* migration diffing
* OpenAPI generation
* TypeScript client generation
* authentication
* admin
* GORM database abstractions
* resource scaffolding semantics for hand-written Gombit applications (`gombit make resource`)
* embedded builds

If Forge needs behavior that also belongs in normal Gombit applications, prefer adding the capability to Gombit first.

> **Amended by ADR-004.** The scaffolding bullet above originally read
> "resource scaffolding semantics" without qualification. Compiling a
> `ProjectSpec` into resource-specific application code under
> `internal/forge_generated/**` is Forge's own responsibility — Gombit owns
> framework primitives, Forge owns application synthesis — and remains subject
> to ADR-004 D3: generated output consumes Gombit's public APIs and never
> duplicates Gombit infrastructure. Every other bullet in this list is
> unchanged and still binding, as is the preference stated immediately above.

---

## P5 — Generated applications remain boring

Exported code should look like code a Go developer could reasonably maintain without Forge.

Avoid:

* giant generated interpreters
* proprietary runtime DSLs
* opaque binary blobs
* runtime dependency on Forge
* remote Forge APIs required for normal application operation

A deployed application should continue working if Forge disappeared.

---

# 3. Target user

Primary MVP user:

> A technical founder, backend developer, consultant, agency, operations team, or power user who needs a conventional database-backed application quickly and wants the ability to own the result.

Forge initially targets users comfortable with concepts such as:

* tables/resources
* fields
* relationships
* forms
* permissions
* deployments

It does not require knowledge of Go.

---

# 4. MVP user journey

## 4.1 Create project

User clicks:

**New Project**

Inputs:

* project name
* slug
* database
* authentication mode
* UI preset

MVP defaults:

```text
Database: PostgreSQL
Auth: cookie/session
UI: Gombit minimal
Admin: enabled
```

Forge creates an empty `ProjectSpec`.

---

## 4.2 Define resources

User adds:

```text
Customer
Invoice
Product
Project
Ticket
```

For each resource the user configures fields.

Example:

```text
Customer
├── name        string     required
├── email       string     required unique
├── active      bool
└── created_at  datetime
```

Supported MVP field types:

```text
string
text
integer
decimal
boolean
datetime
date
enum
belongs_to
```

`has_many` is derived from `belongs_to`.

Many-to-many is deferred.

---

## 4.3 Configure resource behavior

For each resource:

* fields displayed in list
* searchable fields
* sortable fields
* filterable fields
* create enabled
* update enabled
* delete enabled
* admin visibility
* human-readable singular/plural labels

---

## 4.4 Create pages

MVP page types:

### Resource table

Displays:

* rows
* pagination
* sorting
* filters
* search
* create action
* row actions

### Resource form

Supports:

* create
* edit
* validation
* relationship selection

### Resource detail

Displays record fields and related records.

### Dashboard

Simple configurable dashboard supporting:

* count cards
* recent-record lists
* basic numeric aggregates

No arbitrary chart designer in MVP.

---

## 4.5 Configure navigation

User creates a navigation structure:

```text
Dashboard
Customers
Invoices
Products
Settings
```

Navigation entries may point to:

* dashboard
* resource list
* custom external URL

---

## 4.6 Configure access

Forge exposes Gombit's users/groups/permissions system visually.

User can define roles such as:

```text
Administrator
Sales
Support
Viewer
```

Permissions map onto Gombit's normal permission model.

Example:

```text
customers.view
customers.create
customers.update
customers.delete
```

Forge must not create a second authorization system.

---

## 4.7 Preview

Clicking **Preview** creates or updates a temporary environment. Forge triggers
it; Gombit Cloud provisions and runs it (ADR-005 D5).

The preview runs the actual generated application.

The browser receives a URL such as:

```text
https://preview-abc123.forge.example
```

Preview is not a fake renderer.

It is the application.

---

## 4.8 Publish

User clicks:

**Deploy**

> **Reframed by ADR-005.** Forge owns the first stage — validate and compile the
> spec to an ordinary Gombit application (including migration generation). From
> the source archive on, the pipeline is Gombit Cloud's: build, produce the
> immutable artifact, apply migrations under safety gating, deploy, health-check
> and route (gombit-cloud RFC §11–23, C1–C4). The boundary is marked below.

```text
── Forge ──────────────────────
validate ProjectSpec
        ↓
generate application
        ↓
generate migrations
        ↓
generate OpenAPI
        ↓
generate TS client
        ↓
build frontend            (local validation only)
        ↓
── hand source to Gombit Cloud ─
build immutable artifact
        ↓
run migrations (safety-gated)
        ↓
deploy + health check + route
```

The application receives a stable environment URL from Cloud.

---

## 4.9 Export

User clicks:

**Export Source**

MVP export targets:

1. ZIP download
2. GitHub repository

GitHub export may be deferred to the second MVP milestone if OAuth work delays the core product.

Exported projects contain no requirement to call Forge.

---

# 5. High-level architecture

> **Reframed by ADR-005.** The **Forge Control Plane** below keeps Projects,
> Specs, Users, Organizations and Revisions plus a `cloud_project_id` linkage;
> Builds, Deployments and Environments are owned by **Gombit Cloud**. The
> **Build Worker** and **Runtime Platform** tiers are Gombit Cloud, not Forge,
> and are shown only for end-to-end context — see gombit-cloud RFC §11–16
> (build), §18–23 (deploy), §56 (runtime). Forge is a client of that platform
> (ADR-005 D1–D3): it compiles `ProjectSpec` to an ordinary Gombit application
> and submits it; Cloud builds, runs and operates it.

```text
┌───────────────────────────────┐
│          Forge Web UI         │
│ React / TypeScript            │
└──────────────┬────────────────┘
               │
               ▼
┌───────────────────────────────┐
│       Forge Control Plane     │
│            Gombit             │
│                               │
│ Projects                      │
│ Specs                         │
│ Users                         │
│ Organizations                 │
│ Builds                        │
│ Deployments                   │
│ Environments                  │
└──────────────┬────────────────┘
               │
               ▼
┌───────────────────────────────┐
│        Forge Compiler         │
│ ProjectSpec → Gombit project  │
└──────────────┬────────────────┘
               │
               ▼
┌───────────────────────────────┐
│         Build Worker          │
│                               │
│ gombit generation             │
│ Atlas migrations              │
│ TypeScript generation         │
│ Vite build                    │
│ Go build                      │
│ container image               │
└──────────────┬────────────────┘
               │
               ▼
┌───────────────────────────────┐
│       Runtime Platform        │
│                               │
│ customer app container        │
│ PostgreSQL                    │
│ HTTPS routing                 │
└───────────────────────────────┘
```

---

# 6. Forge itself

Forge should dogfood Gombit.

The Forge control plane should itself be implemented as a Gombit application.

> **Amended by ADR-005.** The runtime models below — `Environment`, `Build`,
> `Deployment`, `Domain` — belong to Gombit Cloud, not the Forge control plane.
> Forge's control plane keeps only the authoring-loop models plus a linkage to
> its Cloud counterpart. `Secret` likewise moves to Cloud (ADR-005 D6).

Core models (Forge-owned):

```text
User
Organization
OrganizationMember
Project
ProjectRevision
AuditEvent
cloud_project_id   (linkage to the Cloud counterpart, ADR-005 D6)
```

Runtime models — **owned by Gombit Cloud**, see gombit-cloud RFC §19:

```text
Environment
Build
Deployment
Domain
Database
Secret
```

Possible later Forge models:

```text
Subscription
Invoice
Plugin
Integration
```

---

# 7. Project specification

The `ProjectSpec` is the most important contract in the system.

It must be versioned independently from generated source code.

Example:

```yaml
version: 1

project:
  name: Acme CRM
  slug: acme-crm

database:
  driver: postgres

auth:
  mode: cookie

resources:
  customer:
    label: Customer
    fields:
      name:
        type: string
        required: true

      email:
        type: string
        required: true
        unique: true

      active:
        type: boolean
        default: true

  invoice:
    label: Invoice
    fields:
      customer:
        type: belongs_to
        resource: customer
        required: true

      total:
        type: decimal
        required: true

      paid:
        type: boolean
        default: false

pages:
  dashboard:
    type: dashboard

  customers:
    type: resource_table
    resource: customer

  invoices:
    type: resource_table
    resource: invoice

navigation:
  - page: dashboard
    label: Dashboard

  - page: customers
    label: Customers

  - page: invoices
    label: Invoices
```

The canonical storage form should be JSON.

YAML is allowed for export/readability.

---

# 8. Project revisions

Every successful visual edit batch creates a new immutable `ProjectRevision`.

```text
Project
  ├── Revision 1
  ├── Revision 2
  ├── Revision 3
  └── Revision 4 ← current
```

A revision contains:

```text
id
project_id
spec_version
spec_json
created_by
created_at
```

Deployments reference an exact revision.

This enables:

* deterministic rebuilds
* rollback
* audit history
* diff visualization
* safe asynchronous builds

---

# 9. Forge compiler

The compiler converts:

```text
ProjectSpec → generated Gombit repository
```

It should be deterministic:

```text
same compiler version
+ same ProjectSpec
= same generated application
```

Generation stages:

```text
1. validate spec
2. construct domain graph
3. generate models
4. generate route registrations
5. generate handlers
6. generate admin registry
7. generate frontend configuration
8. generate page components
9. generate app navigation
10. generate project configuration
11. run formatter
12. run Gombit contract generation
13. run migration generation
14. run tests/build
```

The compiler may reuse Gombit's generator packages directly rather than spawning CLI commands where practical.

CLI execution remains useful as an end-to-end validation path.

---

# 10. Generated project ownership

Generated source falls into two classes.

## Compiler-owned

Forge may regenerate these files completely:

```text
internal/forge_generated/**
frontend/src/forge_generated/**
```

These directories carry explicit generated-file banners.

---

## Stable application shell

A Forge-generated repository also contains stable framework wiring.

The user owns all code after export.

Forge managed mode does not attempt to preserve arbitrary edits to generated files.

This avoids complex bidirectional reconciliation.

---

# 11. Build system

> **Superseded by ADR-005.** Build execution is a Gombit Cloud responsibility
> (gombit-cloud RFC §11–16, C1/§89), including the build queue, isolated
> disposable workers, the build state machine, container images, artifact
> storage and immutability. Forge does not build the deployed artifact.

Forge's only role here: it compiles a `ProjectRevision` into an ordinary Gombit
application (a source archive satisfying the Gombit application contract,
gombit-cloud RFC §9) and submits it to Cloud, which produces the immutable build
of record. Forge's local `gombit build` (ADR-002 toolchain) is for generation
and validation only, never the deployed artifact (ADR-005 D3).

---

# 12. Deployment model

> **Superseded by ADR-005.** The deployment model — isolated container per
> project, environments, runtime configuration, HTTPS — is owned by Gombit Cloud
> (gombit-cloud RFC §18–23, §56, C4/§92). D3 (isolated per project) is unchanged
> as a locked decision but is now *realized in Cloud's runtime isolation*
> (gombit-cloud RFC §56, Invariant F), not in Forge.

Forge invokes Cloud to deploy a build to an environment (`preview`,
`production`) and surfaces the resulting status and URL in its Deploy tab. It
does not provision or operate the runtime.

---

# 13. Database model

> **Superseded by ADR-005.** Managed PostgreSQL — provisioning, credentials,
> backups, restore — is owned by Gombit Cloud (gombit-cloud RFC §25–28, C2/§90).
> D4 (managed hosting is PostgreSQL-only) is unchanged as a locked decision but
> is now Cloud's scope (gombit-cloud RFC L7).

Exported applications may still use any database Gombit supports. The
managed-hosting restriction is Cloud's, not the compiler's.

---

# 14. Migration policy

> **Clarified by ADR-005.** The split: Forge *generates* the migration set as
> part of compiling a revision (driving Gombit's Atlas-backed generator, never
> diffing schemas itself); Gombit Cloud *verifies* it against the migration
> safety manifest (ADR-003) and *applies* it, and owns the "fail rather than
> apply an invalid or unapproved migration" gate (gombit-cloud RFC §29–33,
> C3/§91). The manifest format is a Gombit-upstream contract (ADR-003), not a
> Forge or Cloud private format.

Schema changes must use Gombit's Atlas-backed migration system.

Forge never uses GORM AutoMigrate in deployed applications.

Publish flow:

```text
old ProjectSpec
      ↓
new ProjectSpec
      ↓
generated GORM model changes
      ↓
Atlas diff
      ↓
versioned SQL migration
      ↓
review/validation
      ↓
deploy
```

Migrations become build artifacts.

A deployment must fail rather than silently apply an invalid migration.

---

# 15. Deployment safety

> **Superseded by ADR-005.** The publishing flow — preflight, migration apply,
> start, health check, traffic promotion, rollback — is owned by Gombit Cloud
> (gombit-cloud RFC §22–23, §29–35, C3/C4). Traffic-before-health and
> destructive-migration gating are Cloud invariants (RFC Invariant D/E, L9/L12).
> Forge surfaces this flow's status — including the destructive-migration
> approval prompt — in its Deploy tab, but does not execute it.

Application rollback and database rollback remain separate operations
(gombit-cloud RFC L11).

---

# 16. Preview behavior

> **Reframed by ADR-005.** The **preview environment** is a Gombit Cloud
> primitive (an isolated runtime + ephemeral data), pulled forward to Cloud
> C0/C1 (ADR-005 D5; gombit-cloud RFC §46, §85). What stays in Forge is the
> preview **UX** below — when to trigger a rebuild, debouncing, and replacing
> the environment. Forge triggers; Cloud provisions and runs.

Preview prioritizes speed over persistence guarantees.

For each project:

```text
save spec
   ↓
debounce
   ↓
new preview build
   ↓
replace preview environment
```

Do not compile on every keystroke.

Trigger preview builds on explicit:

**Preview changes**

or after meaningful saved changes.

---

# 17. Visual editor

The editor has four main areas:

```text
Data
Pages
Access
Deploy
```

## Data

Resource tree + field editor.

## Pages

Page list + structured page properties.

## Access

Users, groups and permission configuration.

## Deploy

Preview, build history, production deployment and logs.

---

# 18. Page builder

MVP page building is schema-driven rather than pixel-driven.

A resource table supports:

```text
title
columns
search
filters
sorting
pagination
primary action
row actions
```

A form supports:

```text
field ordering
field visibility
labels
help text
required state
relationship selectors
```

Layout options:

```text
single column
two column
section groups
```

No absolute positioning.

---

# 19. Branding

MVP branding:

```text
application name
logo
primary accent color
light/dark/system appearance
```

Advanced theme editing is deferred.

---

# 20. Authentication

Managed Forge projects default to:

```text
--auth cookie
```

Reasons:

* browser-first product
* Gombit admin compatibility
* HttpOnly session security
* built-in CSRF model

JWT remains available for exported/API-oriented applications later.

---

# 21. Secrets

> **Amended by ADR-005.** The *rule* below — secrets never live in `ProjectSpec`
> — stays a Forge/spec invariant. The encrypted **secrets store** (write-only
> values, per-environment scope, runtime injection) is a Gombit Cloud
> responsibility (gombit-cloud RFC §38–39, C5/§93), not a Forge control-plane
> model. Forge renders a secret-management view onto Cloud's store; it does not
> hold secret values.

Secrets never live in `ProjectSpec`. They are supplied as separate encrypted
environment configuration held by Cloud:

```text
DATABASE_URL
GOMBIT_JWT_SECRET
SMTP_PASSWORD
EXTERNAL_API_KEY
```

Cloud stores values write-only after creation, scoped per environment.

---

# 22. Multi-tenancy

There are two unrelated kinds of tenancy.

## Forge tenancy

Forge itself is multi-tenant:

```text
Organization
 └── Projects
```

Organization members receive Forge-level roles.

## Generated application tenancy

Not supported in MVP.

A generated application's business-level multi-tenancy is explicitly deferred.

---

# 23. Audit log

> **Reframed by ADR-005.** §23 is retained, but its action list is reduced: the
> runtime lifecycle events (deployment started / succeeded / failed) and secret
> changes are Gombit Cloud's audit trail (gombit-cloud RFC §62), not Forge's —
> the secrets store itself moved to Cloud (#41). Forge records its own actions,
> including the *trigger* of a preview or deploy, never their runtime lifecycle.
> See docs/ADR-005.md §4.3.

Forge records important control-plane actions:

```text
project created
spec revision created
source exported
member invited
preview triggered
deploy triggered
```

The runtime lifecycle and secret events §23 originally listed belong to Gombit
Cloud (gombit-cloud RFC §62), not Forge:

```text
deployment started / succeeded / failed
secret changed
```

Secret values must never appear in audit data.

---

# 24. Observability

> **Superseded by ADR-005.** Runtime logs, health state, and build/deployment
> history are produced and owned by Gombit Cloud (gombit-cloud RFC §40–42,
> C6/§94). Forge's Deploy tab *reads and displays* them through Cloud's API; it
> does not collect or store them.

The log fields Forge surfaces from Cloud should include:

```text
timestamp
level
message
request_id
```

Preserving Gombit request IDs through the runtime is a Cloud concern
(gombit-cloud RFC §41).

---

# 25. Infrastructure

> **Superseded by ADR-005.** Provider infrastructure — the AWS stack (ECS/
> Fargate, ECR, RDS, S3, Route 53, ACM, ALB, Secrets Manager, SQS) — belongs to
> Gombit Cloud (gombit-cloud RFC §58, PROVISIONAL). No provider names belong in
> Forge's design. Provider details sit below Cloud's platform abstraction and
> are not part of any Forge- or customer-visible contract (gombit-cloud RFC §57,
> L15). Kubernetes is not required for v0.1 (gombit-cloud RFC §59, L16).

Forge holds no infrastructure. It is a client of Cloud's platform API.

---

# 26. Build execution

> **Superseded by ADR-005.** The build queue and workers are Gombit Cloud
> (gombit-cloud RFC §14, C1/§89). D8 (builds are asynchronous) is unchanged as a
> locked decision but is realized by Cloud's workers (gombit-cloud RFC L13),
> because Forge no longer builds. Control-plane HTTP requests never synchronously
> perform builds — that invariant now lives on the Cloud API.

---

# 27. Security boundaries

> **Superseded by ADR-005.** Build isolation is a Gombit Cloud concern
> (gombit-cloud RFC §15, §76, L13). D9 (build execution is isolated; generated
> source is untrusted) is unchanged as a locked decision but is enforced by
> Cloud's disposable workers, not by Forge. The restrictions below are Cloud's
> to implement:

* no access to Cloud production credentials
* scoped artifact credentials
* network access restricted where practical
* CPU/memory/time limits
* ephemeral filesystem
* worker destroyed after build

---

# 28. Export

Export is a first-class product feature.

ZIP structure:

```text
project/
├── cmd/
├── internal/
├── frontend/
├── database/
├── config/
├── docs/
├── gombit.yaml
├── go.mod
└── README.md
```

Exported README should include:

```bash
gombit dev
gombit db migrate
gombit build --embed
```

The generated repository should build outside Forge.

CI must test that condition.

---

# 29. Forge version metadata

Each exported project records:

```yaml
forge:
  spec_version: 1
  compiler_version: 0.1.0
  gombit_version: 0.x.y
```

This is informational after export.

The application does not depend on Forge at runtime.

---

# 30. MVP non-goals

Explicitly excluded:

* freeform drag-and-drop canvas
* arbitrary JavaScript workflow editor
* custom code blocks
* plugin marketplace
* background jobs
* email builder
* storage/file-management builder
* mobile applications
* native applications
* AI application generation
* real-time collaborative editing
* custom domains
* automatic multi-region deployment
* Kubernetes
* application-level multi-tenancy
* Git import / reverse engineering
* source-code ↔ visual-editor round trip
* arbitrary third-party API connectors
* visual SQL editor
* complex chart builder
* billing automation for generated apps

These may become later products.

---

# 31. MVP success criterion

A new user must be able to build this application without writing code:

```text
Customer Portal

Resources:
  Customer
  Project
  Ticket

Authentication:
  users log in

Permissions:
  Admin
  Support

Pages:
  Dashboard
  Customers
  Projects
  Tickets

Admin:
  enabled

Database:
  PostgreSQL
```

They must then be able to:

```text
Preview
Deploy
Create a user
Use the application
Use /admin/
Export the source
Run the exported source locally
```

If this loop works reliably, the MVP is successful.

---

# 32. Milestones

## M0 — Forge compiler spike

Goal: prove the central architectural assumption.

Implement:

```text
ProjectSpec
   ↓
2 resources
   ↓
generated Gombit project
   ↓
build
   ↓
working application
```

Acceptance criteria:

* deterministic generation
* exported app compiles
* migration works
* React CRUD works
* admin works
* no Forge runtime dependency

**Go/no-go gate.**

---

## M1 — Forge control plane

Implement:

* organizations
* projects
* project revisions
* environments
* builds
* deployments
* audit events

Forge itself runs on Gombit.

---

## M2 — Data editor

Implement visual:

* resources
* fields
* validation
* relationships
* CRUD behavior
* admin settings

Produces valid `ProjectSpec`.

---

## M3 — Structured page builder

Implement:

* resource table
* resource form
* detail page
* dashboard
* navigation
* branding

---

## M4 — Build integration

> **Re-scoped by ADR-005 §4.4.** Not "Forge builds a PaaS" — "Forge integrates
> Gombit Cloud." The build queue, isolated workers, container build, registry
> and artifact storage are Cloud C1 (gombit-cloud RFC §89).

Implement (Forge side):

* compile a revision to an ordinary Gombit application (source archive)
* submit it to Cloud's build API via the `cloud_project_id` linkage
* track build state and stream build logs into the Deploy tab

---

## M5 — Preview integration

> **Re-scoped by ADR-005 §4.4/D5.** Preview environment provisioning and URL
> routing are Cloud primitives, pulled forward to Cloud C0/C1 (gombit-cloud RFC
> §46). Forge triggers and surfaces them.

Implement (Forge side):

* trigger a Cloud preview environment for the current revision
* the debounce / explicit-rebuild / environment-replacement UX (§16)
* surface preview URL, build state and application logs in the Deploy tab

---

## M6 — Deploy integration

> **Re-scoped by ADR-005 §4.4.** Production database, secrets injection,
> migration apply, container rollout, health checks and rollback are Cloud C4
> (gombit-cloud RFC §92). Forge invokes them and renders the result.

Implement (Forge side):

* invoke Cloud production deploy for a selected build
* render the destructive-migration approval gate (gombit-cloud RFC §32) in the UI
* surface deploy status, stable URL, health and rollback controls in the Deploy tab

---

## M7 — Export

Implement:

* source ZIP
* generated README
* reproducibility metadata
* CI test proving exported project works outside Forge

GitHub export is optional for MVP release.

---

# 33. Locked MVP decisions

The following should be considered locked unless implementation proves them invalid.

### D1 — Forge is spec-first

`ProjectSpec` is the source of truth.

### D2 — Forge is a compiler, not a proprietary application runtime

Generated applications are normal Gombit applications.

### D3 — Managed applications are isolated per project

No shared generated-app runtime.

> **Realized in Gombit Cloud (ADR-005).** Unchanged as a decision — the isolation
> still holds — but it is Cloud's runtime that enforces it (gombit-cloud RFC §56,
> Invariant F), not the Forge control plane.

### D4 — Managed hosting supports PostgreSQL only in MVP

Other Gombit databases remain export options.

> **Realized in Gombit Cloud (ADR-005).** Managed-hosting scope is Cloud's
> (gombit-cloud RFC L7, §25). Export still targets any driver Gombit supports.

### D5 — Managed applications default to cookie/session auth

Required for the browser-first product and Gombit admin.

### D6 — Structured pages, not freeform canvas

Tables/forms/details/dashboard only.

### D7 — Forge itself uses Gombit

Dogfood the framework.

### D8 — Builds are asynchronous

No HTTP request directly builds an application.

> **Realized in Gombit Cloud (ADR-005).** Unchanged as a decision — the build is
> Cloud's (gombit-cloud RFC §14, C1/§89). Forge submits source and observes; it
> does not run the queue.

### D9 — Build execution is isolated

Generated code is treated as untrusted.

> **Realized in Gombit Cloud (ADR-005).** Build isolation is enforced by Cloud's
> disposable workers (gombit-cloud RFC §15, §76, L13), not Forge.

### D10 — Export is mandatory MVP functionality

No-code without code captivity is part of the product, not a later marketing feature.

### D11 — No round-trip after eject

Export is a one-way ownership boundary.

### D12 — Use Gombit's normal contracts

Do not create Forge-specific auth, migration, API, admin or ORM systems where Gombit already provides them.

---

# 34. Post-MVP direction

Once the core compiler/deploy/export loop is proven, the most valuable expansions are likely:

```text
background jobs
email
file storage
webhooks
external API connectors
workflow automation
custom domains
GitHub synchronization
application templates
AI-assisted ProjectSpec generation
custom components
custom code escape hatches
```

AI should operate primarily on `ProjectSpec`, not directly mutate generated source.

Example:

> “Create a lightweight CRM with customers, contacts, deals and salespeople.”

AI produces a proposed spec.

The user reviews it visually.

Forge compiles it normally.

That keeps the same architecture intact.

---

# 35. Final architecture rule

The entire product should remain understandable through one sentence:

> **Forge edits a declarative application model; Gombit turns that model into ordinary software.**

If a future feature violates that model by requiring applications to depend permanently on Forge internals, it needs strong justification before being accepted.
