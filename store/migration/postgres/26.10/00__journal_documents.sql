-- Private journal resources: one row per preference, snapshot, share or delivery.
CREATE TABLE journal_document (
  owner_id INTEGER NOT NULL,
  kind VARCHAR(64) NOT NULL,
  document_key VARCHAR(128) NOT NULL,
  payload TEXT NOT NULL,
  version BIGINT NOT NULL,
  updated_ts BIGINT NOT NULL,
  PRIMARY KEY (owner_id, kind, document_key)
);
CREATE INDEX idx_journal_kind ON journal_document(kind, owner_id);
