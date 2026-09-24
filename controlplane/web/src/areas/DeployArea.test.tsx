import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DeployArea } from "./DeployArea";

// mockApi answers each control-plane path from a table so DeployArea drives the
// real client over a stubbed fetch.
function mockApi(routes: (url: string) => { status: number; body?: unknown }) {
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();
    const { status, body } = routes(url);
    return { ok: status >= 200 && status < 300, status, statusText: "", json: async () => body } as Response;
  }) as unknown as typeof fetch;
}

afterEach(() => vi.restoreAllMocks());

const ORG = { id: 7, name: "Acme", slug: "acme" };
const PROJECT = { id: 3, organization_id: 7, name: "Acme CRM", slug: "acme-crm" };

async function selectAcmeCRM() {
  const user = userEvent.setup();
  await screen.findByRole("option", { name: "Acme" });
  await user.selectOptions(screen.getByRole("combobox", { name: "Organization" }), "7");
  await user.selectOptions(await screen.findByRole("combobox", { name: "Project" }), "3");
}

describe("DeployArea", () => {
  it("lists build history and live-tails the selected build's logs (read from Cloud)", async () => {
    // A settled build, so polling stops after one tick — deterministic.
    const JOB = { id: 1, project_id: 3, status: "succeeded", cloud_status: "succeeded", artifact_digest: "sha256:cafe" };
    const LOGS = [{ timestamp: "2026-01-01T00:00:00Z", stream: "build", message: "cloning repo" }];
    mockApi((url) => {
      if (url.endsWith("/organizations")) return { status: 200, body: { data: [ORG] } };
      if (url.endsWith(`/organizations/${ORG.id}/projects`)) return { status: 200, body: { data: [PROJECT] } };
      if (url.endsWith(`/projects/${PROJECT.id}/build-jobs`)) return { status: 200, body: { data: [JOB] } };
      if (url.includes(`/build-jobs/${JOB.id}/logs`)) return { status: 200, body: { data: LOGS } };
      if (url.endsWith(`/build-jobs/${JOB.id}`)) return { status: 200, body: { data: JOB } };
      return { status: 404 };
    });

    render(<DeployArea />);
    await selectAcmeCRM();

    // The build history lists the job...
    expect(await screen.findByRole("list", { name: "Build history" })).toHaveTextContent("build #1");
    // ...and its logs are relayed from Cloud and rendered.
    expect(await screen.findByText(/cloning repo/)).toBeInTheDocument();
    expect(screen.getByText(/sha256:cafe/)).toBeInTheDocument();
  });

  it("shows an empty state for a project with no builds", async () => {
    mockApi((url) => {
      if (url.endsWith("/organizations")) return { status: 200, body: { data: [ORG] } };
      if (url.endsWith(`/organizations/${ORG.id}/projects`)) return { status: 200, body: { data: [PROJECT] } };
      if (url.endsWith(`/projects/${PROJECT.id}/build-jobs`)) return { status: 200, body: { data: [] } };
      return { status: 404 };
    });

    render(<DeployArea />);
    await selectAcmeCRM();

    expect(await screen.findByText(/no builds yet/i)).toBeInTheDocument();
  });
});
