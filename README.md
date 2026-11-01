# openrelay

![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)

A self-hosted UDP relay server for peer-to-peer multiplayer games. Players connect with a short code and password — no IP addresses, no port forwarding, no accounts.

---

## Features

- **Zero configuration for players** — host shares a code and password, that's it
- **Public lobby** — list visible sessions and inspect their peers before joining
- **Project isolation** — multiple games on the same instance, each with its own API key and session limits
- **No database** — entirely in-memory, zero infrastructure dependencies
- **Stateless sessions** — sessions and peers expire automatically after inactivity
- **Self-hosted** — deploy anywhere that runs Go or Docker

---

## Tech Stack

- **Go**
- **chi** — HTTP router
- **net/http** — HTTP signaling server
- **net (UDP)** — relay, standard library

---

## How it works

1. The host calls `POST /sessions` (optionally providing a `code`) → receives `code`, `password`, `sessionToken`, `peerToken`, and `peerIndex: 0`
2. The host shares the code and password with their players out-of-band (in-game lobby, chat, etc.)
3. Each player calls `POST /sessions/:code/join` with the password → receives `sessionToken`, `peerToken`, and their assigned `peerIndex`
4. All players open a UDP socket to the relay and prefix every packet with a 9-byte header: `[sessionToken (4 bytes BE)][peerToken (4 bytes)][targetPeerIndex (1 byte)]`
5. The relay identifies the peer via `peerToken`, then forwards the packet to the target peer (unicast) or all other peers (broadcast, `targetPeerIndex = 0xFF`), prepending the sender's `peerIndex` as a 4-byte big-endian header
6. Every peer keeps sending — game data or keepalives (`targetPeerIndex = 0xFE`) — at least once per `PEER_TIMEOUT_MS`, otherwise it expires

The relay never inspects packet payload — it only reads the 9-byte header to identify and route traffic, and prepends the sender index so clients can distinguish peers.

---

## Quick Start

### 1. Configure projects

Edit `projects.yaml`:

```yaml
projects:
  - id: example-game
    maxPeersPerSession: 16 # max peers per session, 1–254 (default: 16)
    maxSessions: 50 # max concurrent sessions, 0 = unlimited (default: 0)
```

### 2. Set API keys

Generate a secret key for each project (e.g. `openssl rand -hex 32`) and set it as an environment variable. The name is derived from the project `id`: uppercased, hyphens replaced with underscores, `_API_KEY` appended.

```bash
EXAMPLE_GAME_API_KEY=$(openssl rand -hex 32)
```

So `example-game` → `EXAMPLE_GAME_API_KEY`. Your game client uses this key in the `Authorization` header when calling the HTTP API. Every project listed in `projects.yaml` must have its key set, and each project needs a distinct key.

### 3. Run

```bash
go run ./cmd/openrelay
```

The HTTP signaling server starts on port `3000` and the UDP relay on port `7777`.

The server refuses to start (exit code `1`) if the configuration is unusable:

- `projects.yaml` is missing, unparsable, or lists no projects
- a project has an empty or duplicate `id`
- `maxPeersPerSession` is outside `1–254`
- a project's API key environment variable is not set, or two projects share a key
- the UDP relay port cannot be bound

---

## Deployment

The server is a single Go binary with no external dependencies. Deploy it anywhere:

- [Fly.io](https://fly.io)
- [Render](https://render.com)
- [Railway](https://railway.app)
- Any VPS with Docker or Go

### Docker

`projects.yaml` is baked into the image at build time. Build and run:

```bash
docker build -t openrelay .
docker run -p 3000:3000 -p 7777:7777/udp \
  -e EXAMPLE_GAME_API_KEY=your_secret_key \
  openrelay
```

Make sure to expose **both** the HTTP port (TCP) and the relay port (UDP).

### Behind a reverse proxy

If the HTTP API sits behind a reverse proxy or load balancer, every request appears to come from the proxy. Set `TRUSTED_IP_HEADER` to the name of the header your proxy uses to pass the original client IP, e.g. `TRUSTED_IP_HEADER=X-Real-IP` or `TRUSTED_IP_HEADER=X-Forwarded-For`. Without it, every peer is compared against the proxy's IP and rejected.

Only set it if your proxy **overwrites** that header with the IP it saw. If the proxy passes a client-sent value through, clients can forge their IP and bypass the check. If the header holds a list (as `X-Forwarded-For` does), the relay uses the last entry, the one your proxy added. Requests without the header are rejected with `400`, so clients can't bypass the proxy and reach the relay directly.

---

## Environment Variables

| Variable         | Default   | Description                          |
| ---------------- | --------- | ------------------------------------ |
| `PORT_HTTP`      | `3000`    | HTTP signaling port                  |
| `PORT_UDP`       | `7777`    | UDP relay port                       |
| `UDP_BIND_HOST`  | _(empty)_ | UDP bind address; empty = all interfaces, IPv4 and IPv6 |
| `SESSION_TTL_MS` | `300000`  | Session expiry after inactivity (ms) |
| `PEER_TIMEOUT_MS`| `10000`   | Peer expiry after its last packet (ms) |
| `CLEANUP_INTERVAL_MS` | `300000` | How often expired sessions/peers are freed from memory (ms) |
| `RATE_LIMIT_PER_MIN` | `10` | Create, join and lookup requests per minute per client IP, shared (0 = off) |
| `TRUSTED_IP_HEADER` | _(empty)_ | Header your reverse proxy sets with the real client IP. Empty = use the TCP connection address. See [Source IP binding](#source-ip-binding) |

---

## API

All endpoints require `Authorization: Bearer <apiKey>`.

### Rate limits

Create, join and lookup share one limit per client IP (`RATE_LIMIT_PER_MIN`, default 10 per minute). It stops password guessing on join, probing for session codes, and mass session creation. Requests over the limit get `429 Too Many Requests` with a `Retry-After` header. Unauthenticated requests count too, so a bad API key doesn't bypass the limit. Listing sessions and `/health` are not limited.

- The client IP is determined the same way as for [source IP binding](#source-ip-binding): the TCP address, or `TRUSTED_IP_HEADER` behind a proxy. Without the header, all clients behind a proxy share one limit.
- IPv6 clients are grouped by `/64`, since a single client usually controls a whole `/64`.
- Players behind the same public IP (LAN party, campus, carrier-grade NAT) share a limit. The default leaves room for that; raise it if many players join from one IP.
- Limits are per IP; an attacker with many IPs gets the limit on each of them.

### Request limits

- Request bodies over 4 KB are rejected with `413`.
- Clients must send headers within 5 s and the full request within 10 s; idle keep-alive connections are closed after 60 s.

### Create a session

```
POST /sessions
Content-Type: application/json
```

| Field        | Type                      | Required | Description                                                         |
| ------------ | ------------------------- | -------- | ------------------------------------------------------------------- |
| `code`       | string (6 chars, A-Z0-9)  | No       | Custom code; server generates one if absent                         |
| `password`   | string (min 6 chars)      | No       | If set, joining requires the correct password. Omit for open access |
| `label`      | string (max 64 chars)     | No       | Human-readable name shown in the lobby                              |
| `visibility` | `"public"` \| `"private"` | No       | Default `"private"`. Public sessions appear in `GET /sessions`      |

Returns `409 Conflict` if the code is already in use, and `429 Too Many Requests` if the project has reached `maxSessions`.

Response:

```json
{
  "code": "ROOM01",
  "password": "m2qn8pzv",
  "sessionToken": 2947183621,
  "peerToken": "a1b2c3d4",
  "peerIndex": 0
}
```

`password` is `null` for public sessions without a password. Share `code` and `password` with your players out-of-band. Use `sessionToken` and `peerToken` to connect to the UDP relay.

### Join a session

```
POST /sessions/:code/join
Content-Type: application/json
```

| Field      | Type   | Required | Description                              |
| ---------- | ------ | -------- | ---------------------------------------- |
| `password` | string | No       | Required for password-protected sessions |

Response:

```json
{
  "sessionToken": 2947183621,
  "peerToken": "b2c3d4e5",
  "peerIndex": 1
}
```

### List public sessions

```
GET /sessions
```

Returns all visible sessions for the project.

```json
{
  "sessions": [
    {
      "code": "ROOM01",
      "label": "Friday night",
      "peers": 3,
      "maxPeers": 8,
      "passwordRequired": false
    }
  ]
}
```

`peers` reflects only live peers (seen within the last `PEER_TIMEOUT_MS`).

### Session lobby

```
GET /sessions/:code
```

Returns details and the active peer count for any session (public or private).

```json
{
  "code": "ROOM01",
  "label": "Friday night",
  "visibility": "public",
  "passwordRequired": false,
  "peers": 2
}
```

Only live peers appear in the count.

### Health check

```
GET /health
```

---

## UDP Packet Format

### Sending to the relay

Every packet sent to the relay must begin with a 9-byte header:

```
[0–3]  sessionToken    (uint32 BE)
[4–7]  peerToken       (4 bytes, from session response)
[8]    targetPeerIndex (uint8; 0xFE = keepalive, 0xFF = broadcast to all peers)
[9…]   payload
```

Set `targetPeerIndex` to the recipient's `peerIndex` for unicast, `0xFF` to broadcast to all peers in the session, or `0xFE` for a keepalive. A keepalive refreshes the sender and is not forwarded; its payload may be empty (the 9-byte header alone is valid).

Peers are registered automatically on their first packet — no explicit handshake required.

### Source IP binding

Each peer is bound to the IP address of the HTTP request that created or joined it. UDP packets for that peer are accepted only from that IP; packets from any other IP are dropped and change nothing. The port may change at any time (NAT rebinding is fine).

The HTTP IP comes from a completed TCP connection, so it cannot be forged. This prevents anyone from registering a peer with a spoofed UDP source address and using the relay to flood a third party.

Players don't need to do anything. Requirements:

- **Deployment:** the HTTP API and the UDP relay must be reachable over the same IP families. If the UDP relay is reachable over IPv4 only, the hostname clients use for HTTP must resolve to IPv4 only (A record, no AAAA). Otherwise a dual-stack client may join over IPv6, send UDP over IPv4, and have every packet dropped.
- **Behind a proxy:** set `TRUSTED_IP_HEADER` (see [Behind a reverse proxy](#behind-a-reverse-proxy)).
- **Client integration:** make the HTTP calls and send UDP from the same process, so both use the same network. A player switching networks mid-session (e.g. Wi-Fi to mobile data) has to rejoin.

Dropped packets caused by an IP mismatch are logged with both the join IP and the UDP source IP.

### Peer liveness

- Send the first UDP packet right after creating or joining the session.
- After that, send something — game data or a keepalive — more often than `PEER_TIMEOUT_MS` (e.g. a keepalive every `PEER_TIMEOUT_MS / 3` while idle).
- A peer silent for longer than `PEER_TIMEOUT_MS` expires immediately: it stops receiving traffic, its packets are dropped, and its slot is freed. Expiry is final — rejoin via `POST /sessions/:code/join` to get a new `peerToken`.
- A session with no packets or joins for `SESSION_TTL_MS` expires the same way; its code becomes available again.

Expired sessions and peers are ignored immediately; their memory is reclaimed by a background sweep every `CLEANUP_INTERVAL_MS` (default 5 minutes). The interval only affects memory, not behavior.

### Peer indices

A `peerIndex` is a slot in `0..maxPeersPerSession-1`, not a permanent identity. A joining peer gets the lowest free slot, so the slot of an expired peer is reused by the next peer to join. The session creator gets slot `0`; if the host expires, slot `0` can be assigned to a later joiner.

### Receiving from the relay

Every packet forwarded by the relay is prefixed with a 4-byte sender index:

```
[0–3]   senderIndex  (uint32 BE)
[4…]    payload
```

Use `senderIndex` to map incoming packets to the correct peer. The session creator starts at `peerIndex 0` (see [Peer indices](#peer-indices)).

---

## Development

```bash
go run ./cmd/openrelay    # Run directly
go build ./cmd/openrelay  # Compile binary
go test ./...             # Run tests
go vet ./...              # Static analysis
```

---

## License

MIT
