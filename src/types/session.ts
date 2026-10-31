export interface PeerInfo {
  index: number;
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
  maxPeers: number;
  peers: Map<string, PeerInfo>;
  nextPeerIndex: number;
}
