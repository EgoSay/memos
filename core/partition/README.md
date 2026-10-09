# Personal partitions and delivery

A partition is an owner-only label for organization and explicitly configured
outbound delivery. It does not reuse collaboration Space membership or its
cascade-delete behavior. Removing a partition never deletes memo bodies or media.

`partition`, `partition-target`, `partition-mapping`, `partition-delivery`,
`partition-external`, and `partition-sequence` are separate CAS documents in the
journal store. No content copy is kept in the queue: the worker loads the current
original and verifies ownership, normal state, trash exclusion, membership,
partition assignment/version, target epoch and source revision before each send.
Changes that invalidate authorization cancel pending and retrying payloads.
Deleting/archiving suspends the mapping so restoring the record does not resume
publication. The user can explicitly select a partition again.

## Delivery contract

The receiver gets one signed JSON `POST`:

```json
{
  "targetId": "destination-uuid",
  "eventId": "durable-event-id",
  "event": "upsert",
  "memoUid": "stable-memo-uid",
  "sequence": 3,
  "revision": "source-fingerprint",
  "content": "Original words",
  "recordTime": 1790985600
}
```

Only configured fields are included. If media is enabled, `media` contains
`name`, `type`, and base64 `content`, without authentication tokens or internal
storage paths. Images are re-encoded without EXIF; external attachments are not
fetched. The full JSON payload is limited to 64 MiB. Failed media preparation
keeps the private original and does not substitute truncated content.

`webhook-id` and `Idempotency-Key` remain the same for retries. The standard
webhook headers sign `id.timestamp.body` with HMAC-SHA256 using the configured
secret. Receivers must verify the signature and a bounded timestamp window,
persist event IDs before acknowledging, and deduplicate `(targetId, memoUid)`
into one external object. Reject a lower `sequence` for that object; this is
necessary when a timed-out older request finishes after a newer one. Local CAS
leases prevent normal duplicate processing, but cannot make arbitrary third-party
receivers provide exactly-once publication.

All HTTP 2xx responses mean **delivered**, including an empty 204 response. A
receiver may attest publication using this acknowledgement:

```json
{
  "eventId": "durable-event-id",
  "status": "published",
  "externalId": "platform-record-id",
  "publishedUrl": "https://example.com/posts/platform-record-id"
}
```

Only a matching event ID, nonempty external ID and HTTPS publication URL produce
`published`. The UI labels this as a receiver receipt; it does not independently
verify platform acceptance. A `retract` event contains identifiers and sequence
only, never deleted text or media. It requires explicit per-target enablement
and an existing external mapping. HTTP acceptance alone does not prove that an
external copy was removed.

The queue makes up to five attempts with bounded exponential delays. An expired
claim after restart retries the same ID. Targets are independent. Pausing,
changing a target, deleting it or deleting a partition invalidates old work;
resuming does not replay it. Historical sending requires a user-selected batch.
`PauseAll` must run before workers start on a restored backup.

## HTTP integration

Private routes live under `/api/v1/journal` and use the existing authorizer.
Create/update/delete partitions, configure targets, list delivery states and
request selected historical sends through `server/api/v1/journal_partition.go`.
The editor confirms all attachments before calling `finishPartitionSave`.
Legacy user-wide webhook settings remain stored but their automatic dispatch
entrypoints are disabled; they must be explicitly configured as partition
targets before any private record can leave the journal.

Validation includes persistent SQLite service tests, cancellation and retry
races, HTTP receiver/signature tests, and authenticated owner-isolation tests.
Real social-platform credentials and platform-specific publication acceptance
are separate integration requirements.
