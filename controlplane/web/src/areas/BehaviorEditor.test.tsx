import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BehaviorEditor } from "./BehaviorEditor";
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

const FIELDS: SpecField[] = [
  { id: "fld_1", label: "Name", type: "string", code_name: "Name", storage_name: "name" },
  { id: "fld_2", label: "Email", type: "string", code_name: "Email", storage_name: "email" },
];

afterEach(() => vi.restoreAllMocks());

describe("BehaviorEditor", () => {
  it("initializes the toggles from the current behavior", () => {
    render(
      <BehaviorEditor
        projectID={3}
        resourceID="res_1"
        fields={FIELDS}
        behavior={{ create_enabled: true, admin_visible: true }}
        onChanged={vi.fn()}
      />,
    );
    expect(screen.getByLabelText("create")).toBeChecked();
    expect(screen.getByLabelText("admin visible")).toBeChecked();
    expect(screen.getByLabelText("update")).not.toBeChecked();
  });

  it("saves the complete behavior with the edited toggles and field selection", async () => {
    let saved: Record<string, unknown> | undefined;
    mockApi((url, init) => {
      if (url.endsWith("/projects/3/resources/res_1/behavior") && init?.method === "PATCH") {
        saved = JSON.parse(String(init.body));
        return { status: 201, body: { data: { id: 9, spec_hash: "x" } } };
      }
      return { status: 404 };
    });
    const onChanged = vi.fn();
    render(
      <BehaviorEditor
        projectID={3}
        resourceID="res_1"
        fields={FIELDS}
        behavior={{ create_enabled: true, update_enabled: true }}
        onChanged={onChanged}
      />,
    );
    const user = userEvent.setup();

    await user.click(screen.getByLabelText("create")); // turn create off
    await user.selectOptions(screen.getByLabelText("List fields"), ["fld_1"]);
    await user.click(screen.getByRole("button", { name: "Save behavior" }));

    // The whole behavior is sent (replace semantics), reflecting the edits.
    expect(saved).toEqual({
      create_enabled: false,
      update_enabled: true,
      delete_enabled: false,
      admin_visible: false,
      list_fields: ["fld_1"],
      searchable_fields: [],
      sortable_fields: [],
      filterable_fields: [],
    });
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it("surfaces an error when the save is rejected", async () => {
    mockApi((url, init) => {
      if (url.endsWith("/behavior") && init?.method === "PATCH") {
        return { status: 422, body: { error: { code: "validation_error", message: "candidate spec is invalid" } } };
      }
      return { status: 404 };
    });
    render(<BehaviorEditor projectID={3} resourceID="res_1" fields={FIELDS} behavior={{}} onChanged={vi.fn()} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Save behavior" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/invalid/i);
  });
});
