import { useState } from "react";
import type { FormEvent } from "react";
import { describeError } from "../api/client";
import { addRelationship, deleteField, type SpecField, type SpecResource } from "../api/projects";

// The per-resource relationship editor (DESIGN.md §4.2, ADR-001 §54). It creates
// belongs_to relationships to other resources; the has_many side is derived by
// the compiler from the inverse label. Deleting one drops the accessor, so it is
// breaking and comes back as a "requires validation" message the editor surfaces.
export function RelationshipEditor({
  projectID,
  resourceID,
  fields,
  resources,
  onChanged,
}: {
  projectID: number;
  resourceID: string;
  fields: SpecField[];
  resources: SpecResource[];
  onChanged: () => void;
}) {
  const [error, setError] = useState<string | null>(null);
  const [addKey, setAddKey] = useState(0);

  const relationships = fields.filter((f) => f.type === "belongs_to");
  const labelOf = (id?: string) => resources.find((r) => r.id === id)?.label ?? "(unknown)";

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
    <div className="relationship-editor">
      <h4>Relationships</h4>
      {relationships.length === 0 ? (
        <p className="muted">No relationships yet.</p>
      ) : (
        <ul className="relationship-list" aria-label="Relationships">
          {relationships.map((f) => (
            <RelationshipRow key={f.id} projectID={projectID} resourceID={resourceID} field={f} targetLabel={labelOf(f.target)} run={run} onChanged={onChanged} />
          ))}
        </ul>
      )}

      <RelationshipForm
        key={addKey}
        resources={resources}
        onSubmit={(input) =>
          run(async () => {
            await addRelationship(projectID, resourceID, input);
            onChanged();
          }).then((ok) => {
            if (ok) setAddKey((k) => k + 1);
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

function RelationshipRow({
  projectID,
  resourceID,
  field,
  targetLabel,
  run,
  onChanged,
}: {
  projectID: number;
  resourceID: string;
  field: SpecField;
  targetLabel: string;
  run: (op: () => Promise<void>) => Promise<boolean>;
  onChanged: () => void;
}) {
  const [pending, setPending] = useState(false);
  return (
    <li className="relationship-row">
      <span className="field-label">{field.label}</span>
      <span className="muted"> → {targetLabel}</span>
      {field.required && <span className="badge">required</span>}
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
    </li>
  );
}

function RelationshipForm({
  resources,
  onSubmit,
}: {
  resources: SpecResource[];
  onSubmit: (input: { label: string; target: string; inverse_label?: string; required?: boolean }) => Promise<boolean>;
}) {
  const [label, setLabel] = useState("");
  const [target, setTarget] = useState("");
  const [inverse, setInverse] = useState("");
  const [required, setRequired] = useState(false);
  const [pending, setPending] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (pending || target === "") return;
    setPending(true);
    await onSubmit({ label, target, inverse_label: inverse.trim() || undefined, required });
    setPending(false);
  }

  return (
    <form className="relationship-form" aria-label="Add relationship" onSubmit={submit}>
      <input aria-label="Relationship label" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Relationship label" disabled={pending} required />
      <select aria-label="Target resource" value={target} onChange={(e) => setTarget(e.target.value)} disabled={pending} required>
        <option value="">Target resource…</option>
        {resources.map((r) => (
          <option key={r.id} value={r.id}>
            {r.label}
          </option>
        ))}
      </select>
      <input aria-label="Inverse label" value={inverse} onChange={(e) => setInverse(e.target.value)} placeholder="has_many label (optional)" disabled={pending} />
      <label className="check">
        <input type="checkbox" checked={required} onChange={(e) => setRequired(e.target.checked)} disabled={pending} /> required
      </label>
      <button type="submit" disabled={pending}>
        {pending ? "Adding…" : "Add relationship"}
      </button>
    </form>
  );
}
