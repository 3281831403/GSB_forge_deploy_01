import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DesignPreview } from "./DesignPreview";

const ORG = { id: 7, name: "Acme", slug: "acme" };
const PROJECT = { id: 3, organization_id: 7, name: "Acme CRM", slug: "acme-crm" };

const SPEC = {
  resources: [
    {
      id: "res_c",
      label: "Customer",
      label_plural: "Customers",
      code_name: "Customer",
      storage_name: "customers",
      fields: [{ id: "fld_e", label: "Email", type: "string", code_name: "Email", storage_name: "email" }],
    },
    {
      id: "res_i",
      label: "Invoice",
      label_plural: "Invoices",
      code_name: "Invoice",
      storage_name: "invoices",
      fields: [{ id: "fld_fk", label: "Customer", type: "belongs_to", target: "res_c", code_name: "Customer", storage_name: "customer_id" }],
    },
  ],
  pages: [
    { id: "pag_t", slug: "customers", label: "Customers", type: "resource_table", resource: "res_c", table: { columns: ["fld_e"] } },
    { id: "pag_d", slug: "customer", label: "Customer", type: "resource_detail", resource: "res_c" },
    { id: "pag_h", slug: "home", label: "Home", type: "dashboard", dashboard: { count_cards: [{ label: "Customers", resource: "res_c" }] } },
  ],
  navigation: [
    { id: "n1", label: "Home", target: "page", page: "pag_h" },
    { id: "n2", label: "Docs", target: "external", url: "https://example.com" },
  ],
  branding: { app_name: "Acme Suite", accent_color: "#2563eb", appearance: "dark" },
};

function wire() {
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();
    let body: unknown = {};
    if (url.endsWith("/organizations")) body = { data: [ORG] };
    else if (url.endsWith(`/organizations/${ORG.id}/projects`)) body = { data: [PROJECT] };
    else if (url.endsWith(`/projects/${PROJECT.id}/spec`)) body = { data: SPEC };
    return { ok: true, status: 200, statusText: "", json: async () => body } as Response;
  }) as unknown as typeof fetch;
}

afterEach(() => vi.restoreAllMocks());

async function selectProject() {
  const user = userEvent.setup();
  await screen.findByRole("option", { name: "Acme" });
  await user.selectOptions(screen.getByRole("combobox", { name: "Organization" }), "7");
  await user.selectOptions(await screen.findByRole("combobox", { name: "Project" }), "3");
  return user;
}

describe("DesignPreview", () => {
  it("prompts to select a project first", async () => {
    wire();
    render(<DesignPreview />);
    expect(await screen.findByRole("option", { name: "Acme" })).toBeInTheDocument();
    expect(screen.getByText(/select a project to preview its design/i)).toBeInTheDocument();
  });

  it("labels the fidelity boundary (no runtime/hook execution claim)", async () => {
    wire();
    render(<DesignPreview />);
    await selectProject();
    const banner = await screen.findByRole("note", { name: "Design Preview fidelity" });
    expect(banner).toHaveTextContent(/structure only/i);
    expect(banner).toHaveTextContent(/hooks/i);
    expect(banner).toHaveTextContent(/does not validate runtime/i);
  });

  it("renders branding, navigation and page structure from the spec", async () => {
    wire();
    render(<DesignPreview />);
    await selectProject();
    const frame = await screen.findByLabelText("Design preview");

    // Branding: app name + appearance.
    expect(frame).toHaveTextContent("Acme Suite");
    expect(frame).toHaveTextContent(/dark appearance/i);

    // Navigation: ordered entries, external flagged.
    const nav = within(frame).getByRole("navigation", { name: "Preview navigation" });
    expect(nav).toHaveTextContent("Home");
    expect(nav).toHaveTextContent("Docs");
    expect(nav).toHaveTextContent("external");

    // Table page shows its configured column header.
    const table = within(frame).getByLabelText("Customers (resource_table)");
    expect(within(table).getByRole("columnheader", { name: "Email" })).toBeInTheDocument();

    // Detail page shows the has_many (Invoices reference Customer).
    const detail = within(frame).getByLabelText("Customer (resource_detail)");
    expect(detail).toHaveTextContent("Related");
    expect(detail).toHaveTextContent("Invoices");

    // Dashboard shows the count card label.
    const dash = within(frame).getByLabelText("Home (dashboard)");
    expect(dash).toHaveTextContent("Customers");
  });
});
