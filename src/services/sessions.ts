import { PeerInfo } from "../types/session.js";
import { randomBytes, createHash } from "crypto";
import { config } from "../config/env.js";
import logger from "../lib/logger.js";
import {
  fetchSession,
  insertSession,
  deleteSession,
  hasSession,
  fetchCodeByToken,
  insertToken,
  deleteToken,
  fetchAllSessions,
} from "../repositories/sessionsRepository.js";

const CODE_CHARS = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
export const PEER_ACTIVE_MS = 10_000;

function randomString(chars: string, length: number): string {
  const bytes = randomBytes(length);
  return Array.from(bytes)
    .map((b) => chars[b % chars.length])
    .join("");
}

function hashPassword(password: string): string {
  return createHash("sha256").update(password).digest("hex");
}

function generateToken(): number {
  return randomBytes(4).readUInt32BE(0);
}

function generatePeerToken(): string {
  return randomBytes(4).toString("hex");
}

export interface CreateSessionOpts {
  code?: string;
  password?: string;
  label?: string;
  visibility?: "public" | "private";
}

export function createSession(
  projectId: string,
  maxPeers: number,
  opts: CreateSessionOpts = {}
):
  | {
      code: string;
      password: string | null;
      sessionToken: number;
      peerToken: string;
      peerIndex: number;
    }
  | "conflict"
  | null {
  const {
    code: requestedCode,
    password: requestedPassword,
    label = null,
    visibility = "private",
  } = opts;

  let code: string;
  if (requestedCode !== undefined) {
    if (hasSession(requestedCode)) return "conflict";
    code = requestedCode;
  } else {
    let attempts = 0;
    do {
      code = randomString(CODE_CHARS, 6);
      if (++attempts > 200) return null;
    } while (hasSession(code));
  }

  const password = requestedPassword ?? null;
  const sessionToken = generateToken();
  const peerToken = generatePeerToken();
  const now = Date.now();

  const peers = new Map<string, PeerInfo>();
  peers.set(peerToken, { index: 0, address: null, port: null, lastSeen: now });

  insertSession(code, {
    sessionToken,
    passwordHash: password ? hashPassword(password) : null,
    visibility,
    label,
    projectId,
    createdAt: now,
    lastActivity: now,
    maxPeers,
    peers,
    nextPeerIndex: 1,
  });
  insertToken(sessionToken, code);

  logger.info({ projectId, code, maxPeers, visibility }, "Session created");
  return { code, password, sessionToken, peerToken, peerIndex: 0 };
}

export function joinSession(
  code: string,
  password: string | null,
  projectId: string
): { sessionToken: number; peerToken: string; peerIndex: number } | null {
  const session = fetchSession(code);
  if (!session || session.projectId !== projectId) return null;

  if (session.passwordHash !== null) {
    if (!password || session.passwordHash !== hashPassword(password)) return null;
  }

  if (session.peers.size >= session.maxPeers) return null;

  const peerIndex = session.nextPeerIndex++;
  const peerToken = generatePeerToken();
  session.peers.set(peerToken, {
    index: peerIndex,
    address: null,
    port: null,
    lastSeen: Date.now(),
  });
  session.lastActivity = Date.now();

  logger.info({ code, peerIndex }, "Peer joined session");
  return { sessionToken: session.sessionToken, peerToken, peerIndex };
}

export function getActiveSessionCount(projectId: string): number {
  let count = 0;
  for (const [, s] of fetchAllSessions()) {
    if (s.projectId === projectId) count++;
  }
  return count;
}

export function getPublicSessions(projectId: string): Array<{
  code: string;
  label: string | null;
  peers: number;
  maxPeers: number;
  passwordRequired: boolean;
}> {
  const now = Date.now();
  const result = [];
  for (const [code, s] of fetchAllSessions()) {
    if (s.projectId !== projectId || s.visibility !== "public") continue;
    const peers = [...s.peers.values()].filter((p) => now - p.lastSeen < PEER_ACTIVE_MS).length;
    result.push({
      code,
      label: s.label,
      peers,
      maxPeers: s.maxPeers,
      passwordRequired: s.passwordHash !== null,
    });
  }
  return result;
}

export function getSession(
  code: string,
  projectId: string
): {
  code: string;
  label: string | null;
  visibility: "public" | "private";
  passwordRequired: boolean;
  peers: number;
} | null {
  const session = fetchSession(code);
  if (!session || session.projectId !== projectId) return null;

  const now = Date.now();
  let peers = 0;
  for (const [, peer] of session.peers) {
    if (now - peer.lastSeen < PEER_ACTIVE_MS) peers++;
  }

  return {
    code,
    label: session.label,
    visibility: session.visibility,
    passwordRequired: session.passwordHash !== null,
    peers,
  };
}

const BROADCAST = 0xff;

export function registerAndGetPeers(
  sessionToken: number,
  peerToken: string,
  address: string,
  port: number,
  targetPeerIndex: number = BROADCAST
): { senderIndex: number; peers: Array<{ address: string; port: number }> } | null {
  const code = fetchCodeByToken(sessionToken);
  if (!code) return null;

  const session = fetchSession(code);
  if (!session) return null;

  const peer = session.peers.get(peerToken);
  if (!peer) return null;

  peer.address = address;
  peer.port = port;
  peer.lastSeen = Date.now();
  session.lastActivity = Date.now();

  const others: Array<{ address: string; port: number }> = [];
  for (const [secret, p] of session.peers) {
    if (secret === peerToken || p.address === null || p.port === null) continue;
    if (targetPeerIndex !== BROADCAST && p.index !== targetPeerIndex) continue;
    others.push({ address: p.address, port: p.port });
  }

  logger.debug({ sessionToken, address, port }, "Peer packet received");
  return { senderIndex: peer.index, peers: others };
}

export function cleanup(): void {
  const now = Date.now();
  let expired = 0;

  for (const [code, session] of fetchAllSessions()) {
    if (now - session.lastActivity > config.sessionTtlMs) {
      deleteToken(session.sessionToken);
      deleteSession(code);
      expired++;
      continue;
    }

    for (const [secret, peer] of session.peers) {
      if (now - peer.lastSeen > PEER_ACTIVE_MS) {
        session.peers.delete(secret);
      }
    }
  }

  if (expired > 0) logger.info({ expired }, "Expired sessions cleaned up");
}
