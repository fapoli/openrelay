# openrelay

A self-hosted UDP relay server for peer-to-peer multiplayer games. Players connect with a short code and password — no IP addresses, no port forwarding, no accounts.

---

## Features

- **Zero configuration for players** — host shares a 6-character code and password, that's it
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

1. The host calls `POST /games` → receives a `code`, `password`, and `sessionToken`
2. The host shares the code and password with their players out-of-band (in-game lobby, chat, etc.)
3. Each player calls `POST /games/:code/join` with the password → receives the same `sessionToken`
4. All players open a UDP socket to the relay and prefix every packet with their 4-byte `sessionToken`
5. The relay forwards packets between all peers in the session

The relay never inspects packet content — it only reads the 4-byte header to route traffic.

---

## Quick Start

### 1. Configure projects

```bash
cp projects.example.yaml projects.yaml
```

Edit `projects.yaml`:

```yaml
projects:
  - id: mygame
    maxSessions: 100
    maxPlayersPerSession: 16
```

### 2. Set API keys

Set an environment variable for each project. The name is derived from the project `id`: uppercased, hyphens replaced with underscores, `_API_KEY` appended.

```bash
MYGAME_API_KEY=your_secret_key
```

So `my-game` → `MY_GAME_API_KEY`.

### 3. Run

```bash
npm install
npm run dev
```

The HTTP signaling server starts on port `3000` and the UDP relay on port `7777`.

---

## Deployment

The server is a single Node.js process with no external dependencies. Deploy it anywhere:

- [Render](https://render.com)
- [Fly.io](https://fly.io)
- [Railway](https://railway.app)
- Any VPS with Docker or Node.js

### Docker

```bash
docker build -t openrelay .
docker run -p 3000:3000 -p 7777:7777/udp \
  -e MYGAME_API_KEY=your_secret_key \
  -v $(pwd)/projects.yaml:/app/projects.yaml \
  openrelay
```

Make sure to expose **both** the HTTP port (TCP) and the relay port (UDP).

---

## Environment Variables

| Variable         | Default  | Description                          |
| ---------------- | -------- | ------------------------------------ |
| `PORT_HTTP`      | `3000`   | HTTP signaling port                  |
| `PORT_UDP`       | `7777`   | UDP relay port                       |
| `SESSION_TTL_MS` | `300000` | Session expiry after inactivity (ms) |
| `LOG_LEVEL`      | `info`   | Pino log level                       |

---

## API

All endpoints require `Authorization: Bearer <apiKey>`.

### Create a game

```
POST /games
```

Response:

```json
{
  "code": "AB3X7K",
  "password": "m2qn8pzv",
  "sessionToken": 2947183621
}
```

Share `code` and `password` with your players. Use `sessionToken` immediately to connect to the UDP relay.

### Join a game

```
POST /games/:code/join
Content-Type: application/json

{ "password": "m2qn8pzv" }
```

Response:

```json
{
  "sessionToken": 2947183621
}
```

### Health check

```
GET /health
```

---

## UDP Packet Format

Every packet sent to the relay must be prefixed with the 4-byte big-endian `uint32` session token:

```
[0–3]  sessionToken (uint32 BE)
[4…]   payload
```

The relay strips the token header and forwards the payload to all other peers in the session. Peers are registered automatically on their first packet — no explicit handshake required.

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
