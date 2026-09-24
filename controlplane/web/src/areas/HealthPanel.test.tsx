import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { HealthPanel } from "./HealthPanel";

function mockHealth(reply: { status: number; body?: unknown }) {
  globalThis.fetch = vi.fn(async () => ({
    ok: reply.status >= 200 && reply.status < 300,
    status: reply.status,
    statusText: "",
    json: async () => reply.body,
  })) as unknown as typeof fetch;
}

afterEach(() => vi.restoreAllMocks());

describe("HealthPanel", () => {
  it("shows the three states separately", async () => {
    mockHealth({
      status: 200,
      body: {
        data: {
          facets: [
            { name: "Spec", status: "ok", summary: "Valid" },
            { name: "Extension ABI", status: "ok", summary: "Compatible" },
            { name: "Runtime Build", status: "unknown", summary: "no toolchain available" },
          ],
        },
      },
    });
    render(<HealthPanel projectID={3} reloadKey={0} />);
    const panel = await screen.findByRole("complementary", { name: "Project health" });
    for (const name of ["Spec", "Extension ABI", "Runtime Build"]) {
      expect(panel).toHaveTextContent(name);
    }
    // The three stay distinct: build is unknown even though spec/ABI are ok.
    expect(panel).toHaveTextContent(/no toolchain available/i);
  });

  it("surfaces spec diagnostics keyed to the offending path", async () => {
    mockHealth({
      status: 200,
      body: {
        data: {
          facets: [
            { name: "Spec", status: "failed", summary: "Invalid (1 issue(s))" },
            { name: "Extension ABI", status: "unknown", summary: "Not evaluated" },
            { name: "Runtime Build", status: "unknown", summary: "no toolchain" },
          ],
          diagnostics: [{ path: "$.resources[0].fields[1].code_name", code: "duplicate_code_name", message: "already used", entity: "fld_1" }],
        },
      },
    });
    render(<HealthPanel projectID={3} reloadKey={0} />);
    const diags = await screen.findByRole("list", { name: "Spec diagnostics" });
    expect(diags).toHaveTextContent("$.resources[0].fields[1].code_name");
    expect(diags).toHaveTextContent("already used");
  });

  // A transient health-fetch failure must not latch: once a later load (a bumped
  // reloadKey after an edit) succeeds, the panel shows live health again, never
  // the stale error. Guards against the panel going permanently dark on one blip.
  it("recovers after a transient fetch error when a later load succeeds", async () => {
    const ok = {
      ok: true,
      status: 200,
      statusText: "",
      json: async () => ({
        data: {
          facets: [
            { name: "Spec", status: "ok", summary: "Valid" },
            { name: "Extension ABI", status: "ok", summary: "Compatible" },
            { name: "Runtime Build", status: "unknown", summary: "no toolchain" },
          ],
        },
      }),
    } as Response;
    let call = 0;
    globalThis.fetch = vi.fn(async () => {
      call += 1;
      if (call === 1) return { ok: false, status: 500, statusText: "", json: async () => ({}) } as Response;
      return ok;
    }) as unknown as typeof fetch;

    const { rerender } = render(<HealthPanel projectID={3} reloadKey={0} />);
    // The first fetch fails: the error branch renders, no facets.
    expect(await screen.findByRole("alert")).toBeInTheDocument();

    // A later edit bumps reloadKey; the second fetch succeeds.
    rerender(<HealthPanel projectID={3} reloadKey={1} />);
    const panel = await screen.findByRole("complementary", { name: "Project health" });
    expect(panel).toHaveTextContent("Spec");
    // The stale error is gone — the panel reflects live health again.
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
  });
});
