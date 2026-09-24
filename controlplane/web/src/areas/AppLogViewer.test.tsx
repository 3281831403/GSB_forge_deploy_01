import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppLogViewer } from "./AppLogViewer";

function mockApi(routes: (url: string) => { status: number; body?: unknown }) {
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();
    const { status, body } = routes(url);
    return { ok: status >= 200 && status < 300, status, statusText: "", json: async () => body } as Response;
  }) as unknown as typeof fetch;
}

afterEach(() => vi.restoreAllMocks());

describe("AppLogViewer", () => {
  it("renders the four log fields read from Cloud (timestamp, level, message, request)", async () => {
    const LOGS = [
      { id: "log_1", timestamp: "2026-01-01T00:00:01Z", stream: "stdout", message: "listening on :8080", request_id: "req_9" },
      { id: "log_2", timestamp: "2026-01-01T00:00:02Z", stream: "stderr", message: "slow query", request_id: "req_10" },
    ];
    mockApi((url) => {
      if (url.includes("/projects/3/environments/env_prod/deployments/dep_1/logs")) return { status: 200, body: { data: LOGS } };
      return { status: 404 };
    });

    // stopped so it fetches once and doesn't schedule a poll — deterministic.
    render(<AppLogViewer projectID={3} envID="env_prod" deploymentID="dep_1" stopped={true} />);

    const table = await screen.findByRole("table");
    expect(table).toHaveTextContent("listening on :8080");
    expect(table).toHaveTextContent("stderr");
    expect(table).toHaveTextContent("req_10");
    // The four column headers are present.
    expect(screen.getByRole("columnheader", { name: "Level" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Request" })).toBeInTheDocument();
  });

  it("does not duplicate the inclusive-since boundary line across ticks", async () => {
    // Cloud filters ?since inclusively, so the last line seen comes back on the
    // next tick. Dedup on the stable id must keep it from appearing twice.
    vi.useFakeTimers();
    try {
      let call = 0;
      globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : input.toString();
        if (!url.includes("/deployments/dep_1/logs")) return { ok: false, status: 404, json: async () => ({}) } as Response;
        call += 1;
        const A = { id: "log_a", timestamp: "2026-01-01T00:00:01Z", stream: "stdout", message: "first line", request_id: "" };
        const B = { id: "log_b", timestamp: "2026-01-01T00:00:02Z", stream: "stdout", message: "second line", request_id: "" };
        // Tick 1: [A]. Tick 2: [A (boundary, inclusive since), B].
        const data = call === 1 ? [A] : [A, B];
        return { ok: true, status: 200, json: async () => ({ data }) } as Response;
      }) as unknown as typeof fetch;

      render(<AppLogViewer projectID={3} envID="env_prod" deploymentID="dep_1" stopped={false} />);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0); // first tick resolves, schedules the poll
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(3000); // second tick (3s poll) resolves
      });

      // "first line" appears exactly once despite being returned on both ticks.
      expect(screen.getAllByText("first line")).toHaveLength(1);
      expect(screen.getByText("second line")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows an empty state when the deployment has emitted no logs", async () => {
    mockApi((url) => {
      if (url.includes("/deployments/dep_1/logs")) return { status: 200, body: { data: [] } };
      return { status: 404 };
    });

    render(<AppLogViewer projectID={3} envID="env_prod" deploymentID="dep_1" stopped={true} />);

    expect(await screen.findByText(/no application logs yet/i)).toBeInTheDocument();
  });
});
