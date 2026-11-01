# openrelay

![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)

A self-hosted UDP relay server for peer-to-peer multiplayer games. Players connect with a short code and password — no IP addresses, no port forwarding, no accounts.

---

## Features

- **Zero configuration for players** — host shares a code and password, that's it
- **Public lobby** — list visible sessions and inspect their peers before joining
- **Project isolation** — multiple games on the same instance, each with its own API key and session limits
- **No database** — entirely in-memory, zero infrastructure dependencies
- **Stateless sessions** — sessions expire automatically after inactivity
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

The relay never inspects packet payload — it only reads the 9-byte header to identify and route traffic, and prepends the sender index so clients can distinguish peers.

---

## Quick Start

### 1. Configure projects

Edit `projects.yaml`:

```yaml
projects:
  - id: example-game
    maxPeersPerSession: 16 # max peers per session (default: 16)
    maxSessions: 50 # max concurrent sessions, 0 = unlimited (default: 0)
```

### 2. Set API keys

Generate a secret key for each project (e.g. `openssl rand -hex 32`) and set it as an environment variable. The name is derived from the project `id`: uppercased, hyphens replaced with underscores, `_API_KEY` appended.

```bash
EXAMPLE_GAME_API_KEY=$(openssl rand -hex 32)
```

So `example-game` → `EXAMPLE_GAME_API_KEY`. Your game client uses this key in the `Authorization` header when calling the HTTP API.

### 3. Run

```bash
go run ./cmd/openrelay
```

The HTTP signaling server starts on port `3000` and the UDP relay on port `7777`.

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

---

## Environment Variables

| Variable         | Default   | Description                          |
| ---------------- | --------- | ------------------------------------ |
| `PORT_HTTP`      | `3000`    | HTTP signaling port                  |
| `PORT_UDP`       | `7777`    | UDP relay port                       |
| `UDP_BIND_HOST`  | `0.0.0.0` | UDP bind address                     |
| `SESSION_TTL_MS` | `300000`  | Session expiry after inactivity (ms) |

---

## API

All endpoints require `Authorization: Bearer <apiKey>`.

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

Returns `409 Conflict` if the code is already in use.

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

`peers` reflects only active peers (seen within the last 10 seconds).

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

Only active peers appear in the count.

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
[8]    targetPeerIndex (uint8; 0xFF = broadcast to all peers)
[9…]   payload
```

Set `targetPeerIndex` to the recipient's `peerIndex` for unicast, or `0xFF` to broadcast to all peers in the session.

Peers are registered automatically on their first packet — no explicit handshake required.

### Receiving from the relay

Every packet forwarded by the relay is prefixed with a 4-byte sender index:

```
[0–3]   senderIndex  (uint32 BE)
[4…]    payload
```

Use `senderIndex` to map incoming packets to the correct peer. The host is always `peerIndex 0`.

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
