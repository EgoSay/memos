package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/usememos/memos/core/journal"
	v1pb "github.com/usememos/memos/proto/gen/api/v1"
	"github.com/usememos/memos/store"
)

func journalHTTP(ctx context.Context, t *testing.T, e *echo.Echo, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	return response
}
func shareFixture(t *testing.T) (*APIV1Service, *echo.Echo, context.Context, *v1pb.Memo) {
	t.Helper()
	s, ctx := newUploadTestService(t)
	e := echo.New()
	s.registerJournalShareRoutes(e.Group("/api/v1/journal"), e)
	memo, err := s.CreateMemo(ctx, &v1pb.CreateMemoRequest{Memo: &v1pb.Memo{Content: "original private record", Visibility: v1pb.Visibility_PRIVATE}})
	require.NoError(t, err)
	return s, e, ctx, memo
}
func sharePreview(ctx context.Context, t *testing.T, e *echo.Echo, request journalShareRequest) journalShareRequest {
	t.Helper()
	response := journalHTTP(ctx, t, e, "POST", "/api/v1/journal/shares/preview", request)
	require.Equal(t, 200, response.Code, response.Body.String())
	var data struct {
		PreviewDigest string `json:"previewDigest"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &data))
	require.NotEmpty(t, data.PreviewDigest)
	request.PreviewDigest = data.PreviewDigest
	return request
}
func readShare(t *testing.T, e *echo.Echo, summary map[string]any, passcode string) *httptest.ResponseRecorder {
	t.Helper()
	token := strings.TrimPrefix(summary["url"].(string), "/s/")
	return journalHTTP(context.Background(), t, e, "POST", "/api/v1/journal-shares/"+token, map[string]string{"passcode": passcode})
}
func TestJournalSharePreviewSnapshotPauseRotateAndWithdrawal(t *testing.T) {
	s, e, ctx, memo := shareFixture(t)
	req := journalShareRequest{Mode: "fixed", Title: "some days", Filter: journal.ShareFilter{Timezone: "UTC", MemoNames: []string{memo.Name}}, Passcode: "private-pass"}
	req = sharePreview(ctx, t, e, req)
	_, err := s.UpdateMemo(ctx, &v1pb.UpdateMemoRequest{Memo: &v1pb.Memo{Name: memo.Name, Content: "changed after preview"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"content"}}})
	require.NoError(t, err)
	stale := journalHTTP(ctx, t, e, "POST", "/api/v1/journal/shares", req)
	require.Equal(t, 409, stale.Code)
	req = sharePreview(ctx, t, e, req)
	response := journalHTTP(ctx, t, e, "POST", "/api/v1/journal/shares", req)
	require.Equal(t, 200, response.Code, response.Body.String())
	var summary map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &summary))
	denied := readShare(t, e, summary, "wrong")
	require.Equal(t, 404, denied.Code)
	require.NotContains(t, denied.Body.String(), "some days")
	visible := readShare(t, e, summary, "private-pass")
	require.Equal(t, 200, visible.Code, visible.Body.String())
	require.Contains(t, visible.Body.String(), "changed after preview")
	require.NotContains(t, visible.Body.String(), memo.Name)
	require.Contains(t, visible.Header().Get("Cache-Control"), "no-store")
	_, err = s.UpdateMemo(ctx, &v1pb.UpdateMemoRequest{Memo: &v1pb.Memo{Name: memo.Name, Content: "new private text must remain private"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"content"}}})
	require.NoError(t, err)
	path := "/api/v1/journal/shares/" + summary["id"].(string)
	for _, paused := range []bool{true, false} {
		response = journalHTTP(ctx, t, e, "PATCH", path, map[string]any{"version": summary["version"], "paused": paused})
		require.Equal(t, 200, response.Code, response.Body.String())
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &summary))
		read := readShare(t, e, summary, "private-pass")
		if paused {
			require.Equal(t, 404, read.Code)
		} else {
			require.Equal(t, 200, read.Code)
			require.Contains(t, read.Body.String(), "changed after preview")
			require.NotContains(t, read.Body.String(), "new private text")
		}
	}
	oldURL := summary["url"]
	response = journalHTTP(ctx, t, e, "POST", path+"/rotate", map[string]any{"version": summary["version"]})
	require.Equal(t, 200, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &summary))
	require.NotEqual(t, oldURL, summary["url"])
	require.Equal(t, 404, readShare(t, e, map[string]any{"url": oldURL}, "private-pass").Code)
	require.Equal(t, 200, readShare(t, e, summary, "private-pass").Code)
	stranger := createSpaceTestUser(context.Background(), t, s, "share-stranger", store.RoleUser)
	response = journalHTTP(userCtx(context.Background(), stranger.ID), t, e, "GET", path, nil)
	require.Equal(t, 404, response.Code)
	uid := strings.TrimPrefix(memo.Name, "memos/")
	require.Equal(t, 200, journalRecordRequest(ctx, s, "POST", "/memos/"+uid+"/trash").Code)
	require.Equal(t, 200, journalRecordRequest(ctx, s, "POST", "/trash/"+uid+"/restore").Code)
	visible = readShare(t, e, summary, "private-pass")
	require.Equal(t, 200, visible.Code)
	require.Contains(t, visible.Body.String(), `"items":[]`)
	response = journalHTTP(ctx, t, e, "GET", path, nil)
	require.NotContains(t, response.Body.String(), "changed after preview", "withdrawn snapshot bytes must be removed")
	require.Equal(t, 200, journalHTTP(ctx, t, e, "DELETE", path, nil).Code)
	require.Equal(t, 404, readShare(t, e, summary, "private-pass").Code)
}

func TestJournalDynamicSharesDoNotBackfillImportedHistory(t *testing.T) {
	s, e, ctx, memo := shareFixture(t)
	u, err := s.fetchCurrentUser(ctx)
	require.NoError(t, err)
	req := sharePreview(ctx, t, e, journalShareRequest{Mode: "dynamic", Filter: journal.ShareFilter{Timezone: "UTC", From: "2000-01-01"}})
	result := journalHTTP(ctx, t, e, "POST", "/api/v1/journal/shares", req)
	require.Equal(t, 200, result.Code, result.Body.String())
	var summary map[string]any
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &summary))
	ordinary, err := s.CreateMemo(ctx, &v1pb.CreateMemoRequest{Memo: &v1pb.Memo{Content: "new chosen range", Visibility: v1pb.Visibility_PRIVATE}})
	require.NoError(t, err)
	_, err = s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: u.ID, Kind: "provenance", Key: "imported", Payload: json.RawMessage(`{"imported":true}`)}, 0)
	require.NoError(t, err)
	_, err = s.Store.CreateMemo(ctx, &store.Memo{UID: "imported", CreatorID: u.ID, Content: "IMPORTED_SECRET", Visibility: store.Private, CreatedTs: time.Now().Unix()})
	require.NoError(t, err)
	view := readShare(t, e, summary, "")
	require.Equal(t, 200, view.Code, view.Body.String())
	require.Contains(t, view.Body.String(), memo.Content)
	require.Contains(t, view.Body.String(), ordinary.Content)
	require.NotContains(t, view.Body.String(), "IMPORTED_SECRET")
}

func TestJournalShareRejectsEmptyPublication(t *testing.T) {
	_, e, ctx, _ := shareFixture(t)
	req := sharePreview(ctx, t, e, journalShareRequest{Filter: journal.ShareFilter{Timezone: "UTC", MemoNames: []string{"memos/missing"}}})
	response := journalHTTP(ctx, t, e, "POST", "/api/v1/journal/shares", req)
	require.Equal(t, 400, response.Code, response.Body.String())
}
