package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"log/slog"
	"os"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/usememos/memos/core/access"
	"github.com/usememos/memos/core/journal"
	"github.com/usememos/memos/core/memopayload"
	"github.com/usememos/memos/internal/ratelimit"
	v1pb "github.com/usememos/memos/proto/gen/api/v1"
	storepb "github.com/usememos/memos/proto/gen/store"
	"github.com/usememos/memos/store"
)

const maxBatchGetLinkMetadata = 10

func (s *APIV1Service) CreateMemo(ctx context.Context, request *v1pb.CreateMemoRequest) (*v1pb.Memo, error) {
	user, err := s.fetchCurrentUser(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get user")
	}
	if user == nil {
		return nil, status.Errorf(codes.Unauthenticated, "user not authenticated")
	}
	if err := s.throttleAndCharge(ratelimit.ScopeWriteUser, userKey(user.ID), 1); err != nil {
		return nil, err
	}
	if request.Memo == nil {
		return nil, status.Errorf(codes.InvalidArgument, "memo is required")
	}

	memoUID, err := ValidateAndGenerateUID(request.MemoId)
	if err != nil {
		return nil, err
	}

	prepared, err := s.prepareMemoCreate(ctx, user, request.Memo, memoUID)
	if err != nil {
		return nil, err
	}

	// Persist the actual entry time separately from a user-selected record date.
	// Reserving before the memo transaction means a retry can never report a
	// saved record while silently omitting its first entry timestamp.
	provenance, err := s.reserveJournalProvenance(ctx, user.ID, memoUID)
	if err != nil {
		return nil, err
	}

	if err := s.createMemoWithMutation(ctx, user, prepared.memo, nil, prepared.attachments, prepared.requiredAttachmentIDs, prepared.referenceRelations); err != nil {
		if provenance != nil {
			existing, findErr := s.Store.GetMemo(ctx, &store.FindMemo{UID: &memoUID})
			if findErr == nil && (existing == nil || existing.CreatorID != user.ID) {
				_ = s.Store.DeleteJournalDocument(ctx, user.ID, "provenance", memoUID, provenance.Version)
			}
		}
		return nil, mapMemoCreateError(err, memoUID, "failed to create memo")
	}
	memo := prepared.memo

	attachments, err := s.Store.ListAttachments(ctx, &store.FindAttachment{
		MemoID: &memo.ID,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get memo attachments")
	}

	relations, err := s.loadMemoRelations(ctx, memo)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load memo relations")
	}
	memoMessage, err := s.convertMemoFromStore(ctx, memo, nil, attachments, relations)
	if err != nil {
		return nil, errors.Wrap(err, "failed to convert memo")
	}
	// Try to dispatch webhook when memo is created.
	if err := s.DispatchMemoCreatedWebhook(ctx, memoMessage); err != nil {
		slog.Warn("Failed to dispatch memo created webhook", slog.Any("err", err))
	}

	s.SSEHub.publishMemoChanged()

	s.dispatchMemoMentionNotificationsBestEffort(ctx, memo, nil, "")

	return memoMessage, nil
}

func (s *APIV1Service) ListMemos(ctx context.Context, request *v1pb.ListMemosRequest) (*v1pb.ListMemosResponse, error) {
	memoFind := &store.FindMemo{
		// Exclude comments by default.
		ExcludeComments: true,
	}
	accessScope, currentUser, err := s.resolveMemoAccessScope(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	// An anonymous caller may only list at all when the instance permits it.
	if currentUser == nil && !accessScope.AllowPublic {
		return nil, status.Errorf(codes.Unauthenticated, "user not authenticated")
	}
	memoFind.Access = accessScope

	if request.State == v1pb.State_ARCHIVED {
		state := store.Archived
		memoFind.RowStatus = &state
		// Archived memos are only visible to their creator.
		if currentUser == nil {
			return &v1pb.ListMemosResponse{}, nil
		}
		memoFind.CreatorID = &currentUser.ID
	} else {
		state := store.Normal
		memoFind.RowStatus = &state
	}

	// Parse order_by field (replaces the old sort and direction fields)
	if request.OrderBy != "" {
		if err := s.parseMemoOrderBy(request.OrderBy, memoFind); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid order_by: %v", err)
		}
	} else {
		// Default ordering by create_time desc.
		memoFind.OrderByTimeAsc = false
	}

	if request.Filter != "" {
		if err := s.validateMemoFilterForUser(ctx, request.Filter, currentUser); err != nil {
			return nil, err
		}
		memoFind.Filters = append(memoFind.Filters, request.Filter)
	}

	var limit, offset int
	if request.PageToken != "" {
		var pageToken v1pb.PageToken
		if err := unmarshalPageToken(request.PageToken, &pageToken); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid page token: %v", err)
		}
		limit = normalizePageSize(pageToken.Limit)
		offset = max(int(pageToken.Offset), 0)
	} else {
		limit = normalizePageSize(request.PageSize)
	}
	limit = min(limit, MaxPageSize)
	limitPlusOne := limit + 1
	memoFind.Limit = &limitPlusOne
	memoFind.Offset = &offset
	memos, err := s.Store.ListMemos(ctx, memoFind)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list memos: %v", err)
	}

	memoMessages := []*v1pb.Memo{}
	nextPageToken := ""
	if len(memos) == limitPlusOne {
		memos = memos[:limit]
		nextPageToken, err = getPageToken(limit, offset+limit)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to get next page token, error: %v", err)
		}
	}

	if len(memos) == 0 {
		response := &v1pb.ListMemosResponse{
			Memos:         memoMessages,
			NextPageToken: nextPageToken,
		}
		return response, nil
	}

	reactionMap := make(map[int32][]*store.Reaction)

	attachmentMap := make(map[int32][]*store.Attachment)
	memoIDs := make([]int32, 0, len(memos))

	for _, m := range memos {
		memoIDs = append(memoIDs, m.ID)
	}

	// REACTIONS
	reactions, err := s.Store.ListReactions(ctx, &store.FindReaction{MemoIDList: memoIDs})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list reactions")
	}
	for _, reaction := range reactions {
		reactionMap[reaction.MemoID] = append(reactionMap[reaction.MemoID], reaction)
	}

	// ATTACHMENTS
	attachments, err := s.Store.ListAttachments(ctx, &store.FindAttachment{MemoIDList: memoIDs})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list attachments")
	}
	for _, attachment := range attachments {
		attachmentMap[*attachment.MemoID] = append(attachmentMap[*attachment.MemoID], attachment)
	}

	// RELATIONS (batch load to avoid N+1)
	relationMap, err := s.batchConvertMemoRelations(ctx, memos, false)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to batch load memo relations")
	}
	creatorIDs := make([]int32, 0, len(memos)+len(reactions))
	for _, memo := range memos {
		creatorIDs = append(creatorIDs, memo.CreatorID)
	}
	for _, reaction := range reactions {
		creatorIDs = append(creatorIDs, reaction.CreatorID)
	}
	creatorMap, err := s.listUsersByID(ctx, creatorIDs)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list memo creators: %v", err)
	}
	for _, memo := range memos {
		reactions := reactionMap[memo.ID]
		attachments := attachmentMap[memo.ID]
		relations := relationMap[memo.ID]

		memoMessage, err := s.convertMemoFromStoreWithCreators(ctx, memo, reactions, attachments, relations, creatorMap)
		if err != nil {
			if stderrors.Is(err, errMemoCreatorNotFound) {
				slog.Warn("Skipping memo with missing creator",
					slog.Int64("memo_id", int64(memo.ID)),
					slog.String("memo_uid", memo.UID),
					slog.Int64("creator_id", int64(memo.CreatorID)),
				)
				continue
			}
			return nil, errors.Wrap(err, "failed to convert memo")
		}

		memoMessages = append(memoMessages, memoMessage)
	}

	response := &v1pb.ListMemosResponse{
		Memos:         memoMessages,
		NextPageToken: nextPageToken,
	}
	return response, nil
}

func (s *APIV1Service) GetMemo(ctx context.Context, request *v1pb.GetMemoRequest) (*v1pb.Memo, error) {
	memoUID, err := ExtractMemoUIDFromName(request.Name)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid memo name: %v", err)
	}
	memo, err := s.Store.GetMemo(ctx, &store.FindMemo{
		UID: &memoUID,
	})
	if err != nil {
		return nil, err
	}
	if memo == nil {
		return nil, status.Errorf(codes.NotFound, "memo not found")
	}

	if err := s.checkMemoReadAccess(ctx, memo); err != nil {
		return nil, err
	}

	reactions, err := s.Store.ListReactions(ctx, &store.FindReaction{
		MemoID: &memo.ID,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list reactions")
	}

	attachments, err := s.Store.ListAttachments(ctx, &store.FindAttachment{
		MemoID: &memo.ID,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list attachments")
	}

	relations, err := s.loadMemoRelations(ctx, memo)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load memo relations")
	}
	memoMessage, err := s.convertMemoFromStore(ctx, memo, reactions, attachments, relations)
	if err != nil {
		if stderrors.Is(err, errMemoCreatorNotFound) {
			return nil, status.Errorf(codes.NotFound, "memo creator not found")
		}
		return nil, errors.Wrap(err, "failed to convert memo")
	}
	return memoMessage, nil
}

// UpdateMemo updates an existing memo.
func (s *APIV1Service) UpdateMemo(ctx context.Context, request *v1pb.UpdateMemoRequest) (*v1pb.Memo, error) {
	if request.Memo == nil {
		return nil, status.Errorf(codes.InvalidArgument, "memo is required")
	}
	memoUID, err := ExtractMemoUIDFromName(request.Memo.Name)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid memo name: %v", err)
	}
	if request.UpdateMask == nil || len(request.UpdateMask.Paths) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "update mask is required")
	}

	memo, err := s.Store.GetMemo(ctx, &store.FindMemo{UID: &memoUID})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get memo: %v", err)
	}
	if memo == nil {
		return nil, status.Errorf(codes.NotFound, "memo not found")
	}

	user, err := s.fetchCurrentUser(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get current user")
	}
	if user == nil {
		return nil, status.Errorf(codes.Unauthenticated, "user not authenticated")
	}
	if !access.CanManageMemo(user, memo) {
		return nil, status.Errorf(codes.PermissionDenied, "permission denied")
	}

	update := &store.UpdateMemo{
		ID: memo.ID,
	}
	updatePaths := make(map[string]bool, len(request.UpdateMask.Paths))
	for _, path := range request.UpdateMask.Paths {
		updatePaths[path] = true
	}
	lifecycleOnly := updatePaths["space"]
	for path := range updatePaths {
		if path != "space" && path != "visibility" {
			lifecycleOnly = false
		}
	}
	nextSpaceID := memo.SpaceID
	if updatePaths["space"] {
		if request.Memo.Space == nil || request.Memo.GetSpace() == "" {
			nextSpaceID = nil
			update.ClearSpace = true
		} else {
			target, err := s.resolveSpaceForMemoPlacement(ctx, request.Memo.GetSpace(), user)
			if err != nil {
				return nil, err
			}
			nextSpaceID = &target.ID
			update.SpaceID = &target.ID
			update.ClearSpace = false
		}
	}

	nextVisibility := memo.Visibility
	if updatePaths["visibility"] {
		nextVisibility, err = validateUpdateMemoVisibility(request.Memo.Visibility)
		if err != nil {
			return nil, err
		}
		update.Visibility = &nextVisibility
	}
	spaceChanged := !sameOptionalInt32(memo.SpaceID, nextSpaceID)
	// A removed author receives a lifecycle exception only for a real withdraw
	// or move. Merely including the current Space in the mask must not smuggle
	// an audience-only mutation past membership checks.
	lifecycleOnly = lifecycleOnly && spaceChanged
	if nextVisibility == store.SpaceAudience && nextSpaceID == nil {
		return nil, status.Errorf(codes.InvalidArgument, "SPACE visibility requires a space")
	}
	if memo.Visibility == store.SpaceAudience && spaceChanged && !updatePaths["visibility"] {
		return nil, status.Errorf(codes.InvalidArgument, "moving a memo with SPACE visibility requires an explicit visibility update")
	}
	if memo.Visibility == store.SpaceAudience && nextSpaceID == nil && nextVisibility == store.SpaceAudience {
		return nil, status.Errorf(codes.InvalidArgument, "unassigning a memo with SPACE visibility requires a replacement visibility")
	}
	update.Policy = memoWritePolicy(user.ID, lifecycleOnly)
	trash, trashErr := s.Store.GetJournalDocument(ctx, memo.CreatorID, "trash", memo.UID)
	if trashErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to inspect record lifecycle: %v", trashErr)
	}
	if trash != nil {
		return nil, status.Error(codes.FailedPrecondition, "restore this record from recently deleted before editing")
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if expected := md.Get("x-memos-expected-content-sha256"); len(expected) > 0 {
			hash := sha256.Sum256([]byte(memo.Content))
			if len(expected) != 1 || expected[0] != hex.EncodeToString(hash[:]) {
				return nil, status.Error(codes.Aborted, "record changed on another device; local content was preserved")
			}
		}
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if expected := md.Get("x-memos-expected-record-sha256"); len(expected) > 0 {
			actual, err := s.journalRecordHash(ctx, memo)
			if err != nil {
				return nil, status.Error(codes.Internal, "failed to read record version")
			}
			if len(expected) != 1 || len(expected[0]) != 64 || expected[0] != actual {
				return nil, status.Error(codes.Aborted, "record metadata or attachments changed; local content was preserved")
			}
			update.ExpectedRecordHash = expected[0]
		}
	}

	previousContent := memo.Content
	contentUpdated := false
	attachmentsUpdated := false
	relationsUpdated := false
	nextMemo := *memo
	if memo.Payload != nil {
		nextMemo.Payload = &storepb.MemoPayload{}
		proto.Merge(nextMemo.Payload, memo.Payload)
	}

	for _, path := range request.UpdateMask.Paths {
		// Collaboration fields were validated together above so placement and
		// audience transitions cannot be observed independently.
		if path == "visibility" || path == "space" {
			continue
		}
		if path == "content" {
			contentUpdated = true
			contentLengthLimit, err := s.getContentLengthLimit(ctx)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to get content length limit")
			}
			if len(request.Memo.Content) > contentLengthLimit {
				return nil, status.Errorf(codes.InvalidArgument, "content too long (max %d characters)", contentLengthLimit)
			}
			nextMemo.Content = request.Memo.Content
			if err := memopayload.RebuildMemoPayload(ctx, &nextMemo, s.MarkdownService); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to rebuild memo payload: %v", err)
			}
			update.Content = &nextMemo.Content
			update.Payload = nextMemo.Payload
		} else if path == "pinned" {
			update.Pinned = &request.Memo.Pinned
		} else if path == "state" {
			rowStatus := convertStateToStore(request.Memo.State)
			update.RowStatus = &rowStatus
		} else if path == "create_time" {
			if request.Memo.CreateTime == nil || !request.Memo.CreateTime.IsValid() {
				return nil, status.Errorf(codes.InvalidArgument, "create_time is invalid")
			}
			createdTs := request.Memo.CreateTime.AsTime().Unix()
			update.CreatedTs = &createdTs
		} else if path == "update_time" {
			updatedTsSec := time.Now().Unix()
			if request.Memo.UpdateTime != nil {
				if !request.Memo.UpdateTime.IsValid() {
					return nil, status.Errorf(codes.InvalidArgument, "update_time is invalid")
				}
				updatedTsSec = request.Memo.UpdateTime.AsTime().Unix()
			}
			update.UpdatedTs = &updatedTsSec
		} else if path == "display_time" {
			return nil, status.Errorf(codes.InvalidArgument, "display_time is not supported")
		} else if path == "location" {
			if nextMemo.Payload == nil {
				nextMemo.Payload = &storepb.MemoPayload{}
			}
			nextMemo.Payload.Location = convertLocationToStore(request.Memo.Location)
			update.Payload = nextMemo.Payload
		} else if path == "attachments" {
			attachmentsUpdated = true
		} else if path == "relations" {
			relationsUpdated = true
		} else {
			return nil, status.Errorf(codes.InvalidArgument, "invalid update path: %s", path)
		}
	}

	var preparedAttachments *preparedMemoAttachments
	if attachmentsUpdated {
		preparedAttachments, err = s.prepareMemoAttachments(ctx, user, memo, request.Memo.Attachments)
		if err != nil {
			return nil, err
		}
	}
	var preparedRelations []*store.MemoRelation
	if relationsUpdated {
		preparedRelations, err = s.prepareMemoRelations(ctx, memo, request.Memo.Relations)
		if err != nil {
			return nil, err
		}
	}
	var requiredAttachmentIDs []int32
	if contentUpdated || attachmentsUpdated {
		var finalAttachments []*store.Attachment
		if preparedAttachments != nil {
			finalAttachments = preparedAttachments.normalized
		} else {
			finalAttachments, err = s.Store.ListAttachments(ctx, &store.FindAttachment{MemoID: &memo.ID})
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to list attachments")
			}
		}
		requiredAttachmentIDs, err = s.resolveMemoAttachmentReferences(nextMemo.Content, finalAttachments)
		if err != nil {
			return nil, err
		}
	}

	if contentUpdated || attachmentsUpdated || updatePaths["location"] || updatePaths["create_time"] {
		if err := s.captureJournalRevision(ctx, memo); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to retain previous version: %v", err)
		}
	}
	if contentUpdated || attachmentsUpdated || relationsUpdated || update.ExpectedRecordHash != "" {
		var relations *[]*store.MemoRelation
		if relationsUpdated {
			relations = &preparedRelations
		}
		if err := s.applyMemoMutation(ctx, memo, preparedAttachments, update, requiredAttachmentIDs, relations); err != nil {
			return nil, err
		}
	} else if err = s.Store.UpdateMemo(ctx, update); err != nil {
		return nil, mapMemoWriteError(err, "failed to update memo")
	}

	if contentUpdated || attachmentsUpdated || updatePaths["location"] || updatePaths["create_time"] || updatePaths["update_time"] || updatePaths["visibility"] || updatePaths["space"] {
		if err := s.markJournalRecordModified(ctx, memo.CreatorID, memo.UID, time.Now().Unix()); err != nil {
			return nil, status.Errorf(codes.Internal, "record was saved but its actual modification time could not be preserved: %v", err)
		}
	}

	memo, commentContext, memoMessage, err := s.buildUpdatedMemoState(ctx, memo.ID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build updated memo state")
	}
	if contentUpdated {
		s.dispatchMemoMentionNotificationsBestEffort(ctx, memo, commentContext, previousContent)
	}
	if memo.RowStatus == store.Archived {
		if err := s.revokeLegacyMemoShares(ctx, memo); err != nil {
			return nil, err
		}
		if err := journal.RevokeMemoDerivatives(ctx, s.Store, memo.CreatorID, memo.UID); err != nil {
			return nil, err
		}
		if err := s.cancelPartitionMemoDeliveries(ctx, memo.CreatorID, memo.UID); err != nil {
			return nil, err
		}
	} else if err := s.enqueuePartitionMemo(ctx, memo.CreatorID, memo.UID, false); err != nil {
		slog.Warn("Record saved but partition delivery could not be queued", slog.Any("err", err))
	}
	s.dispatchMemoUpdatedSideEffects(ctx, memoMessage)

	return memoMessage, nil
}

func (s *APIV1Service) DeleteMemo(ctx context.Context, request *v1pb.DeleteMemoRequest) (*emptypb.Empty, error) {
	memoUID, err := ExtractMemoUIDFromName(request.Name)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid memo name: %v", err)
	}
	memo, err := s.Store.GetMemo(ctx, &store.FindMemo{
		UID: &memoUID,
	})
	if err != nil {
		return nil, err
	}
	if memo == nil {
		return nil, status.Errorf(codes.NotFound, "memo not found")
	}

	user, err := s.fetchCurrentUser(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get current user")
	}
	if user == nil {
		return nil, status.Errorf(codes.Unauthenticated, "user not authenticated")
	}
	if !access.CanManageMemo(user, memo) {
		return nil, status.Errorf(codes.PermissionDenied, "permission denied")
	}
	var deletedMemoMessage *v1pb.Memo
	// Deletion is a narrow lifecycle capability and may remain available after
	// the author loses read access to a memo with the SPACE audience. Only
	// build a content-bearing webhook payload when the actor can still read the
	// memo immediately before deletion.
	if s.checkMemoReadAccess(ctx, memo) == nil {
		reactions, err := s.Store.ListReactions(ctx, &store.FindReaction{MemoID: &memo.ID})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to list reactions")
		}
		attachments, err := s.Store.ListAttachments(ctx, &store.FindAttachment{MemoID: &memo.ID})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to list attachments")
		}
		deleteRelations, _ := s.loadMemoRelations(ctx, memo)
		if memoMessage, err := s.convertMemoFromStore(ctx, memo, reactions, attachments, deleteRelations); err == nil {
			deletedMemoMessage = memoMessage
		}
	}

	if err := journal.RevokeMemoDerivatives(ctx, s.Store, memo.CreatorID, memo.UID); err != nil {
		return nil, err
	}
	if err := s.cancelPartitionMemoDeliveries(ctx, memo.CreatorID, memo.UID); err != nil {
		return nil, err
	}
	deleteResult, err := s.Store.DeleteMemoWithPolicy(ctx, &store.DeleteMemoWithPolicy{MemoID: memo.ID, ActorUserID: user.ID})
	if err != nil {
		switch {
		case stderrors.Is(err, store.ErrMemoPermissionDenied):
			return nil, status.Error(codes.PermissionDenied, "permission denied")
		case stderrors.Is(err, store.ErrMemoMutationConflict):
			return nil, status.Error(codes.FailedPrecondition, "memo changed or no longer exists")
		default:
			return nil, status.Errorf(codes.Internal, "failed to delete memo: %v", err)
		}
	}
	if deleteResult == nil {
		return nil, status.Error(codes.Internal, "memo was deleted without authorization state")
	}

	s.SSEHub.publishMemoChanged()
	if deletedMemoMessage != nil && deleteResult.ActorCanRead {
		if err := s.DispatchMemoDeletedWebhook(ctx, deletedMemoMessage); err != nil {
			slog.Warn("Failed to dispatch memo deleted webhook", slog.Any("err", err))
		}
	}
	if err := s.purgeJournalRevisions(ctx, memo.CreatorID, memo.UID); err != nil {
		return nil, err
	}
	if err := s.cleanupDeletedAttachmentStorage(ctx, deleteResult.Attachments); err != nil {
		return nil, status.Errorf(codes.Internal, "memo was deleted but attachment storage cleanup failed: %v", err)
	}

	for _, attachment := range deleteResult.Attachments {
		if path, err := attachmentOriginalPath(s.Profile.Data, attachment.UID); err == nil {
			_ = os.Remove(path)
		}
	}
	for _, kind := range []string{"trash", "provenance"} {
		if err := s.Store.DeleteJournalDocument(ctx, memo.CreatorID, kind, memo.UID, -1); err != nil {
			return nil, err
		}
	}
	return &emptypb.Empty{}, nil
}

func (s *APIV1Service) getContentLengthLimit(ctx context.Context) (int, error) {
	instanceMemoRelatedSetting, err := s.Store.GetInstanceMemoRelatedSetting(ctx)
	if err != nil {
		return 0, status.Errorf(codes.Internal, "failed to get instance memo related setting")
	}
	return int(instanceMemoRelatedSetting.ContentLengthLimit), nil
}

// DispatchMemoCreatedWebhook dispatches webhook when memo is created.
