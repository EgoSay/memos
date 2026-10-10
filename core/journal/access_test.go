package journal_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/EgoSay/kairos/core/journal"
	"github.com/EgoSay/kairos/store"
	storetest "github.com/EgoSay/kairos/store/test"
)

func TestJournalReadRejectsFormerSpaceMemberAndKeepsPrivateOriginals(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewTestingStore(ctx, t)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	owner, err := s.CreateUser(ctx, &store.User{Username: "former-author", Role: store.RoleUser})
	require.NoError(t, err)
	admin, err := s.CreateUser(ctx, &store.User{Username: "space-administrator", Role: store.RoleUser})
	require.NoError(t, err)
	space, err := s.CreateSpace(ctx, &store.Space{UID: "collaboration", Title: "Earlier collaboration"}, admin.ID)
	require.NoError(t, err)
	_, err = s.CreateSpaceInvitation(ctx, &store.SpaceInvitation{SpaceID: space.ID, UserID: owner.ID, Role: store.SpaceMemberRoleUser}, admin.ID)
	require.NoError(t, err)
	_, err = s.AcceptSpaceInvitation(ctx, &store.AcceptSpaceInvitation{SpaceID: space.ID, UserID: owner.ID}, owner.ID)
	require.NoError(t, err)
	assigned, err := s.CreateMemo(ctx, &store.Memo{UID: "space-original", CreatorID: owner.ID, SpaceID: &space.ID, Visibility: store.SpaceAudience, Content: "Only while membership grants access"})
	require.NoError(t, err)
	private, err := s.CreateMemo(ctx, &store.Memo{UID: "private-original", CreatorID: owner.ID, Visibility: store.Private, Content: "My personal record"})
	require.NoError(t, err)
	_, err = journal.OwnedMemo(ctx, s, owner.ID, "memos/"+assigned.UID)
	require.NoError(t, err)
	before, err := journal.MemoNames(ctx, s, owner.ID, true)
	require.NoError(t, err)
	require.Len(t, before, 2)
	require.NoError(t, s.DeleteSpaceMember(ctx, &store.DeleteSpaceMember{SpaceID: space.ID, UserID: owner.ID}, admin.ID))
	_, err = journal.OwnedMemo(ctx, s, owner.ID, "memos/"+assigned.UID)
	require.Error(t, err, "AI and fixed shares must recheck current Space membership")
	after, err := journal.MemoNames(ctx, s, owner.ID, true)
	require.NoError(t, err)
	require.Len(t, after, 1, "dynamic selection, calendar, review and export cannot bypass membership")
	require.Equal(t, private.UID, after[0].UID)
	readable, err := journal.OwnedMemo(ctx, s, owner.ID, "memos/"+private.UID)
	require.NoError(t, err)
	require.Equal(t, private.Content, readable.Content)
	_, err = journal.OwnedMemo(ctx, s, admin.ID, "memos/"+private.UID)
	require.Error(t, err, "journal selection remains creator-scoped")
}
