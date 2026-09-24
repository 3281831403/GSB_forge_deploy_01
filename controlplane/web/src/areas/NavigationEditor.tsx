import { useState } from "react";
import type { FormEvent } from "react";
import { describeError } from "../api/client";
import { setNavigation, type NavItemInput, type SpecNavItem, type SpecPage } from "../api/projects";

// A draft entry mirrors NavItemInput; the editor edits a local ordered list and
// saves the whole list at once (SetNavigation replaces, not merges).
type Draft = NavItemInput;

// Only dashboards and resource tables are navigable destinations (DESIGN.md §4.5);
// detail/form pages route on an :id.
const navigablePages = (pages: SpecPage[]) => pages.filter((p) => p.type === "dashboard" || p.type === "resource_table");

// NavigationEditor edits a project's ordered navigation (DESIGN.md §4.5): each
// entry points at a dashboard/resource-list page or an external URL. It edits a
// local draft list (reorder / add / remove) and replaces the whole navigation on
// save, then calls onChanged so the parent reloads.
export function NavigationEditor({
  projectID,
  navigation,
  pages,
  onChanged,
}: {
  projectID: number;
  navigation: SpecNavItem[];
  pages: SpecPage[];
  onChanged: () => void;
}) {
  const targets = navigablePages(pages);
  const [items, setItems] = useState<Draft[]>(() =>
    navigation.map((n) => ({ label: n.label, target: n.target, page: n.page, url: n.url })),
  );
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const update = (i: number, patch: Partial<Draft>) =>
    setItems((prev) => prev.map((it, j) => (j === i ? { ...it, ...patch } : it)));
  const remove = (i: number) => setItems((prev) => prev.filter((_, j) => j !== i));
  const move = (i: number, delta: number) =>
    setItems((prev) => {
      const j = i + delta;
      if (j < 0 || j >= prev.length) return prev;
      const next = [...prev];
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });
  const add = () =>
    setItems((prev) => [
      ...prev,
      targets.length > 0
        ? { label: "", target: "page", page: targets[0].id }
        : { label: "", target: "external", url: "" },
    ]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (pending) return;
    setPending(true);
    setError(null);
    try {
      // Send only the field each target uses, so a page entry never carries a
      // stray url (which the backend would reject).
      const payload: NavItemInput[] = items.map((it) =>
        it.target === "external"
          ? { label: it.label, target: "external", url: it.url ?? "" }
          : { label: it.label, target: "page", page: it.page ?? "" },
      );
      await setNavigation(projectID, payload);
      onChanged();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setPending(false);
    }
  }

  return (
    <form className="nav-editor" aria-label="Navigation" onSubmit={onSubmit}>
      <h3>Navigation</h3>
      {items.length === 0 ? (
        <p className="muted">No navigation yet. Add an entry below.</p>
      ) : (
        <ol className="nav-list">
          {items.map((it, i) => (
            <li key={i} className="nav-entry">
              <input
                aria-label={`Entry ${i + 1} label`}
                value={it.label}
                onChange={(e) => update(i, { label: e.target.value })}
                disabled={pending}
                placeholder="Label"
                required
              />
              <select
                aria-label={`Entry ${i + 1} target`}
                value={it.target}
                onChange={(e) =>
                  update(i, {
                    target: e.target.value as NavItemInput["target"],
                    // Reset the target-specific field to a valid default.
                    page: e.target.value === "page" ? (targets[0]?.id ?? "") : undefined,
                    url: e.target.value === "external" ? (it.url ?? "") : undefined,
                  })
                }
                disabled={pending}
              >
                <option value="page">Page</option>
                <option value="external">External URL</option>
              </select>
              {it.target === "page" ? (
                <select
                  aria-label={`Entry ${i + 1} page`}
                  value={it.page ?? ""}
                  onChange={(e) => update(i, { page: e.target.value })}
                  disabled={pending}
                  required
                >
                  <option value="">Select a page…</option>
                  {targets.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.label}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  aria-label={`Entry ${i + 1} url`}
                  type="url"
                  value={it.url ?? ""}
                  onChange={(e) => update(i, { url: e.target.value })}
                  disabled={pending}
                  placeholder="https://…"
                  required
                />
              )}
              <span className="nav-entry-actions">
                <button type="button" aria-label={`Move entry ${i + 1} up`} onClick={() => move(i, -1)} disabled={pending || i === 0}>
                  ↑
                </button>
                <button
                  type="button"
                  aria-label={`Move entry ${i + 1} down`}
                  onClick={() => move(i, 1)}
                  disabled={pending || i === items.length - 1}
                >
                  ↓
                </button>
                <button type="button" aria-label={`Remove entry ${i + 1}`} onClick={() => remove(i)} disabled={pending}>
                  Remove
                </button>
              </span>
            </li>
          ))}
        </ol>
      )}

      <div className="nav-editor-actions">
        <button type="button" onClick={add} disabled={pending}>
          Add entry
        </button>
        <button type="submit" disabled={pending}>
          {pending ? "Saving…" : "Save navigation"}
        </button>
      </div>

      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </form>
  );
}
