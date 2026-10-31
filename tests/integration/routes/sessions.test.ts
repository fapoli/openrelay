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
      { id: "test-project", apiKey: "test-api-key", maxSessions: 20, maxPeersPerSession: 4 },
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
      sessionToken: expect.any(Number),
      peerToken: expect.any(String),
      peerIndex: 0,
    });
  });

  it("returns 409 when the requested code is already in use", async () => {
    await request(app).post("/sessions").set(AUTH).send({ code: "DUPDUP" });
    const res = await request(app).post("/sessions").set(AUTH).send({ code: "DUPDUP" });
    expect(res.status).toBe(409);
  });

  it("accepts label and public flag", async () => {
    const res = await request(app)
      .post("/sessions")
      .set(AUTH)
      .send({ code: "PUBLC1", visibility: "public", label: "General Chat" });
    expect(res.status).toBe(201);
    expect(res.body.password).toBeNull();
  });

  it("accepts a password for a public room", async () => {
    const res = await request(app)
      .post("/sessions")
      .set(AUTH)
      .send({ code: "PUBLC2", visibility: "public", password: "secret" });
    expect(res.status).toBe(201);
    expect(res.body.password).toBe("secret");
  });
});

describe("POST /sessions/:code/join", () => {
  it("returns 401 without authorization", async () => {
    const res = await request(app)
      .post("/sessions/AAAAAA/join")
      .send({ password: "validpassword" });
    expect(res.status).toBe(401);
  });

  it("returns 404 for an unknown code", async () => {
    const res = await request(app)
      .post("/sessions/XXXXXX/join")
      .set(AUTH)
      .send({ password: "validpassword" });
    expect(res.status).toBe(404);
  });

  it("returns 404 for a wrong password", async () => {
    await request(app)
      .post("/sessions")
      .set(AUTH)
      .send({ code: "WRGPWD", password: "correctpassword" });
    const res = await request(app)
      .post("/sessions/WRGPWD/join")
      .set(AUTH)
      .send({ password: "wrongpassword" });
    expect(res.status).toBe(404);
  });

  it("joins successfully and returns the expected shape", async () => {
    await request(app)
      .post("/sessions")
      .set(AUTH)
      .send({ code: "JOINOK", password: "supersecret" });
    const res = await request(app)
      .post("/sessions/JOINOK/join")
      .set(AUTH)
      .send({ password: "supersecret" });
    expect(res.status).toBe(200);
    expect(res.body).toMatchObject({
      sessionToken: expect.any(Number),
      peerToken: expect.any(String),
      peerIndex: 1,
    });
  });

  it("joins a public passwordless room without a password", async () => {
    await request(app).post("/sessions").set(AUTH).send({ code: "NOPW22", visibility: "public" });
    const res = await request(app).post("/sessions/NOPW22/join").set(AUTH).send({});
    expect(res.status).toBe(200);
    expect(res.body.peerIndex).toBe(1);
  });
});

describe("GET /sessions", () => {
  it("returns 401 without authorization", async () => {
    const res = await request(app).get("/sessions");
    expect(res.status).toBe(401);
  });

  it("lists only public sessions", async () => {
    await request(app)
      .post("/sessions")
      .set(AUTH)
      .send({ code: "PUBLS1", visibility: "public", label: "Open Room" });
    await request(app)
      .post("/sessions")
      .set(AUTH)
      .send({ code: "PRIVS1", password: "supersecret" });

    const res = await request(app).get("/sessions").set(AUTH);
    expect(res.status).toBe(200);
    const codes = (res.body.sessions as Array<{ code: string }>).map((s) => s.code);
    expect(codes).toContain("PUBLS1");
    expect(codes).not.toContain("PRIVS1");
  });

  it("returns the expected session shape", async () => {
    await request(app)
      .post("/sessions")
      .set(AUTH)
      .send({ code: "SHAPE1", visibility: "public", label: "Shaped Room" });

    const res = await request(app).get("/sessions").set(AUTH);
    const session = (res.body.sessions as Array<Record<string, unknown>>).find(
      (s) => s.code === "SHAPE1"
    );
    expect(session).toMatchObject({
      code: "SHAPE1",
      label: "Shaped Room",
      peers: expect.any(Number),
      maxPeers: expect.any(Number),
      passwordRequired: false,
    });
  });
});

describe("GET /sessions/:code", () => {
  it("returns 401 without authorization", async () => {
    const res = await request(app).get("/sessions/AAAAAA");
    expect(res.status).toBe(401);
  });

  it("returns 404 for an unknown code", async () => {
    const res = await request(app).get("/sessions/ZZZZZZ").set(AUTH);
    expect(res.status).toBe(404);
  });

  it("returns lobby info with peers", async () => {
    await request(app)
      .post("/sessions")
      .set(AUTH)
      .send({ code: "LOBBY1", visibility: "public", label: "Lobby Room" });

    const res = await request(app).get("/sessions/LOBBY1").set(AUTH);
    expect(res.status).toBe(200);
    expect(res.body).toMatchObject({
      code: "LOBBY1",
      label: "Lobby Room",
      visibility: "public",
      passwordRequired: false,
      peers: expect.any(Number),
    });
  });
});
