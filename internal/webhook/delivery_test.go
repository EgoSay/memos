package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeliverPreservesEventIDAcrossRetries(t *testing.T) {
	resetPrivateDestinationPolicy(t)
	require.NoError(t, ConfigurePrivateDestinationAllowlist([]string{"127.0.0.1"}))
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "event-17", r.Header.Get("webhook-id"))
		require.Equal(t, "event-17", r.Header.Get("Idempotency-Key"))
		require.NotEmpty(t, r.Header.Get("webhook-signature"))
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	request := &Request{URL: server.URL, MessageID: "event-17", SigningSecret: "secret", Payload: map[string]string{"text": "original"}}
	_, err := Deliver(context.Background(), request)
	require.ErrorContains(t, err, "HTTP 503")
	receipt, err := Deliver(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, receipt.StatusCode)
	require.Empty(t, receipt.Body)
	require.Equal(t, 2, calls)
}

func TestDeliverNeverFollowsRedirects(t *testing.T) {
	resetPrivateDestinationPolicy(t)
	require.NoError(t, ConfigurePrivateDestinationAllowlist([]string{"127.0.0.1"}))
	var received atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	_, err := Deliver(context.Background(), &Request{URL: source.URL, MessageID: "event-17"})
	require.ErrorContains(t, err, "HTTP 307")
	require.False(t, received.Load())
}

func TestDeliverCancellationAndMissingEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Deliver(ctx, &Request{URL: "https://example.com/private?secret=hidden", MessageID: "event-17"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "hidden")
	_, err = Deliver(ctx, &Request{})
	require.ErrorContains(t, err, "event ID")
}
