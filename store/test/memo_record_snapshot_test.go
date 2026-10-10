package test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	storepb "github.com/EgoSay/kairos/proto/gen/store"
	"github.com/EgoSay/kairos/store"
)

// This suite runs under DRIVER=sqlite/mysql/postgres. Both writers retain the
// exact text but change metadata, exercising the transaction's full snapshot.
func TestMemoRecordCASConcurrentMetadataWriters(t *testing.T) {
	ctx := context.Background()
	s := NewTestingStore(ctx, t)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	owner, err := createTestingHostUser(ctx, s)
	require.NoError(t, err)
	for _, field := range []string{"date", "location"} {
		t.Run(field, func(t *testing.T) {
			memo, err := s.CreateMemo(ctx, &store.Memo{UID: "cas-" + field, CreatorID: owner.ID, Content: "same text", Visibility: store.Private})
			require.NoError(t, err)
			hash := store.MemoRecordSnapshot{Content: memo.Content, CreatedTs: memo.CreatedTs, UpdatedTs: memo.UpdatedTs, Visibility: memo.Visibility, RowStatus: memo.RowStatus}.Hash()
			start := make(chan struct{})
			result := make(chan error, 2)
			for index := range 2 {
				go func(index int) {
					update := &store.UpdateMemo{ID: memo.ID}
					if field == "date" {
						date := memo.CreatedTs - int64(index+1)*86400
						update.CreatedTs = &date
					} else {
						update.Payload = &storepb.MemoPayload{Location: &storepb.MemoPayload_Location{Latitude: float64(index + 1)}}
					}
					<-start
					result <- s.ApplyMemoMutation(ctx, &store.MemoMutation{MemoID: memo.ID, MemoCreatorID: memo.CreatorID, ExpectedMemoContent: memo.Content, ExpectedRecordHash: hash, MemoUpdate: update})
				}(index)
			}
			close(start)
			successes := 0
			for range 2 {
				err := <-result
				if err == nil {
					successes++
				} else {
					require.ErrorIs(t, err, store.ErrMemoMutationConflict)
				}
			}
			require.Equal(t, 1, successes)
		})
	}
}

func TestAttachmentCreatePreservesOriginalDates(t *testing.T) {
	ctx := context.Background()
	s := NewTestingStore(ctx, t)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	owner, err := createTestingHostUser(ctx, s)
	require.NoError(t, err)
	attachment, err := s.CreateAttachment(ctx, &store.Attachment{UID: "dated-original", CreatorID: owner.ID, Filename: "old-audio.wav", Type: "audio/wav", CreatedTs: 946684800, UpdatedTs: 946685000, Blob: []byte("original")})
	require.NoError(t, err)
	require.Equal(t, int64(946684800), attachment.CreatedTs)
	require.Equal(t, int64(946685000), attachment.UpdatedTs)
	restored, err := s.GetAttachment(ctx, &store.FindAttachment{UID: &attachment.UID})
	require.NoError(t, err)
	require.Equal(t, attachment.CreatedTs, restored.CreatedTs)
	require.Equal(t, attachment.UpdatedTs, restored.UpdatedTs)
}
