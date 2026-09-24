import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RelationshipEditor } from "./RelationshipEditor";
import type { SpecField, SpecResource } from "../api/projects";

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

const CUSTOMER: SpecResource = { id: "res_c", label: "Customer", code_name: "Customer", storage_name: "customers" };
const INVOICE: SpecResource = { id: "res_i", label: "Invoice", code_name: "Invoice", storage_name: "invoices" };
const RESOURCES = [CUSTOMER, INVOICE];

afterEach(() => vi.restoreAllMocks());

describe("RelationshipEditor", () => {
  it("creates a belongs_to relationship to a chosen target", async () => {
    let posted: unknown;
    mockApi((url, init) => {
      if (url.endsWith("/projects/3/resources/res_i/relationships") && init?.method === "POST") {
        posted = JSON.parse(String(init.body));
        return { status: 201, body: { data: { id: 1, spec_hash: "x" } } };
      }
      return { status: 404 };
    });
    const onChanged = vi.fn();
    render(<RelationshipEditor projectID={3} resourceID="res_i" fields={[]} resources={RESOURCES} onChanged={onChanged} />);
    const user = userEvent.setup();

    const form = screen.getByRole("form", { name: "Add relationship" });
    await user.type(within(form).getByLabelText("Relationship label"), "Buyer");
    await user.selectOptions(within(form).getByLabelText("Target resource"), "res_c");
    await user.type(within(form).getByLabelText("Inverse label"), "Invoices");
    await user.click(within(form).getByLabelText("required"));
    await user.click(within(form).getByRole("button", { name: "Add relationship" }));

    expect(posted).toEqual({ label: "Buyer", target: "res_c", inverse_label: "Invoices", required: true });
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it("lists an existing relationship with its target resource", () => {
    const rel: SpecField = { id: "fld_r", label: "Buyer", type: "belongs_to", code_name: "Buyer", storage_name: "buyer_id", target: "res_c" };
    render(<RelationshipEditor projectID={3} resourceID="res_i" fields={[rel]} resources={RESOURCES} onChanged={vi.fn()} />);
    const list = screen.getByRole("list", { name: "Relationships" });
    expect(list).toHaveTextContent("Buyer");
    expect(list).toHaveTextContent("Customer"); // the derived target label
  });

  it("surfaces the breaking message when a relationship delete is rejected", async () => {
    const rel: SpecField = { id: "fld_r", label: "Buyer", type: "belongs_to", code_name: "Buyer", storage_name: "buyer_id", target: "res_c" };
    mockApi((url, init) => {
      if (url.endsWith("/projects/3/resources/res_i/fields/fld_r") && init?.method === "DELETE") {
        return { status: 409, body: { error: { code: "conflict", message: "candidate is ABI-breaking and requires compatibility validation: accessor removed" } } };
      }
      return { status: 404 };
    });
    const onChanged = vi.fn();
    render(<RelationshipEditor projectID={3} resourceID="res_i" fields={[rel]} resources={RESOURCES} onChanged={onChanged} />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Delete" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/breaking|validation/i);
    expect(onChanged).not.toHaveBeenCalled();
  });
});
