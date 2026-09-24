import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PagesArea } from "./PagesArea";

const ORG = { id: 7, name: "Acme", slug: "acme" };
const PROJECT = { id: 3, organization_id: 7, name: "Acme CRM", slug: "acme-crm" };
const CUSTOMER = { id: "res_1", label: "Customer", label_plural: "Customers", code_name: "Customer", storage_name: "customers" };

// mutableSpec lets a test change what /spec returns between reloads (an add/delete
// bumps reloadKey and refetches), so the flow reflects the committed change.
function wire(getSpec: () => { pages?: unknown[]; resources?: unknown[] }) {
  const calls: { method: string; url: string; body?: unknown }[] = [];
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    const method = init?.method ?? "GET";
    calls.push({ method, url, body: init?.body ? JSON.parse(init.body as string) : undefined });
    let status = 404;
    let body: unknown = {};
    if (url.endsWith("/organizations")) [status, body] = [200, { data: [ORG] }];
    else if (url.endsWith(`/organizations/${ORG.id}/projects`)) [status, body] = [200, { data: [PROJECT] }];
    else if (url.endsWith(`/projects/${PROJECT.id}/spec`)) [status, body] = [200, { data: getSpec() }];
    else if (url.match(/\/projects\/3\/pages$/) && method === "POST") [status, body] = [201, { data: { id: 1, spec_hash: "h" } }];
    else if (url.match(/\/projects\/3\/pages\//) && method === "DELETE") [status, body] = [200, { data: { id: 2, spec_hash: "h" } }];
    return { ok: status >= 200 && status < 300, status, statusText: "", json: async () => body } as Response;
  }) as unknown as typeof fetch;
  return calls;
}

afterEach(() => vi.restoreAllMocks());

async function selectProject() {
  const user = userEvent.setup();
  await screen.findByRole("option", { name: "Acme" });
  await user.selectOptions(screen.getByRole("combobox", { name: "Organization" }), "7");
  await user.selectOptions(await screen.findByRole("combobox", { name: "Project" }), "3");
  return user;
}

describe("PagesArea", () => {
  it("prompts to select a project first", async () => {
    wire(() => ({ resources: [CUSTOMER], pages: [] }));
    render(<PagesArea />);
    expect(await screen.findByRole("option", { name: "Acme" })).toBeInTheDocument();
    expect(screen.getByText(/select a project to build its pages/i)).toBeInTheDocument();
  });

  it("lists the project's pages", async () => {
    wire(() => ({
      resources: [CUSTOMER],
      pages: [{ id: "pag_1", slug: "customers", label: "Customers", type: "resource_table", resource: "res_1" }],
    }));
    render(<PagesArea />);
    await selectProject();
    const list = await screen.findByRole("list", { name: "Pages" });
    expect(list).toHaveTextContent("Customers");
    expect(list).toHaveTextContent("Resource table");
    // The bound resource is resolved to its label, not shown as a raw id.
    expect(list).toHaveTextContent("Customer");
    expect(list).not.toHaveTextContent("res_1");
  });

  it("creates a resource_table page bound to a resource", async () => {
    let pages: unknown[] = [];
    const calls = wire(() => ({ resources: [CUSTOMER], pages }));
    render(<PagesArea />);
    const user = await selectProject();
    // Empty state until a page exists.
    expect(await screen.findByText(/no pages yet/i)).toBeInTheDocument();

    const form = screen.getByRole("form", { name: "Add page" });
    await user.type(within(form).getByRole("textbox", { name: "New page label" }), "Customers");
    await user.selectOptions(within(form).getByRole("combobox", { name: "Page type" }), "resource_table");
    await user.selectOptions(within(form).getByRole("combobox", { name: "Bound resource" }), "res_1");
    // The next spec reload should reflect the created page.
    pages = [{ id: "pag_1", slug: "customers", label: "Customers", type: "resource_table", resource: "res_1" }];
    await user.click(within(form).getByRole("button", { name: "Add page" }));

    const post = calls.find((c) => c.method === "POST");
    expect(post).toBeTruthy();
    expect(post!.body).toEqual({ label: "Customers", type: "resource_table", resource: "res_1" });
    expect(await screen.findByRole("list", { name: "Pages" })).toHaveTextContent("Customers");
  });

  it("hides the resource select for a dashboard page and omits the binding", async () => {
    let pages: unknown[] = [];
    const calls = wire(() => ({ resources: [CUSTOMER], pages }));
    render(<PagesArea />);
    const user = await selectProject();
    await screen.findByText(/no pages yet/i);

    const form = screen.getByRole("form", { name: "Add page" });
    await user.type(within(form).getByRole("textbox", { name: "New page label" }), "Home");
    await user.selectOptions(within(form).getByRole("combobox", { name: "Page type" }), "dashboard");
    // A dashboard binds to no resource, so the resource select is not rendered.
    expect(within(form).queryByRole("combobox", { name: "Bound resource" })).not.toBeInTheDocument();
    pages = [{ id: "pag_d", slug: "home", label: "Home", type: "dashboard" }];
    await user.click(within(form).getByRole("button", { name: "Add page" }));

    const post = calls.find((c) => c.method === "POST");
    expect(post!.body).toEqual({ label: "Home", type: "dashboard" });
  });

  it("deletes a page after confirmation", async () => {
    let pages: unknown[] = [{ id: "pag_1", slug: "customers", label: "Customers", type: "resource_table", resource: "res_1" }];
    const calls = wire(() => ({ resources: [CUSTOMER], pages }));
    render(<PagesArea />);
    const user = await selectProject();
    await screen.findByRole("list", { name: "Pages" });

    await user.click(screen.getByRole("button", { name: "Delete" }));
    pages = [];
    await user.click(screen.getByRole("button", { name: "Confirm delete" }));

    expect(calls.some((c) => c.method === "DELETE" && c.url.includes("/pages/pag_1"))).toBe(true);
    expect(await screen.findByText(/no pages yet/i)).toBeInTheDocument();
  });
});
