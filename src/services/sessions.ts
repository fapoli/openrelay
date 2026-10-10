import { RelaySession, PeerInfo } from "../types/session.js";
import { randomBytes, createHash } from "crypto";
import { config } from "../config/env.js";
import logger from "../lib/logger.js";

const CODE_CHARS = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
const PASSWORD_CHARS = "abcdefghjkmnpqrstuvwxyz23456789";



// code -> session
const sessions = new Map<string, RelaySession>();
// sessionToken -> code (reverse lookup)
const tokenToCode = new Map<number, string>();

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

function generatePeerSecret(): string {
  return randomBytes(16).toString("hex");
}

export function createSession(
  projectId: string,
  maxPlayers: number,
  requestedCode?: string,
  requestedPassword?: string
): { code: string; password: string; sessionToken: number; peerSecret: string; peerIndex: number } | "conflict" | null {
  let code: string;
  if (requestedCode !== undefined) {
    if (sessions.has(requestedCode)) return "conflict";
    code = requestedCode;
  } else {
    let attempts = 0;
    do {
      code = randomString(CODE_CHARS, 6);
      if (++attempts > 200) return null;
    } while (sessions.has(code));
  }

  const password = requestedPassword ?? randomString(PASSWORD_CHARS, 8);
  const sessionToken = generateToken();
  const peerSecret = generatePeerSecret();
  const now = Date.now();

  const peers = new Map<string, PeerInfo>();
  peers.set(peerSecret, { index: 0, address: null, port: null, lastSeen: now });

  sessions.set(code, {
    sessionToken,
    passwordHash: hashPassword(password),
    projectId,
    createdAt: now,
    lastActivity: now,
    maxPlayers,
    peers,
  });
  tokenToCode.set(sessionToken, code);

  logger.info({ projectId, code, maxPlayers }, "Session created");
  return { code, password, sessionToken, peerSecret, peerIndex: 0 };
}

export function joinSession(
  code: string,
  password: string,
  projectId: string
): { sessionToken: number; peerSecret: string; peerIndex: number } | null {
  const session = sessions.get(code);
  if (!session || session.projectId !== projectId) return null;
  if (session.passwordHash !== hashPassword(password)) return null;
  if (session.peers.size >= session.maxPlayers) return null;

  const peerIndex = session.peers.size;
  const peerSecret = generatePeerSecret();
  session.peers.set(peerSecret, { index: peerIndex, address: null, port: null, lastSeen: Date.now() });
  session.lastActivity = Date.now();

  logger.info({ code, peerIndex }, "Peer joined session");
  return { sessionToken: session.sessionToken, peerSecret, peerIndex };
}

export function getActiveSessionCount(projectId: string): number {
  let count = 0;
  for (const s of sessions.values()) {
    if (s.projectId === projectId) count++;
  }
  return count;
}

// Returns addresses of all other peers to forward to, or null if secret is invalid.
export function registerAndGetPeers(
  sessionToken: number,
  peerSecret: string,
  address: string,
  port: number
): { senderIndex: number; peers: Array<{ address: string; port: number }> } | null {
  const code = tokenToCode.get(sessionToken);
  if (!code) return null;

  const session = sessions.get(code);
  if (!session) return null;

  const peer = session.peers.get(peerSecret);
  if (!peer) return null; // unknown secret — reject

  peer.address = address;
  peer.port = port;
  peer.lastSeen = Date.now();
  session.lastActivity = Date.now();

  const others: Array<{ address: string; port: number }> = [];
  for (const [secret, p] of session.peers) {
    if (secret !== peerSecret && p.address !== null && p.port !== null) {
      others.push({ address: p.address, port: p.port });
    }
  }

  logger.debug({ sessionToken, address, port }, "Peer packet received");
  return { senderIndex: peer.index, peers: others };
}

export function cleanup(): void {
  const now = Date.now();
  let expired = 0;

  for (const [code, session] of sessions) {
    if (now - session.lastActivity > config.sessionTtlMs) {
      tokenToCode.delete(session.sessionToken);
      sessions.delete(code);
      expired++;
      continue;
    }

    // Remove peers inactive for 30s
    for (const [secret, peer] of session.peers) {
      if (now - peer.lastSeen > 30_000) {
        session.peers.delete(secret);
      }
    }
  }

  if (expired > 0) logger.info({ expired }, "Expired sessions cleaned up");
}
