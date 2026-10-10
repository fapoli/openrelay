import { describe, it, expect, vi, beforeEach } from "vitest";
import type { Request, Response, NextFunction } from "express";

vi.mock("../../../src/config/env.js", () => ({
  config: { sessionTtlMs: 300_000 },
  projects: new Map([
    ["valid-key", { id: "proj1", apiKey: "valid-key", maxSessions: 10, maxPlayersPerSession: 4 }],
  ]),
}));

import { requireApiKey } from "../../../src/middleware/requireApiKey.js";
import { ApiError } from "../../../src/errors/ApiError.js";

function makeReq(auth?: string): Request {
  return { headers: { authorization: auth } } as unknown as Request;
}

const res = {} as Response;

beforeEach(() => vi.clearAllMocks());

describe("requireApiKey", () => {
  it("throws 401 when authorization header is missing", () => {
    expect(() => requireApiKey(makeReq(), res, vi.fn())).toThrow(ApiError);
  });

  it("throws 401 when header does not start with Bearer", () => {
    expect(() => requireApiKey(makeReq("Basic abc"), res, vi.fn())).toThrow(ApiError);
  });

  it("throws 401 for an invalid API key", () => {
    let caught: ApiError | undefined;
    try {
      requireApiKey(makeReq("Bearer invalid-key"), res, vi.fn());
    } catch (e) {
      caught = e as ApiError;
    }
    expect(caught?.status).toBe(401);
  });

  it("calls next and attaches project for a valid API key", () => {
    const req = makeReq("Bearer valid-key");
    const next = vi.fn() as unknown as NextFunction;
    requireApiKey(req, res, next);
    expect(next).toHaveBeenCalledOnce();
    expect((req as never as { project: { id: string } }).project.id).toBe("proj1");
  });
});
