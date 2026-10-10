package insight

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/EgoSay/kairos/provider/ai"
)

func TestGenerateSendsOnlySelectedInputAndRejectsInventedReferences(t *testing.T) {
	var count atomic.Int32
	var invented atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer fixture-key", r.Header.Get("Authorization"))
		var body struct {
			Model    string                           `json:"model"`
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "fixture-model", body.Model)
		require.Len(t, body.Messages, 2)
		var sources []Source
		require.NoError(t, json.Unmarshal([]byte(body.Messages[1].Content), &sources))
		require.Equal(t, []Source{{Name: "memos/selected", Date: "2026-09-02", Text: "一个普通的下午"}}, sources)
		ref := "memos/selected"
		if invented.Load() {
			ref = "memos/unselected"
		}
		result, _ := json.Marshal(Result{Text: "这里记录了一个普通的下午。", Citations: []string{ref}})
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(result)}}}}))
	}))
	defer server.Close()
	config := ai.ProviderConfig{Endpoint: server.URL + "/v1", APIKey: "fixture-key"}
	sources := []Source{{Name: "memos/selected", Date: "2026-09-02", Text: "一个普通的下午"}}
	result, err := Generate(context.Background(), config, "fixture-model", sources)
	require.NoError(t, err)
	require.Equal(t, []string{"memos/selected"}, result.Citations)
	invented.Store(true)
	_, err = Generate(context.Background(), config, "fixture-model", sources)
	require.ErrorContains(t, err, "unselected")
	sources[0].Text = strings.Repeat("a", 60001)
	_, err = Generate(context.Background(), config, "fixture-model", sources)
	require.ErrorContains(t, err, "too long")
	require.Equal(t, int32(2), count.Load(), "overlong input must not be silently truncated or sent")
}

func TestGenerateCancellationAndProviderErrorsAreBounded(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Generate(ctx, ai.ProviderConfig{Endpoint: server.URL}, "model", []Source{{Name: "memos/a", Text: "a"}})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not stop the request")
	}
}

func TestGenerateDoesNotForwardSecretToRedirect(t *testing.T) {
	var received atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { received.Store(true) }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	_, err := Generate(context.Background(), ai.ProviderConfig{Endpoint: redirect.URL, APIKey: "private-key"}, "model", []Source{{Name: "memos/a", Text: "a"}})
	require.Error(t, err)
	require.False(t, received.Load())
	require.NotContains(t, err.Error(), "private-key")
}
