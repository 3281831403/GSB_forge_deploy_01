import { useEffect, useState } from "react";
import { describeError } from "../api/client";
import { getProjectSpec, type ProjectSpec } from "../api/projects";
import { HealthPanel } from "./HealthPanel";
import { ProjectPicker } from "./ProjectPicker";
import { ResourceTree } from "./ResourceTree";

// The Data area (DESIGN.md §17): the organization → project picker drives the
// three-state health panel and the editable resource tree. Selection lives in
// the shared ProjectPicker; this area owns the loaded project's spec.
export function DataArea() {
  const [projectID, setProjectID] = useState<number | null>(null);
  const [spec, setSpec] = useState<ProjectSpec | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const [error, setError] = useState<string | null>(null);

  // Clear the spec on a project change (not on a reload) so switching projects
  // never flashes the previous project's resources.
  useEffect(() => {
    setSpec(null);
  }, [projectID]);

  // Load (and reload) the selected project's spec. Reloads bump reloadKey without
  // clearing, so an edit refreshes in place. The active flag ignores a stale
  // response when the selection has moved on — otherwise a fast A→B switch could
  // show A's data under B (a correctness hazard once the tree mutates the spec).
  useEffect(() => {
    setError(null);
    if (projectID == null) return;
    let active = true;
    getProjectSpec(projectID)
      .then((s) => active && setSpec(s))
      .catch((e) => active && setError(describeError(e)));
    return () => {
      active = false;
    };
  }, [projectID, reloadKey]);

  return (
    <section aria-labelledby="area-data">
      <h2 id="area-data">Data</h2>

      <ProjectPicker onSelect={setProjectID} />

      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}

      {projectID == null ? (
        <p className="muted">Select a project to edit its data model.</p>
      ) : (
        <>
          <HealthPanel projectID={projectID} reloadKey={reloadKey} />
          {/* An empty project (spec === null, no revisions) still renders the tree
              so the first AddResource can bootstrap it. */}
          <ResourceTree
            projectID={projectID}
            resources={spec?.resources ?? []}
            onChanged={() => setReloadKey((k) => k + 1)}
          />
        </>
      )}
    </section>
  );
}
