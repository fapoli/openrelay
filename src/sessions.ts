import { randomBytes, createHash } from "crypto";
import { config } from "./config.js";
import logger from "./lib/logger.js";

const CODE_CHARS = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
const PASSWORD_CHARS = "abcdefghjkmnpqrstuvwxyz23456789";

interface GameSession {
  sessionToken: number;
  passwordHash: string;
  projectId: string;
  createdAt: number;
  lastActivity: number;
  maxPlayers: number;
}

interface Peer {
  address: string;
  port: number;
  lastSeen: number;
}

// code -> session
const games = new Map<string, GameSession>();
// sessionToken -> peers
const peers = new Map<number, Peer[]>();
// sessionToken -> code (reverse lookup for activity updates)
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

export function createGame(
  projectId: string,
  maxPlayers: number
): { code: string; password: string; sessionToken: number } | null {
  let code: string;
  let attempts = 0;
  do {
    code = randomString(CODE_CHARS, 6);
    if (++attempts > 200) return null;
  } while (games.has(code));

  const password = randomString(PASSWORD_CHARS, 8);
  const sessionToken = generateToken();
  const now = Date.now();

  games.set(code, {
    sessionToken,
    passwordHash: hashPassword(password),
    projectId,
    createdAt: now,
    lastActivity: now,
    maxPlayers,
  });
  peers.set(sessionToken, []);
  tokenToCode.set(sessionToken, code);

  return { code, password, sessionToken };
}

export function joinGame(code: string, password: string, projectId: string): number | null {
  const game = games.get(code);
  if (!game || game.projectId !== projectId) return null;
  if (game.passwordHash !== hashPassword(password)) return null;

  game.lastActivity = Date.now();
  return game.sessionToken;
}

export function getActiveSessionCount(projectId: string): number {
  let count = 0;
  for (const g of games.values()) {
    if (g.projectId === projectId) count++;
  }
  return count;
}

export function registerOrUpdatePeer(
  sessionToken: number,
  address: string,
  port: number
): Peer[] | null {
  const sessionPeers = peers.get(sessionToken);
  if (!sessionPeers) return null;

  const existing = sessionPeers.find((p) => p.address === address && p.port === port);
  if (existing) {
    existing.lastSeen = Date.now();
  } else {
    sessionPeers.push({ address, port, lastSeen: Date.now() });
    logger.debug({ sessionToken, address, port }, "Peer registered");
  }

  const code = tokenToCode.get(sessionToken);
  if (code) {
    const game = games.get(code);
    if (game) game.lastActivity = Date.now();
  }

  return sessionPeers;
}

export function cleanup(): void {
  const now = Date.now();
  let expired = 0;

  for (const [code, game] of games) {
    if (now - game.lastActivity > config.sessionTtlMs) {
      peers.delete(game.sessionToken);
      tokenToCode.delete(game.sessionToken);
      games.delete(code);
      expired++;
    }
  }

  // Remove stale peers (no packet in 30s)
  for (const [token, sessionPeers] of peers) {
    const active = sessionPeers.filter((p) => now - p.lastSeen < 30_000);
    if (active.length !== sessionPeers.length) {
      peers.set(token, active);
    }
  }

  if (expired > 0) logger.info({ expired }, "Expired sessions cleaned up");
}
