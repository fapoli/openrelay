import { describe, it, expect, vi, beforeEach } from "vitest";
import { createHash } from "crypto";

vi.mock("../../../src/repositories/sessionsRepository.js", () => ({
  fetchSession: vi.fn(),
  insertSession: vi.fn(),
  deleteSession: vi.fn(),
  hasSession: vi.fn(),
  fetchCodeByToken: vi.fn(),
  insertToken: vi.fn(),
  deleteToken: vi.fn(),
  fetchAllSessions: vi.fn(),
}));

vi.mock("../../../src/lib/logger.js", () => ({
  default: { info: vi.fn(), warn: vi.fn(), error: vi.fn(), debug: vi.fn() },
}));

vi.mock("../../../src/config/env.js", () => ({
  config: { sessionTtlMs: 300_000 },
  projects: new Map(),
}));

import * as repo from "../../../src/repositories/sessionsRepository.js";
import {
  createSession,
  joinSession,
  getActiveSessionCount,
  registerAndGetPeers,
  cleanup,
} from "../../../src/services/sessions.js";

function makeSession(overrides: Record<string, unknown> = {}) {
  return {
    sessionToken: 99999,
    passwordHash: createHash("sha256").update("correctpw").digest("hex"),
    projectId: "proj1",
    createdAt: Date.now(),
    lastActivity: Date.now() - 1000,
    maxPlayers: 4,
    peers: new Map([
      ["hostsecret", { index: 0, address: "1.2.3.4", port: 5000, lastSeen: Date.now() }],
    ]),
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(repo.hasSession).mockReturnValue(false);
  vi.mocked(repo.fetchAllSessions).mockReturnValue([].values() as never);
});

describe("createSession", () => {
  it("returns conflict when the requested code already exists", () => {
    vi.mocked(repo.hasSession).mockReturnValue(true);
    expect(createSession("proj1", 4, "ABC123")).toBe("conflict");
    expect(repo.insertSession).not.toHaveBeenCalled();
  });

  it("uses requestedCode and stores the session", () => {
    const result = createSession("proj1", 4, "XYZ999");
    if (!result || result === "conflict") throw new Error("unexpected");
    expect(result.code).toBe("XYZ999");
    expect(repo.insertSession).toHaveBeenCalledWith(
      "XYZ999",
      expect.objectContaining({ projectId: "proj1", maxPlayers: 4 })
    );
  });

  it("uses requestedPassword", () => {
    const result = createSession("proj1", 4, "AAABBB", "mypassword");
    if (!result || result === "conflict") throw new Error("unexpected");
    expect(result.password).toBe("mypassword");
  });

  it("generates a 6-char code when none is provided", () => {
    const result = createSession("proj1", 4);
    if (!result || result === "conflict") throw new Error("unexpected");
    expect(result.code).toHaveLength(6);
  });

  it("returns peerIndex 0 for the host", () => {
    const result = createSession("proj1", 4, "CCCDDD");
    if (!result || result === "conflict") throw new Error("unexpected");
    expect(result.peerIndex).toBe(0);
  });

  it("registers the session token", () => {
    const result = createSession("proj1", 4, "EEEFFF");
    if (!result || result === "conflict") throw new Error("unexpected");
    expect(repo.insertToken).toHaveBeenCalledWith(result.sessionToken, "EEEFFF");
  });
});

describe("joinSession", () => {
  it("returns null for an unknown code", () => {
    vi.mocked(repo.fetchSession).mockReturnValue(undefined);
    expect(joinSession("XXXXXX", "pw", "proj1")).toBeNull();
  });

  it("returns null for wrong project", () => {
    vi.mocked(repo.fetchSession).mockReturnValue(makeSession() as never);
    expect(joinSession("AAAAAA", "correctpw", "other-project")).toBeNull();
  });

  it("returns null for wrong password", () => {
    vi.mocked(repo.fetchSession).mockReturnValue(makeSession() as never);
    expect(joinSession("AAAAAA", "wrongpw", "proj1")).toBeNull();
  });

  it("returns null when session is full", () => {
    const peers = new Map(
      Array.from({ length: 4 }, (_, i) => [
        `secret${i}`,
        { index: i, address: null, port: null, lastSeen: Date.now() },
      ])
    );
    vi.mocked(repo.fetchSession).mockReturnValue(makeSession({ maxPlayers: 4, peers }) as never);
    expect(joinSession("AAAAAA", "correctpw", "proj1")).toBeNull();
  });

  it("returns peerIndex and token on valid join", () => {
    vi.mocked(repo.fetchSession).mockReturnValue(makeSession() as never);
    const result = joinSession("AAAAAA", "correctpw", "proj1");
    expect(result).not.toBeNull();
    expect(result?.peerIndex).toBe(1);
    expect(result?.sessionToken).toBe(99999);
  });
});

describe("getActiveSessionCount", () => {
  it("counts sessions matching the projectId", () => {
    vi.mocked(repo.fetchAllSessions).mockReturnValue(
      new Map([
        ["A", makeSession({ projectId: "proj1" })],
        ["B", makeSession({ projectId: "proj2" })],
        ["C", makeSession({ projectId: "proj1" })],
      ]).entries() as never
    );
    expect(getActiveSessionCount("proj1")).toBe(2);
  });
});

describe("registerAndGetPeers", () => {
  it("returns null for an unknown token", () => {
    vi.mocked(repo.fetchCodeByToken).mockReturnValue(undefined);
    expect(registerAndGetPeers(12345, "secret", "1.2.3.4", 5000)).toBeNull();
  });

  it("returns null for an unknown session", () => {
    vi.mocked(repo.fetchCodeByToken).mockReturnValue("AAAAAA");
    vi.mocked(repo.fetchSession).mockReturnValue(undefined);
    expect(registerAndGetPeers(12345, "secret", "1.2.3.4", 5000)).toBeNull();
  });

  it("returns null for an unknown peer secret", () => {
    vi.mocked(repo.fetchCodeByToken).mockReturnValue("AAAAAA");
    vi.mocked(repo.fetchSession).mockReturnValue(makeSession() as never);
    expect(registerAndGetPeers(99999, "wrongsecret", "1.2.3.4", 5000)).toBeNull();
  });

  it("returns empty peers list when no other peers have registered", () => {
    vi.mocked(repo.fetchCodeByToken).mockReturnValue("AAAAAA");
    vi.mocked(repo.fetchSession).mockReturnValue(makeSession() as never);
    const result = registerAndGetPeers(99999, "hostsecret", "1.2.3.4", 5000);
    expect(result).not.toBeNull();
    expect(result?.peers).toHaveLength(0);
    expect(result?.senderIndex).toBe(0);
  });

  it("forwards to peers that have a registered address", () => {
    const peers = new Map([
      ["secret0", { index: 0, address: "1.2.3.4", port: 5000, lastSeen: Date.now() }],
      ["secret1", { index: 1, address: "5.6.7.8", port: 6000, lastSeen: Date.now() }],
    ]);
    vi.mocked(repo.fetchCodeByToken).mockReturnValue("AAAAAA");
    vi.mocked(repo.fetchSession).mockReturnValue(makeSession({ peers }) as never);
    const result = registerAndGetPeers(99999, "secret0", "1.2.3.4", 5000);
    expect(result?.peers).toHaveLength(1);
    expect(result?.peers[0]).toEqual({ address: "5.6.7.8", port: 6000 });
  });
});

describe("cleanup", () => {
  it("removes sessions that have exceeded the TTL", () => {
    const session = makeSession({ lastActivity: Date.now() - 400_000 });
    vi.mocked(repo.fetchAllSessions).mockReturnValue(
      new Map([["AAAAAA", session]]).entries() as never
    );
    cleanup();
    expect(repo.deleteSession).toHaveBeenCalledWith("AAAAAA");
    expect(repo.deleteToken).toHaveBeenCalledWith(99999);
  });

  it("keeps sessions within the TTL", () => {
    const session = makeSession({ lastActivity: Date.now() - 1000 });
    vi.mocked(repo.fetchAllSessions).mockReturnValue(
      new Map([["AAAAAA", session]]).entries() as never
    );
    cleanup();
    expect(repo.deleteSession).not.toHaveBeenCalled();
  });
});
