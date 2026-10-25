# Deployment

Expo web build served via nginx in Docker, exposed to the internet via Cloudflare
Tunnel from a VM. Actual compose file, env template, and delivery script live in
`../../deploy/` (outside this git repo, since it wires together both
`recipe-generator` and `recipe-generator-frontend`). See `../../deploy/README.md`.

## URL layout

Single host, path-split: `recipe-generator-beta.ili16.de`

- `/api/v1/*` → backend (`http://backend:8080` in-cluster, or
  `http://host.docker.internal:8080` for the tunnel ingress rule)
- everything else → this frontend's nginx (`http://frontend:80` / `:8081` on the host)

Same-origin in the browser, so no cross-origin API calls in production — `CORS_ORIGIN`
on the backend is defense in depth, not load-bearing for the web build.

## Config that must match the deployed host

`src/constants/index.ts`:

```ts
export const API_BASE_URL = __DEV__
  ? 'http://localhost:8080/api/v1'
  : 'https://recipe-generator-beta.ili16.de/api/v1';
```

## Keycloak: add the web redirect URI

The native scheme `com.recipegenerator://oauth/callback` only works in the native
app. `authService.ts` already calls `AuthSession.makeRedirectUri({ scheme, path:
'oauth/callback' })`, which on web resolves to the page's own origin — no code
change needed — but Keycloak must be told to trust it:

In Keycloak → `recipe-generator` realm → `frontend` client → **Valid Redirect URIs**, add:

```
https://recipe-generator-beta.ili16.de/oauth/callback
```

## Build

Handled by `deploy/docker-compose.yml` (`docker compose build frontend`), which runs:

```bash
npm ci
npx expo export --platform web   # -> dist/, a static SPA
```
then serves `dist/` via the `Dockerfile`/`nginx.conf` in this directory.

## Cloudflare Tunnel routing

Public Hostname `recipe-generator-beta.ili16.de` on the tunnel, with two path rules
(dashboard → Zero Trust → Networks → Tunnels → your tunnel → Public Hostname):

| Path | Service |
|---|---|
| `api/*` | `http://host.docker.internal:8080` |
| `*` (catch-all) | `http://host.docker.internal:8081` |

Routing to `host.docker.internal` (not the compose service names) is deliberate: it
lets the same tunnel serve either the built containers or local dev processes
(`go run ./cmd/`, `npm run web`) bound to the same host ports — see
`../../deploy/README.md` for the fast-iteration workflow.

## Deploy

```bash
cd ../../deploy
cp .env.example .env   # first time only, then fill in secrets
./deploy.sh
```
