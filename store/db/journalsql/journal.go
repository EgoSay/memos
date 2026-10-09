// Package journalsql implements the shared SQL contract for private journal metadata.
package journalsql

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/usememos/memos/store"
)

// Repository binds portable queries to a driver's placeholder convention.
type Repository struct {
	DB       *sql.DB
	Postgres bool
}

func (r Repository) bind(query string) string {
	if !r.Postgres {
		return query
	}
	n := 0
	return queryWithArgs(query, &n)
}

func queryWithArgs(query string, n *int) string {
	parts := strings.Split(query, "?")
	var out strings.Builder
	for i, part := range parts {
		if i > 0 {
			*n++
			out.WriteString("$" + strconv.Itoa(*n))
		}
		out.WriteString(part)
	}
	return out.String()
}

// Get returns an owner-scoped document or nil.
func (r Repository) Get(ctx context.Context, ownerID int32, kind, key string) (*store.JournalDocument, error) {
	doc := &store.JournalDocument{}
	var payload string
	err := r.DB.QueryRowContext(ctx, r.bind("SELECT owner_id, kind, document_key, payload, version, updated_ts FROM journal_document WHERE owner_id = ? AND kind = ? AND document_key = ?"), ownerID, kind, key).Scan(&doc.OwnerID, &doc.Kind, &doc.Key, &payload, &doc.Version, &doc.UpdatedTs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "get journal document")
	}
	doc.Payload = []byte(payload)
	return doc, nil
}

// List lists one kind, optionally restricted to an owner and literal key prefix.
func (r Repository) List(ctx context.Context, find *store.FindJournalDocument) ([]*store.JournalDocument, error) {
	query := "SELECT owner_id, kind, document_key, payload, version, updated_ts FROM journal_document WHERE kind = ?"
	args := []any{find.Kind}
	if find.OwnerID != nil {
		query += " AND owner_id = ?"
		args = append(args, *find.OwnerID)
	}
	query += " ORDER BY owner_id, document_key"
	rows, err := r.DB.QueryContext(ctx, r.bind(query), args...)
	if err != nil {
		return nil, errors.Wrap(err, "list journal documents")
	}
	defer rows.Close()
	result := []*store.JournalDocument{}
	for rows.Next() {
		doc := &store.JournalDocument{}
		var payload string
		if err := rows.Scan(&doc.OwnerID, &doc.Kind, &doc.Key, &payload, &doc.Version, &doc.UpdatedTs); err != nil {
			return nil, errors.Wrap(err, "scan journal document")
		}
		if find.KeyPrefix != "" && !strings.HasPrefix(doc.Key, find.KeyPrefix) {
			continue
		}
		doc.Payload = []byte(payload)
		result = append(result, doc)
	}
	return result, rows.Err()
}

// Put uses a database-level version comparison so processes cannot silently overwrite.
func (r Repository) Put(ctx context.Context, doc *store.JournalDocument, expected int64) (*store.JournalDocument, error) {
	copy := *doc
	copy.Version = expected + 1
	copy.UpdatedTs = time.Now().Unix()
	if expected == 0 {
		_, err := r.DB.ExecContext(ctx, r.bind("INSERT INTO journal_document(owner_id,kind,document_key,payload,version,updated_ts) VALUES(?,?,?,?,?,?)"), copy.OwnerID, copy.Kind, copy.Key, string(copy.Payload), copy.Version, copy.UpdatedTs)
		if err != nil {
			existing, readErr := r.Get(ctx, doc.OwnerID, doc.Kind, doc.Key)
			if readErr == nil && existing != nil {
				return nil, store.ErrJournalConflict
			}
			return nil, errors.Wrap(err, "create journal document")
		}
	} else {
		res, err := r.DB.ExecContext(ctx, r.bind("UPDATE journal_document SET payload=?,version=?,updated_ts=? WHERE owner_id=? AND kind=? AND document_key=? AND version=?"), string(copy.Payload), copy.Version, copy.UpdatedTs, copy.OwnerID, copy.Kind, copy.Key, expected)
		if err != nil {
			return nil, errors.Wrap(err, "update journal document")
		}
		count, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if count != 1 {
			return nil, store.ErrJournalConflict
		}
	}
	return &copy, nil
}

// Delete removes only the requested owner's document.
func (r Repository) Delete(ctx context.Context, ownerID int32, kind, key string, expected int64) error {
	query := "DELETE FROM journal_document WHERE owner_id=? AND kind=? AND document_key=?"
	args := []any{ownerID, kind, key}
	if expected >= 0 {
		query += " AND version=?"
		args = append(args, expected)
	}
	res, err := r.DB.ExecContext(ctx, r.bind(query), args...)
	if err != nil {
		return errors.Wrap(err, "delete journal document")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 && expected >= 0 {
		return store.ErrJournalConflict
	}
	return nil
}
