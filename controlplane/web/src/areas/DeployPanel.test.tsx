import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DeployPanel } from "./DeployPanel";

// mockApi answers control-plane paths from a handler, letting DeployPanel drive
// the real API client over a stubbed fetch. Each POST records its body so a test
// can assert what Forge forwarded to Cloud.
type Reply = { status: number; body?: unknown };
function mockApi(handler: (method: string, url: string, body: unknown) => Reply) {
  const calls: { method: string; url: string; body: unknown }[] = [];
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(init.body as string) : undefined;
    calls.push({ method, url, body });
    const { status, body: resBody } = handler(method, url, body);
    return { ok: status >= 200 && status < 300, status, statusText: "", json: async () => resBody } as Response;
  }) as unknown as typeof fetch;
  return calls;
}

afterEach(() => vi.restoreAllMocks());

const PROJECT = 3;
const BUILD = "bld_cloud_1";
const ENVS = [
  { id: "env_prod", name: "production", kind: "persistent" },
  { id: "env_stg", name: "staging", kind: "persistent" },
  { id: "env_pr12", name: "preview-pr-12", kind: "ephemeral", state: "active", expires_at: "2026-01-02T00:00:00Z" },
];

describe("DeployPanel", () => {
  it("lists Cloud environments and names the chosen deploy target on the button", async () => {
    mockApi((method, url) => {
      if (method === "GET" && url.endsWith(`/projects/${PROJECT}/environments`)) return { status: 200, body: { data: ENVS } };
      return { status: 404 };
    });
    const user = userEvent.setup();
    render(<DeployPanel projectID={PROJECT} cloudBuildID={BUILD} />);

    // Both environments are offered; the target must be picked explicitly.
    await screen.findByRole("option", { name: /production/ });
    expect(screen.getByRole("option", { name: /staging/ })).toBeInTheDocument();

    await user.selectOptions(screen.getByRole("combobox", { name: /target environment/i }), "env_prod");
    // The action names its target — no generic "deploy to prod" footgun.
    expect(screen.getByRole("button", { name: "Deploy build to production" })).toBeInTheDocument();

    // A preview environment is marked as throwaway, with its expiry, when chosen.
    await user.selectOptions(screen.getByRole("combobox", { name: /target environment/i }), "env_pr12");
    expect(screen.getByText(/preview environment — throwaway/i)).toHaveTextContent(/expires/i);
  });

  it("deploys the build to the chosen environment and forwards the build id to Cloud", async () => {
    const calls = mockApi((method, url) => {
      if (method === "GET" && url.endsWith(`/projects/${PROJECT}/environments`)) return { status: 200, body: { data: ENVS } };
      if (method === "POST" && url.endsWith(`/projects/${PROJECT}/environments/env_stg/deployments`)) {
        return { status: 202, body: { data: { id: "dep_1", environment_id: "env_stg", build_id: BUILD, status: "pending" } } };
      }
      if (method === "GET" && url.includes("/deployments/dep_1/logs")) return { status: 200, body: { data: [] } };
      return { status: 404 };
    });
    const user = userEvent.setup();
    render(<DeployPanel projectID={PROJECT} cloudBuildID={BUILD} />);

    await screen.findByRole("option", { name: /staging/ });
    await user.selectOptions(screen.getByRole("combobox", { name: /target environment/i }), "env_stg");
    await user.click(screen.getByRole("button", { name: "Deploy build to staging" }));

    // The status appears, and Cloud received the chosen env + build id.
    expect(await screen.findByLabelText("Deployment status")).toHaveTextContent(/Pending/);
    const deploy = calls.find((c) => c.method === "POST" && c.url.includes("/environments/env_stg/deployments"));
    expect(deploy?.body).toEqual({ build_id: BUILD });
  });

  it("surfaces the destructive-migration block with a link to approve in Cloud (never approves itself)", async () => {
    mockApi((method, url) => {
      if (method === "GET" && url.endsWith(`/projects/${PROJECT}/environments`)) return { status: 200, body: { data: ENVS } };
      if (method === "POST" && url.includes("/environments/env_prod/deployments")) {
        return {
          status: 202,
          body: {
            data: {
              id: "dep_2",
              environment_id: "env_prod",
              build_id: BUILD,
              status: "blocked_pending_approval",
              block: {
                code: "migration_approval_required",
                migration_id: "mig_7",
                approval_url: "https://console.example/databases/db_2/migrations",
              },
            },
          },
        };
      }
      return { status: 404 };
    });
    const user = userEvent.setup();
    render(<DeployPanel projectID={PROJECT} cloudBuildID={BUILD} />);

    await screen.findByRole("option", { name: /production/ });
    await user.selectOptions(screen.getByRole("combobox", { name: /target environment/i }), "env_prod");
    await user.click(screen.getByRole("button", { name: "Deploy build to production" }));

    const banner = await screen.findByRole("alert", { name: "Deployment blocked" });
    expect(banner).toHaveTextContent(/human approval required/i);
    // The deep link points at Cloud's console (Forge follows Cloud's routing; it
    // does not build console URLs), and the copy promises automatic resume.
    const link = within(banner).getByRole("link", { name: /review migration in gombit cloud/i });
    expect(link).toHaveAttribute("href", "https://console.example/databases/db_2/migrations");
    expect(banner).toHaveTextContent(/resumes automatically once the migration is approved/i);
  });

  it("rolls an environment back through Cloud", async () => {
    const calls = mockApi((method, url) => {
      if (method === "GET" && url.endsWith(`/projects/${PROJECT}/environments`)) return { status: 200, body: { data: ENVS } };
      if (method === "POST" && url.endsWith(`/projects/${PROJECT}/environments/env_prod/rollback`)) {
        return {
          status: 202,
          body: { data: { id: "dep_3", environment_id: "env_prod", build_id: "bld_prev", status: "pending", rolled_back_from_id: "dep_1" } },
        };
      }
      if (method === "GET" && url.includes("/deployments/dep_3/logs")) return { status: 200, body: { data: [] } };
      return { status: 404 };
    });
    const user = userEvent.setup();
    render(<DeployPanel projectID={PROJECT} cloudBuildID={BUILD} />);

    await screen.findByRole("option", { name: /production/ });
    await user.selectOptions(screen.getByRole("combobox", { name: /target environment/i }), "env_prod");
    await user.click(screen.getByRole("button", { name: "Roll back production" }));

    expect(await screen.findByText(/restoring an earlier revision/i)).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/environments/env_prod/rollback"))).toBe(true);
  });

  it("creates a preview environment and selects it as the deploy target", async () => {
    const calls = mockApi((method, url) => {
      if (method === "GET" && url.endsWith(`/projects/${PROJECT}/environments`)) return { status: 200, body: { data: [] } };
      if (method === "POST" && url.endsWith(`/projects/${PROJECT}/preview-environments`)) {
        return {
          status: 201,
          body: { data: { id: "env_pr42", name: "preview-r42", kind: "ephemeral", state: "active", expires_at: "2026-01-08T00:00:00Z" } },
        };
      }
      return { status: 404 };
    });
    const user = userEvent.setup();
    render(<DeployPanel projectID={PROJECT} cloudBuildID={BUILD} />);

    // A linked project with no environments still offers the preview action.
    await screen.findByRole("button", { name: /create preview environment/i });
    await user.click(screen.getByRole("button", { name: /create preview environment/i }));

    // The new preview becomes the selected target and can be deployed to.
    expect(await screen.findByRole("button", { name: "Deploy build to preview-r42" })).toBeInTheDocument();
    expect(screen.getByText(/preview environment — throwaway/i)).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/preview-environments"))).toBe(true);
  });

  it("shows an inapplicable state for a project not linked to Cloud (422)", async () => {
    mockApi((method, url) => {
      if (method === "GET" && url.endsWith(`/projects/${PROJECT}/environments`)) {
        return { status: 422, body: { error: { code: "invalid_request", message: "the project is not linked to a Gombit Cloud project" } } };
      }
      return { status: 404 };
    });
    render(<DeployPanel projectID={PROJECT} cloudBuildID={BUILD} />);

    expect(await screen.findByText(/isn't linked to a Gombit Cloud project/i)).toBeInTheDocument();
    // No environment picker, no deploy action.
    expect(screen.queryByRole("combobox", { name: /target environment/i })).not.toBeInTheDocument();
  });
});
