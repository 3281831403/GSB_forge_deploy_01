import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FormConfigEditor } from "./FormConfigEditor";
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

describe("FormConfigEditor", () => {
  it("seeds from the existing config", () => {
    const page: SpecPage = {
      id: "pag_1", slug: "edit-order", label: "Edit order", type: "resource_form", resource: "res_1",
      form: { layout: "two_column", fields: ["fld_2"] },
    };
    render(<FormConfigEditor projectID={3} page={page} resource={RESOURCE} onChanged={() => {}} />);
    expect(screen.getByRole("combobox", { name: "Layout" })).toHaveValue("two_column");
    expect(screen.getByRole("checkbox", { name: "Field Email" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Field Name" })).not.toBeChecked();
  });

  it("sends the label, layout and selected fields in resource field order", async () => {
    const page: SpecPage = {
      id: "pag_1", slug: "edit-order", label: "Edit order", type: "resource_form", resource: "res_1",
      form: { fields: ["fld_2"] }, // Email pre-selected
    };
    const calls = capturePatch();
    const onChanged = vi.fn();
    render(<FormConfigEditor projectID={3} page={page} resource={RESOURCE} onChanged={onChanged} />);
    const user = userEvent.setup();

    await user.selectOptions(screen.getByRole("combobox", { name: "Layout" }), "two_column");
    // Add Name (declared before Email), so ordered fields must be [Name, Email].
    await user.click(screen.getByRole("checkbox", { name: "Field Name" }));
    await user.click(screen.getByRole("button", { name: "Save form" }));

    const patch = calls.find((c) => c.method === "PATCH");
    expect(patch?.url).toContain("/projects/3/pages/pag_1/form");
    expect(patch?.body).toEqual({ label: "Edit order", layout: "two_column", fields: ["fld_1", "fld_2"] });
    expect(onChanged).toHaveBeenCalled();
  });
});
