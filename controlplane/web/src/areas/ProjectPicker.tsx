import { useEffect, useState } from "react";
import { describeError } from "../api/client";
import { listOrganizations, listProjects, type Organization, type Project } from "../api/projects";

// ProjectPicker owns the organization → project selection shared by every editor
// area (Data, Pages, …). It reports the selected project id (or null) through
// onSelect, so each area loads its own view of that project without duplicating
// the selection wiring. Selection is loaded under an `active` flag so a slow
// list response for a selection the user has already changed cannot commit.
export function ProjectPicker({ onSelect }: { onSelect: (projectID: number | null) => void }) {
  const [orgs, setOrgs] = useState<Organization[]>([]);
  const [orgID, setOrgID] = useState<number | null>(null);
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectID, setProjectID] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);

  const fail = (err: unknown) => setError(describeError(err));

  useEffect(() => {
    let active = true;
    listOrganizations()
      .then((o) => active && setOrgs(o))
      .catch((e) => active && fail(e));
    return () => {
      active = false;
    };
  }, []);

  // Changing the organization resets the downstream project selection and clears
  // any prior error, then loads the org's projects.
  useEffect(() => {
    setProjectID(null);
    setProjects([]);
    setError(null);
    if (orgID == null) return;
    let active = true;
    listProjects(orgID)
      .then((p) => active && setProjects(p))
      .catch((e) => active && fail(e));
    return () => {
      active = false;
    };
  }, [orgID]);

  // Report the current selection upward. onSelect is a stable setState updater,
  // so this fires exactly when the selected project changes (including the reset
  // to null on an org switch).
  useEffect(() => {
    onSelect(projectID);
  }, [projectID, onSelect]);

  return (
    <>
      <div className="toolbar">
        <label>
          Organization
          <select
            aria-label="Organization"
            value={orgID ?? ""}
            onChange={(e) => setOrgID(e.target.value ? Number(e.target.value) : null)}
          >
            <option value="">Select an organization…</option>
            {orgs.map((o) => (
              <option key={o.id} value={o.id}>
                {o.name}
              </option>
            ))}
          </select>
        </label>

        <label>
          Project
          <select
            aria-label="Project"
            value={projectID ?? ""}
            disabled={orgID == null}
            onChange={(e) => setProjectID(e.target.value ? Number(e.target.value) : null)}
          >
            <option value="">Select a project…</option>
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
      </div>

      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </>
  );
}
