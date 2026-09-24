import { useEffect, useState } from "react";
import { describeError } from "../api/client";
import { getProjectHealth, type ProjectHealth } from "../api/projects";

// The three-state health panel (ADR-001 §36, §71): spec validity, ABI
// compatibility and runtime build shown as separate states, so it is clear why
// visual editing may continue while the runtime build cannot. It reloads with
// reloadKey after each edit, and surfaces any spec-validity diagnostics keyed to
// the offending entity.
export function HealthPanel({ projectID, reloadKey }: { projectID: number; reloadKey: number }) {
  const [health, setHealth] = useState<ProjectHealth | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    // Clear any prior error at the start of every load so a transient failure
    // cannot latch: a recovered fetch (after an edit bumps reloadKey, or a
    // project switch) must render live health again, not the stale error.
    setError(null);
    let active = true;
    getProjectHealth(projectID)
      .then((h) => active && setHealth(h))
      .catch((e) => active && setError(describeError(e)));
    return () => {
      active = false;
    };
  }, [projectID, reloadKey]);

  if (error) {
    return (
      <aside className="health-panel" aria-label="Project health">
        <p role="alert" className="error">
          {error}
        </p>
      </aside>
    );
  }
  if (!health) {
    return null;
  }

  return (
    <aside className="health-panel" aria-label="Project health">
      <div className="facets">
        {health.facets.map((f) => (
          <span key={f.name} className={`facet status-${f.status}`} title={f.summary}>
            <span className="indicator" aria-hidden="true">
              {indicator(f.status)}
            </span>
            <span className="facet-name">{f.name}</span>
            <span className="facet-summary muted">{f.summary}</span>
          </span>
        ))}
      </div>
      {health.diagnostics && health.diagnostics.length > 0 && (
        <ul className="diagnostics" aria-label="Spec diagnostics">
          {health.diagnostics.map((d, i) => (
            <li key={i}>
              <span className="diag-path">{d.path}</span>: {d.message}
            </li>
          ))}
        </ul>
      )}
    </aside>
  );
}

function indicator(status: string): string {
  switch (status) {
    case "ok":
      return "✓";
    case "failed":
      return "✗";
    default:
      return "–";
  }
}
