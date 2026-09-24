import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FieldEditor } from "./FieldEditor";
import type { SpecField } from "../api/projects";

type Reply = { status: number; body?: unknown };
function mockApi(routes: (url: string, init?: RequestInit) => Reply) {
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    const { status, body } = routes(url, init);
    return { ok: status >= 200 && status < 300, status, statusText: "", json: async () => body } as Response;
  });
  globalThis.fetch = fn as unknown as typeof fetch;
  return fn;
}

const TOTAL: SpecField = {
  id: "fld_1",
  label: "Total",
  type: "decimal",
  code_name: "Total",
  storage_name: "total",
  required: true,
};

afterEach(() => vi.restoreAllMocks());

const FIELDS_URL = "/projects/3/resources/res_1/fields";

describe("FieldEditor", () => {
  it("adds a field with its type and constraints", async () => {
    let posted: unknown;
    mockApi((url, init) => {
      if (url.endsWith(FIELDS_URL) && init?.method === "POST") {
        posted = JSON.parse(String(init.body));
        return { status: 201, body: { data: { id: 1, spec_hash: "x" } } };
      }
      return { status: 404 };
    });
    const onChanged = vi.fn();
    render(<FieldEditor projectID={3} resourceID="res_1" fields={[]} onChanged={onChanged} />);
    const user = userEvent.setup();

    const form = screen.getByRole("form", { name: "Add field" });
    await user.type(within(form).getByLabelText("Field label"), "Total");
    await user.selectOptions(within(form).getByLabelText("Field type"), "decimal");
    await user.click(within(form).getByLabelText("required"));
    await user.click(within(form).getByRole("button", { name: "Add field" }));

    expect(posted).toMatchObject({ label: "Total", type: "decimal", required: true, unique: false });
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it("parses enum values when the type is enum", async () => {
    let posted: { enum_values?: { value: string }[] } | undefined;
    mockApi((url, init) => {
      if (url.endsWith(FIELDS_URL) && init?.method === "POST") {
        posted = JSON.parse(String(init.body));
        return { status: 201, body: { data: { id: 1, spec_hash: "x" } } };
      }
      return { status: 404 };
    });
    render(<FieldEditor projectID={3} resourceID="res_1" fields={[]} onChanged={vi.fn()} />);
    const user = userEvent.setup();

    const form = screen.getByRole("form", { name: "Add field" });
    await user.type(within(form).getByLabelText("Field label"), "Status");
    await user.selectOptions(within(form).getByLabelText("Field type"), "enum");
    await user.type(within(form).getByLabelText("Enum values"), "open, closed, void");
    await user.click(within(form).getByRole("button", { name: "Add field" }));

    expect(posted?.enum_values).toEqual([{ value: "open" }, { value: "closed" }, { value: "void" }]);
  });

  it("surfaces the breaking message when a field delete is rejected", async () => {
    mockApi((url, init) => {
      if (url.endsWith(`${FIELDS_URL}/fld_1`) && init?.method === "DELETE") {
        return {
          status: 409,
          body: { error: { code: "conflict", message: "candidate is ABI-breaking and requires compatibility validation: resource ... accessor Total removed" } },
        };
      }
      return { status: 404 };
    });
    const onChanged = vi.fn();
    render(<FieldEditor projectID={3} resourceID="res_1" fields={[TOTAL]} onChanged={onChanged} />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Delete" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/breaking|validation/i);
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("omits belongs_to fields from the field list (managed as relationships)", () => {
    const rel: SpecField = { id: "fld_r", label: "Customer", type: "belongs_to", code_name: "Customer", storage_name: "customer_id", target: "res_c" };
    render(<FieldEditor projectID={3} resourceID="res_1" fields={[rel]} onChanged={vi.fn()} />);
    // The only field is a belongs_to, so the scalar field list is empty and it
    // gets no Edit/Delete here (the relationship editor owns it).
    expect(screen.getByText(/no fields yet/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });

  it("edits a field's label", async () => {
    let patched: unknown;
    mockApi((url, init) => {
      if (url.endsWith(`${FIELDS_URL}/fld_1`) && init?.method === "PATCH") {
        patched = JSON.parse(String(init.body));
        return { status: 201, body: { data: { id: 2, spec_hash: "y", abi_class: "neutral" } } };
      }
      return { status: 404 };
    });
    const onChanged = vi.fn();
    render(<FieldEditor projectID={3} resourceID="res_1" fields={[TOTAL]} onChanged={onChanged} />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Edit" }));
    const form = screen.getByRole("form", { name: "Edit Total" });
    const label = within(form).getByLabelText("Field label");
    await user.clear(label);
    await user.type(label, "Amount");
    await user.click(within(form).getByRole("button", { name: "Edit Total" }));

    expect(patched).toMatchObject({ label: "Amount", type: "decimal" });
    expect(onChanged).toHaveBeenCalledTimes(1);
  });
});
