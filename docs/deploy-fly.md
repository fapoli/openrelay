# Example: deploying to Fly.io

A quick start for running your own openrelay on [Fly.io](https://fly.io). It takes about ten minutes.

## 1. Fork and clone

Fork this repository on GitHub, then clone your fork:

```bash
git clone git@github.com:<you>/openrelay.git
cd openrelay
```

## 2. Add your projects

Edit `projects.yaml` with one entry per game:

```yaml
projects:
  - id: my-game
    maxPeersPerSession: 4 # 1–254 (default: 16)
    maxSessions: 100 # 0 = unlimited (default: 0)
```

Each project needs an API key, set in step 6. Its name comes from the `id`: `my-game` → `MY_GAME_API_KEY`.

## 3. Install flyctl and log in

Install [flyctl](https://fly.io/docs/flyctl/install/), then:

```bash
fly auth login
```

## 4. Create the app and `fly.toml`

```bash
fly apps create <your-app-name>
```

Create `fly.toml` in the repository root:

```toml
app = "<your-app-name>"
primary_region = "iad" # region closest to your players; see: fly platform regions

[deploy]
  strategy = "immediate"

[env]
  UDP_BIND_HOST = "fly-global-services"
  TRUSTED_IP_HEADER = "Fly-Client-IP"

[http_service]
  internal_port = 3000
  force_https = true
  auto_stop_machines = true
  auto_start_machines = true
  min_machines_running = 0

  [[http_service.checks]]
    interval = "30s"
    timeout = "5s"
    grace_period = "10s"
    method = "GET"
    path = "/health"

[[services]]
  protocol = "udp"
  internal_port = 7777

  [[services.ports]]
    port = 7777

[[vm]]
  memory = "256mb"
  cpu_kind = "shared"
  cpus = 1
```

Both `[env]` settings are required:

- `UDP_BIND_HOST = "fly-global-services"`: Fly only delivers UDP to apps bound to this address.
- `TRUSTED_IP_HEADER = "Fly-Client-IP"`: the HTTP API sits behind Fly's proxy. Without this, every player appears to come from the proxy's IP, so [source IP binding](../README.md#source-ip-binding) rejects all UDP traffic and all players share one rate limit.

## 5. Set up the IP address

UDP on Fly needs a dedicated IPv4 address (about $2/month), and the app must not have an IPv6 address:

```bash
fly ips allocate-v4
fly ips list
```

If `fly ips list` shows an IPv6 address, remove it:

```bash
fly ips release <ipv6-address>
```

## 6. Set the API keys

One secret per project. Generate the key, copy it, then set it:

```bash
openssl rand -hex 32
# copy the output: this is the key your game sends as "Authorization: Bearer <key>"
fly secrets set MY_GAME_API_KEY=<the key you copied>
```

Fly can't show a secret again after it's set, so keep the key somewhere safe. The server refuses to start if any project in `projects.yaml` has no key.

## 7. Deploy

```bash
fly deploy --ha=false
```

`--ha=false` stops flyctl from adding a spare machine when it creates the app's first one. See [Caveats](#caveats).

## 8. Verify

```bash
curl https://<your-app-name>.fly.dev/health
curl -X POST https://<your-app-name>.fly.dev/sessions \
  -H "Authorization: Bearer <your-key>" -d '{}'
fly logs
```

At startup the logs show one `project loaded` line per project, then `UDP relay listening` and `HTTP signaling listening`. While players are connected, a `source IP does not match join IP` warning means the setup in steps 4 and 5 is incomplete.

Point your games at `https://<your-app-name>.fly.dev` for HTTP and `<your-app-name>.fly.dev:7777` (or the dedicated IPv4) for UDP.

## 9. Optional: deploy on every push

Create a deploy token and store it as the `FLY_API_TOKEN` secret of your GitHub fork:

```bash
fly tokens create deploy
```

Then add these steps to the end of `.github/workflows/ci.yml`:

```yaml
      - uses: superfly/flyctl-actions/setup-flyctl@master
        if: github.ref == 'refs/heads/main'

      - run: flyctl deploy --remote-only --ha=false
        if: github.ref == 'refs/heads/main'
        env:
          FLY_API_TOKEN: ${{ secrets.FLY_API_TOKEN }}
```

## Caveats

- Run a single machine. Sessions live in memory, and a session's HTTP join and UDP traffic must reach the same process. Always deploy with `--ha=false`; if `fly scale show` ever reports more than one machine, run `fly scale count 1`.
- The relay only accepts a player's UDP from the IP they joined from. Fly UDP is IPv4-only, so an IPv6 address on the app would make some players' HTTP and UDP come from different IPs.
- With `auto_stop_machines`, Fly stops the machine when it sees no traffic and starts it again on the next request. A stop or restart ends all sessions. If players lose sessions mid-game, set `min_machines_running = 1`.
