import { describe, it, expect, vi } from "vitest";
import express from "express";
import "express-async-errors";
import request from "supertest";
import sessionsRouter from "../../../src/routes/sessions.js";
import { errorHandler } from "../../../src/middleware/errorHandler.js";

vi.mock("../../../src/config/env.js", () => ({
  config: { sessionTtlMs: 300_000 },
  projects: new Map([
    [
      "test-api-key",
      { id: "test-project", apiKey: "test-api-key", maxSessions: 10, maxPlayersPerSession: 4 },
    ],
  ]),
}));

vi.mock("../../../src/lib/logger.js", () => ({
  default: { info: vi.fn(), warn: vi.fn(), error: vi.fn(), debug: vi.fn() },
}));

const app = express();
app.use(express.json());
app.use("/", sessionsRouter);
app.use(errorHandler);

const AUTH = { Authorization: "Bearer test-api-key" };

describe("POST /sessions", () => {
  it("returns 401 without authorization", async () => {
    const res = await request(app).post("/sessions").send({});
    expect(res.status).toBe(401);
  });

  it("creates a session and returns 201 with the expected shape", async () => {
    const res = await request(app).post("/sessions").set(AUTH).send({});
    expect(res.status).toBe(201);
    expect(res.body).toMatchObject({
      code: expect.stringMatching(/^[A-Z0-9]{6}$/),
      password: expect.any(String),
      sessionToken: expect.any(Number),
      peerSecret: expect.any(String),
      peerIndex: 0,
    });
  });

  it("returns 409 when the requested code is already in use", async () => {
    await request(app).post("/sessions").set(AUTH).send({ code: "DUPDUP" });
    const res = await request(app).post("/sessions").set(AUTH).send({ code: "DUPDUP" });
    expect(res.status).toBe(409);
  });
});

describe("POST /sessions/:code/join", () => {
  it("returns 401 without authorization", async () => {
    const res = await request(app).post("/sessions/AAAAAA/join").send({ password: "pw" });
    expect(res.status).toBe(401);
  });

  it("returns 404 for an unknown code", async () => {
    const res = await request(app).post("/sessions/XXXXXX/join").set(AUTH).send({ password: "pw" });
    expect(res.status).toBe(404);
  });

  it("returns 404 for a wrong password", async () => {
    await request(app).post("/sessions").set(AUTH).send({ code: "WRGPWD" });
    const res = await request(app)
      .post("/sessions/WRGPWD/join")
      .set(AUTH)
      .send({ password: "wrongpassword" });
    expect(res.status).toBe(404);
  });

  it("joins successfully and returns the expected shape", async () => {
    const create = await request(app).post("/sessions").set(AUTH).send({ code: "JOINOK" });
    const { password } = create.body as { password: string };
    const res = await request(app).post("/sessions/JOINOK/join").set(AUTH).send({ password });
    expect(res.status).toBe(200);
    expect(res.body).toMatchObject({
      sessionToken: expect.any(Number),
      peerSecret: expect.any(String),
      peerIndex: 1,
    });
  });
});
