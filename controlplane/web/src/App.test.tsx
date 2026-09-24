import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BrowserRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { SessionProvider } from "./auth/session";

// mockApi installs a fetch stub that answers each control-plane path from a
// small table, so the tests drive the real components (App, shell, login) over
// the real client and session layer without a server.
type Reply = { status: number; body?: unknown };
function mockApi(routes: (url: string, init?: RequestInit) => Reply) {
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    const { status, body } = routes(url, init);
    return {
      ok: status >= 200 && status < 300,
      status,
      statusText: "",
      json: async () => body,
    } as Response;
  }) as unknown as typeof fetch;
}

function renderApp() {
  return render(
    <BrowserRouter>
      <SessionProvider>
        <App />
      </SessionProvider>
    </BrowserRouter>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
  window.history.pushState({}, "", "/");
});

describe("App gate", () => {
  it("shows the sign-in view when there is no session", async () => {
    mockApi((url) => (url.endsWith("/me") ? { status: 401, body: { error: { code: "unauthorized" } } } : { status: 404 }));
    renderApp();
    expect(await screen.findByRole("form", { name: /sign in/i })).toBeInTheDocument();
  });

  it("shows the four-area shell when authenticated, defaulting to Data", async () => {
    mockApi((url) => {
      if (url.endsWith("/me")) return { status: 200, body: { data: { id: 1, email: "ada@example.test" } } };
      if (url.endsWith("/organizations")) return { status: 200, body: { data: [] } };
      return { status: 404 };
    });
    renderApp();

    // Every editor area appears in the nav (DESIGN.md §17).
    for (const label of ["Data", "Pages", "Access", "Deploy"]) {
      expect(await screen.findByRole("link", { name: label })).toBeInTheDocument();
    }
    // The default route lands on Data.
    expect(await screen.findByRole("heading", { name: "Data" })).toBeInTheDocument();
    expect(screen.getByText("ada@example.test")).toBeInTheDocument();
  });

  it("signs in with credentials and reveals the shell", async () => {
    let authed = false;
    mockApi((url, init) => {
      if (url.endsWith("/auth/login")) {
        const creds = JSON.parse(String(init?.body ?? "{}"));
        if (creds.email === "ada@example.test" && creds.password === "correct-horse") {
          authed = true;
          return { status: 200, body: { data: {} } };
        }
        return { status: 401, body: { error: { code: "unauthorized", message: "invalid credentials" } } };
      }
      if (url.endsWith("/me")) {
        return authed
          ? { status: 200, body: { data: { id: 1, email: "ada@example.test" } } }
          : { status: 401, body: { error: { code: "unauthorized" } } };
      }
      return { status: 404 };
    });

    renderApp();
    const user = userEvent.setup();
    await screen.findByRole("form", { name: /sign in/i });

    await user.type(screen.getByLabelText("Email"), "ada@example.test");
    await user.type(screen.getByLabelText("Password"), "correct-horse");
    await user.click(screen.getByRole("button", { name: /sign in/i }));

    // The session re-probes /me and the shell replaces the login form.
    expect(await screen.findByRole("link", { name: "Data" })).toBeInTheDocument();
  });

  it("surfaces a failed sign-in without leaving the login view", async () => {
    mockApi((url) => {
      if (url.endsWith("/auth/login")) {
        return { status: 401, body: { error: { code: "unauthorized", message: "invalid credentials" } } };
      }
      return { status: 401, body: { error: { code: "unauthorized" } } };
    });

    renderApp();
    const user = userEvent.setup();
    await screen.findByRole("form", { name: /sign in/i });

    await user.type(screen.getByLabelText("Email"), "ada@example.test");
    await user.type(screen.getByLabelText("Password"), "wrong");
    await user.click(screen.getByRole("button", { name: /sign in/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/invalid credentials/i);
    expect(screen.getByRole("form", { name: /sign in/i })).toBeInTheDocument();
  });
});
