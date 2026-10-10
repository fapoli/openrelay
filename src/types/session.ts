export interface PeerInfo {
  index: number;
  address: string | null;
  port: number | null;
  lastSeen: number;
}

export interface RelaySession {
  sessionToken: number;
  passwordHash: string;
  projectId: string;
  createdAt: number;
  lastActivity: number;
  maxPlayers: number;
  peers: Map<string, PeerInfo>;
}
