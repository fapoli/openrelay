import { describe, it, expect, vi } from "vitest";
import type { Request, Response } from "express";

vi.mock("../../../src/lib/logger.js", () => ({
  default: { info: vi.fn(), warn: vi.fn(), error: vi.fn(), debug: vi.fn() },
}));

import { errorHandler } from "../../../src/middleware/errorHandler.js";
import { ApiError } from "../../../src/errors/ApiError.js";

function makeRes() {
  const res = { status: vi.fn(), json: vi.fn() };
  res.status.mockReturnValue(res);
  return res as unknown as Response;
}

describe("errorHandler", () => {
  it("responds with the ApiError status and message", () => {
    const res = makeRes();
    errorHandler(new ApiError(404, "Not found"), {} as Request, res, vi.fn());
    expect(res.status).toHaveBeenCalledWith(404);
    expect(res.json).toHaveBeenCalledWith({ error: "Not found" });
  });

  it("responds with 500 for unknown errors", () => {
    const res = makeRes();
    errorHandler(new Error("boom"), {} as Request, res, vi.fn());
    expect(res.status).toHaveBeenCalledWith(500);
    expect(res.json).toHaveBeenCalledWith({ error: "Internal server error" });
  });
});
