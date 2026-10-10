import { RelaySession } from "../types/session.js";

const sessions = new Map<string, RelaySession>();
const tokenToCode = new Map<number, string>();

export function fetchSession(code: string): RelaySession | undefined {
  return sessions.get(code);
}

export function insertSession(code: string, session: RelaySession): void {
  sessions.set(code, session);
}

export function deleteSession(code: string): void {
  sessions.delete(code);
}

export function hasSession(code: string): boolean {
  return sessions.has(code);
}

export function fetchCodeByToken(token: number): string | undefined {
  return tokenToCode.get(token);
}

export function insertToken(token: number, code: string): void {
  tokenToCode.set(token, code);
}

export function deleteToken(token: number): void {
  tokenToCode.delete(token);
}

export function fetchAllSessions(): IterableIterator<[string, RelaySession]> {
  return sessions.entries();
}
