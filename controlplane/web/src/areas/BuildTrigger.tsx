import { useCallback, useEffect, useRef, useState } from "react";
import { describeError } from "../api/client";
import { triggerDeploy, type BuildJob } from "../api/deploy";
import { getProject } from "../api/projects";

// How often auto-rebuild checks the project's head revision, and how long it
// waits for edits to settle before rebuilding. The debounce is what keeps a burst
// of saved edits from each kicking a build — the rebuild fires once, after the
// head revision has been stable for DEBOUNCE_MS (DESIGN.md §16: not per keystroke).
const POLL_MS = 4000;
const DEBOUNCE_MS = 4000;

// BuildTrigger starts Cloud builds for a project's current revision (#71): a
// manual "Build current revision" action, and an opt-in "auto-rebuild on changes"
// that watches the head revision and rebuilds — debounced — whenever the editor
// commits a meaningful change. Builds are asynchronous (D8): this enqueues one and
// hands the new job up; the Deploy area tracks it. Auto-rebuild only ever triggers
// the same enqueue a person could, so it adds no new authority.
//
// The debounce is a true quiet-period one, with no max-wait: while the head keeps
// advancing (steady commits), the rebuild is intentionally postponed until edits
// settle, so it never builds mid-burst. A project under continuous change thus
// defers its auto-rebuild indefinitely — the correct behavior for a preview
// convenience, not a bug.
export function BuildTrigger({
  projectID,
  onBuildStarted,
}: {
  projectID: number;
  onBuildStarted: (job: BuildJob) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [autoRebuild, setAutoRebuild] = useState(false);
  const [autoStatus, setAutoStatus] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  // onBuildStarted is captured by the auto-rebuild effect; keep a ref so the
  // effect doesn't re-subscribe (and reset its baseline) on every parent render.
  const onStarted = useRef(onBuildStarted);
  onStarted.current = onBuildStarted;

  const runBuild = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const job = await triggerDeploy(projectID);
      onStarted.current(job);
      return job.id;
    } catch (e) {
      setError(describeError(e));
      return null;
    } finally {
      setBusy(false);
    }
  }, [projectID]);

  // Auto-rebuild: poll the head revision; when it advances (a committed edit),
  // debounce, then rebuild. Baseline is the head at enable-time, so turning it on
  // doesn't rebuild what's already current — only genuine subsequent changes.
  useEffect(() => {
    if (!autoRebuild) {
      setAutoStatus(null);
      return;
    }
    let active = true;
    let pollTimer: ReturnType<typeof setTimeout> | undefined;
    let debounceTimer: ReturnType<typeof setTimeout> | undefined;
    let initialized = false;
    let lastSeen: number | null = null;
    let lastBuilt: number | null = null;

    const scheduleBuild = (head: number) => {
      if (debounceTimer) clearTimeout(debounceTimer);
      setAutoStatus(`Change detected (r${head}) — rebuilding when it settles…`);
      debounceTimer = setTimeout(() => {
        if (!active || head === lastBuilt) return;
        lastBuilt = head;
        setAutoStatus(`Rebuilding r${head}…`);
        void runBuild().then(() => active && setAutoStatus(`Watching for changes (last built r${head})…`));
      }, DEBOUNCE_MS);
    };

    const poll = () => {
      getProject(projectID)
        .then((p) => {
          if (!active) return;
          setError(null); // a successful poll clears a prior transient error
          const head = p.head_revision_id ?? null;
          if (!initialized) {
            initialized = true;
            lastSeen = head;
            lastBuilt = head; // don't rebuild the revision that's already current
            setAutoStatus("Watching for changes…");
          } else if (head != null && head !== lastSeen) {
            lastSeen = head;
            scheduleBuild(head);
          }
          pollTimer = setTimeout(poll, POLL_MS);
        })
        .catch((e) => {
          if (!active) return;
          setError(describeError(e));
          pollTimer = setTimeout(poll, POLL_MS * 2);
        });
    };
    poll();
    return () => {
      active = false;
      if (pollTimer) clearTimeout(pollTimer);
      if (debounceTimer) clearTimeout(debounceTimer);
    };
  }, [projectID, autoRebuild, runBuild]);

  return (
    <div className="build-trigger">
      <button type="button" onClick={() => void runBuild()} disabled={busy}>
        Build current revision
      </button>
      <label className="auto-rebuild">
        <input type="checkbox" checked={autoRebuild} onChange={(e) => setAutoRebuild(e.target.checked)} />
        Auto-rebuild on changes
      </label>
      {autoRebuild && autoStatus && (
        <span className="muted auto-status" aria-live="polite">
          {autoStatus}
        </span>
      )}
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </div>
  );
}
