# Recoverable journal records

This package defines the 30-day retention window and the persisted revision/trash payloads. Journal document keys are `revision: memoUID_revisionID` and `trash: memoUID`, scoped by owner. A revision contains the original memo's ProtoJSON snapshot; original attachment bytes are retained separately in the instance data directory.

`server/api/v1/journal_record.go` owns owner-only routes, retention cleanup and restoration. Moving to recently deleted creates a tombstone before withdrawing the memo, revokes shares and queued delivery, and prevents ordinary reads. Restoring is private and does not reactivate sharing or delivery. Permanent deletion removes current and historical originals after the memo database transaction commits. Revision restoration preserves the current version and checks the complete current record version before applying the selected snapshot.

`store.MemoRecordSnapshot` is the full record comparison token used by journal clients. Each database driver verifies it inside the memo mutation transaction. The Go/TypeScript shared vector and metadata concurrency tests define the protocol; native clients without this optional header keep their existing API contract.
