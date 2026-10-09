# Portable journal archives

This package implements version 1 of the owner-scoped, offline-readable ZIP format.
The authenticated owner exports only readable, non-deleted originals. Each body is
plain Markdown; `manifest.json` preserves record dates, archive state, references,
media metadata, attachment filenames, and SHA-256 checksums. Managed attachment
bytes and retained original upload bytes are separate entries. Missing historical
originals and external media references appear as explicit warnings. Optional
30-day edit history remains separate from the current original; saved AI insights
remain independent documents, marked stale on restore.

The archive contains no passwords, access tokens, AI credentials, webhook URL or
signing secret, live sharing token, or embedded legacy S3 credentials. It retains
paused share selection, disabled destination configuration, terminal delivery
history, source event identifiers, sequence counters, and external post mappings.
Restoration never replays old work. Credentials must be configured independently.
Destinations must be enabled and individual suspended record assignments explicitly
selected before future automatic delivery. No earlier share URL is restored.

`POST /api/v1/journal/backups/preview` validates the ZIP and checks every byte before
creating an owner-bound two-hour preview. It does not import records. Explicit
restore checks the digest again, pauses that owner's current sharing/delivery,
imports into that owner as private records, and writes provenance before each memo
can be observed by a dynamic share. Source numeric owner IDs are never accepted.
Collisions get deterministic independent IDs; originals are not overwritten.
Retried archives reuse persistent per-archive mappings and do not duplicate data.
Partial I/O failures retain already restored private data and return an incomplete
report so the same archive can be retried. Existing preferences are preserved.

Limits: 1 GiB ZIP, 2 GiB expanded, 256 MiB per file, 16 MiB manifest or Markdown
record, and 30,000 entries. Unsupported versions, unsafe/duplicate/symlink paths,
inventory discrepancies and checksum failures are rejected. Archive-controlled
paths are never used as filesystem extraction destinations. Temporary previews
are removed on success or on the next upload after expiry.

This logical archive is **not** a physical database backup. For SQLite instance
and managed-media backups, use `scripts/journal_backup.py` and its documented
new-directory restore flow. Choose an independent storage destination yourself;
a copy on the same disk cannot protect against disk loss. Self-hosting is not
end-to-end encryption. An independent older backup may retain subsequently deleted
content until that backup is removed under your retention policy.

Validation is in `archive_test.go` and `server/api/v1/journal_backup_test.go`:
malicious ZIPs, fresh SQLite restore with exact bodies/dates/media originals and
relations, source owner isolation, paused publication/mappings, API preview/digest
isolation, existing-instance collisions, and idempotent retries.
