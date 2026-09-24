import { useEffect, useState } from "react";
import { describeError } from "../api/client";
import { getProjectSpec, type ProjectSpec } from "../api/projects";
import { BrandingEditor } from "./BrandingEditor";
import { NavigationEditor } from "./NavigationEditor";
import { PageList } from "./PageList";
import { ProjectPicker } from "./ProjectPicker";

// The Pages area (DESIGN.md §18, M3): a schema-driven page list. It reuses the
// shared project picker, loads the selected project's spec and lets the user
// create and delete structured pages (table / form / detail / dashboard). The
// structured per-page property editors land in the following M3 issues; this is
// the page model + list/create/delete.
export function PagesArea() {
  const [projectID, setProjectID] = useState<number | null>(null);
  const [spec, setSpec] = useState<ProjectSpec | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setSpec(null);
  }, [projectID]);

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
    <section aria-labelledby="area-pages">
      <h2 id="area-pages">Pages</h2>

      <ProjectPicker onSelect={setProjectID} />

      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}

      {projectID == null ? (
        <p className="muted">Select a project to build its pages.</p>
      ) : (
        <>
          <PageList
            projectID={projectID}
            pages={spec?.pages ?? []}
            resources={spec?.resources ?? []}
            onChanged={() => setReloadKey((k) => k + 1)}
          />
          {/* Re-key on the persisted navigation so a reload that changed it re-seeds
              the draft rather than showing stale entries. */}
          <NavigationEditor
            key={JSON.stringify(spec?.navigation ?? [])}
            projectID={projectID}
            navigation={spec?.navigation ?? []}
            pages={spec?.pages ?? []}
            onChanged={() => setReloadKey((k) => k + 1)}
          />
          <BrandingEditor
            key={JSON.stringify(spec?.branding ?? {})}
            projectID={projectID}
            branding={spec?.branding ?? {}}
            onChanged={() => setReloadKey((k) => k + 1)}
          />
        </>
      )}
    </section>
  );
}
