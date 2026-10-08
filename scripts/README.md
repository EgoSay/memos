# Deployment and recovery tools

`Dockerfile` supports the existing release pipeline, which builds the frontend
before building the Go image. `Dockerfile.dokploy` builds both from a clean Git
checkout. Its separate context allowlist excludes local data, secrets,
node_modules and previously generated frontend files.

## Personal journal deployment

`journal-image.yml` builds the image on GitHub, runs the existing fresh-install,
frontend, restart-persistence and upgrade smoke tests, and then publishes an
immutable commit tag and the `production` tag. This avoids compiling on the
application server. A configured `MEMOS_DOKPLOY_DEPLOY_WEBHOOK` repository secret
triggers Dokploy only after publication; otherwise deployment is manual.

Configure a Dokploy Docker Compose service for this repository and the
`codex/personal-life-journal` branch, with path `./scripts/compose.dokploy.yaml`.
Disable the ordinary push-triggered autodeploy: the image publication webhook
is the deployment trigger. Set:

```dotenv
MEMOS_INSTANCE_URL=https://journal.example.com
MEMOS_IMAGE=ghcr.io/egosay/memos-journal:production
MEMOS_TUNNEL_TOKEN=<dedicated remotely managed Cloudflare Tunnel token>
```

Keep the tunnel token in Dokploy's environment, never in Git. Configure the
tunnel origin as `http://memos:5230` only after owner initialization. It shares
the Compose network and needs no published server port.

The current CI image targets `linux/amd64`; confirm server architecture before
deployment. The registry must permit the server to pull the image. The Compose
file does not publish a host port or create a public route. Initialize or
restore the private owner account before enabling ingress. Keep one replica
because SQLite and uploaded media share the `memos-data` named volume. Never
use `docker compose down --volumes` for an update. Runtime limits are 384 MiB
and half a CPU; adjust only from measured usage.

For a local build (Docker required):

```bash
docker build -f scripts/Dockerfile.dokploy \
  --build-arg VERSION="$(bash scripts/release_version.sh development-version)" \
  --build-arg COMMIT="$(git rev-parse HEAD)" -t journal:verify .
bash scripts/release_smoke_test.sh \
  --candidate-image journal:verify --previous-image neosmemo/memos:0.31.0
```

For rollback, set `MEMOS_IMAGE` to the previous verified commit tag and redeploy.
Do not roll back across an incompatible database migration without restoring
an independently verified snapshot into a new volume.

## Backups

`journal_backup.py` creates and verifies private instance archives, including
SQLite, attachments and retained originals. It currently requires a stopped
instance. Restoration must target a new directory and disables restored public
shares, authentication tokens and outgoing delivery credentials.

A persistent Docker volume protects data during redeployment; it is not an
off-server backup. Creating an R2 bucket alone does not enable automatic backups.
Do not claim cloud recovery is ready until a scheduled backup has succeeded and
the resulting archive has been restored and checked in an isolated instance.
