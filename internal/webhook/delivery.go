package webhook

import (
	"context"
	"io"
	"net/http"

	"github.com/pkg/errors"
)

// DeliveryReceipt is the bounded HTTP acknowledgement of a delivery. A 2xx
// response confirms receipt only; it does not prove publication on a platform.
type DeliveryReceipt struct {
	StatusCode int
	Body       []byte
}

// Deliver sends one durable event. It preserves the event ID on every retry
// and refuses redirects so a receiver cannot forward private payloads elsewhere.
func Deliver(ctx context.Context, request *Request) (*DeliveryReceipt, error) {
	if request == nil || request.MessageID == "" {
		return nil, errors.New("durable delivery requires an event ID")
	}
	client := *safeClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := doRequest(ctx, request, &client)
	if err != nil {
		// Do not retain URLs (possibly credentials), remote bodies, or payloads
		// in a durable error field. The caller can identify the configured target.
		return nil, errors.New("webhook connection failed or timed out")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, errors.Errorf("webhook returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return nil, errors.New("could not read webhook acknowledgement")
	}
	if len(body) > 64<<10 {
		return nil, errors.New("webhook acknowledgement exceeded 64 KiB")
	}
	return &DeliveryReceipt{StatusCode: response.StatusCode, Body: body}, nil
}
