import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TableConfigEditor } from "./TableConfigEditor";
import type { SpecPage, SpecResource } from "../api/projects";

const RESOURCE: SpecResource = {
  id: "res_1",
  label: "Order",
  code_name: "Order",
  storage_name: "orders",
  fields: [
    { id: "fld_1", label: "Name", type: "string", code_name: "Name", storage_name: "name" },
    { id: "fld_2", label: "Email", type: "string", code_name: "Email", storage_name: "email" },
  ],
};

function capturePatch() {
  const calls: { url: string; method: string; body: unknown }[] = [];
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({
      url: typeof input === "string" ? input : input.toString(),
      method: init?.method ?? "GET",
      body: init?.body ? JSON.parse(init.body as string) : undefined,
    });
    return { ok: true, status: 201, statusText: "", json: async () => ({ data: { id: 1, spec_hash: "h" } }) } as Response;
  }) as unknown as typeof fetch;
  return calls;
}

afterEach(() => vi.restoreAllMocks());

describe("TableConfigEditor", () => {
  it("seeds from the page's existing config", () => {
    const page: SpecPage = {
      id: "pag_1", slug: "orders", label: "Orders", type: "resource_table", resource: "res_1",
      table: { title: "Every order", columns: ["fld_2"], search: true, page_size: 25 },
    };
    render(<TableConfigEditor projectID={3} page={page} resource={RESOURCE} onChanged={() => {}} />);

    expect(screen.getByRole("textbox", { name: "Table title" })).toHaveValue("Every order");
    expect(screen.getByRole("checkbox", { name: "Column Email" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Column Name" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Search" })).toBeChecked();
    expect(screen.getByRole("spinbutton", { name: "Page size" })).toHaveValue(25);
  });

  it("sends the full configuration with columns in resource field order", async () => {
    const page: SpecPage = {
      id: "pag_1", slug: "orders", label: "Orders", type: "resource_table", resource: "res_1",
      table: { columns: ["fld_2"] }, // Email pre-selected
    };
    const calls = capturePatch();
    const onChanged = vi.fn();
    render(<TableConfigEditor projectID={3} page={page} resource={RESOURCE} onChanged={onChanged} />);
    const user = userEvent.setup();

    await user.type(screen.getByRole("textbox", { name: "Table title" }), "All orders");
    // Add Name (declared before Email), so the ordered columns must be [Name, Email].
    await user.click(screen.getByRole("checkbox", { name: "Column Name" }));
    await user.click(screen.getByRole("checkbox", { name: "Search" }));
    await user.click(screen.getByRole("button", { name: "Save table" }));

    const patch = calls.find((c) => c.method === "PATCH");
    expect(patch?.url).toContain("/projects/3/pages/pag_1/table");
    expect(patch?.body).toEqual({
      label: "Orders",
      title: "All orders",
      columns: ["fld_1", "fld_2"],
      search: true,
      page_size: 0,
    });
    expect(onChanged).toHaveBeenCalled();
  });

  it("omits an empty title so the config can fall back to defaults", async () => {
    const page: SpecPage = {
      id: "pag_1", slug: "orders", label: "Orders", type: "resource_table", resource: "res_1",
    };
    const calls = capturePatch();
    render(<TableConfigEditor projectID={3} page={page} resource={RESOURCE} onChanged={() => {}} />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Save table" }));
    const patch = calls.find((c) => c.method === "PATCH");
    expect(patch?.body).toMatchObject({ label: "Orders", columns: [], search: false, page_size: 0 });
    expect((patch?.body as { title?: string }).title).toBeUndefined();
  });
});
