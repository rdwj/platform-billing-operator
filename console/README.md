# Console — MaaS FinOps Console

React SPA + Go BFF. The BFF embeds the built SPA via `embed.FS` and reverse-proxies `/api/*` requests to ledger-svc's `/v1/*` HTTP API.

## Development

Two processes for hot-reload:

```bash
# Terminal 1: Go BFF (serves API proxy)
LEDGER_URL=http://localhost:8080 go run cmd/console/main.go

# Terminal 2: Vite dev server (hot-reload SPA, proxies /api to BFF)
cd console
npm install
npm run dev
```

Open `http://localhost:3000` (Vite). API calls go through Vite's proxy to the BFF on `:9090`, which proxies to ledger-svc on `:8080`.

For production-like testing (single binary, embedded SPA):

```bash
make console-build   # npm ci + vite build → console/dist/
make console         # go build with embedded dist/
LEDGER_URL=http://localhost:8080 ./bin/console
```

Open `http://localhost:9090`.

## Build

```bash
make console-build   # SPA → console/dist/
make console         # Go binary → bin/console
```

Container (multi-stage, builds both SPA and Go):

```bash
podman build --platform linux/amd64 -f cmd/console/Containerfile -t console:latest .
```

## Environment variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `LEDGER_URL` | yes | — | ledger-svc HTTP address (e.g. `http://ledger-svc:8080`) |
| `LISTEN_ADDR` | no | `:9090` | BFF listen address |

## API proxy mapping

The BFF rewrites `/api/` to `/v1/` and forwards to ledger-svc:

| BFF endpoint | ledger-svc endpoint |
|---|---|
| `GET /api/tenants` | `GET /v1/tenants` |
| `GET /api/balance?tenant=X` | `GET /v1/balance?tenant=X` |
| `GET /api/usage?tenant=X` | `GET /v1/usage?tenant=X` |
| `GET /api/invoices?tenant=X` | `GET /v1/invoices?tenant=X` |
| `GET /api/drift?tenant=X` | `GET /v1/drift?tenant=X` |
| `GET /api/export?tenant=X` | `GET /v1/export?tenant=X` |
| `GET /api/pricing?tenant=X` | `GET /v1/pricing?tenant=X` |
