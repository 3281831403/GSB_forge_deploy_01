import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BuildTrigger } from "./BuildTrigger";

afterEach(() => vi.restoreAllMocks());

describe("BuildTrigger", () => {
  it("builds the current revision on demand", async () => {
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      if ((init?.method ?? "GET") === "POST" && url.endsWith("/projects/3/deploy")) {
        return { ok: true, status: 202, json: async () => ({ data: { id: 7, project_id: 3, status: "queued" } }) } as Response;
      }
      return { ok: false, status: 404, json: async () => ({}) } as Response;
    }) as unknown as typeof fetch;

    const started = vi.fn();
    const user = userEvent.setup();
    render(<BuildTrigger projectID={3} onBuildStarted={started} />);

    await user.click(screen.getByRole("button", { name: /build current revision/i }));
    await waitFor(() => expect(started).toHaveBeenCalledWith(expect.objectContaining({ id: 7, status: "queued" })));
  });

  it("auto-rebuilds — debounced — when the head revision advances, and not before", async () => {
    vi.useFakeTimers();
    try {
      let head = 5;
      let deploys = 0;
      globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = typeof input === "string" ? input : input.toString();
        const method = init?.method ?? "GET";
        if (method === "GET" && url.endsWith("/projects/3")) {
          return { ok: true, status: 200, json: async () => ({ data: { id: 3, head_revision_id: head } }) } as Response;
        }
        if (method === "POST" && url.endsWith("/projects/3/deploy")) {
          deploys += 1;
          return { ok: true, status: 202, json: async () => ({ data: { id: 100 + deploys, project_id: 3, status: "queued" } }) } as Response;
        }
        return { ok: false, status: 404, json: async () => ({}) } as Response;
      }) as unknown as typeof fetch;

      const started = vi.fn();
      render(<BuildTrigger projectID={3} onBuildStarted={started} />);

      // Enable auto-rebuild; the first poll establishes the baseline (head r5) and
      // must NOT rebuild what's already current.
      fireEvent.click(screen.getByRole("checkbox", { name: /auto-rebuild on changes/i }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(deploys).toBe(0);

      // A committed edit advances the head; the next poll detects it but the build
      // is debounced — still nothing at the poll tick.
      head = 6;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(4000); // POLL_MS — detect the change
      });
      expect(deploys).toBe(0);

      // After the debounce window settles, exactly one rebuild fires for r6.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(4000); // DEBOUNCE_MS — settle → rebuild
      });
      expect(deploys).toBe(1);
      expect(started).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });
});
