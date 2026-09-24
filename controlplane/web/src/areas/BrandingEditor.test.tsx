import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BrandingEditor } from "./BrandingEditor";
import type { SpecBranding } from "../api/projects";

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

describe("BrandingEditor", () => {
  it("seeds from the existing branding", () => {
    const branding: SpecBranding = { app_name: "Shop", logo_ref: "/l.svg", accent_color: "#ff0000", appearance: "dark" };
    render(<BrandingEditor projectID={3} branding={branding} onChanged={() => {}} />);
    expect(screen.getByRole("textbox", { name: "Application name" })).toHaveValue("Shop");
    expect(screen.getByRole("textbox", { name: "Accent color hex" })).toHaveValue("#ff0000");
    expect(screen.getByRole("combobox", { name: "Appearance" })).toHaveValue("dark");
  });

  it("saves the branding fields", async () => {
    const calls = capturePut();
    const onChanged = vi.fn();
    render(<BrandingEditor projectID={3} branding={{}} onChanged={onChanged} />);
    const user = userEvent.setup();

    await user.type(screen.getByRole("textbox", { name: "Application name" }), "Shopfront");
    await user.type(screen.getByRole("textbox", { name: "Accent color hex" }), "#2563eb");
    await user.selectOptions(screen.getByRole("combobox", { name: "Appearance" }), "dark");
    await user.click(screen.getByRole("button", { name: "Save branding" }));

    const put = calls.find((c) => c.method === "PUT");
    expect(put?.url).toContain("/projects/3/branding");
    expect(put?.body).toMatchObject({ app_name: "Shopfront", accent_color: "#2563eb", appearance: "dark" });
    expect(onChanged).toHaveBeenCalled();
  });
});
