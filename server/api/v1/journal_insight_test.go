package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	v1pb "github.com/usememos/memos/proto/gen/api/v1"
	storepb "github.com/usememos/memos/proto/gen/store"
	"github.com/usememos/memos/store"
)

func TestJournalInsightConsentOwnerScopeSaveStaleAndDeletion(t *testing.T) {
	s, ctx := newUploadTestService(t)
	e := echo.New()
	s.registerJournalInsightRoutes(e.Group("/api/v1/journal"))
	selected, err := s.CreateMemo(ctx, &v1pb.CreateMemoRequest{Memo: &v1pb.Memo{Content: "selected words", Visibility: v1pb.Visibility_PRIVATE}})
	require.NoError(t, err)
	other := createSpaceTestUser(context.Background(), t, s, "insight-stranger", store.RoleUser)
	stranger, err := s.CreateMemo(userCtx(context.Background(), other.ID), &v1pb.CreateMemoRequest{Memo: &v1pb.Memo{Content: "FOREIGN_SECRET", Visibility: v1pb.Visibility_PRIVATE}})
	require.NoError(t, err)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), selected.Content)
		require.NotContains(t, string(body), stranger.Content)
		generated, _ := json.Marshal(map[string]any{"text": "一段安静的记忆", "citations": []string{selected.Name}})
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(generated)}}}}))
	}))
	defer provider.Close()
	_, err = s.Store.UpsertInstanceSetting(ctx, &storepb.InstanceSetting{Key: storepb.InstanceSettingKey_AI, Value: &storepb.InstanceSetting_AiSetting{AiSetting: &storepb.InstanceAISetting{Providers: []*storepb.AIProviderConfig{{Id: "fixture", Type: storepb.AIProviderType_OPENAI, Endpoint: provider.URL, ApiKey: "fixture-secret"}}}}})
	require.NoError(t, err)
	settings := journalHTTP(ctx, t, e, "GET", "/api/v1/journal/ai/config", nil)
	require.Equal(t, 200, settings.Code)
	require.NotContains(t, settings.Body.String(), "fixture-secret")
	require.Zero(t, calls.Load())
	configured := journalHTTP(ctx, t, e, "PUT", "/api/v1/journal/ai/config", journalAIConfig{ProviderID: "fixture", Model: "fixture-model"})
	require.Equal(t, 200, configured.Code, configured.Body.String())
	require.Zero(t, calls.Load())
	rejected := journalHTTP(ctx, t, e, "POST", "/api/v1/journal/insights/generate", map[string]any{"memoNames": []string{stranger.Name}})
	require.NotEqual(t, 200, rejected.Code)
	require.Zero(t, calls.Load())
	generated := journalHTTP(ctx, t, e, "POST", "/api/v1/journal/insights/generate", map[string]any{"memoNames": []string{selected.Name}})
	require.Equal(t, 200, generated.Code, generated.Body.String())
	require.Equal(t, int32(1), calls.Load())
	listed := journalHTTP(ctx, t, e, "GET", "/api/v1/journal/insights", nil)
	require.JSONEq(t, `[]`, listed.Body.String(), "observations are transient until explicitly saved")
	var result journalInsightResult
	require.NoError(t, json.Unmarshal(generated.Body.Bytes(), &result))
	saved := journalHTTP(ctx, t, e, "POST", "/api/v1/journal/insights", result)
	require.Equal(t, 201, saved.Code, saved.Body.String())
	_, err = s.UpdateMemo(ctx, &v1pb.UpdateMemoRequest{Memo: &v1pb.Memo{Name: selected.Name, Content: "edited source"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"content"}}})
	require.NoError(t, err)
	listed = journalHTTP(ctx, t, e, "GET", "/api/v1/journal/insights", nil)
	require.Contains(t, listed.Body.String(), `"stale":true`)
	require.Equal(t, 200, journalRecordRequest(ctx, s, "POST", "/memos/"+strings.TrimPrefix(selected.Name, "memos/")+"/trash").Code)
	listed = journalHTTP(ctx, t, e, "GET", "/api/v1/journal/insights", nil)
	require.JSONEq(t, `[]`, listed.Body.String())
	require.Equal(t, int32(1), calls.Load())
}
