import { useState } from "react";
import type { FormEvent } from "react";
import { describeError } from "../api/client";
import {
  addField,
  deleteField,
  updateField,
  EDITABLE_FIELD_TYPES,
  type FieldInput,
  type FieldType,
  type SpecField,
} from "../api/projects";

// The per-resource field editor (DESIGN.md §4.2). It edits fields through the
// backend field operations — which mint symbols and classify a type change as
// ABI-breaking — so a breaking change (type change, delete) comes back as an
// error the editor surfaces ("requires validation") rather than a silent commit.
export function FieldEditor({
  projectID,
  resourceID,
  fields,
  onChanged,
}: {
  projectID: number;
  resourceID: string;
  fields: SpecField[];
  onChanged: () => void;
}) {
  const [error, setError] = useState<string | null>(null);
  const [addKey, setAddKey] = useState(0);

  // run reports success and turns a breaking/invalid response into a readable
  // message (the backend's 409/422 explains why).
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

  // belongs_to fields are owned by the relationship editor below, so they are
  // left out of the scalar field list rather than shown twice.
  const scalarFields = fields.filter((f) => f.type !== "belongs_to");

  return (
    <div className="field-editor">
      {scalarFields.length === 0 ? (
        <p className="muted">No fields yet.</p>
      ) : (
        <ul className="field-list" aria-label="Fields">
          {scalarFields.map((f) => (
            <FieldRow key={f.id} projectID={projectID} resourceID={resourceID} field={f} onChanged={onChanged} run={run} />
          ))}
        </ul>
      )}

      <FieldForm
        key={addKey}
        title="Add field"
        onSubmit={(input) =>
          run(async () => {
            await addField(projectID, resourceID, input);
            onChanged();
          }).then((ok) => {
            if (ok) setAddKey((k) => k + 1); // remount to clear the add form
            return ok;
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

function FieldRow({
  projectID,
  resourceID,
  field,
  onChanged,
  run,
}: {
  projectID: number;
  resourceID: string;
  field: SpecField;
  onChanged: () => void;
  run: (op: () => Promise<void>) => Promise<boolean>;
}) {
  const [editing, setEditing] = useState(false);
  const [pending, setPending] = useState(false);

  // Only scalar fields reach here — belongs_to fields are filtered out by
  // FieldEditor and managed in the relationship editor.

  if (editing) {
    return (
      <li>
        <FieldForm
          title={`Edit ${field.label}`}
          initial={field}
          onSubmit={async (input) => {
            const ok = await run(async () => {
              await updateField(projectID, resourceID, field.id, input);
              onChanged();
            });
            if (ok) setEditing(false);
            return ok;
          }}
          onCancel={() => setEditing(false)}
        />
      </li>
    );
  }

  return (
    <li className="field-row">
      <span className="field-label">{field.label}</span>
      <span className="field-type muted">{field.type}</span>
      {field.required && <span className="badge">required</span>}
      {field.unique && <span className="badge">unique</span>}
      <span className="row-actions">
        <button type="button" onClick={() => setEditing(true)}>
          Edit
        </button>
        <button
          type="button"
          disabled={pending}
          onClick={async () => {
            if (pending) return;
            setPending(true);
            await run(async () => {
              await deleteField(projectID, resourceID, field.id);
              onChanged();
            });
            setPending(false);
          }}
        >
          {pending ? "Deleting…" : "Delete"}
        </button>
      </span>
    </li>
  );
}

// FieldForm edits one field's label, type and constraints. It is used for both
// add (no initial) and edit (pre-filled). onSubmit resolves true on success.
function FieldForm({
  title,
  initial,
  onSubmit,
  onCancel,
}: {
  title: string;
  initial?: SpecField;
  onSubmit: (input: FieldInput) => Promise<boolean>;
  onCancel?: () => void;
}) {
  const startType: FieldType =
    initial && EDITABLE_FIELD_TYPES.includes(initial.type) ? initial.type : "string";
  const [label, setLabel] = useState(initial?.label ?? "");
  const [type, setType] = useState<FieldType>(startType);
  const [required, setRequired] = useState(initial?.required ?? false);
  const [unique, setUnique] = useState(initial?.unique ?? false);
  const [def, setDef] = useState(initial?.default ?? "");
  const [enumCsv, setEnumCsv] = useState((initial?.enum_values ?? []).map((e) => e.value).join(", "));
  const [pending, setPending] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (pending) return;
    setPending(true);
    const input: FieldInput = {
      label,
      type,
      required,
      unique,
      default: def.trim() === "" ? null : def,
      enum_values:
        type === "enum"
          ? enumCsv
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean)
              .map((value) => ({ value }))
          : undefined,
    };
    await onSubmit(input);
    setPending(false);
  }

  return (
    <form className="field-form" aria-label={title} onSubmit={submit}>
      <input aria-label="Field label" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Field label" disabled={pending} required />
      <select aria-label="Field type" value={type} onChange={(e) => setType(e.target.value as FieldType)} disabled={pending}>
        {EDITABLE_FIELD_TYPES.map((t) => (
          <option key={t} value={t}>
            {t}
          </option>
        ))}
      </select>
      <label className="check">
        <input type="checkbox" checked={required} onChange={(e) => setRequired(e.target.checked)} disabled={pending} /> required
      </label>
      <label className="check">
        <input type="checkbox" checked={unique} onChange={(e) => setUnique(e.target.checked)} disabled={pending} /> unique
      </label>
      <input aria-label="Default value" value={def ?? ""} onChange={(e) => setDef(e.target.value)} placeholder="Default (optional)" disabled={pending} />
      {type === "enum" && (
        <input aria-label="Enum values" value={enumCsv} onChange={(e) => setEnumCsv(e.target.value)} placeholder="value1, value2, …" disabled={pending} />
      )}
      <button type="submit" disabled={pending}>
        {pending ? "Saving…" : title}
      </button>
      {onCancel && (
        <button type="button" onClick={onCancel} disabled={pending}>
          Cancel
        </button>
      )}
    </form>
  );
}
