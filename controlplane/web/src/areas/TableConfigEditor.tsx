import { useState } from "react";
import type { FormEvent } from "react";
import { describeError } from "../api/client";
import { updateTableConfig, type SpecPage, type SpecResource } from "../api/projects";

// TableConfigEditor configures one resource_table page (DESIGN.md §4.4, §18):
// its display label, table heading, which of the bound resource's fields are
// columns, whether it offers search, and its page size. It replaces the whole
// configuration on save (the backend resets omitted settings), and calls
// onChanged so the parent reloads. Column order follows the resource's field
// order; explicit reordering is a later refinement.
export function TableConfigEditor({
  projectID,
  page,
  resource,
  onChanged,
}: {
  projectID: number;
  page: SpecPage;
  resource: SpecResource | undefined;
  onChanged: () => void;
}) {
  const fields = resource?.fields ?? [];
  const [label, setLabel] = useState(page.label);
  const [title, setTitle] = useState(page.table?.title ?? "");
  const [columns, setColumns] = useState<Set<string>>(new Set(page.table?.columns ?? []));
  const [search, setSearch] = useState(page.table?.search ?? false);
  const [pageSize, setPageSize] = useState(page.table?.page_size ?? 0);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function toggleColumn(id: string, on: boolean) {
    setColumns((prev) => {
      const next = new Set(prev);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (pending) return;
    setPending(true);
    setError(null);
    try {
      // Column order follows the resource's authored field order.
      const orderedColumns = fields.filter((f) => columns.has(f.id)).map((f) => f.id);
      await updateTableConfig(projectID, page.id, {
        label,
        title: title.trim() === "" ? undefined : title,
        columns: orderedColumns,
        search,
        page_size: pageSize,
      });
      onChanged();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setPending(false);
    }
  }

  return (
    <form className="table-config" aria-label={`Configure ${page.label}`} onSubmit={onSubmit}>
      <label className="stacked">
        Label
        <input aria-label="Page label" value={label} onChange={(e) => setLabel(e.target.value)} disabled={pending} required />
      </label>
      <label className="stacked">
        Title
        <input
          aria-label="Table title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          disabled={pending}
          placeholder="Defaults to the label"
        />
      </label>

      <fieldset className="columns">
        <legend>Columns</legend>
        {fields.length === 0 ? (
          <p className="muted">The bound resource has no fields yet.</p>
        ) : (
          fields.map((f) => (
            <label key={f.id} className="check">
              <input
                type="checkbox"
                aria-label={`Column ${f.label}`}
                checked={columns.has(f.id)}
                onChange={(e) => toggleColumn(f.id, e.target.checked)}
                disabled={pending}
              />
              {f.label}
            </label>
          ))
        )}
      </fieldset>

      <div className="table-config-row">
        <label className="check">
          <input type="checkbox" aria-label="Search" checked={search} onChange={(e) => setSearch(e.target.checked)} disabled={pending} />
          Search
        </label>
        <label className="stacked">
          Page size
          <input
            type="number"
            aria-label="Page size"
            min={0}
            value={pageSize}
            onChange={(e) => setPageSize(Number(e.target.value))}
            disabled={pending}
          />
        </label>
      </div>

      <button type="submit" disabled={pending}>
        {pending ? "Saving…" : "Save table"}
      </button>

      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </form>
  );
}
