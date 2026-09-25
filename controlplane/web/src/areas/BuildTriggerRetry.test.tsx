import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BuildTrigger } from "./BuildTrigger";

afterEach(() => vi.restoreAllMocks());

// Auto-rebuild enqueue semantics (#207): a revision is marked built only after
// its build is really enqueued, and a failed enqueue is retried rather than
// silently skipped forever.
describe("BuildTrigger auto-rebuild retry", () => {
  // Renders with auto-rebuild enabled and a controllable head revision and
  // deploy outcome. Returns the counters the assertions drive.
  function setup(opts: { failDeploys?: number; alwaysFail?: boolean }) {
    let head = 5;
    let deploys = 0;
    let failures = 0;
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      const method = init?.method ?? "GET";
      if (method === "GET" && url.endsWith("/projects/3")) {
        return { ok: true, status: 200, json: async () => ({ data: { id: 3, head_revision_id: head } }) } as Response;
      }
      if (method === "POST" && url.endsWith("/projects/3/deploy")) {
        deploys += 1;
        const fail = opts.alwaysFail || failures < (opts.failDeploys ?? 0);
        if (fail) {
          failures += 1;
          return {
            ok: false,
            status: 500,
            statusText: "Internal Server Error",
            json: async () => ({ error: { code: "internal", message: "boom" } }),
          } as Response;
        }
        return { ok: true, status: 202, json: async () => ({ data: { id: 100 + deploys, project_id: 3, status: "queued" } }) } as Response;
      }
      return { ok: false, status: 404, json: async () => ({}) } as Response;
    }) as unknown as typeof fetch;

    const started = vi.fn();
    render(<BuildTrigger projectID={3} onBuildStarted={started} />);
    fireEvent.click(screen.getByRole("checkbox", { name: /auto-rebuild on changes/i }));
    return {
      started,
      setHead: (h: number) => {
        head = h;
      },
      deploys: () => deploys,
    };
  }

  const advance = async (ms: number) => {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(ms);
    });
  };

  it("marks a revision built only after the enqueue succeeds, and retries a failed enqueue", async () => {
    vi.useFakeTimers();
    try {
      const t = setup({ failDeploys: 1 });

      // Baseline: head r5 is already current, nothing builds.
      await advance(0);
      expect(t.deploys()).toBe(0);

      // Head advances to r6; the change is detected at the next poll…
      t.setHead(6);
      await advance(4000); // POLL_MS
      expect(t.deploys()).toBe(0);

      // …and the debounced rebuild fires — but the enqueue FAILS. The revision
      // must not be marked built: no "last built r6" claim, and the failure is
      // surfaced with a retry promise instead.
      await advance(4000); // DEBOUNCE_MS — attempt 1 fails
      expect(t.deploys()).toBe(1);
      expect(t.started).not.toHaveBeenCalled();
      expect(screen.queryByText(/last built r6/i)).toBeNull();
      expect(screen.getByText(/rebuild of r6 failed/i)).toBeTruthy();

      // The next poll re-arms the same revision and the debounced retry really
      // happens — only then is r6 marked built.
      await advance(4000); // POLL_MS — re-detect r6
      await advance(4000); // DEBOUNCE_MS — attempt 2 succeeds
      expect(t.deploys()).toBe(2);
      expect(t.started).toHaveBeenCalledTimes(1);
      expect(screen.getByText(/last built r6/i)).toBeTruthy();

      // And a built revision is not rebuilt again.
      await advance(16000);
      expect(t.deploys()).toBe(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps retrying across consecutive failures and never claims the build", async () => {
    vi.useFakeTimers();
    try {
      const t = setup({ alwaysFail: true });

      await advance(0);
      t.setHead(7);
      await advance(4000); // detect
      await advance(4000); // attempt 1 fails
      expect(t.deploys()).toBe(1);

      await advance(4000); // re-detect
      await advance(4000); // attempt 2 fails
      expect(t.deploys()).toBe(2);

      await advance(4000); // re-detect
      await advance(4000); // attempt 3 fails
      expect(t.deploys()).toBe(3);

      // Continuous failure never produces a "last built" claim.
      expect(t.started).not.toHaveBeenCalled();
      expect(screen.queryByText(/last built r7/i)).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });
});
