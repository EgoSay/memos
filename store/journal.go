package store

import (
	"context"
	"encoding/json"

	"github.com/pkg/errors"
)

// ErrJournalConflict means another request changed the document first.
var ErrJournalConflict = errors.New("journal document changed; reload before retrying")

// JournalDocument stores one private journal resource. Memo text remains in memo.
// Versions provide compare-and-swap for preferences, snapshots and delivery leases.
type JournalDocument struct {
	OwnerID   int32           `json:"ownerId"`
	Kind      string          `json:"kind"`
	Key       string          `json:"key"`
	Payload   json.RawMessage `json:"payload"`
	Version   int64           `json:"version"`
	UpdatedTs int64           `json:"updatedTs"`
}

// FindJournalDocument bounds queries by owner and resource kind.
// A nil owner is reserved for trusted background processing.
type FindJournalDocument struct {
	OwnerID   *int32
	Kind      string
	KeyPrefix string
}

// GetJournalDocument returns nil for a missing owner-scoped resource.
func (s *Store) GetJournalDocument(ctx context.Context, ownerID int32, kind, key string) (*JournalDocument, error) {
	return s.driver.GetJournalDocument(ctx, ownerID, kind, key)
}

// ListJournalDocuments lists durable resource documents.
func (s *Store) ListJournalDocuments(ctx context.Context, find *FindJournalDocument) ([]*JournalDocument, error) {
	return s.driver.ListJournalDocuments(ctx, find)
}

// PutJournalDocument creates with expectedVersion zero, or updates with a version match.
func (s *Store) PutJournalDocument(ctx context.Context, doc *JournalDocument, expectedVersion int64) (*JournalDocument, error) {
	if doc == nil || doc.OwnerID <= 0 || len(doc.Kind) == 0 || len(doc.Kind) > 64 || len(doc.Key) == 0 || len(doc.Key) > 128 || !json.Valid(doc.Payload) || expectedVersion < 0 {
		return nil, errors.New("invalid journal document")
	}
	return s.driver.PutJournalDocument(ctx, doc, expectedVersion)
}

// DeleteJournalDocument deletes a matching version; -1 deliberately ignores version.
func (s *Store) DeleteJournalDocument(ctx context.Context, ownerID int32, kind, key string, expectedVersion int64) error {
	return s.driver.DeleteJournalDocument(ctx, ownerID, kind, key, expectedVersion)
}
