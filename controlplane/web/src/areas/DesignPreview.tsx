import { useEffect, useState } from "react";
import type { CSSProperties } from "react";
import { describeError } from "../api/client";
import {
  getProjectSpec,
  type ProjectSpec,
  type SpecField,
  type SpecPage,
  type SpecResource,
} from "../api/projects";
import { ProjectPicker } from "./ProjectPicker";

// The Design Preview area (ADR-001 §65): a structural preview rendered entirely
// from the ProjectSpec. It executes no backend extension code and makes no
// request to a running app, so it deliberately cannot validate runtime behavior
// — a fidelity boundary the UI states explicitly. This is distinct from the real
// Runtime Preview (M5).
export function DesignPreview() {
  const [projectID, setProjectID] = useState<number | null>(null);
  const [spec, setSpec] = useState<ProjectSpec | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setSpec(null);
    setError(null);
    if (projectID == null) return;
    let active = true;
    getProjectSpec(projectID)
      .then((s) => active && setSpec(s))
      .catch((e) => active && setError(describeError(e)));
    return () => {
      active = false;
    };
  }, [projectID]);

  return (
    <section aria-labelledby="area-preview">
      <h2 id="area-preview">Preview</h2>

      <ProjectPicker onSelect={setProjectID} />

      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}

      {projectID == null ? (
        <p className="muted">Select a project to preview its design.</p>
      ) : (
        <>
          <FidelityBanner />
          <PreviewFrame spec={spec} />
        </>
      )}
    </section>
  );
}

// FidelityBanner states the boundary up front (ADR-001 §65): the preview is
// structural and does not run extension code, so it cannot validate runtime
// behavior. Kept prominent and unmissable.
function FidelityBanner() {
  return (
    <aside className="fidelity-banner" role="note" aria-label="Design Preview fidelity">
      <strong>Design Preview — structure only.</strong> Rendered from the spec. Custom lifecycle hooks and permissions
      are <em>not</em> executed here, and no data is loaded, so this does not validate runtime extension behavior. Use
      Runtime Preview to exercise the built application.
    </aside>
  );
}

function PreviewFrame({ spec }: { spec: ProjectSpec | null }) {
  const resources = spec?.resources ?? [];
  const pages = spec?.pages ?? [];
  const nav = spec?.navigation ?? [];
  const branding = spec?.branding ?? {};

  const accent = branding.accent_color && /^#[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$/.test(branding.accent_color)
    ? branding.accent_color
    : undefined;
  const frameStyle = accent ? ({ "--preview-accent": accent } as CSSProperties) : undefined;

  return (
    <div className="preview-frame" style={frameStyle} aria-label="Design preview">
      <header className="preview-header">
        <span className="preview-app-name">{branding.app_name || "Application"}</span>
        {branding.appearance && <span className="preview-appearance muted">{branding.appearance} appearance</span>}
      </header>

      <nav className="preview-nav" aria-label="Preview navigation">
        {nav.length === 0 ? (
          <span className="muted">No navigation configured</span>
        ) : (
          nav.map((n, i) => (
            <span key={i} className="preview-nav-item">
              {n.label}
              {n.target === "external" && <span className="badge">external</span>}
            </span>
          ))
        )}
      </nav>

      <div className="preview-pages">
        {pages.length === 0 ? (
          <p className="muted">No pages yet.</p>
        ) : (
          pages.map((p) => <PagePreview key={p.id} page={p} resources={resources} />)
        )}
      </div>
    </div>
  );
}

function findResource(resources: SpecResource[], id?: string) {
  return resources.find((r) => r.id === id);
}

function resolveFields(resource: SpecResource | undefined, ids?: string[]): SpecField[] {
  const fields = resource?.fields ?? [];
  if (!ids || ids.length === 0) return fields;
  return ids.map((id) => fields.find((f) => f.id === id)).filter((f): f is SpecField => f != null);
}

// hasMany finds the resources that reference `resourceId` through a belongs_to
// field — the has_many side surfaced on a detail page (ADR §53 structure).
function hasMany(resources: SpecResource[], resourceId: string) {
  return resources.filter((r) => (r.fields ?? []).some((f) => f.type === "belongs_to" && f.target === resourceId));
}

function PagePreview({ page, resources }: { page: SpecPage; resources: SpecResource[] }) {
  const resource = findResource(resources, page.resource);
  return (
    <article className="preview-page" aria-label={`${page.label} (${page.type})`}>
      <div className="preview-page-head">
        <span className="preview-page-title">{page.label}</span>
        <span className="badge">{page.type}</span>
        <span className="muted preview-slug">/{page.slug}</span>
      </div>
      {page.type === "resource_table" && <TablePreview page={page} resource={resource} />}
      {page.type === "resource_form" && <FormPreview page={page} resource={resource} />}
      {page.type === "resource_detail" && <DetailPreview resource={resource} resources={resources} />}
      {page.type === "dashboard" && <DashboardPreview page={page} resources={resources} />}
    </article>
  );
}

function TablePreview({ page, resource }: { page: SpecPage; resource: SpecResource | undefined }) {
  const columns = resolveFields(resource, page.table?.columns);
  return (
    <table className="preview-table">
      <thead>
        <tr>
          <th>id</th>
          {columns.map((c) => (
            <th key={c.id}>{c.label}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {[0, 1].map((r) => (
          <tr key={r}>
            <td>—</td>
            {columns.map((c) => (
              <td key={c.id}>—</td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function FormPreview({ page, resource }: { page: SpecPage; resource: SpecResource | undefined }) {
  const fields = resolveFields(resource, page.form?.fields);
  return (
    <div className="preview-form">
      {fields.map((f) => (
        <label key={f.id} className="preview-field">
          <span>
            {f.label}
            {f.required && <span className="preview-required"> *</span>}
          </span>
          <span className="preview-input" aria-hidden="true" />
        </label>
      ))}
    </div>
  );
}

function DetailPreview({ resource, resources }: { resource: SpecResource | undefined; resources: SpecResource[] }) {
  const fields = resource?.fields ?? [];
  const related = resource ? hasMany(resources, resource.id) : [];
  return (
    <div className="preview-detail">
      <dl>
        {fields.map((f) => (
          <div key={f.id} className="preview-detail-row">
            <dt>{f.label}</dt>
            <dd>—</dd>
          </div>
        ))}
      </dl>
      {related.length > 0 && (
        <div className="preview-related">
          <h4>Related</h4>
          {related.map((r) => (
            <span key={r.id} className="badge">
              {r.label_plural || r.label}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function DashboardPreview({ page, resources }: { page: SpecPage; resources: SpecResource[] }) {
  const counts = page.dashboard?.count_cards ?? [];
  const recents = page.dashboard?.recent_lists ?? [];
  const label = (id: string) => findResource(resources, id)?.label ?? id;
  return (
    <div className="preview-dashboard">
      <div className="preview-count-cards">
        {counts.map((c, i) => (
          <div key={i} className="preview-count-card">
            <span className="preview-count-label">{c.label}</span>
            <strong className="preview-count-value">—</strong>
          </div>
        ))}
      </div>
      {recents.map((c, i) => (
        <div key={i} className="preview-recent">
          <span>{c.label}</span>
          <span className="muted"> · {label(c.resource)}</span>
        </div>
      ))}
    </div>
  );
}
