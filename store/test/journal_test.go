package test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"

	"github.com/usememos/memos/store"
)

func TestJournalDocumentsOwnerIsolationAndConcurrentCAS(t *testing.T) {
	ctx := context.Background()
	s := NewTestingStore(ctx, t)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	owner, err := createTestingHostUser(ctx, s)
	require.NoError(t, err)
	doc, err := s.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner.ID, Kind: "preferences", Key: "main", Payload: json.RawMessage(`{"version":"initial"}`)}, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), doc.Version)
	_, err = s.PutJournalDocument(ctx, doc, 0)
	require.ErrorIs(t, err, store.ErrJournalConflict)
	unrelated, err := s.GetJournalDocument(ctx, owner.ID+1, "preferences", "main")
	require.NoError(t, err)
	require.Nil(t, unrelated)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			candidate := *doc
			candidate.Payload = json.RawMessage(`{"version":"updated"}`)
			_, err := s.PutJournalDocument(ctx, &candidate, 1)
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, store.ErrJournalConflict) {
				t.Errorf("unexpected CAS error: %v", err)
			}
		})
	}
	wg.Wait()
	require.Equal(t, int32(1), winners.Load())
	latest, err := s.GetJournalDocument(ctx, owner.ID, "preferences", "main")
	require.NoError(t, err)
	require.Equal(t, int64(2), latest.Version)
	require.ErrorIs(t, s.DeleteJournalDocument(ctx, owner.ID, "preferences", "main", 1), store.ErrJournalConflict)
	require.NoError(t, s.DeleteJournalDocument(ctx, owner.ID, "preferences", "main", 2))
	removed, err := s.GetJournalDocument(ctx, owner.ID, "preferences", "main")
	require.NoError(t, err)
	require.Nil(t, removed)
}

func TestJournalSchemaUpgradePreservesBaselineAndAccountDeletionCleansDocuments(t *testing.T) {
	ctx := context.Background()
	s := NewTestingStore(ctx, t)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	owner, err := createTestingHostUser(ctx, s)
	require.NoError(t, err)
	memo, err := s.CreateMemo(ctx, &store.Memo{UID: "legacy-life", CreatorID: owner.ID, Content: "original words", Visibility: store.Private})
	require.NoError(t, err)
	_, err = s.GetDriver().GetDB().ExecContext(ctx, "DROP TABLE journal_document")
	require.NoError(t, err)
	setSchemaVersion(ctx, t, s, "0.31.8")
	require.NoError(t, s.Migrate(ctx))
	current, err := s.GetMemo(ctx, &store.FindMemo{ID: &memo.ID})
	require.NoError(t, err)
	require.Equal(t, memo.Content, current.Content)
	_, err = s.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner.ID, Kind: "insight", Key: "private", Payload: json.RawMessage(`{"text":"derived personal material"}`)}, 0)
	require.NoError(t, err)
	_, err = s.DeleteUser(store.WithDeleteUserFailpoint(ctx, store.DeleteUserFailpointBeforeCommit), &store.DeleteUser{ID: owner.ID})
	require.Error(t, err)
	retained, err := s.GetJournalDocument(ctx, owner.ID, "insight", "private")
	require.NoError(t, err)
	require.NotNil(t, retained, "failed account deletion must roll back every journal document")
	_, err = s.DeleteUser(ctx, &store.DeleteUser{ID: owner.ID})
	require.NoError(t, err)
	removed, err := s.GetJournalDocument(ctx, owner.ID, "insight", "private")
	require.NoError(t, err)
	require.Nil(t, removed)
}
