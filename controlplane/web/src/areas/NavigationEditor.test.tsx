import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { NavigationEditor } from "./NavigationEditor";
import type { SpecNavItem, SpecPage } from "../api/projects";

const PAGES: SpecPage[] = [
  { id: "pag_dash", slug: "home", label: "Home", type: "dashboard" },
  { id: "pag_tbl", slug: "customers", label: "Customers", type: "resource_table", resource: "res_1" },
  // A detail page must not be offered as a nav target.
  { id: "pag_det", slug: "customer", label: "Customer", type: "resource_detail", resource: "res_1" },
];

function capturePut() {
  const calls: { method: string; url: string; body: unknown }[] = [];
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({
      method: init?.method ?? "GET",
      url: typeof input === "string" ? input : input.toString(),
      body: init?.body ? JSON.parse(init.body as string) : undefined,
    });
    return { ok: true, status: 201, statusText: "", json: async () => ({ data: { id: 1, spec_hash: "h" } }) } as Response;
  }) as unknown as typeof fetch;
  return calls;
}

afterEach(() => vi.restoreAllMocks());

describe("NavigationEditor", () => {
  it("only offers dashboard/table pages as page targets", () => {
    render(<NavigationEditor projectID={3} navigation={[{ id: "n1", label: "Home", target: "page", page: "pag_dash" }]} pages={PAGES} onChanged={() => {}} />);
    const pageSelect = screen.getByRole("combobox", { name: "Entry 1 page" });
    expect(within(pageSelect).getByRole("option", { name: "Home" })).toBeInTheDocument();
    expect(within(pageSelect).getByRole("option", { name: "Customers" })).toBeInTheDocument();
    // The detail page is not a navigable target.
    expect(within(pageSelect).queryByRole("option", { name: "Customer" })).not.toBeInTheDocument();
  });

  it("saves the ordered navigation, sending only each target's field", async () => {
    const nav: SpecNavItem[] = [
      { id: "n1", label: "Home", target: "page", page: "pag_dash" },
      { id: "n2", label: "Docs", target: "external", url: "https://example.com" },
    ];
    const calls = capturePut();
    const onChanged = vi.fn();
    render(<NavigationEditor projectID={3} navigation={nav} pages={PAGES} onChanged={onChanged} />);
    const user = userEvent.setup();

    // Reorder: move the external entry above the page entry.
    await user.click(screen.getByRole("button", { name: "Move entry 2 up" }));
    await user.click(screen.getByRole("button", { name: "Save navigation" }));

    const put = calls.find((c) => c.method === "PUT");
    expect(put?.url).toContain("/projects/3/navigation");
    expect(put?.body).toEqual({
      items: [
        { label: "Docs", target: "external", url: "https://example.com" },
        { label: "Home", target: "page", page: "pag_dash" },
      ],
    });
    expect(onChanged).toHaveBeenCalled();
  });

  it("adds and removes entries", async () => {
    const calls = capturePut();
    render(<NavigationEditor projectID={3} navigation={[]} pages={PAGES} onChanged={() => {}} />);
    const user = userEvent.setup();

    expect(screen.getByText(/no navigation yet/i)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add entry" }));
    await user.type(screen.getByRole("textbox", { name: "Entry 1 label" }), "Home");
    await user.click(screen.getByRole("button", { name: "Save navigation" }));

    const put = calls.find((c) => c.method === "PUT");
    // The new entry defaults to a page target on the first navigable page.
    expect(put?.body).toEqual({ items: [{ label: "Home", target: "page", page: "pag_dash" }] });
  });

  it("switching an entry to external swaps the page select for a url field", async () => {
    render(<NavigationEditor projectID={3} navigation={[{ id: "n1", label: "Home", target: "page", page: "pag_dash" }]} pages={PAGES} onChanged={() => {}} />);
    const user = userEvent.setup();

    expect(screen.getByRole("combobox", { name: "Entry 1 page" })).toBeInTheDocument();
    await user.selectOptions(screen.getByRole("combobox", { name: "Entry 1 target" }), "external");
    expect(screen.queryByRole("combobox", { name: "Entry 1 page" })).not.toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Entry 1 url" })).toBeInTheDocument();
  });
});
