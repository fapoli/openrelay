# openrelay

Self-hosted UDP relay server for peer-to-peer multiplayer games. Drop it on fly.io, configure projects via YAML, connect with a 6-character code and password.

## How it works

1. Host calls `POST /games` → gets a `code`, `password`, and `sessionToken`
2. Host shares the code and password with their players (in-game lobby, chat, etc.)
3. Each player calls `POST /games/:code/join` with the password → gets the same `sessionToken`
4. All players open a UDP socket to the relay and prefix every packet with their 4-byte `sessionToken`
5. The relay forwards packets between all peers in the session

No accounts, no database, no cloud vendor lock-in.

## Deployment

### fly.io

```bash
fly launch
fly secrets set MYGAME_API_KEY=your_secret_key
fly deploy
```

### Docker

```bash
docker build -t openrelay .
MYGAME_API_KEY=your_secret_key docker run -p 3000:3000 -p 7777:7777/udp \
  -v $(pwd)/projects.yaml:/app/projects.yaml openrelay
```

## Configuration

Copy `projects.example.yaml` to `projects.yaml` (never commit this file):

```yaml
projects:
  - id: mygame
    maxSessions: 100
    maxPlayersPerSession: 16
```

Set the API key for each project as an environment variable:

```
MYGAME_API_KEY=your_secret_key
```

The env var name is derived from the project `id`: uppercased, hyphens replaced with underscores, `_API_KEY` appended. So `my-game` → `MY_GAME_API_KEY`.

### Environment variables

| Variable         | Default  | Description                          |
| ---------------- | -------- | ------------------------------------ |
| `PORT_HTTP`      | `3000`   | HTTP signaling port                  |
| `PORT_UDP`       | `7777`   | UDP relay port                       |
| `SESSION_TTL_MS` | `300000` | Session expiry after inactivity (ms) |
| `LOG_LEVEL`      | `info`   | Pino log level                       |

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

The host uses `sessionToken` to connect to the UDP relay. Share `code` and `password` with players.

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

## UDP packet format

Every packet sent to the relay must be prefixed with a 4-byte big-endian `uint32` session token:

```
[0-3]  sessionToken (uint32 BE)
[4..]  payload
```

The relay strips the token header and forwards the payload to all other peers in the session. Peers are registered automatically on their first packet.

## Multi-region

Deploy the same app to multiple fly.io regions and point your game client to the nearest one. Sessions are in-memory per instance — there is no shared state between regions, which is what you want (players in a session are always on the same machine).

## Development

```bash
cp projects.example.yaml projects.yaml
# edit projects.yaml and set your env vars in .env
npm install
npm run dev
```
