import { useState } from "react";
import type { ChangeEvent, FormEvent } from "react";
import { describeError } from "../api/client";
import { updateBehavior, type ResourceBehavior, type SpecField } from "../api/projects";

// The per-resource behavior editor (DESIGN.md §4.3): CRUD toggles, admin
// visibility, and which fields appear in the list / are searchable / sortable /
// filterable. It sends the whole behavior on save (the backend PATCH replaces
// wholesale), and a behavior edit is ABI-neutral, so it commits.
export function BehaviorEditor({
  projectID,
  resourceID,
  fields,
  behavior,
  onChanged,
}: {
  projectID: number;
  resourceID: string;
  fields: SpecField[];
  behavior: ResourceBehavior | undefined;
  onChanged: () => void;
}) {
  const [state, setState] = useState<ResourceBehavior>(() => ({
    create_enabled: behavior?.create_enabled ?? false,
    update_enabled: behavior?.update_enabled ?? false,
    delete_enabled: behavior?.delete_enabled ?? false,
    admin_visible: behavior?.admin_visible ?? false,
    list_fields: behavior?.list_fields ?? [],
    searchable_fields: behavior?.searchable_fields ?? [],
    sortable_fields: behavior?.sortable_fields ?? [],
    filterable_fields: behavior?.filterable_fields ?? [],
  }));
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const toggle = (key: keyof ResourceBehavior) => (e: ChangeEvent<HTMLInputElement>) =>
    setState((s) => ({ ...s, [key]: e.target.checked }));

  const selectFields =
    (key: "list_fields" | "searchable_fields" | "sortable_fields" | "filterable_fields") =>
    (e: ChangeEvent<HTMLSelectElement>) => {
      const chosen = Array.from(e.target.selectedOptions, (o) => o.value);
      setState((s) => ({ ...s, [key]: chosen }));
    };

  async function save(e: FormEvent) {
    e.preventDefault();
    if (pending) return;
    setPending(true);
    setError(null);
    try {
      await updateBehavior(projectID, resourceID, state);
      onChanged();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setPending(false);
    }
  }

  return (
    <form className="behavior-editor" aria-label="Resource behavior" onSubmit={save}>
      <h4>Behavior</h4>
      <div className="toggles">
        <label className="check">
          <input type="checkbox" checked={state.create_enabled} onChange={toggle("create_enabled")} disabled={pending} /> create
        </label>
        <label className="check">
          <input type="checkbox" checked={state.update_enabled} onChange={toggle("update_enabled")} disabled={pending} /> update
        </label>
        <label className="check">
          <input type="checkbox" checked={state.delete_enabled} onChange={toggle("delete_enabled")} disabled={pending} /> delete
        </label>
        <label className="check">
          <input type="checkbox" checked={state.admin_visible} onChange={toggle("admin_visible")} disabled={pending} /> admin visible
        </label>
      </div>

      <FieldSelect label="List fields" selected={state.list_fields ?? []} fields={fields} onChange={selectFields("list_fields")} disabled={pending} />
      <FieldSelect label="Searchable" selected={state.searchable_fields ?? []} fields={fields} onChange={selectFields("searchable_fields")} disabled={pending} />
      <FieldSelect label="Sortable" selected={state.sortable_fields ?? []} fields={fields} onChange={selectFields("sortable_fields")} disabled={pending} />
      <FieldSelect label="Filterable" selected={state.filterable_fields ?? []} fields={fields} onChange={selectFields("filterable_fields")} disabled={pending} />

      <button type="submit" disabled={pending}>
        {pending ? "Saving…" : "Save behavior"}
      </button>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </form>
  );
}

function FieldSelect({
  label,
  selected,
  fields,
  onChange,
  disabled,
}: {
  label: string;
  selected: string[];
  fields: SpecField[];
  onChange: (e: ChangeEvent<HTMLSelectElement>) => void;
  disabled: boolean;
}) {
  return (
    <label className="field-select">
      {label}
      <select multiple aria-label={label} value={selected} onChange={onChange} disabled={disabled}>
        {fields.map((f) => (
          <option key={f.id} value={f.id}>
            {f.label}
          </option>
        ))}
      </select>
    </label>
  );
}
