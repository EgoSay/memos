package v1

import (
	"context"

	"github.com/pkg/errors"

	v1pb "github.com/EgoSay/kairos/proto/gen/api/v1"
	"github.com/EgoSay/kairos/store"
)

// DispatchMemoCreatedWebhook preserves legacy configuration without sending private
// journal content automatically. Only an explicitly enabled partition target
// may create durable work through JournalPartitions.Commit.
func (*APIV1Service) DispatchMemoCreatedWebhook(context.Context, *v1pb.Memo) error { return nil }

// DispatchMemoUpdatedWebhook does not publish through legacy user-wide hooks.
func (*APIV1Service) DispatchMemoUpdatedWebhook(context.Context, *v1pb.Memo) error { return nil }

// DispatchMemoDeletedWebhook does not expose deleted originals.
func (*APIV1Service) DispatchMemoDeletedWebhook(context.Context, *v1pb.Memo) error { return nil }

// DispatchMemoCommentCreatedWebhook does not publish private comments.
func (*APIV1Service) DispatchMemoCommentCreatedWebhook(context.Context, *store.Memo, *store.Memo, int32) error {
	return nil
}

func (s *APIV1Service) getMemoContentSnippet(content string) (string, error) {
	// Use goldmark service for snippet generation
	snippet, err := s.MarkdownService.GenerateSnippet([]byte(content), 64)
	if err != nil {
		return "", errors.Wrap(err, "failed to generate snippet")
	}
	return snippet, nil
}
