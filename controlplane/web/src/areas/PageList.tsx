import { useState } from "react";
import type { FormEvent } from "react";
import { describeError } from "../api/client";
import {
  addPage,
  deletePage,
  PAGE_TYPES,
  type PageType,
  type SpecPage,
  type SpecResource,
} from "../api/projects";
import { FormConfigEditor } from "./FormConfigEditor";
import { TableConfigEditor } from "./TableConfigEditor";

// PageList renders a project's structured pages and the create/delete controls
// (DESIGN.md §4.4, §18). The backend derives each page's slug and mints its id;
// this supplies the label, type and (for the resource page types) the bound
// resource. After any successful edit it calls onChanged so the parent reloads.
export function PageList({
  projectID,
  pages,
  resources,
  onChanged,
}: {
  projectID: number;
  pages: SpecPage[];
  resources: SpecResource[];
  onChanged: () => void;
}) {
  const [error, setError] = useState<string | null>(null);

  // Resolve a bound resource id to its label for display; fall back to the id.
  const resourceLabel = (id?: string) => resources.find((r) => r.id === id)?.label ?? id ?? "";
  const typeLabel = (t: PageType) => PAGE_TYPES.find((p) => p.type === t)?.label ?? t;

  const run = async (op: () => Promise<void>): Promise<boolean> => {
    setError(null);
    try {
      await op();
      return true;
    } catch (err) {
      setError(describeError(err));
      return false;
    }
  };

  return (
    <div className="page-editor">
      {pages.length === 0 ? (
        <p className="muted">No pages yet. Add the first one below.</p>
      ) : (
        <ul className="page-list" aria-label="Pages">
          {pages.map((p) => (
            <PageRow
              key={p.id}
              projectID={projectID}
              page={p}
              typeLabel={typeLabel(p.type)}
              resource={resources.find((r) => r.id === p.resource)}
              resourceLabel={resourceLabel(p.resource)}
              onChanged={onChanged}
              onDelete={() =>
                run(async () => {
                  await deletePage(projectID, p.id);
                  onChanged();
                })
              }
            />
          ))}
        </ul>
      )}

      <AddPageForm
        resources={resources}
        onAdd={(input) =>
          run(async () => {
            await addPage(projectID, input);
            onChanged();
          })
        }
      />

      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </div>
  );
}

function PageRow({
  projectID,
  page,
  typeLabel,
  resource,
  resourceLabel,
  onChanged,
  onDelete,
}: {
  projectID: number;
  page: SpecPage;
  typeLabel: string;
  resource: SpecResource | undefined;
  resourceLabel: string;
  onChanged: () => void;
  onDelete: () => Promise<boolean>;
}) {
  const [confirming, setConfirming] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [pending, setPending] = useState(false);

  // Resource table and form pages have a configuration to edit (detail/dashboard
  // builders are later M3 issues).
  const configurable = page.type === "resource_table" || page.type === "resource_form";

  async function confirmDelete() {
    if (pending) return;
    setPending(true);
    await onDelete();
    setPending(false);
    setConfirming(false);
  }

  return (
    <li className="page-row">
      <div className="page-summary">
        {configurable && (
          <button
            type="button"
            className="expand"
            aria-expanded={expanded}
            aria-label={`Configure ${page.label}`}
            onClick={() => setExpanded((v) => !v)}
          >
            {expanded ? "▾" : "▸"}
          </button>
        )}
        <span className="page-label">{page.label}</span>
        <span className="badge">{typeLabel}</span>
        {page.resource && <span className="muted"> · {resourceLabel}</span>}
        <span className="muted page-slug"> /{page.slug}</span>
        <span className="row-actions">
          <button type="button" onClick={() => setConfirming(true)}>
            Delete
          </button>
        </span>
      </div>

      {/* Re-key on the persisted config so a reload that changed it remounts the
          editor and re-seeds its state rather than showing stale values. */}
      {expanded && page.type === "resource_table" && (
        <TableConfigEditor
          key={JSON.stringify(page.table ?? {}) + page.label}
          projectID={projectID}
          page={page}
          resource={resource}
          onChanged={onChanged}
        />
      )}
      {expanded && page.type === "resource_form" && (
        <FormConfigEditor
          key={JSON.stringify(page.form ?? {}) + page.label}
          projectID={projectID}
          page={page}
          resource={resource}
          onChanged={onChanged}
        />
      )}

      {confirming && (
        <div className="confirm" role="group" aria-label={`Delete ${page.label}`}>
          <span>Delete “{page.label}”?</span>
          <button type="button" onClick={confirmDelete} disabled={pending}>
            {pending ? "Deleting…" : "Confirm delete"}
          </button>
          <button type="button" onClick={() => setConfirming(false)} disabled={pending}>
            Cancel
          </button>
        </div>
      )}
    </li>
  );
}

// AddPageForm collects a label, a page type and — for the resource page types —
// the bound resource. The resource select is shown only when the chosen type
// binds to one; a dashboard carries no resource. The form clears only on a
// successful add, so a rejected add keeps the input for the user to fix.
function AddPageForm({
  resources,
  onAdd,
}: {
  resources: SpecResource[];
  onAdd: (input: { label: string; type: PageType; resource?: string }) => Promise<boolean>;
}) {
  const [label, setLabel] = useState("");
  const [type, setType] = useState<PageType>("resource_table");
  const [resource, setResource] = useState("");
  const [pending, setPending] = useState(false);

  const boundToResource = PAGE_TYPES.find((p) => p.type === type)?.boundToResource ?? false;

  return (
    <form
      className="page-form add"
      aria-label="Add page"
      onSubmit={async (e: FormEvent) => {
        e.preventDefault();
        if (pending) return;
        setPending(true);
        const ok = await onAdd({ label, type, resource: boundToResource ? resource : undefined });
        setPending(false);
        if (ok) {
          setLabel("");
          setResource("");
        }
      }}
    >
      <input
        aria-label="New page label"
        value={label}
        onChange={(e) => setLabel(e.target.value)}
        disabled={pending}
        placeholder="Page label"
        required
      />
      <select aria-label="Page type" value={type} onChange={(e) => setType(e.target.value as PageType)} disabled={pending}>
        {PAGE_TYPES.map((p) => (
          <option key={p.type} value={p.type}>
            {p.label}
          </option>
        ))}
      </select>
      {boundToResource && (
        <select
          aria-label="Bound resource"
          value={resource}
          onChange={(e) => setResource(e.target.value)}
          disabled={pending}
          required
        >
          <option value="">Select a resource…</option>
          {resources.map((r) => (
            <option key={r.id} value={r.id}>
              {r.label}
            </option>
          ))}
        </select>
      )}
      <button type="submit" disabled={pending}>
        {pending ? "Adding…" : "Add page"}
      </button>
    </form>
  );
}
