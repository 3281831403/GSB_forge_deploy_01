import { useState } from "react";
import type { FormEvent } from "react";
import { describeError } from "../api/client";
import { updateFormConfig, type FormLayout, type SpecPage, type SpecResource } from "../api/projects";

const LAYOUTS: { value: FormLayout; label: string }[] = [
  { value: "single_column", label: "Single column" },
  { value: "two_column", label: "Two column" },
  { value: "section_groups", label: "Section groups" },
];

// FormConfigEditor configures one resource_form page (DESIGN.md §18): its label,
// layout, and which of the bound resource's fields appear (in resource field
// order). It replaces the whole configuration on save, then calls onChanged so
// the parent reloads. Field order follows the resource's authored order.
export function FormConfigEditor({
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
  const [layout, setLayout] = useState<FormLayout>((page.form?.layout as FormLayout) || "single_column");
  const [selected, setSelected] = useState<Set<string>>(new Set(page.form?.fields ?? []));
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function toggleField(id: string, on: boolean) {
    setSelected((prev) => {
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
      const orderedFields = fields.filter((f) => selected.has(f.id)).map((f) => f.id);
      await updateFormConfig(projectID, page.id, { label, layout, fields: orderedFields });
      onChanged();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setPending(false);
    }
  }

  return (
    <form className="form-config" aria-label={`Configure ${page.label}`} onSubmit={onSubmit}>
      <label className="stacked">
        Label
        <input aria-label="Page label" value={label} onChange={(e) => setLabel(e.target.value)} disabled={pending} required />
      </label>
      <label className="stacked">
        Layout
        <select aria-label="Layout" value={layout} onChange={(e) => setLayout(e.target.value as FormLayout)} disabled={pending}>
          {LAYOUTS.map((l) => (
            <option key={l.value} value={l.value}>
              {l.label}
            </option>
          ))}
        </select>
      </label>

      <fieldset className="columns">
        <legend>Fields</legend>
        {fields.length === 0 ? (
          <p className="muted">The bound resource has no fields yet.</p>
        ) : (
          fields.map((f) => (
            <label key={f.id} className="check">
              <input
                type="checkbox"
                aria-label={`Field ${f.label}`}
                checked={selected.has(f.id)}
                onChange={(e) => toggleField(f.id, e.target.checked)}
                disabled={pending}
              />
              {f.label}
            </label>
          ))
        )}
      </fieldset>

      <button type="submit" disabled={pending}>
        {pending ? "Saving…" : "Save form"}
      </button>

      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </form>
  );
}
