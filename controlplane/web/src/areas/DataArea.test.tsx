import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DataArea } from "./DataArea";

// mockApi answers each control-plane path from a table so DataArea drives the
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
const SPEC = {
  resources: [
    { id: "res_1", label: "Customer", label_plural: "Customers", code_name: "Customer", storage_name: "customers" },
  ],
};

function wire(specBody: unknown = { data: SPEC }) {
  mockApi((url) => {
    if (url.endsWith("/organizations")) return { status: 200, body: { data: [ORG] } };
    if (url.endsWith(`/organizations/${ORG.id}/projects`)) return { status: 200, body: { data: [PROJECT] } };
    if (url.endsWith(`/projects/${PROJECT.id}/health`)) return { status: 200, body: { data: { facets: [] } } };
    if (url.endsWith(`/projects/${PROJECT.id}/spec`)) return { status: 200, body: specBody };
    return { status: 404 };
  });
}

describe("DataArea", () => {
  it("prompts to select a project before anything is chosen", async () => {
    wire();
    render(<DataArea />);
    // The org picker is populated from the API.
    expect(await screen.findByRole("option", { name: "Acme" })).toBeInTheDocument();
    expect(screen.getByText(/select a project to edit its data model/i)).toBeInTheDocument();
  });

  it("loads and lists the selected project's resources", async () => {
    wire();
    render(<DataArea />);
    const user = userEvent.setup();

    await screen.findByRole("option", { name: "Acme" });
    await user.selectOptions(screen.getByRole("combobox", { name: "Organization" }), "7");
    await user.selectOptions(await screen.findByRole("combobox", { name: "Project" }), "3");

    // The head spec's resources render, with their human-facing labels.
    const list = await screen.findByRole("list", { name: "Resources" });
    expect(list).toHaveTextContent("Customer");
    expect(list).toHaveTextContent("Customers");
  });

  it("lets an empty project add its first resource (no read-only dead end)", async () => {
    wire({ data: null }); // no revisions yet
    render(<DataArea />);
    const user = userEvent.setup();
    await screen.findByRole("option", { name: "Acme" });
    await user.selectOptions(screen.getByRole("combobox", { name: "Organization" }), "7");
    await user.selectOptions(await screen.findByRole("combobox", { name: "Project" }), "3");
    // The tree renders with the add form even before any revision exists.
    expect(await screen.findByText(/no resources yet/i)).toBeInTheDocument();
    expect(screen.getByRole("form", { name: "Add resource" })).toBeInTheDocument();
  });

  // A stale response must not win: select project 3, then 4; resolve 4's spec
  // first and 3's later. The UI must show 4's data, never 3's late arrival.
  it("ignores a stale spec response when the selection has moved on", async () => {
    const deferred = <T,>() => {
      let resolve!: (v: T) => void;
      const promise = new Promise<T>((r) => (resolve = r));
      return { promise, resolve };
    };
    const spec3 = deferred<{ status: number; body: unknown }>();
    const spec4 = deferred<{ status: number; body: unknown }>();

    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();
      let reply: { status: number; body: unknown };
      if (url.endsWith("/organizations")) reply = { status: 200, body: { data: [ORG] } };
      else if (url.endsWith(`/organizations/${ORG.id}/projects`))
        reply = { status: 200, body: { data: [PROJECT, { id: 4, organization_id: 7, name: "Blog", slug: "blog" }] } };
      else if (url.endsWith("/health")) reply = { status: 200, body: { data: { facets: [] } } };
      else if (url.endsWith("/projects/3/spec")) reply = await spec3.promise;
      else if (url.endsWith("/projects/4/spec")) reply = await spec4.promise;
      else reply = { status: 404, body: {} };
      return { ok: reply.status >= 200 && reply.status < 300, status: reply.status, statusText: "", json: async () => reply.body } as Response;
    }) as unknown as typeof fetch;

    render(<DataArea />);
    const user = userEvent.setup();
    await screen.findByRole("option", { name: "Acme" });
    await user.selectOptions(screen.getByRole("combobox", { name: "Organization" }), "7");
    await user.selectOptions(await screen.findByRole("combobox", { name: "Project" }), "3");
    await user.selectOptions(screen.getByRole("combobox", { name: "Project" }), "4");

    // Resolve the later selection (4) first, then the stale earlier one (3).
    spec4.resolve({ status: 200, body: { data: { resources: [{ id: "res_p", label: "Post", label_plural: "Posts", code_name: "Post", storage_name: "posts" }] } } });
    expect(await screen.findByText("Post")).toBeInTheDocument();
    spec3.resolve({ status: 200, body: { data: SPEC } });

    // The stale project-3 spec (Customer) must never replace project 4's.
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByText("Customer")).not.toBeInTheDocument();
    expect(screen.getByText("Post")).toBeInTheDocument();
  });
});
