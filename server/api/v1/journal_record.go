package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/usememos/memos/core/journal"
	"github.com/usememos/memos/core/journalrecord"
	"github.com/usememos/memos/internal/random"
	v1pb "github.com/usememos/memos/proto/gen/api/v1"
	"github.com/usememos/memos/server/auth"
	"github.com/usememos/memos/store"
)

func (s *APIV1Service) registerJournalRecordRoutes(group *echo.Group) {
	group.GET("/trash", s.journalTrash)
	group.POST("/memos/:uid/trash", s.journalTrashMemo)
	group.POST("/trash/:uid/restore", s.journalRestoreMemo)
	group.DELETE("/trash/:uid", s.journalPurgeMemo)
	group.GET("/memos/:uid/revisions", s.journalRevisions)
	group.POST("/memos/:uid/revisions/:revision/restore", s.journalRestoreRevision)
	group.GET("/attachments/:uid/original", s.journalOriginal)
	group.GET("/attachments/:uid/original-info", s.journalOriginalInfo)
}

func (s *APIV1Service) journalOwnedRecord(c *echo.Context) (*store.User, *store.Memo, error) {
	user, err := s.journalOwner(c)
	if err != nil {
		return nil, nil, err
	}
	uid := c.Param("uid")
	memo, err := s.Store.GetMemo(c.Request().Context(), &store.FindMemo{UID: &uid, CreatorID: &user.ID})
	if err != nil {
		return nil, nil, err
	}
	if memo == nil {
		return nil, nil, status.Error(codes.NotFound, "record not found")
	}
	return user, memo, nil
}

func (s *APIV1Service) journalRecordHash(ctx context.Context, memo *store.Memo) (string, error) {
	snapshot := store.MemoRecordSnapshot{Content: memo.Content, CreatedTs: memo.CreatedTs, UpdatedTs: memo.UpdatedTs, Visibility: memo.Visibility, RowStatus: memo.RowStatus}
	if memo.Payload != nil {
		snapshot.Location = memo.Payload.Location
	}
	if memo.SpaceID != nil {
		space, err := s.Store.GetSpace(ctx, &store.FindSpace{ID: memo.SpaceID})
		if err != nil {
			return "", err
		}
		if space == nil {
			return "", errors.New("record space missing")
		}
		snapshot.SpaceUID = space.UID
	}
	attachments, err := s.Store.ListAttachments(ctx, &store.FindAttachment{MemoID: &memo.ID})
	if err != nil {
		return "", err
	}
	for _, attachment := range attachments {
		snapshot.AttachmentUIDs = append(snapshot.AttachmentUIDs, attachment.UID)
	}
	return snapshot.Hash(), nil
}

// reserveJournalProvenance only creates owner-scoped metadata for a new UID.
// A duplicate creation response is still handled by the existing API contract.
func (s *APIV1Service) reserveJournalProvenance(ctx context.Context, ownerID int32, uid string) (*store.JournalDocument, error) {
	existing, err := s.Store.GetMemo(ctx, &store.FindMemo{UID: &uid})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to check record identifier")
	}
	if existing != nil {
		return nil, status.Error(codes.AlreadyExists, "memo already exists")
	}
	nowSec := time.Now().Unix()
	payload, _ := json.Marshal(map[string]any{"source": "manual", "imported": false, "enteredTs": nowSec, "modifiedTs": nowSec})
	doc, err := s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: ownerID, Kind: "provenance", Key: uid, Payload: payload}, 0)
	if errors.Is(err, store.ErrJournalConflict) {
		return nil, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to preserve entry timestamp")
	}
	return doc, nil
}

// markJournalRecordModified records a real committed edit separately from
// user-selected memo dates. Old records keep their unknown entry provenance.
func (s *APIV1Service) markJournalRecordModified(ctx context.Context, ownerID int32, uid string, modifiedTs int64) error {
	for attempt := 0; attempt < 8; attempt++ {
		doc, err := s.Store.GetJournalDocument(ctx, ownerID, "provenance", uid)
		if err != nil {
			return err
		}
		values := map[string]json.RawMessage{}
		version := int64(0)
		if doc != nil {
			version = doc.Version
			if err := json.Unmarshal(doc.Payload, &values); err != nil {
				return err
			}
		}
		var previous int64
		if raw := values["modifiedTs"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &previous); err != nil {
				return err
			}
		}
		if previous >= modifiedTs {
			return nil
		}
		values["modifiedTs"], _ = json.Marshal(modifiedTs)
		payload, err := json.Marshal(values)
		if err != nil {
			return err
		}
		_, err = s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: ownerID, Kind: "provenance", Key: uid, Payload: payload}, version)
		if errors.Is(err, store.ErrJournalConflict) {
			continue
		}
		return err
	}
	return store.ErrJournalConflict
}

func (s *APIV1Service) captureJournalRevision(ctx context.Context, memo *store.Memo) error {
	attachments, err := s.Store.ListAttachments(ctx, &store.FindAttachment{MemoID: &memo.ID})
	if err != nil {
		return err
	}
	snapshot, err := s.convertMemoFromStore(ctx, memo, nil, attachments, nil)
	if err != nil {
		return err
	}
	raw, err := protojson.Marshal(snapshot)
	if err != nil {
		return err
	}
	revisionID, err := random.String(16)
	if err != nil {
		return err
	}
	revision := journalrecord.Revision{ID: revisionID, CreatedTs: time.Now().Unix(), Memo: raw}
	payload, err := json.Marshal(revision)
	if err != nil {
		return err
	}
	_, err = s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: memo.CreatorID, Kind: "revision", Key: memo.UID + "_" + revision.ID, Payload: payload}, 0)
	return err
}

func (s *APIV1Service) revokeLegacyMemoShares(ctx context.Context, memo *store.Memo) error {
	shares, err := s.Store.ListMemoShares(ctx, &store.FindMemoShare{MemoID: &memo.ID})
	if err != nil {
		return err
	}
	for _, share := range shares {
		if err := s.Store.DeleteMemoShare(ctx, &store.DeleteMemoShare{UID: &share.UID}); err != nil {
			return err
		}
	}
	return nil
}

func (s *APIV1Service) journalTrashMemo(c *echo.Context) error {
	user, memo, err := s.journalOwnedRecord(c)
	if err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	doc, err := s.Store.GetJournalDocument(ctx, user.ID, "trash", memo.UID)
	if err != nil {
		return journalError(c, err)
	}
	if doc == nil {
		payload, _ := json.Marshal(journalrecord.Trash{DeletedTs: time.Now().Unix(), PreviousState: string(memo.RowStatus)})
		if _, err := s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: user.ID, Kind: "trash", Key: memo.UID, Payload: payload}, 0); err != nil && !errors.Is(err, store.ErrJournalConflict) {
			return journalError(c, err)
		}
	}
	archived, private := store.Archived, store.Private
	if err := s.Store.UpdateMemo(ctx, &store.UpdateMemo{ID: memo.ID, RowStatus: &archived, Visibility: &private}); err != nil {
		return journalError(c, err)
	}
	if err := s.revokeLegacyMemoShares(ctx, memo); err != nil {
		return journalError(c, err)
	}
	if err := s.cancelPartitionMemoDeliveries(ctx, user.ID, memo.UID); err != nil {
		return journalError(c, err)
	}
	if err := journal.RevokeMemoDerivatives(ctx, s.Store, user.ID, memo.UID); err != nil {
		return journalError(c, err)
	}
	s.SSEHub.publishMemoChanged()
	return c.JSON(http.StatusOK, map[string]bool{"deleted": true})
}

func (s *APIV1Service) journalTrash(c *echo.Context) error {
	user, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	docs, err := s.Store.ListJournalDocuments(c.Request().Context(), &store.FindJournalDocument{OwnerID: &user.ID, Kind: "trash"})
	if err != nil {
		return journalError(c, err)
	}
	memos := []json.RawMessage{}
	for _, doc := range docs {
		var trash journalrecord.Trash
		if err := json.Unmarshal(doc.Payload, &trash); err != nil {
			return journalError(c, err)
		}
		if journalrecord.Expired(trash.DeletedTs, time.Now()) {
			continue
		}
		memo, err := s.Store.GetMemo(c.Request().Context(), &store.FindMemo{UID: &doc.Key, CreatorID: &user.ID})
		if err != nil {
			return journalError(c, err)
		}
		if memo == nil {
			continue
		}
		attachments, err := s.Store.ListAttachments(c.Request().Context(), &store.FindAttachment{MemoID: &memo.ID})
		if err != nil {
			return journalError(c, err)
		}
		message, err := s.convertMemoFromStore(c.Request().Context(), memo, nil, attachments, nil)
		if err != nil {
			return journalError(c, err)
		}
		raw, err := protojson.Marshal(message)
		if err != nil {
			return journalError(c, err)
		}
		memos = append(memos, raw)
	}
	return c.JSON(http.StatusOK, map[string]any{"memos": memos, "retentionDays": 30})
}

func (s *APIV1Service) journalRestoreMemo(c *echo.Context) error {
	user, memo, err := s.journalOwnedRecord(c)
	if err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	doc, err := s.Store.GetJournalDocument(ctx, user.ID, "trash", memo.UID)
	if err != nil {
		return journalError(c, err)
	}
	if doc == nil {
		return journalError(c, status.Error(codes.NotFound, "deleted record not found"))
	}
	var trash journalrecord.Trash
	if err := json.Unmarshal(doc.Payload, &trash); err != nil {
		return journalError(c, err)
	}
	if journalrecord.Expired(trash.DeletedTs, time.Now()) {
		return journalError(c, status.Error(codes.FailedPrecondition, "recovery window has ended"))
	}
	normal, private := store.Normal, store.Private
	// Keep the tombstone until private restoration succeeds. Never replay sync.
	if err := s.Store.UpdateMemo(ctx, &store.UpdateMemo{ID: memo.ID, RowStatus: &normal, Visibility: &private}); err != nil {
		return journalError(c, err)
	}
	if err := s.Store.DeleteJournalDocument(ctx, user.ID, "trash", memo.UID, doc.Version); err != nil {
		return journalError(c, err)
	}
	s.SSEHub.publishMemoChanged()
	return c.JSON(http.StatusOK, map[string]bool{"restored": true})
}

func (s *APIV1Service) journalPurgeMemo(c *echo.Context) error {
	user, memo, err := s.journalOwnedRecord(c)
	if err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	doc, err := s.Store.GetJournalDocument(ctx, user.ID, "trash", memo.UID)
	if err != nil {
		return journalError(c, err)
	}
	if doc == nil {
		return journalError(c, status.Error(codes.FailedPrecondition, "record must be in recently deleted"))
	}
	if _, err := s.DeleteMemo(ctx, &v1pb.DeleteMemoRequest{Name: "memos/" + memo.UID}); err != nil {
		return journalError(c, err)
	}
	if err := s.Store.DeleteJournalDocument(ctx, user.ID, "trash", memo.UID, -1); err != nil {
		return journalError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"deleted": true})
}

func (s *APIV1Service) journalRevisions(c *echo.Context) error {
	user, memo, err := s.journalOwnedRecord(c)
	if err != nil {
		return journalError(c, err)
	}
	docs, err := s.Store.ListJournalDocuments(c.Request().Context(), &store.FindJournalDocument{OwnerID: &user.ID, Kind: "revision", KeyPrefix: memo.UID + "_"})
	if err != nil {
		return journalError(c, err)
	}
	result := []journalrecord.Revision{}
	for _, doc := range docs {
		var revision journalrecord.Revision
		if err := json.Unmarshal(doc.Payload, &revision); err != nil {
			return journalError(c, err)
		}
		if !journalrecord.Expired(revision.CreatedTs, time.Now()) {
			result = append(result, revision)
		}
	}
	provenance, err := s.Store.GetJournalDocument(c.Request().Context(), user.ID, "provenance", memo.UID)
	if err != nil {
		return journalError(c, err)
	}
	var source json.RawMessage
	if provenance != nil {
		source = provenance.Payload
	}
	return c.JSON(http.StatusOK, map[string]any{"revisions": result, "retentionDays": 30, "provenance": source})
}

func (s *APIV1Service) journalRestoreRevision(c *echo.Context) error {
	user, memo, err := s.journalOwnedRecord(c)
	if err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	trash, err := s.Store.GetJournalDocument(ctx, user.ID, "trash", memo.UID)
	if err != nil {
		return journalError(c, err)
	}
	if trash != nil {
		return journalError(c, status.Error(codes.FailedPrecondition, "restore the deleted record first"))
	}
	doc, err := s.Store.GetJournalDocument(ctx, user.ID, "revision", memo.UID+"_"+c.Param("revision"))
	if err != nil {
		return journalError(c, err)
	}
	if doc == nil {
		return journalError(c, status.Error(codes.NotFound, "revision not found"))
	}
	var revision journalrecord.Revision
	if err := json.Unmarshal(doc.Payload, &revision); err != nil {
		return journalError(c, err)
	}
	if journalrecord.Expired(revision.CreatedTs, time.Now()) {
		return journalError(c, status.Error(codes.FailedPrecondition, "revision expired"))
	}
	snapshot := &v1pb.Memo{}
	if err := protojson.Unmarshal(revision.Memo, snapshot); err != nil {
		return journalError(c, err)
	}
	expected, err := s.journalRecordHash(ctx, memo)
	if err != nil {
		return journalError(c, err)
	}
	md, _ := metadata.FromIncomingContext(ctx)
	md = md.Copy()
	md.Set("x-memos-expected-record-sha256", expected)
	ctx = metadata.NewIncomingContext(ctx, md)

	// Recreate removed attachments from retained originals. A revision never
	// invents missing historical bytes or silently restores broken references.
	for _, attachment := range snapshot.Attachments {
		uid := strings.TrimPrefix(attachment.Name, "attachments/")
		existing, err := s.Store.GetAttachment(ctx, &store.FindAttachment{UID: &uid})
		if err != nil {
			return journalError(c, err)
		}
		if existing != nil && existing.CreatorID == user.ID && existing.MemoID != nil && *existing.MemoID == memo.ID {
			continue
		}
		path, err := attachmentOriginalPath(s.Profile.Data, uid)
		if err != nil {
			return journalError(c, err)
		}
		file, err := os.Open(path)
		if err != nil {
			return journalError(c, status.Error(codes.FailedPrecondition, "a historical original is unavailable; the current record was preserved"))
		}
		stat, err := file.Stat()
		if err != nil {
			file.Close()
			return journalError(c, err)
		}
		settings, err := s.Store.GetInstanceStorageSetting(ctx)
		if err != nil {
			file.Close()
			return journalError(c, err)
		}
		attachmentUID, err := random.String(22)
		if err != nil {
			file.Close()
			return journalError(c, err)
		}
		create := &store.Attachment{UID: attachmentUID, CreatorID: user.ID, Filename: attachment.Filename, Type: attachment.Type, Size: stat.Size()}
		restored, err := s.processAndSaveAttachment(ctx, create, settings, file)
		file.Close()
		if err != nil {
			return journalError(c, err)
		}
		snapshot.Content = strings.ReplaceAll(snapshot.Content, "/file/"+attachment.Name, "/file/"+restored.Name)
		attachment.Name = restored.Name
	}
	snapshot.Name = "memos/" + memo.UID
	result, err := s.UpdateMemo(ctx, &v1pb.UpdateMemoRequest{Memo: snapshot, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"content", "attachments", "location", "create_time", "update_time"}}})
	if err != nil {
		return journalError(c, err)
	}
	raw, err := protojson.Marshal(result)
	if err != nil {
		return journalError(c, err)
	}
	return c.JSONBlob(http.StatusOK, raw)
}

func (s *APIV1Service) journalOwnedOriginal(c *echo.Context) (*store.Attachment, string, error) {
	user, err := s.journalOwner(c)
	if err != nil {
		return nil, "", err
	}
	uid := c.Param("uid")
	attachment, err := s.Store.GetAttachment(c.Request().Context(), &store.FindAttachment{UID: &uid, CreatorID: &user.ID})
	if err != nil {
		return nil, "", err
	}
	if attachment == nil {
		return nil, "", status.Error(codes.NotFound, "attachment not found")
	}
	path, err := attachmentOriginalPath(s.Profile.Data, uid)
	return attachment, path, err
}

func (s *APIV1Service) journalOriginalInfo(c *echo.Context) error {
	_, path, err := s.journalOwnedOriginal(c)
	if err != nil {
		return journalError(c, err)
	}
	digest, size, err := originalFileDigest(path)
	if os.IsNotExist(err) {
		return c.JSON(http.StatusOK, map[string]any{"available": false})
	}
	if err != nil {
		return journalError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"available": true, "sha256": digest, "size": size})
}

func (s *APIV1Service) journalOriginal(c *echo.Context) error {
	attachment, path, err := s.journalOwnedOriginal(c)
	if err != nil {
		return journalError(c, err)
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return journalError(c, status.Error(codes.NotFound, "original was not retained for this historical attachment"))
	}
	if err != nil {
		return journalError(c, err)
	}
	defer file.Close()
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", attachment.Filename))
	c.Response().Header().Set("Content-Type", "application/octet-stream")
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(c.Response(), c.Request(), attachment.Filename, time.Unix(attachment.CreatedTs, 0), file)
	return nil
}

func (s *APIV1Service) purgeJournalRevisions(ctx context.Context, owner int32, uid string) error {
	docs, err := s.Store.ListJournalDocuments(ctx, &store.FindJournalDocument{OwnerID: &owner, Kind: "revision", KeyPrefix: uid + "_"})
	if err != nil {
		return err
	}
	for _, doc := range docs {
		var revision journalrecord.Revision
		if err := json.Unmarshal(doc.Payload, &revision); err != nil {
			return err
		}
		memo := &v1pb.Memo{}
		if err := protojson.Unmarshal(revision.Memo, memo); err != nil {
			return err
		}
		for _, attachment := range memo.Attachments {
			originalUID := strings.TrimPrefix(attachment.Name, "attachments/")
			if path, err := attachmentOriginalPath(s.Profile.Data, originalUID); err == nil {
				_ = os.Remove(path)
			}
		}
		if err := s.Store.DeleteJournalDocument(ctx, owner, "revision", doc.Key, -1); err != nil {
			return err
		}
	}
	return nil
}

// RunJournalRetention expires recovery windows without making the user open trash.
func (s *APIV1Service) RunJournalRetention(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := s.expireJournalRecovery(ctx, time.Now()); err != nil && ctx.Err() == nil {
			slog.Warn("Journal recovery cleanup will retry", slog.Any("err", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *APIV1Service) expireJournalRecovery(ctx context.Context, now time.Time) error {
	trashDocs, err := s.Store.ListJournalDocuments(ctx, &store.FindJournalDocument{Kind: "trash"})
	if err != nil {
		return err
	}
	for _, doc := range trashDocs {
		var trash journalrecord.Trash
		if err := json.Unmarshal(doc.Payload, &trash); err != nil {
			return err
		}
		if !journalrecord.Expired(trash.DeletedTs, now) {
			continue
		}
		owner, err := s.Store.GetUser(ctx, &store.FindUser{ID: &doc.OwnerID})
		if err != nil {
			return err
		}
		if owner == nil {
			continue
		}
		ownerCtx := auth.SetUserInContext(ctx, owner, "")
		if _, err := s.DeleteMemo(ownerCtx, &v1pb.DeleteMemoRequest{Name: "memos/" + doc.Key}); err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		if err := s.Store.DeleteJournalDocument(ctx, doc.OwnerID, "trash", doc.Key, -1); err != nil {
			return err
		}
	}
	revisionDocs, err := s.Store.ListJournalDocuments(ctx, &store.FindJournalDocument{Kind: "revision"})
	if err != nil {
		return err
	}
	protected := map[string]bool{}
	for _, doc := range revisionDocs {
		var revision journalrecord.Revision
		if err := json.Unmarshal(doc.Payload, &revision); err != nil {
			return err
		}
		if journalrecord.Expired(revision.CreatedTs, now) {
			if err := s.Store.DeleteJournalDocument(ctx, doc.OwnerID, "revision", doc.Key, doc.Version); err != nil {
				return err
			}
			continue
		}
		memo := &v1pb.Memo{}
		if err := protojson.Unmarshal(revision.Memo, memo); err != nil {
			return err
		}
		for _, attachment := range memo.Attachments {
			protected[strings.TrimPrefix(attachment.Name, "attachments/")] = true
		}
	}
	directory := filepath.Join(s.Profile.Data, "originals")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		uid := entry.Name()
		if entry.IsDir() || protected[uid] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !journalrecord.Expired(info.ModTime().Unix(), now) {
			continue
		}
		current, err := s.Store.GetAttachment(ctx, &store.FindAttachment{UID: &uid})
		if err != nil {
			return err
		}
		if current == nil {
			if err := os.Remove(filepath.Join(directory, uid)); err != nil {
				return err
			}
		}
	}
	return nil
}
