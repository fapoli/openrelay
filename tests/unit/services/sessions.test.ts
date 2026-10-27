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
  getPublicSessions,
  getSession,
  registerAndGetPeers,
  cleanup,
  PEER_ACTIVE_MS,
} from "../../../src/services/sessions.js";

function makeSession(overrides: Record<string, unknown> = {}) {
  return {
    sessionToken: 99999,
    passwordHash: createHash("sha256").update("correctpw").digest("hex"),
    visibility: "private",
    label: null,
    projectId: "proj1",
    createdAt: Date.now(),
    lastActivity: Date.now() - 1000,
    maxPeers: 4,
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
    expect(createSession("proj1", 4, { code: "ABC123" })).toBe("conflict");
    expect(repo.insertSession).not.toHaveBeenCalled();
  });

  it("uses requestedCode and stores the session", () => {
    const result = createSession("proj1", 4, { code: "XYZ999" });
    if (!result || result === "conflict") throw new Error("unexpected");
    expect(result.code).toBe("XYZ999");
    expect(repo.insertSession).toHaveBeenCalledWith(
      "XYZ999",
      expect.objectContaining({ projectId: "proj1", maxPeers: 4 })
    );
  });

  it("uses requestedPassword", () => {
    const result = createSession("proj1", 4, { code: "AAABBB", password: "mypassword" });
    if (!result || result === "conflict") throw new Error("unexpected");
    expect(result.password).toBe("mypassword");
  });

  it("generates a 6-char code when none is provided", () => {
    const result = createSession("proj1", 4);
    if (!result || result === "conflict") throw new Error("unexpected");
    expect(result.code).toHaveLength(6);
  });

  it("returns peerIndex 0 for the host", () => {
    const result = createSession("proj1", 4, { code: "CCCDDD" });
    if (!result || result === "conflict") throw new Error("unexpected");
    expect(result.peerIndex).toBe(0);
  });

  it("registers the session token", () => {
    const result = createSession("proj1", 4, { code: "EEEFFF" });
    if (!result || result === "conflict") throw new Error("unexpected");
    expect(repo.insertToken).toHaveBeenCalledWith(result.sessionToken, "EEEFFF");
  });

  it("sets visibility and label on the session", () => {
    createSession("proj1", 4, { code: "PUBLC1", visibility: "public", label: "General" });
    const [, session] = vi.mocked(repo.insertSession).mock.calls[0] as [
      string,
      ReturnType<typeof makeSession>,
    ];
    expect(session.visibility).toBe("public");
    expect(session.label).toBe("General");
  });

  it("sets passwordHash to null for public rooms with no password", () => {
    createSession("proj1", 4, { code: "NOPW11", visibility: "public" });
    const [, session] = vi.mocked(repo.insertSession).mock.calls[0] as [
      string,
      ReturnType<typeof makeSession>,
    ];
    expect(session.passwordHash).toBeNull();
  });

  it("keeps a password when a public room provides one", () => {
    const result = createSession("proj1", 4, {
      code: "PWPUBL",
      visibility: "public",
      password: "secret",
    });
    if (!result || result === "conflict") throw new Error("unexpected");
    expect(result.password).toBe("secret");
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
    vi.mocked(repo.fetchSession).mockReturnValue(makeSession({ maxPeers: 4, peers }) as never);
    expect(joinSession("AAAAAA", "correctpw", "proj1")).toBeNull();
  });

  it("returns peerIndex and token on valid join", () => {
    vi.mocked(repo.fetchSession).mockReturnValue(makeSession() as never);
    const result = joinSession("AAAAAA", "correctpw", "proj1");
    expect(result).not.toBeNull();
    expect(result?.peerIndex).toBe(1);
    expect(result?.sessionToken).toBe(99999);
  });

  it("allows joining a public passwordless room with no password", () => {
    vi.mocked(repo.fetchSession).mockReturnValue(
      makeSession({ passwordHash: null, visibility: "public" }) as never
    );
    const result = joinSession("AAAAAA", null, "proj1");
    expect(result).not.toBeNull();
  });

  it("returns null when a password-protected public room gets wrong password", () => {
    vi.mocked(repo.fetchSession).mockReturnValue(makeSession({ visibility: "public" }) as never);
    expect(joinSession("AAAAAA", "wrong", "proj1")).toBeNull();
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

describe("getPublicSessions", () => {
  it("returns only public sessions for the given project", () => {
    vi.mocked(repo.fetchAllSessions).mockReturnValue(
      new Map([
        [
          "PUB1",
          makeSession({
            projectId: "proj1",
            visibility: "public",
            label: "Room A",
            passwordHash: null,
          }),
        ],
        ["PRIV", makeSession({ projectId: "proj1", visibility: "private" })],
        ["OTH1", makeSession({ projectId: "proj2", visibility: "public" })],
      ]).entries() as never
    );
    const result = getPublicSessions("proj1");
    expect(result).toHaveLength(1);
    expect(result[0]).toMatchObject({ code: "PUB1", label: "Room A", passwordRequired: false });
  });

  it("reflects passwordRequired correctly", () => {
    vi.mocked(repo.fetchAllSessions).mockReturnValue(
      new Map([
        ["PW001", makeSession({ projectId: "proj1", visibility: "public" })],
      ]).entries() as never
    );
    const result = getPublicSessions("proj1");
    expect(result[0].passwordRequired).toBe(true);
  });

  it("returns active peer count and maxPeers", () => {
    const peers = new Map([
      ["s1", { index: 0, address: null, port: null, lastSeen: Date.now() }],
      ["s2", { index: 1, address: null, port: null, lastSeen: Date.now() }],
      [
        "s3",
        {
          index: 2,
          nickname: "c",
          address: null,
          port: null,
          lastSeen: Date.now() - PEER_ACTIVE_MS - 1,
        },
      ],
    ]);
    vi.mocked(repo.fetchAllSessions).mockReturnValue(
      new Map([
        ["PC001", makeSession({ projectId: "proj1", visibility: "public", maxPeers: 8, peers })],
      ]).entries() as never
    );
    const result = getPublicSessions("proj1")[0];
    expect(result.peers).toBe(2);
    expect(result.maxPeers).toBe(8);
  });
});

describe("getSession", () => {
  it("returns null for an unknown code", () => {
    vi.mocked(repo.fetchSession).mockReturnValue(undefined);
    expect(getSession("XXXXXX", "proj1")).toBeNull();
  });

  it("returns null for wrong project", () => {
    vi.mocked(repo.fetchSession).mockReturnValue(makeSession() as never);
    expect(getSession("AAAAAA", "other")).toBeNull();
  });

  it("returns session info and active peer count", () => {
    const peers = new Map([
      ["s2", { index: 1, address: null, port: null, lastSeen: Date.now() }],
      ["s1", { index: 0, address: null, port: null, lastSeen: Date.now() }],
    ]);
    vi.mocked(repo.fetchSession).mockReturnValue(
      makeSession({ visibility: "public", label: "Room A", passwordHash: null, peers }) as never
    );
    const lobby = getSession("AAAAAA", "proj1");
    expect(lobby).not.toBeNull();
    expect(lobby?.visibility).toBe("public");
    expect(lobby?.passwordRequired).toBe(false);
    expect(lobby?.peers).toHaveLength(2);
  });

  it("excludes inactive peers from the lobby", () => {
    const peers = new Map([
      ["s1", { index: 0, address: null, port: null, lastSeen: Date.now() }],
      ["s2", { index: 1, address: null, port: null, lastSeen: Date.now() - PEER_ACTIVE_MS - 1 }],
    ]);
    vi.mocked(repo.fetchSession).mockReturnValue(makeSession({ peers }) as never);
    const lobby = getSession("AAAAAA", "proj1")!;
    expect(lobby.peers).toHaveLength(1);
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
