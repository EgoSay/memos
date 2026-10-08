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

### Online encrypted snapshots

`journal_snapshot.py` uses SQLite's online backup API, validates the database,
and pins the immutable local files referenced by attachments and revisions.
It includes retained originals and creates a SHA-256 manifest. Concurrent media
deletion, missing originals, unsafe paths and unsupported external/S3 media fail
the attempt instead of producing a supposedly complete snapshot. A subsequent
attempt starts from a new database snapshot. Avatars embedded in the database are
covered; arbitrary remote images linked in text still depend on their remote host.
Unreferenced files and regenerable caches are excluded.

`journal_cloud_backup.py` serializes snapshot creation, encrypted restic upload,
remote snapshot confirmation and local manifest verification. A partial restic
backup (including exit 3) never advances `last-success.json`. Optional monitoring
delivery has a separate `last-report.json`: a delivery failure does not invalidate
a completed backup. Raw subprocess output and credentials are not logged.

Store configuration outside Git with directory mode 0700 and file mode 0600:

```json
{
  "source": "/actual/docker-volume/_data",
  "logicalRoot": "/var/opt/memos",
  "workDir": "/var/lib/memos-backup",
  "repository": "s3:https://ACCOUNT.r2.cloudflarestorage.com/PRIVATE_BUCKET/restic",
  "passwordFile": "/etc/memos-backup/restic-password",
  "host": "memos-production",
  "instanceVersion": "DEPLOYED_COMMIT",
  "credentials": {
    "AWS_ACCESS_KEY_ID": "BUCKET_SCOPED_KEY",
    "AWS_SECRET_ACCESS_KEY": "BUCKET_SCOPED_SECRET",
    "AWS_DEFAULT_REGION": "auto"
  }
}
```

Use a cryptographically random repository password and retain an independent
recovery copy. Keep the staging directory outside the live volume. Its disk must
fit the SQLite snapshot and, when hard links cannot be used, the referenced media.
Install a checksum-verified restic binary (tested with 0.19.1) and Python 3.9+.
Initialize once; never automatically initialize a new repository after an access
failure:

```bash
umask 077
python3 scripts/journal_cloud_backup.py init --config /etc/memos-backup/config.json
python3 scripts/journal_cloud_backup.py backup --config /etc/memos-backup/config.json
python3 scripts/journal_cloud_backup.py check --config /etc/memos-backup/config.json
python3 scripts/journal_cloud_backup.py retain --config /etc/memos-backup/config.json
```

`check` reads all remote repository data. `retain` keeps all snapshots from the
latest two days, 30 daily, 12 monthly and up to 100 yearly snapshots for the
configured host. Never apply object-age lifecycle deletion to restic data packs.
These scripts alone do not schedule jobs: provision and verify the backup timer,
retention/check jobs and independent monitoring before claiming automatic backup.

Restore the explicit snapshot ID from a verified success receipt with restic into
a new, isolated directory; do not assume the newest snapshot is complete, because
restic can retain snapshots from failed partial uploads. Then run
`journal_snapshot.py verify --destination RESTORED_DIRECTORY`. Verify account and
media behavior in an isolated app as well. Before starting a restored copy, use
the existing `journal_backup.pause_restored_permissions` routine to revoke old
sessions/shares and pause outgoing delivery, and map absolute attachment paths
to the restored data root. Do not replace a running database or start two writable
production instances. Repository passwords and raw restored data stay private.

Run backup consistency and failure-path tests with:

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/tests -v
```
