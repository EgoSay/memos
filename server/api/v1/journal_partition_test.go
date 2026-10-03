package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"

	"github.com/usememos/memos/core/partition"
	"github.com/usememos/memos/internal/webhook"
	v1pb "github.com/usememos/memos/proto/gen/api/v1"
	"github.com/usememos/memos/store"
)

func TestJournalPartitionHTTPCompleteSaveAndIsolation(t *testing.T) {
	svc := newIntegrationService(t)
	owner := createSpaceTestUser(context.Background(), t, svc, "partition-owner", store.RoleUser)
	other := createSpaceTestUser(context.Background(), t, svc, "partition-other", store.RoleUser)
	currentID := owner.ID
	e := echo.New()
	group := e.Group("/api/v1/journal")
	group.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.SetRequest(c.Request().WithContext(userCtx(c.Request().Context(), currentID)))
			return next(c)
		}
	})
	svc.registerJournalPartitionRoutes(group)
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		var reader io.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			require.NoError(t, err)
			reader = bytes.NewReader(encoded)
		}
		r := httptest.NewRequest(method, "/api/v1/journal"+path, reader)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		return w
	}
	w := request("POST", "/partitions", map[string]any{"name": "Private moments"})
	require.Equal(t, 200, w.Code, w.Body.String())
	var p partition.Partition
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	memo, err := svc.CreateMemo(userCtx(context.Background(), owner.ID), &v1pb.CreateMemoRequest{Memo: &v1pb.Memo{Content: "Only explicitly selected words", Visibility: v1pb.Visibility_PRIVATE}})
	require.NoError(t, err)
	uid := strings.TrimPrefix(memo.Name, "memos/")
	received := make(chan partition.Payload, 2)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NotEmpty(t, r.Header.Get("webhook-id"))
		require.Equal(t, r.Header.Get("webhook-id"), r.Header.Get("Idempotency-Key"))
		var payload partition.Payload
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		received <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	require.NoError(t, webhook.ConfigurePrivateDestinationAllowlist([]string{"127.0.0.1"}))
	t.Cleanup(func() { require.NoError(t, webhook.ConfigurePrivateDestinationAllowlist(nil)) })
	w = request("POST", "/partitions/"+p.ID+"/targets", map[string]any{"name": "My receiver", "url": receiver.URL, "enabled": true, "fields": map[string]bool{"content": true}})
	require.Equal(t, 200, w.Code, w.Body.String())
	var target partition.Target
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &target))
	require.NotEmpty(t, target.SigningSecret)
	w = request("GET", "/partitions/"+p.ID+"/targets", nil)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), target.SigningSecret)
	require.NoError(t, svc.JournalPartitions.ProcessDue(context.Background()))
	require.Empty(t, received, "creating a target must not send existing records")
	w = request("PUT", "/memos/"+uid+"/partition", map[string]any{"partitionId": p.ID, "version": 0})
	require.Equal(t, 200, w.Code, w.Body.String())
	w = request("POST", "/memos/"+uid+"/send", map[string]any{"manual": false})
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, svc.JournalPartitions.ProcessDue(context.Background()))
	select {
	case payload := <-received:
		require.Equal(t, memo.Content, *payload.Content)
		require.Equal(t, int64(1), payload.Sequence)
		require.Nil(t, payload.RecordTime)
		require.Empty(t, payload.Media)
	default:
		t.Fatal("expected an HTTP delivery")
	}
	w = request("GET", "/deliveries", nil)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"status":"delivered"`)
	require.NotContains(t, w.Body.String(), `"status":"published"`)
	currentID = other.ID
	w = request("GET", "/partitions", nil)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), p.Name)
	w = request("POST", "/memos/"+uid+"/send", map[string]any{"manual": true})
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"deliveries":[]`)
	w = request("PUT", "/memos/"+uid+"/partition", map[string]any{"partitionId": p.ID, "version": 0})
	require.Equal(t, 404, w.Code)
	currentID = owner.ID
	w = request("DELETE", "/partitions/"+p.ID+"?version=1", nil)
	require.Equal(t, 204, w.Code, w.Body.String())
	kept, err := svc.Store.GetMemo(context.Background(), &store.FindMemo{UID: &uid})
	require.NoError(t, err)
	require.Equal(t, memo.Content, kept.Content)
}
