package journal_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/EgoSay/kairos/core/journal"
	storepb "github.com/EgoSay/kairos/proto/gen/store"
	"github.com/EgoSay/kairos/store"
	storetest "github.com/EgoSay/kairos/store/test"
)

func TestReviewFrozenSetRemovalAndCalendarTimezone(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewTestingStore(ctx, t)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	owner, err := s.CreateUser(ctx, &store.User{Username: "journal-owner", Role: store.RoleAdmin})
	require.NoError(t, err)
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	for n := range 7 {
		_, err := s.CreateMemo(ctx, &store.Memo{UID: fmt.Sprintf("memo-%d", n), CreatorID: owner.ID, Content: "ordinary", Visibility: store.Private, CreatedTs: now.AddDate(0, 0, -n-1).Unix()})
		require.NoError(t, err)
	}
	first, err := journal.DailyReview(ctx, s, owner.ID, today, "UTC")
	require.NoError(t, err)
	require.Len(t, first, 3)
	_, err = s.CreateMemo(ctx, &store.Memo{Visibility: store.Private, UID: "late-backdated", CreatorID: owner.ID, Content: "backdated", CreatedTs: now.AddDate(-1, 0, 0).Unix()})
	require.NoError(t, err)
	second, err := journal.DailyReview(ctx, s, owner.ID, today, "UTC")
	require.NoError(t, err)
	require.Equal(t, first, second)
	payload, _ := json.Marshal(journal.Preferences{ExcludedMemoNames: []string{first[0]}, Timezone: "UTC"})
	_, err = s.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner.ID, Kind: "preferences", Key: "main", Payload: payload}, 0)
	require.NoError(t, err)
	reduced, err := journal.DailyReview(ctx, s, owner.ID, today, "UTC")
	require.NoError(t, err)
	require.Equal(t, first[1:], reduced, "withdrawal never silently fills another record")
	// A frozen yesterday session remains readable after local midnight.
	oldKey := now.AddDate(0, 0, -1).Format("2006-01-02")
	frozen, _ := json.Marshal(first[1:])
	_, err = s.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner.ID, Kind: "daily-review", Key: oldKey + "@UTC", Payload: frozen}, 0)
	require.NoError(t, err)
	_, err = journal.DailyReview(ctx, s, owner.ID, oldKey, "UTC")
	require.NoError(t, err)
	start, _ := time.Parse(time.RFC3339, "2025-03-09T07:00:00Z") // DST transition in New York.
	m, err := s.CreateMemo(ctx, &store.Memo{Visibility: store.Private, UID: "dst-note", CreatorID: owner.ID, CreatedTs: start.Unix(), Content: "DST"})
	require.NoError(t, err)
	archived := store.Archived
	require.NoError(t, s.UpdateMemo(ctx, &store.UpdateMemo{ID: m.ID, RowStatus: &archived}))
	counts, err := journal.Calendar(ctx, s, owner.ID, "2025-03", "America/New_York")
	require.NoError(t, err)
	require.Equal(t, 1, counts["2025-03-09"])
	_, err = s.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner.ID, Kind: "trash", Key: m.UID, Payload: json.RawMessage(`{}`)}, 0)
	require.NoError(t, err)
	counts, err = journal.Calendar(ctx, s, owner.ID, "2025-03", "America/New_York")
	require.NoError(t, err)
	require.Empty(t, counts)
}

func TestShareSelectionAndSafeText(t *testing.T) {
	date, _ := time.Parse(time.RFC3339, "2026-10-02T16:15:00Z")
	m := &store.Memo{UID: "a", CreatedTs: date.Unix(), Payload: &storepb.MemoPayload{Tags: []string{"life/travel", "day"}}}
	f := journal.ShareFilter{From: "2026-10-03", To: "2026-10-03", Timezone: "Asia/Shanghai", Tags: []string{"life", "day"}, TagMode: "all", IncludeSubtags: true}
	require.NoError(t, f.Validate())
	require.True(t, f.Matches(m))
	f.IncludeSubtags = false
	require.False(t, f.Matches(m))
	f.Tags = nil
	f.MemoNames = []string{"memos/other"}
	require.False(t, f.Matches(m))
	safe := journal.ShareText(`![photo](https://tracker.example/pixel) [secret](/file/attachments/private/photo.jpg?token=x) <img src="https://track.example"> /memos/private`)
	require.NotContains(t, safe, "tracker.example")
	require.NotContains(t, safe, "track.example")
	require.NotContains(t, safe, "/file/")
	require.NotContains(t, safe, "/memos/")
}
