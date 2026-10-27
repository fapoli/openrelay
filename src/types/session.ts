export interface PeerInfo {
  index: number;
  nickname: string;
  address: string | null;
  port: number | null;
  lastSeen: number;
}

export interface RelaySession {
  sessionToken: number;
  passwordHash: string | null; // null = public room with no password
  visibility: "public" | "private";
  label: string | null;
  projectId: string;
  createdAt: number;
  lastActivity: number;
  maxPlayers: number;
  peers: Map<string, PeerInfo>;
}
