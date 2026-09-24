import { api } from "./client";

// Cloud build/deploy resources the Deploy area reads. These mirror the Go wire
// types (contract.Data envelopes); only the fields the UI uses are typed. The
// control plane relays build logs from Gombit Cloud — Forge stores neither the
// build nor its logs (ADR-005 §24, D2).

// A Cloud build job: Forge's tracking record for a build it submitted to Cloud.
export interface BuildJob {
  id: number;
  project_id: number;
  // status is Forge's local job lifecycle: queued | running | succeeded | failed.
  status: string;
  // cloud_status is the last state observed from Cloud's own build state machine
  // (queued | preparing | building | … | succeeded | failed), once known.
  cloud_status?: string;
  cloud_build_id?: string;
  artifact_digest?: string;
  error?: string;
}

// One build log line relayed from Cloud.
export interface BuildLog {
  timestamp: string;
  stream?: string;
  message: string;
}

// A build job's status is terminal when its Forge lifecycle has settled — no
// more log lines or status changes will arrive, so polling can stop.
export function isBuildTerminal(status: string): boolean {
  return status === "succeeded" || status === "failed";
}

// triggerDeploy freezes the project's head revision and enqueues a Cloud build
// (async — no request performs a build, D8). Returns the new build job to track.
export const triggerDeploy = (projectID: number) => api.post<BuildJob>(`/projects/${projectID}/deploy`);

export const listBuildJobs = (projectID: number) => api.get<BuildJob[]>(`/projects/${projectID}/build-jobs`);

export const getBuildJob = (jobID: number) => api.get<BuildJob>(`/build-jobs/${jobID}`);

// getBuildJobLogs reads the job's Cloud build logs, optionally only those after
// `since` (an RFC3339 timestamp) for tailing.
export const getBuildJobLogs = (jobID: number, since?: string) =>
  api.get<BuildLog[]>(`/build-jobs/${jobID}/logs${since ? `?since=${encodeURIComponent(since)}` : ""}`);

// --- Deploy action (#105) ---
//
// The deploy surface is a pass-through to Gombit Cloud: Cloud owns the
// environment set, the deployment lifecycle, health, rollback and the
// destructive-migration gate (ADR-005 D2/D6). Forge submits an already-built
// artifact to a chosen environment and renders whatever state Cloud reports; it
// holds no deployment state of its own.

// An environment on the project's linked Cloud project — a deploy target the
// operator picks explicitly (the target is shown before the action, never a
// generic "deploy to prod" button).
export interface Environment {
  id: string;
  name: string;
  kind?: string;
  // Set only for an ephemeral preview environment (§46/§89): its reclamation
  // state and when it lapses, so the UI can mark a preview as throwaway.
  state?: string;
  expires_at?: string;
}

// isPreviewEnvironment reports an ephemeral preview target (as opposed to a
// persistent environment). Cloud names it "ephemeral"; a set expiry is the
// fallback signal for a Cloud that labels the kind differently.
export function isPreviewEnvironment(env: Environment): boolean {
  return env.kind === "ephemeral" || env.kind === "preview" || Boolean(env.expires_at);
}

// One application/runtime log line for a deployment, read from Cloud (§40).
// `stream` is Cloud's channel (stdout/stderr) — surfaced as the line's level.
// `id` is Cloud's stable per-line id, used to dedup the tail's inclusive-since
// boundary line. `instance_id` is carried from the wire but not shown in the
// deploy-tab tail (kept for a fuller viewer later).
export interface DeploymentLog {
  id: string;
  timestamp: string;
  stream?: string;
  message: string;
  instance_id?: string;
  request_id?: string;
}

// Cloud's structured hold on a deployment that is waiting on a human to approve a
// destructive migration (§32; L9/L10). It is a legitimate lifecycle state, not an
// error: the deployment exists and resumes once the exact migration is approved
// in Cloud. Forge surfaces it and links to Cloud — it never approves.
export interface DeploymentBlock {
  code: string;
  migration_id: string;
  approval_url?: string;
}

// A Cloud deployment, pass-through. `block` is present only while `status` is
// "blocked_pending_approval".
export interface Deployment {
  id: string;
  environment_id: string;
  build_id: string;
  status: string;
  artifact_digest?: string;
  restores_deployment_id?: string;
  rolled_back_from_id?: string;
  block?: DeploymentBlock;
}

// A deployment's status is terminal when Cloud's §22 lifecycle has settled — no
// further transitions arrive, so status polling can stop. This mirrors Cloud's
// DeploymentStatus.IsTerminal(). Note `blocked_pending_approval` is deliberately
// NOT terminal: polling continues through the hold so the same deployment is seen
// to resume once a human approves the migration in Cloud.
export function isDeploymentTerminal(status: string): boolean {
  switch (status) {
    case "healthy":
    case "migration_blocked":
    case "migration_failed":
    case "startup_failed":
    case "health_failed":
    case "promotion_failed":
    case "cancelled":
      return true;
    default:
      return false;
  }
}

// isDeploymentBlocked reports the destructive-migration hold — a deployment that
// exists but is waiting on a human to approve its migration in Cloud.
export function isDeploymentBlocked(d: Deployment): boolean {
  return d.status === "blocked_pending_approval";
}

export const listEnvironments = (projectID: number) =>
  api.get<Environment[]>(`/projects/${projectID}/environments`);

// createPreviewEnvironment asks Cloud to create an ephemeral preview environment
// for the project's current revision. Name/TTL are optional (Cloud/Forge default
// them). Deploying a build into the returned environment refreshes the preview;
// Cloud promotes the new healthy revision atomically.
export const createPreviewEnvironment = (projectID: number, opts?: { name?: string; ttl_seconds?: number }) =>
  api.post<Environment>(`/projects/${projectID}/preview-environments`, opts ?? {});

// deployBuild deploys a Cloud build (by cloud build id) to one of the project's
// environments. Cloud runs the migration preflight and may return the created
// deployment held in blocked_pending_approval with a block.
export const deployBuild = (projectID: number, envID: string, buildID: string) =>
  api.post<Deployment>(`/projects/${projectID}/environments/${envID}/deployments`, { build_id: buildID });

export const getDeployment = (projectID: number, envID: string, deploymentID: string) =>
  api.get<Deployment>(`/projects/${projectID}/environments/${envID}/deployments/${deploymentID}`);

// getDeploymentLogs reads a deployment's application logs, optionally only those
// after `since` (RFC3339) for tailing.
export const getDeploymentLogs = (projectID: number, envID: string, deploymentID: string, since?: string) =>
  api.get<DeploymentLog[]>(
    `/projects/${projectID}/environments/${envID}/deployments/${deploymentID}/logs${since ? `?since=${encodeURIComponent(since)}` : ""}`,
  );

// rollbackEnvironment rolls an environment back to its previous healthy revision;
// Cloud creates a new forward deployment restoring the earlier build (§92).
export const rollbackEnvironment = (projectID: number, envID: string) =>
  api.post<Deployment>(`/projects/${projectID}/environments/${envID}/rollback`, undefined);
