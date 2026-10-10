import { describe, it, expect } from "vitest";
import { z } from "zod";
import { parseBody } from "../../../src/schemas/parse.js";
import { ApiError } from "../../../src/errors/ApiError.js";

const Schema = z.object({ name: z.string(), age: z.number() });

describe("parseBody", () => {
  it("returns parsed data for a valid body", () => {
    expect(parseBody(Schema, { name: "Alice", age: 30 })).toEqual({ name: "Alice", age: 30 });
  });

  it("throws ApiError 400 for an invalid body", () => {
    expect(() => parseBody(Schema, { name: "Alice" })).toThrow(ApiError);
  });

  it("uses the first Zod issue as the error message", () => {
    let caught: ApiError | undefined;
    try {
      parseBody(Schema, { name: 123, age: 30 });
    } catch (e) {
      caught = e as ApiError;
    }
    expect(caught?.status).toBe(400);
    expect(caught?.message).toBeTruthy();
  });
});
