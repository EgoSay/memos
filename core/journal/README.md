# journal

Owner-scoped reading preferences, stable daily review selection, record-date
calendar counts, explicit sharing filters and lifecycle withdrawal. Original
text stays in the existing Memo store. Generated observations and collection
share grants are separate versioned `journal_document` records.

- Daily review freezes at most three older originals for a local date. Reopening
  never adds replacements when a source is excluded or deleted. A session that
  crosses midnight can keep reading its previously frozen date.
- Calendar includes archived originals, excludes comments and trash, and uses
  a configured IANA timezone rather than fixed 24-hour date arithmetic.
- Sharing defaults to snapshots. HTTP handlers bind publishing to a digest of
  the exact preview, enforce owner and media access, and keep state-only changes
  separate from recapturing the snapshot. Imported history needs a fresh
  explicit preview before entering an existing dynamic grant.
- Withdrawal removes saved AI results referring to the source and removes both
  membership and frozen bytes from managed collection shares. Restoring an
  original does not restore its old grants.

All persistence uses owner-scoped compare-and-swap documents. HTTP auth,
passcodes, safe media transformation and provider calls remain transport or
provider responsibilities. These features never run an automatic full-archive
AI job or create prompts, streaks, goals or notification tasks.
