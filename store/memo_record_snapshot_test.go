package store

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	storepb "github.com/EgoSay/kairos/proto/gen/store"
)

func TestMemoRecordSnapshotSharedClientVector(t *testing.T) {
	snapshot := MemoRecordSnapshot{Content: "今天 🌙\u2028<真实>", CreatedTs: 946684800, UpdatedTs: 946684899, Visibility: Private, SpaceUID: "room-1", RowStatus: Normal,
		Location: &storepb.MemoPayload_Location{Placeholder: "家\n灯", Latitude: math.Copysign(0, -1), Longitude: 121.4737}, AttachmentUIDs: []string{"z-last", "a-first"}}
	require.Equal(t, "fe104109c1ab6e895f33b9d719aea8d9b723aa4e076edede43b539ac6f20fef1", snapshot.Hash())
	snapshot.AttachmentUIDs = []string{"a-first", "z-last"}
	require.Equal(t, "fe104109c1ab6e895f33b9d719aea8d9b723aa4e076edede43b539ac6f20fef1", snapshot.Hash())
	snapshot.Location.Latitude = 0
	require.NotEqual(t, "fe104109c1ab6e895f33b9d719aea8d9b723aa4e076edede43b539ac6f20fef1", snapshot.Hash())
}
