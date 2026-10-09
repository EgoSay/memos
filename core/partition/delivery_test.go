package partition

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"

	"github.com/usememos/memos/internal/webhook"
	"github.com/usememos/memos/store"
	storetest "github.com/usememos/memos/store/test"
)

type fixture struct {
	service   *Service
	db        *store.Store
	owner     int32
	partition *Partition
	memo      *Memo
	now       time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db := storetest.NewTestingStore(ctx, t)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	u, err := db.CreateUser(ctx, &store.User{Username: "partition-owner", Role: store.RoleUser, PasswordHash: "unused"})
	require.NoError(t, err)
	f := &fixture{db: db, owner: u.ID, memo: &Memo{UID: "original-memo", Content: "my actual words", Revision: "revision-1", Active: true}, now: time.Unix(1900000000, 0)}
	f.service = New(db, func(_ context.Context, owner int32, uid string, _ bool) (*Memo, error) {
		if owner != u.ID || uid != f.memo.UID {
			return nil, ErrNotFound
		}
		return f.memo, nil
	})
	f.service.Now = func() time.Time { return f.now }
	f.service.Send = func(context.Context, *webhook.Request) (*webhook.DeliveryReceipt, error) {
		return &webhook.DeliveryReceipt{StatusCode: 204}, nil
	}
	f.partition, err = f.service.SavePartition(ctx, u.ID, &Partition{Name: "Selected originals"})
	require.NoError(t, err)
	_, err = f.service.Assign(ctx, u.ID, f.memo.UID, f.partition.ID, 0)
	require.NoError(t, err)
	return f
}
func (f *fixture) target(t *testing.T, enabled bool) *Target {
	t.Helper()
	target, err := f.service.SaveTarget(context.Background(), f.owner, &Target{PartitionID: f.partition.ID, Name: "Receiver", URL: "https://8.8.8.8/hook", Enabled: enabled, Fields: Fields{Content: true}})
	require.NoError(t, err)
	return target
}
func (f *fixture) commit(t *testing.T) []*Delivery {
	t.Helper()
	values, err := f.service.Commit(context.Background(), f.owner, f.memo.UID, false)
	require.NoError(t, err)
	return values
}
func (f *fixture) process(t *testing.T) {
	t.Helper()
	require.NoError(t, f.service.ProcessDue(context.Background()))
}
func (f *fixture) deliveries(t *testing.T) []*Delivery {
	t.Helper()
	d, err := f.service.ListDeliveries(context.Background(), f.owner)
	require.NoError(t, err)
	return d
}

func TestPartitionTargetsNeverReplayHistory(t *testing.T) {
	f := newFixture(t)
	target := f.target(t, false)
	require.Empty(t, f.commit(t))
	require.Empty(t, f.deliveries(t))
	target.Enabled = true
	_, err := f.service.SaveTarget(context.Background(), f.owner, target)
	require.NoError(t, err)
	require.Empty(t, f.deliveries(t), "enabling is not a historical send")
	require.Len(t, f.commit(t), 1)
	calls := 0
	f.service.Send = func(_ context.Context, r *webhook.Request) (*webhook.DeliveryReceipt, error) {
		calls++
		var p Payload
		require.NoError(t, json.Unmarshal(r.Payload.(json.RawMessage), &p))
		require.Equal(t, f.memo.Content, *p.Content)
		require.Nil(t, p.RecordTime)
		require.Empty(t, p.Media)
		return &webhook.DeliveryReceipt{StatusCode: 204}, nil
	}
	f.process(t)
	f.process(t)
	require.Equal(t, 1, calls)
	require.Equal(t, "delivered", f.deliveries(t)[0].Status)
}

func TestPartitionImportSuspensionDoesNotRetractOrReplay(t *testing.T) {
	f := newFixture(t)
	target := f.target(t, true)
	target.Retract = true
	_, err := f.service.SaveTarget(context.Background(), f.owner, target)
	require.NoError(t, err)
	_, err = f.service.put(context.Background(), f.owner, ExternalKind, RecordTargetKey(target.ID, f.memo.UID), &External{TargetID: target.ID, MemoUID: f.memo.UID, ExternalID: "existing-post"}, 0)
	require.NoError(t, err)
	f.commit(t)
	require.NoError(t, f.service.SuspendMemo(context.Background(), f.owner, f.memo.UID))
	mapping, err := f.service.GetMapping(context.Background(), f.owner, f.memo.UID)
	require.NoError(t, err)
	require.True(t, mapping.Suspended)
	deliveries := f.deliveries(t)
	require.Len(t, deliveries, 1)
	require.Equal(t, "upsert", deliveries[0].Event)
	require.Equal(t, "cancelled", deliveries[0].Status)
	f.memo.Content = "restored source"
	f.memo.Revision = "restored-version"
	require.Empty(t, f.commit(t), "ordinary edit after import must not silently resume")
	calls := 0
	f.service.Send = func(context.Context, *webhook.Request) (*webhook.DeliveryReceipt, error) {
		calls++
		return &webhook.DeliveryReceipt{StatusCode: 204}, nil
	}
	f.process(t)
	require.Zero(t, calls, "restoration cannot trigger retract either")
}

func TestPartitionPendingDeliverySurvivesServiceRestart(t *testing.T) {
	f := newFixture(t)
	f.target(t, true)
	event := f.commit(t)[0]
	// Save a lease as if a process died after claiming but before persisting ACK.
	event.Status = "delivering"
	event.LeaseUntilTs = f.now.Add(-time.Minute).Unix()
	_, err := f.service.put(context.Background(), f.owner, DeliveryKind, event.ID, event, event.Version)
	require.NoError(t, err)
	restarted := New(f.db, f.service.ReadMemo)
	restarted.Now = f.service.Now
	restarted.Send = func(_ context.Context, r *webhook.Request) (*webhook.DeliveryReceipt, error) {
		require.Equal(t, event.ID, r.MessageID)
		return &webhook.DeliveryReceipt{StatusCode: 204}, nil
	}
	require.NoError(t, restarted.ProcessDue(context.Background()))
	require.Equal(t, "delivered", f.deliveries(t)[0].Status)
}

func TestPartitionTargetsRetryIndependently(t *testing.T) {
	f := newFixture(t)
	first := f.target(t, true)
	second := f.target(t, true)
	events := f.commit(t)
	require.Len(t, events, 2)
	calls := map[string]int{}
	failedID := ""
	for _, d := range events {
		if d.TargetID == second.ID {
			failedID = d.ID
		}
	}
	f.service.Send = func(_ context.Context, r *webhook.Request) (*webhook.DeliveryReceipt, error) {
		calls[r.MessageID]++
		if r.MessageID == failedID && calls[r.MessageID] == 1 {
			return nil, errors.New("timeout")
		}
		return &webhook.DeliveryReceipt{StatusCode: 204}, nil
	}
	f.process(t)
	f.now = f.now.Add(10 * time.Minute)
	f.process(t)
	for _, d := range f.deliveries(t) {
		require.Equal(t, "delivered", d.Status)
		if d.TargetID == first.ID {
			require.Equal(t, 1, calls[d.ID])
		} else {
			require.Equal(t, 2, calls[d.ID])
		}
	}
}

func TestPartitionRechecksPauseRevisionAndDeletion(t *testing.T) {
	for _, change := range []string{"pause", "revision", "delete", "unassign", "partition-delete"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			target := f.target(t, true)
			f.commit(t)
			switch change {
			case "pause":
				target.Enabled = false
				_, err := f.service.SaveTarget(context.Background(), f.owner, target)
				require.NoError(t, err)
			case "revision":
				f.memo.Revision = "newer-version"
			case "delete":
				f.memo.Active = false
				require.NoError(t, f.service.CancelMemo(context.Background(), f.owner, f.memo.UID))
			case "unassign":
				m, err := f.service.GetMapping(context.Background(), f.owner, f.memo.UID)
				require.NoError(t, err)
				_, err = f.service.Assign(context.Background(), f.owner, f.memo.UID, "", m.Version)
				require.NoError(t, err)
			case "partition-delete":
				require.NoError(t, f.service.DeletePartition(context.Background(), f.owner, f.partition.ID, f.partition.Version))
				m, err := f.service.GetMapping(context.Background(), f.owner, f.memo.UID)
				require.NoError(t, err)
				require.Empty(t, m.PartitionID)
				require.Equal(t, "my actual words", f.memo.Content)
			default:
				t.Fatal("unknown change")
			}
			f.service.Send = func(context.Context, *webhook.Request) (*webhook.DeliveryReceipt, error) {
				t.Fatal("revoked original must not leave the process")
				return nil, nil
			}
			f.process(t)
			require.Equal(t, "cancelled", f.deliveries(t)[0].Status)
		})
	}
}

func TestPartitionChangesRequireExplicitSendByDefault(t *testing.T) {
	f := newFixture(t)
	f.target(t, true)
	f.commit(t)
	f.process(t)
	f.memo.Revision = "revision-2"
	f.memo.Content = "changed words"
	changed := f.commit(t)
	require.Len(t, changed, 1)
	require.Equal(t, "local_changed", changed[0].Status)
	f.process(t)
	_, err := f.service.Retry(context.Background(), f.owner, changed[0].ID)
	require.NoError(t, err)
	f.process(t)
	for _, d := range f.deliveries(t) {
		require.Equal(t, "delivered", d.Status)
	}
}

func TestPartitionRestoreDoesNotReactivateMapping(t *testing.T) {
	f := newFixture(t)
	f.target(t, true)
	f.commit(t)
	require.NoError(t, f.service.CancelMemo(context.Background(), f.owner, f.memo.UID))
	f.memo.Active = true
	f.memo.Revision = "restored-version"
	require.Empty(t, f.commit(t))
	require.NoError(t, f.service.PauseAll(context.Background()))
	targets, err := f.service.ListTargets(context.Background(), f.owner, "")
	require.NoError(t, err)
	require.False(t, targets[0].Enabled)
}

func TestPartitionRetryIsBounded(t *testing.T) {
	f := newFixture(t)
	f.target(t, true)
	f.commit(t)
	f.service.Send = func(context.Context, *webhook.Request) (*webhook.DeliveryReceipt, error) {
		return nil, errors.New("private receiver error body")
	}
	for range 8 {
		f.process(t)
		f.now = f.now.Add(time.Hour)
	}
	d := f.deliveries(t)[0]
	require.Equal(t, "failed", d.Status)
	require.Equal(t, 5, d.Attempts)
	require.NotContains(t, d.LastError, "private receiver")
}

func TestPartitionPublishedRequiresMatchingReceipt(t *testing.T) {
	for _, matching := range []bool{false, true} {
		t.Run(map[bool]string{true: "matching", false: "mismatch"}[matching], func(t *testing.T) {
			f := newFixture(t)
			f.target(t, true)
			d := f.commit(t)[0]
			f.service.Send = func(_ context.Context, r *webhook.Request) (*webhook.DeliveryReceipt, error) {
				id := "wrong"
				if matching {
					id = r.MessageID
				}
				body, _ := json.Marshal(Receipt{EventID: id, Status: "published", ExternalID: "post-1", PublishedURL: "https://example.com/posts/1"})
				return &webhook.DeliveryReceipt{StatusCode: 200, Body: body}, nil
			}
			f.process(t)
			result := f.deliveries(t)[0]
			require.Equal(t, d.ID, result.ID)
			if matching {
				require.Equal(t, "published", result.Status)
			} else {
				require.Equal(t, "delivered", result.Status)
				require.Empty(t, result.PublishedURL)
			}
		})
	}
}

func TestPartitionPauseCancelsInflightRequest(t *testing.T) {
	f := newFixture(t)
	target := f.target(t, true)
	f.commit(t)
	started := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	f.service.Send = func(ctx context.Context, _ *webhook.Request) (*webhook.DeliveryReceipt, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- f.service.ProcessDue(context.Background()) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	target.Enabled = false
	_, err := f.service.SaveTarget(context.Background(), f.owner, target)
	require.NoError(t, err)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("pause did not cancel request")
	}
	require.NoError(t, <-done)
	require.Equal(t, "cancelled", f.deliveries(t)[0].Status)
}

func TestPartitionRetractionHasNoDeletedContent(t *testing.T) {
	f := newFixture(t)
	target := f.target(t, true)
	target.Retract = true
	_, err := f.service.SaveTarget(context.Background(), f.owner, target)
	require.NoError(t, err)
	f.service.Send = func(_ context.Context, r *webhook.Request) (*webhook.DeliveryReceipt, error) {
		body, _ := json.Marshal(Receipt{EventID: r.MessageID, Status: "published", ExternalID: "external-1", PublishedURL: "https://example.com/post/1"})
		return &webhook.DeliveryReceipt{StatusCode: 200, Body: body}, nil
	}
	f.commit(t)
	f.process(t)
	require.NoError(t, f.service.CancelMemo(context.Background(), f.owner, f.memo.UID))
	f.memo.Active = false
	f.service.Send = func(_ context.Context, r *webhook.Request) (*webhook.DeliveryReceipt, error) {
		var p Payload
		require.NoError(t, json.Unmarshal(r.Payload.(json.RawMessage), &p))
		require.Equal(t, "retract", p.Event)
		require.Equal(t, "external-1", p.ExternalID)
		require.Nil(t, p.Content)
		require.Nil(t, p.RecordTime)
		require.Empty(t, p.Media)
		require.Greater(t, p.Sequence, int64(1))
		require.NotContains(t, string(r.Payload.(json.RawMessage)), f.memo.Content)
		return &webhook.DeliveryReceipt{StatusCode: 204}, nil
	}
	f.process(t)
}
