# openrelay

A self-hosted UDP relay server for peer-to-peer multiplayer games. Players connect with a short code and password — no IP addresses, no port forwarding, no accounts.

---

## Features

- **Zero configuration for players** — host shares a code and password, that's it
- **Project isolation** — multiple games on the same instance, each with its own API key and session limits
- **No database** — entirely in-memory, zero infrastructure dependencies
- **Stateless sessions** — sessions expire automatically after inactivity
- **Self-hosted** — deploy anywhere that runs Node.js or Docker

---

## Tech Stack

- **Node.js** + **TypeScript**
- **Express** — HTTP signaling server
- **dgram** — UDP relay (Node built-in, no extra dependencies)
- **Zod** — Request validation

---

## How it works

1. The host calls `POST /sessions` (optionally providing a `code`) → receives `code`, `password`, `sessionToken`, `peerSecret`, and `peerIndex: 0`
2. The host shares the code and password with their players out-of-band (in-game lobby, chat, etc.)
3. Each player calls `POST /sessions/:code/join` with the password → receives `sessionToken`, `peerSecret`, and their assigned `peerIndex`
4. All players open a UDP socket to the relay and prefix every packet with `[sessionToken (4 bytes BE)][peerSecret (16 bytes)]`
5. The relay authenticates the peer, then forwards packets to all other peers in the session, prepending the sender's `peerIndex` as a 4-byte big-endian header

The relay never inspects packet payload — it only reads the 20-byte header to authenticate and route traffic, and prepends the sender index so clients can distinguish peers.

---

## Quick Start

### 1. Configure projects

Edit `projects.yaml`:

```yaml
projects:
  - id: mygame
    maxSessions: 100
    maxPlayersPerSession: 16
```

### 2. Set API keys

Generate a secret key for each project (e.g. `openssl rand -hex 32`) and set it as an environment variable. The name is derived from the project `id`: uppercased, hyphens replaced with underscores, `_API_KEY` appended.

```bash
MYGAME_API_KEY=$(openssl rand -hex 32)
```

So `my-game` → `MY_GAME_API_KEY`. Your game client uses this key in the `Authorization` header when calling the HTTP API.

### 3. Run

```bash
npm install
npm run dev
```

The HTTP signaling server starts on port `3000` and the UDP relay on port `7777`.

---

## Deployment

The server is a single Node.js process with no external dependencies. Deploy it anywhere:

- [Fly.io](https://fly.io)
- [Render](https://render.com)
- [Railway](https://railway.app)
- Any VPS with Docker or Node.js

### Docker

`projects.yaml` is baked into the image at build time. Build and run:

```bash
docker build -t openrelay .
docker run -p 3000:3000 -p 7777:7777/udp \
  -e MYGAME_API_KEY=your_secret_key \
  openrelay
```

Make sure to expose **both** the HTTP port (TCP) and the relay port (UDP).

---

## Environment Variables

| Variable         | Default  | Description                          |
| ---------------- | -------- | ------------------------------------ |
| `PORT`           | `3000`   | HTTP signaling port                  |
| `PORT_UDP`       | `7777`   | UDP relay port                       |
| `SESSION_TTL_MS` | `300000` | Session expiry after inactivity (ms) |
| `LOG_LEVEL`      | `info`   | Pino log level                       |

---

## API

All endpoints require `Authorization: Bearer <apiKey>`.

### Create a session

```
POST /sessions
Content-Type: application/json

{ "code": "ROOM01" }   ← optional; server generates one if absent
```

Returns `409 Conflict` if the code is already in use.

Response:

```json
{
  "code": "ROOM01",
  "password": "m2qn8pzv",
  "sessionToken": 2947183621,
  "peerSecret": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
  "peerIndex": 0
}
```

Share `code` and `password` with your players. Use `sessionToken` and `peerSecret` to connect to the UDP relay.

### Join a session

```
POST /sessions/:code/join
Content-Type: application/json

{ "password": "m2qn8pzv" }
```

Response:

```json
{
  "sessionToken": 2947183621,
  "peerSecret": "b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5",
  "peerIndex": 1
}
```

### Health check

```
GET /health
```

---

## UDP Packet Format

### Sending to the relay

Every packet sent to the relay must begin with a 20-byte header:

```
[0–3]   sessionToken  (uint32 BE)
[4–19]  peerSecret    (16 bytes, from session response)
[20…]   payload
```

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
npm run dev        # Start with hot reload
npm run build      # Compile TypeScript
npm start          # Run compiled output
npm run lint       # ESLint
npm run lint:fix   # ESLint with auto-fix
npm run format     # Prettier
```

---

## License

MIT
