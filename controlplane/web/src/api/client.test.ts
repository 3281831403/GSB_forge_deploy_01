import { describe, expect, it } from "vitest";
import { ApiError, describeError } from "./client";

describe("describeError", () => {
  it("surfaces field diagnostics keyed to the offending path", () => {
    const err = new ApiError(422, "validation_error", "candidate spec is invalid", {
      "$.resources[0].fields[1].code_name": ["already used by field fld_1"],
    });
    const msg = describeError(err);
    expect(msg).toContain("$.resources[0].fields[1].code_name");
    expect(msg).toContain("already used");
  });

  it("falls back to the envelope message when there are no fields", () => {
    expect(describeError(new ApiError(409, "conflict", "candidate is ABI-breaking"))).toBe("candidate is ABI-breaking");
  });

  it("handles a non-ApiError", () => {
    expect(describeError(new TypeError("boom"))).toBe("Something went wrong");
  });
});
