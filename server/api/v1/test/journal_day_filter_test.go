package test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/usememos/memos/proto/gen/api/v1"
)

// The journal's range helper emits CEL timestamp values, not API field names or
// bare integers. Exercise that wire contract through ListMemos and real SQLite.
func TestJournalDayTimestampFilterBoundaries(t *testing.T) {
	for _, tc := range []struct {
		date, timezone string
		hours          int
	}{
		{"2025-10-03", "Asia/Shanghai", 24},
		{"2026-03-08", "America/New_York", 23},
		{"2026-11-01", "America/New_York", 25},
	} {
		t.Run(tc.date, func(t *testing.T) {
			ctx := context.Background()
			ts := NewTestService(t)
			defer ts.Cleanup()
			owner, err := ts.CreateRegularUser(ctx, "journal-day-owner")
			require.NoError(t, err)
			ownerCtx := ts.CreateUserContext(ctx, owner.ID)
			zone, err := time.LoadLocation(tc.timezone)
			require.NoError(t, err)
			start, err := time.ParseInLocation("2006-01-02", tc.date, zone)
			require.NoError(t, err)
			end := start.AddDate(0, 0, 1)
			require.Equal(t, tc.hours, int(end.Sub(start).Hours()))
			filter := fmt.Sprintf(`(creator == "users/%s") && (created_ts >= timestamp(%d) && created_ts < timestamp(%d))`, owner.Username, start.Unix(), end.Unix())
			for _, state := range []apiv1.State{apiv1.State_NORMAL, apiv1.State_ARCHIVED} {
				for _, point := range []struct {
					label string
					at    time.Time
				}{
					{"before", start.Add(-time.Second)},
					{"start", start},
					{"last-second", end.Add(-time.Second)},
					{"next-day", end},
				} {
					memo, err := ts.Service.CreateMemo(ownerCtx, &apiv1.CreateMemoRequest{Memo: &apiv1.Memo{
						Content: point.label, Visibility: apiv1.Visibility_PRIVATE,
						CreateTime: timestamppb.New(point.at), UpdateTime: timestamppb.New(end.Add(time.Hour)),
					}})
					require.NoError(t, err)
					if state == apiv1.State_ARCHIVED {
						_, err = ts.Service.UpdateMemo(ownerCtx, &apiv1.UpdateMemoRequest{
							Memo: &apiv1.Memo{Name: memo.Name, State: state}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"state"}},
						})
						require.NoError(t, err)
					}
				}
				response, err := ts.Service.ListMemos(ownerCtx, &apiv1.ListMemosRequest{
					Filter: filter, State: state, PageSize: 100, OrderBy: "create_time desc",
				})
				require.NoError(t, err)
				require.Len(t, response.Memos, 2, "only original record dates inside the half-open day belong in this view")
				require.Equal(t, "last-second", response.Memos[0].Content)
				require.Equal(t, "start", response.Memos[1].Content)
			}
		})
	}
}
