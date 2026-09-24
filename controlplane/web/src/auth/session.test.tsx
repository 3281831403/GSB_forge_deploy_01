import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SessionProvider, useSession } from "./session";

function mockMe(reply: { status: number; body?: unknown }) {
  globalThis.fetch = vi.fn(async () => ({
    ok: reply.status >= 200 && reply.status < 300,
    status: reply.status,
    statusText: "",
    json: async () => reply.body,
  })) as unknown as typeof fetch;
}

// probe renders the session status and user, so a test can read the provider's
// resolved state from the DOM.
function Probe() {
  const { status, user } = useSession();
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="user">{user?.email ?? "none"}</span>
    </div>
  );
}

afterEach(() => vi.restoreAllMocks());

describe("SessionProvider", () => {
  it("resolves to authenticated when /me returns a user", async () => {
    mockMe({ status: 200, body: { data: { id: 5, email: "grace@example.test" } } });
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    expect(await screen.findByText("authenticated")).toBeInTheDocument();
    expect(screen.getByTestId("user")).toHaveTextContent("grace@example.test");
  });

  it("resolves to anonymous when /me is 401", async () => {
    mockMe({ status: 401, body: { error: { code: "unauthorized" } } });
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    expect(await screen.findByText("anonymous")).toBeInTheDocument();
    expect(screen.getByTestId("user")).toHaveTextContent("none");
  });

  it("falls back to anonymous when the probe network-fails", async () => {
    globalThis.fetch = vi.fn(async () => {
      throw new TypeError("network down");
    }) as unknown as typeof fetch;
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    expect(await screen.findByText("anonymous")).toBeInTheDocument();
  });

  it("stays authenticated when logout fails server-side", async () => {
    // /me authenticates; POST /auth/logout fails. The session must NOT drop to
    // anonymous, because the HttpOnly cookie is still valid and a reload would
    // sign the user right back in.
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.endsWith("/auth/logout")) {
        return { ok: false, status: 500, statusText: "", json: async () => ({ error: { code: "internal" } }) } as Response;
      }
      return { ok: true, status: 200, statusText: "", json: async () => ({ data: { id: 5, email: "grace@example.test" } }) } as Response;
    }) as unknown as typeof fetch;

    function LogoutProbe() {
      const { status, logout } = useSession();
      return (
        <div>
          <span data-testid="status">{status}</span>
          <button onClick={() => void logout().catch(() => {})}>sign out</button>
        </div>
      );
    }
    render(
      <SessionProvider>
        <LogoutProbe />
      </SessionProvider>,
    );
    expect(await screen.findByText("authenticated")).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "sign out" }));
    // Still authenticated: the failed logout left the real (cookie-backed) state.
    expect(screen.getByTestId("status")).toHaveTextContent("authenticated");
  });
});
