import { useState } from "react";
import type { FormEvent } from "react";
import { describeError } from "../api/client";
import {
  addResource,
  deleteResource,
  renameResource,
  type DeletionBlocker,
  type SpecResource,
} from "../api/projects";
import { BehaviorEditor } from "./BehaviorEditor";
import { FieldEditor } from "./FieldEditor";
import { RelationshipEditor } from "./RelationshipEditor";

// The editable resource tree (DESIGN.md §17 Data, §4.2). It edits resources
// through the backend operations — the backend mints code symbols and runs the
// F0 deletion dependency analysis; this only supplies human-readable labels.
// After any successful edit it calls onChanged so the parent reloads the spec.
export function ResourceTree({
  projectID,
  resources,
  onChanged,
}: {
  projectID: number;
  resources: SpecResource[];
  onChanged: () => void;
}) {
  const [error, setError] = useState<string | null>(null);

  // run reports success so a form can clear only when the edit actually landed.
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
    <div className="resource-editor">
      {resources.length === 0 ? (
        <p className="muted">No resources yet. Add the first one below.</p>
      ) : (
        <ul className="resource-tree" aria-label="Resources">
          {resources.map((r) => (
            <ResourceRow key={r.id} projectID={projectID} resource={r} resources={resources} onChanged={onChanged} setError={setError} />
          ))}
        </ul>
      )}

      <AddResourceForm
        onAdd={(label, plural) =>
          run(async () => {
            await addResource(projectID, label, plural);
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

function ResourceRow({
  projectID,
  resource,
  resources,
  onChanged,
  setError,
}: {
  projectID: number;
  resource: SpecResource;
  resources: SpecResource[];
  onChanged: () => void;
  setError: (m: string | null) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [pending, setPending] = useState(false);
  const [blockers, setBlockers] = useState<DeletionBlocker[] | null>(null);

  const fail = (err: unknown) => setError(describeError(err));

  async function save(label: string, plural: string) {
    if (pending) return;
    setPending(true);
    setError(null);
    try {
      await renameResource(projectID, resource.id, label, plural);
      setEditing(false);
      onChanged();
    } catch (err) {
      fail(err);
    } finally {
      setPending(false);
    }
  }

  async function confirmDelete() {
    if (pending) return; // guard a double "Confirm delete"
    setPending(true);
    setError(null);
    setBlockers(null);
    try {
      const result = await deleteResource(projectID, resource.id);
      if (result.committed) {
        onChanged();
      } else {
        // Blocked by dependencies (§45): show what still references the resource.
        setBlockers(result.blockers ?? []);
      }
    } catch (err) {
      fail(err);
    } finally {
      setPending(false);
      setConfirming(false);
    }
  }

  if (editing) {
    return (
      <li>
        <RenameForm
          resource={resource}
          pending={pending}
          onSave={save}
          onCancel={() => setEditing(false)}
        />
      </li>
    );
  }

  return (
    <li>
      <div className="resource-row">
        <button
          type="button"
          className="expand"
          aria-expanded={expanded}
          aria-label={`Fields of ${resource.label}`}
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? "▾" : "▸"}
        </button>
        <span className="resource-label">{resource.label}</span>
        {resource.label_plural && <span className="muted"> · {resource.label_plural}</span>}
        <span className="row-actions">
          <button type="button" onClick={() => setEditing(true)}>
            Rename
          </button>
          <button type="button" onClick={() => setConfirming(true)}>
            Delete
          </button>
        </span>
      </div>

      {expanded && (
        <>
          <FieldEditor
            projectID={projectID}
            resourceID={resource.id}
            fields={resource.fields ?? []}
            onChanged={onChanged}
          />
          <RelationshipEditor
            projectID={projectID}
            resourceID={resource.id}
            fields={resource.fields ?? []}
            resources={resources}
            onChanged={onChanged}
          />
          <BehaviorEditor
            // Re-key on the behavior so a reload that changed it (e.g. a
            // concurrent edit by another member) remounts the form and re-seeds
            // its state rather than showing stale initial values.
            key={JSON.stringify(resource.behavior ?? {})}
            projectID={projectID}
            resourceID={resource.id}
            fields={resource.fields ?? []}
            behavior={resource.behavior}
            onChanged={onChanged}
          />
        </>
      )}

      {confirming && (
        <div className="confirm" role="group" aria-label={`Delete ${resource.label}`}>
          <span>Delete “{resource.label}”? Any custom code is archived at build time.</span>
          <button type="button" onClick={confirmDelete} disabled={pending}>
            {pending ? "Deleting…" : "Confirm delete"}
          </button>
          <button type="button" onClick={() => setConfirming(false)} disabled={pending}>
            Cancel
          </button>
        </div>
      )}

      {blockers && (
        <div className="blockers" role="alert">
          <p>Can’t delete “{resource.label}” — it is still referenced:</p>
          <ul>
            {blockers.map((b, i) => (
              <li key={i}>{b.message}</li>
            ))}
          </ul>
        </div>
      )}
    </li>
  );
}

function RenameForm({
  resource,
  pending,
  onSave,
  onCancel,
}: {
  resource: SpecResource;
  pending: boolean;
  onSave: (label: string, plural: string) => void;
  onCancel: () => void;
}) {
  const [label, setLabel] = useState(resource.label);
  const [plural, setPlural] = useState(resource.label_plural ?? "");
  return (
    <form
      className="resource-form"
      aria-label={`Rename ${resource.label}`}
      onSubmit={(e: FormEvent) => {
        e.preventDefault();
        if (!pending) onSave(label, plural);
      }}
    >
      <input aria-label="Label" value={label} onChange={(e) => setLabel(e.target.value)} disabled={pending} required />
      <input aria-label="Plural label" value={plural} onChange={(e) => setPlural(e.target.value)} disabled={pending} placeholder="Plural" />
      <button type="submit" disabled={pending}>
        {pending ? "Saving…" : "Save"}
      </button>
      <button type="button" onClick={onCancel} disabled={pending}>
        Cancel
      </button>
    </form>
  );
}

// onAdd resolves true when the add landed, so the form clears only on success —
// a failed add keeps the typed label for the user to fix and retry. A pending
// flag disables the form so a double-click cannot create two resources.
function AddResourceForm({ onAdd }: { onAdd: (label: string, plural: string) => Promise<boolean> }) {
  const [label, setLabel] = useState("");
  const [plural, setPlural] = useState("");
  const [pending, setPending] = useState(false);
  return (
    <form
      className="resource-form add"
      aria-label="Add resource"
      onSubmit={async (e: FormEvent) => {
        e.preventDefault();
        if (pending) return;
        setPending(true);
        const ok = await onAdd(label, plural);
        setPending(false);
        if (ok) {
          setLabel("");
          setPlural("");
        }
      }}
    >
      <input aria-label="New resource label" value={label} onChange={(e) => setLabel(e.target.value)} disabled={pending} placeholder="Resource label" required />
      <input aria-label="New resource plural" value={plural} onChange={(e) => setPlural(e.target.value)} disabled={pending} placeholder="Plural (optional)" />
      <button type="submit" disabled={pending}>
        {pending ? "Adding…" : "Add resource"}
      </button>
    </form>
  );
}
