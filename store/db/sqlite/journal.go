package sqlite

import (
	"context"

	"github.com/usememos/memos/store"
	"github.com/usememos/memos/store/db/journalsql"
)

func (d *DB) journalRepository() journalsql.Repository {
	return journalsql.Repository{DB: d.db, Postgres: false}
}
func (d *DB) GetJournalDocument(ctx context.Context, ownerID int32, kind, key string) (*store.JournalDocument, error) {
	return d.journalRepository().Get(ctx, ownerID, kind, key)
}
func (d *DB) ListJournalDocuments(ctx context.Context, find *store.FindJournalDocument) ([]*store.JournalDocument, error) {
	return d.journalRepository().List(ctx, find)
}
func (d *DB) PutJournalDocument(ctx context.Context, doc *store.JournalDocument, expected int64) (*store.JournalDocument, error) {
	return d.journalRepository().Put(ctx, doc, expected)
}
func (d *DB) DeleteJournalDocument(ctx context.Context, ownerID int32, kind, key string, expected int64) error {
	return d.journalRepository().Delete(ctx, ownerID, kind, key, expected)
}
