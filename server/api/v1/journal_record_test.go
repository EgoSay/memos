package v1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/EgoSay/kairos/core/journalrecord"
	v1pb "github.com/EgoSay/kairos/proto/gen/api/v1"
	storepb "github.com/EgoSay/kairos/proto/gen/store"
	"github.com/EgoSay/kairos/store"
)

func journalRecordRequest(ctx context.Context, s *APIV1Service, method, path string) *httptest.ResponseRecorder {
	router := echo.New()
	s.registerJournalRecordRoutes(router.Group("/api/v1/journal"))
	request := httptest.NewRequest(method, "/api/v1/journal"+path, nil).WithContext(ctx)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestJournalOriginalRetainsBytesAndRequiresOwner(t *testing.T) {
	service, ctx := newUploadTestService(t)
	original := []byte("voice bytes are retained exactly\x00\x01")
	attachment, err := service.CreateAttachment(ctx, &v1pb.CreateAttachmentRequest{Attachment: &v1pb.Attachment{Filename: "voice.webm", Type: "audio/webm", Content: original}})
	require.NoError(t, err)
	uid := strings.TrimPrefix(attachment.Name, "attachments/")
	path, err := attachmentOriginalPath(service.Profile.Data, uid)
	require.NoError(t, err)
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, actual)
	response := journalRecordRequest(ctx, service, http.MethodGet, "/attachments/"+uid+"/original")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, original, response.Body.Bytes())
	stranger := createSpaceTestUser(context.Background(), t, service, "stranger-original", store.RoleUser)
	denied := journalRecordRequest(userCtx(context.Background(), stranger.ID), service, http.MethodGet, "/attachments/"+uid+"/original")
	require.Equal(t, http.StatusNotFound, denied.Code)
	require.NotContains(t, denied.Body.String(), string(original))
	// A reused UID cannot replace the existing private original.
	_, err = service.CreateAttachment(ctx, &v1pb.CreateAttachmentRequest{AttachmentId: uid, Attachment: &v1pb.Attachment{Filename: "wrong.bin", Type: "application/octet-stream", Content: []byte("replacement")}})
	require.Equal(t, codes.AlreadyExists, status.Code(err))
	again, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, again)
}

func TestJournalConflictKeepsBothVersionsAndRevisionRestore(t *testing.T) {
	service, ctx := newUploadTestService(t)
	memo, err := service.CreateMemo(ctx, &v1pb.CreateMemoRequest{Memo: &v1pb.Memo{Content: "original"}})
	require.NoError(t, err)
	uid := strings.TrimPrefix(memo.Name, "memos/")
	originalHash := sha256.Sum256([]byte("original"))
	versionCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("x-memos-expected-content-sha256", hex.EncodeToString(originalHash[:])))
	_, err = service.UpdateMemo(versionCtx, &v1pb.UpdateMemoRequest{Memo: &v1pb.Memo{Name: memo.Name, Content: "device one"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"content"}}})
	require.NoError(t, err)
	_, err = service.UpdateMemo(versionCtx, &v1pb.UpdateMemoRequest{Memo: &v1pb.Memo{Name: memo.Name, Content: "device two"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"content"}}})
	require.Equal(t, codes.Aborted, status.Code(err))
	latest, err := service.GetMemo(ctx, &v1pb.GetMemoRequest{Name: memo.Name})
	require.NoError(t, err)
	require.Equal(t, "device one", latest.Content)
	versions := journalRecordRequest(ctx, service, http.MethodGet, "/memos/"+uid+"/revisions")
	require.Equal(t, http.StatusOK, versions.Code, versions.Body.String())
	var response struct {
		Revisions []journalrecord.Revision `json:"revisions"`
	}
	require.NoError(t, json.Unmarshal(versions.Body.Bytes(), &response))
	require.Len(t, response.Revisions, 1)
	restored := journalRecordRequest(ctx, service, http.MethodPost, "/memos/"+uid+"/revisions/"+response.Revisions[0].ID+"/restore")
	require.Equal(t, http.StatusOK, restored.Code, restored.Body.String())
	require.Contains(t, restored.Body.String(), "original")
}

func TestJournalTrashRestoresPrivatelyWithoutSharesAndExpires(t *testing.T) {
	service, ctx := newUploadTestService(t)
	memo, err := service.CreateMemo(ctx, &v1pb.CreateMemoRequest{Memo: &v1pb.Memo{Content: "private history"}})
	require.NoError(t, err)
	uid := strings.TrimPrefix(memo.Name, "memos/")
	share, err := service.CreateMemoShare(ctx, &v1pb.CreateMemoShareRequest{Parent: memo.Name})
	require.NoError(t, err)
	removed := journalRecordRequest(ctx, service, http.MethodPost, "/memos/"+uid+"/trash")
	require.Equal(t, http.StatusOK, removed.Code, removed.Body.String())
	_, err = service.GetMemo(ctx, &v1pb.GetMemoRequest{Name: memo.Name})
	require.Equal(t, codes.NotFound, status.Code(err))
	visible, err := service.ListMemos(ctx, &v1pb.ListMemosRequest{})
	require.NoError(t, err)
	require.Empty(t, visible.Memos)
	shareToken := share.Name[strings.LastIndex(share.Name, "/")+1:]
	oldGrant, err := service.Store.GetMemoShare(ctx, &store.FindMemoShare{UID: &shareToken})
	require.NoError(t, err)
	require.Nil(t, oldGrant)
	restored := journalRecordRequest(ctx, service, http.MethodPost, "/trash/"+uid+"/restore")
	require.Equal(t, http.StatusOK, restored.Code, restored.Body.String())
	latest, err := service.GetMemo(ctx, &v1pb.GetMemoRequest{Name: memo.Name})
	require.NoError(t, err)
	require.Equal(t, v1pb.Visibility_PRIVATE, latest.Visibility)
	removed = journalRecordRequest(ctx, service, http.MethodPost, "/memos/"+uid+"/trash")
	require.Equal(t, http.StatusOK, removed.Code)
	require.NoError(t, service.expireJournalRecovery(ctx, time.Now().Add(31*24*time.Hour)))
	memoRow, err := service.Store.GetMemo(ctx, &store.FindMemo{UID: &uid})
	require.NoError(t, err)
	require.Nil(t, memoRow)
}

func TestPreserveOriginalRejectsUnsafeUIDAndCanceledWrite(t *testing.T) {
	directory := t.TempDir()
	_, err := preserveAttachmentOriginal(context.Background(), directory, &store.Attachment{UID: "../../escape"}, bytes.NewReader([]byte("x")))
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = preserveAttachmentOriginal(ctx, directory, &store.Attachment{UID: "safe-id"}, bytes.NewReader([]byte("content")))
	require.Error(t, err)
	path, err := attachmentOriginalPath(directory, "safe-id")
	require.NoError(t, err)
	require.NoFileExists(t, path)
}

func TestJournalPhotoOriginalAndRevisionMediaRecovery(t *testing.T) {
	service, ctx := newUploadTestService(t)
	var encoded bytes.Buffer
	require.NoError(t, jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 8, 8)), &jpeg.Options{Quality: 65}))
	original := append([]byte(nil), encoded.Bytes()...)
	photo, err := service.CreateAttachment(ctx, &v1pb.CreateAttachmentRequest{Attachment: &v1pb.Attachment{Filename: "day.jpg", Type: "image/jpeg", Content: original}})
	require.NoError(t, err)
	uid := strings.TrimPrefix(photo.Name, "attachments/")
	path, err := attachmentOriginalPath(service.Profile.Data, uid)
	require.NoError(t, err)
	retained, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, retained)
	stored, err := service.Store.GetAttachment(ctx, &store.FindAttachment{UID: &uid})
	require.NoError(t, err)
	display, err := service.GetAttachmentBlob(ctx, stored)
	require.NoError(t, err)
	require.NotEqual(t, original, display, "the private original is separate from the re-encoded display version")
	memo, err := service.CreateMemo(ctx, &v1pb.CreateMemoRequest{Memo: &v1pb.Memo{Content: "photo", Attachments: []*v1pb.Attachment{photo}}})
	require.NoError(t, err)
	memoUID := strings.TrimPrefix(memo.Name, "memos/")
	_, err = service.UpdateMemo(ctx, &v1pb.UpdateMemoRequest{Memo: &v1pb.Memo{Name: memo.Name, Content: "without photo"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"content", "attachments"}}})
	require.NoError(t, err)
	require.FileExists(t, path)
	var revisions struct {
		Revisions []journalrecord.Revision `json:"revisions"`
	}
	result := journalRecordRequest(ctx, service, http.MethodGet, "/memos/"+memoUID+"/revisions")
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &revisions))
	require.Len(t, revisions.Revisions, 1)
	restored := journalRecordRequest(ctx, service, http.MethodPost, "/memos/"+memoUID+"/revisions/"+revisions.Revisions[0].ID+"/restore")
	require.Equal(t, http.StatusOK, restored.Code, restored.Body.String())
	updated, err := service.GetMemo(ctx, &v1pb.GetMemoRequest{Name: memo.Name})
	require.NoError(t, err)
	require.Equal(t, "photo", updated.Content)
	require.Len(t, updated.Attachments, 1)
	recoveredUID := strings.TrimPrefix(updated.Attachments[0].Name, "attachments/")
	recoveredPath, err := attachmentOriginalPath(service.Profile.Data, recoveredUID)
	require.NoError(t, err)
	recovered, err := os.ReadFile(recoveredPath)
	require.NoError(t, err)
	require.Equal(t, original, recovered)
}

func TestJournalEntryTimeSurvivesBackdatingRetryAndEditing(t *testing.T) {
	service, ctx := newUploadTestService(t)
	beforeSec := time.Now().Unix()
	request := &v1pb.CreateMemoRequest{MemoId: "dated-journal-entry", Memo: &v1pb.Memo{Content: "a remembered day", CreateTime: timestamppb.New(time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC))}}
	memo, err := service.CreateMemo(ctx, request)
	require.NoError(t, err)
	user, err := service.fetchCurrentUser(ctx)
	require.NoError(t, err)
	doc, err := service.Store.GetJournalDocument(ctx, user.ID, "provenance", request.MemoId)
	require.NoError(t, err)
	require.NotNil(t, doc)
	var provenance struct {
		EnteredTs int64  `json:"enteredTs"`
		Source    string `json:"source"`
		Imported  bool   `json:"imported"`
	}
	require.NoError(t, json.Unmarshal(doc.Payload, &provenance))
	require.GreaterOrEqual(t, provenance.EnteredTs, beforeSec)
	require.Equal(t, "manual", provenance.Source)
	require.False(t, provenance.Imported)
	require.Equal(t, request.Memo.CreateTime.Seconds, memo.CreateTime.Seconds)
	_, err = service.CreateMemo(ctx, request)
	require.Equal(t, codes.AlreadyExists, status.Code(err))
	_, err = service.UpdateMemo(ctx, &v1pb.UpdateMemoRequest{Memo: &v1pb.Memo{Name: memo.Name, Content: "edited"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"content"}}})
	require.NoError(t, err)
	after, err := service.Store.GetJournalDocument(ctx, user.ID, "provenance", request.MemoId)
	require.NoError(t, err)
	var afterProvenance struct {
		EnteredTs int64  `json:"enteredTs"`
		Source    string `json:"source"`
		Imported  bool   `json:"imported"`
	}
	require.NoError(t, json.Unmarshal(after.Payload, &afterProvenance))
	require.Equal(t, provenance.EnteredTs, afterProvenance.EnteredTs)
	require.Equal(t, provenance.Source, afterProvenance.Source)
	require.Equal(t, provenance.Imported, afterProvenance.Imported)
}

func TestJournalRecordMetadataCASRejectsChangesAfterPreflight(t *testing.T) {
	for _, change := range []string{"date", "location", "attachment"} {
		t.Run(change, func(t *testing.T) {
			service, ctx := newUploadTestService(t)
			memo, err := service.CreateMemo(ctx, &v1pb.CreateMemoRequest{Memo: &v1pb.Memo{Content: "same text"}})
			require.NoError(t, err)
			uid := strings.TrimPrefix(memo.Name, "memos/")
			original, err := service.Store.GetMemo(ctx, &store.FindMemo{UID: &uid})
			require.NoError(t, err)
			version, err := service.journalRecordHash(ctx, original)
			require.NoError(t, err)
			switch change {
			case "date":
				nextDate := original.CreatedTs - 86400
				require.NoError(t, service.Store.UpdateMemo(ctx, &store.UpdateMemo{ID: original.ID, CreatedTs: &nextDate}))
			case "location":
				require.NoError(t, service.Store.UpdateMemo(ctx, &store.UpdateMemo{ID: original.ID, Payload: &storepb.MemoPayload{Location: &storepb.MemoPayload_Location{Placeholder: "another device"}}}))
			case "attachment":
				_, err := service.Store.CreateAttachment(ctx, &store.Attachment{UID: "extra-media", CreatorID: original.CreatorID, MemoID: &original.ID, Filename: "new.txt", Type: "text/plain", Blob: []byte("added remotely")})
				require.NoError(t, err)
			default:
				t.Fatalf("unexpected change: %s", change)
			}
			next := "would overwrite local metadata"
			err = service.Store.ApplyMemoMutation(ctx, &store.MemoMutation{MemoID: original.ID, MemoCreatorID: original.CreatorID, ExpectedMemoContent: original.Content, ExpectedRecordHash: version, MemoUpdate: &store.UpdateMemo{ID: original.ID, Content: &next}})
			require.ErrorIs(t, err, store.ErrMemoMutationConflict)
			saved, err := service.Store.GetMemo(ctx, &store.FindMemo{UID: &uid})
			require.NoError(t, err)
			require.Equal(t, original.Content, saved.Content)
		})
	}
}

func TestJournalRecordPureMetadataWritersCannotBothCommit(t *testing.T) {
	service, ctx := newUploadTestService(t)
	memo, err := service.CreateMemo(ctx, &v1pb.CreateMemoRequest{Memo: &v1pb.Memo{Content: "same text"}})
	require.NoError(t, err)
	uid := strings.TrimPrefix(memo.Name, "memos/")
	original, err := service.Store.GetMemo(ctx, &store.FindMemo{UID: &uid})
	require.NoError(t, err)
	version, err := service.journalRecordHash(ctx, original)
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, delta := range []int64{86400, 172800} {
		go func(delta int64) {
			<-start
			nextDate := original.CreatedTs - delta
			results <- service.Store.ApplyMemoMutation(ctx, &store.MemoMutation{MemoID: original.ID, MemoCreatorID: original.CreatorID, ExpectedMemoContent: original.Content, ExpectedRecordHash: version, MemoUpdate: &store.UpdateMemo{ID: original.ID, CreatedTs: &nextDate}})
		}(delta)
	}
	close(start)
	failures, successes := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, store.ErrMemoMutationConflict)
			failures++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, failures)
	// An HTTP caller using the same old complete version must also be rejected,
	// even though neither writer changed the memo text.
	staleCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("x-memos-expected-record-sha256", version))
	_, err = service.UpdateMemo(staleCtx, &v1pb.UpdateMemoRequest{Memo: &v1pb.Memo{Name: memo.Name, Location: &v1pb.Location{Placeholder: "stale"}}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"location"}}})
	require.Equal(t, codes.Aborted, status.Code(err))
	current, err := service.Store.GetMemo(ctx, &store.FindMemo{UID: &uid})
	require.NoError(t, err)
	freshVersion, err := service.journalRecordHash(ctx, current)
	require.NoError(t, err)
	freshCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("x-memos-expected-record-sha256", freshVersion))
	updated, err := service.UpdateMemo(freshCtx, &v1pb.UpdateMemoRequest{Memo: &v1pb.Memo{Name: memo.Name, Location: &v1pb.Location{Placeholder: "fresh metadata edit"}}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"location"}}})
	require.NoError(t, err)
	require.Equal(t, "fresh metadata edit", updated.Location.Placeholder)
	require.Equal(t, memo.Content, updated.Content)
}

func TestJournalActualModificationTimeDoesNotChangeSelectedDates(t *testing.T) {
	service, ctx := newUploadTestService(t)
	selected := timestamppb.New(time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC))
	memo, err := service.CreateMemo(ctx, &v1pb.CreateMemoRequest{MemoId: "actual-modification-time", Memo: &v1pb.Memo{Content: "before", CreateTime: selected, UpdateTime: selected}})
	require.NoError(t, err)
	user, err := service.fetchCurrentUser(ctx)
	require.NoError(t, err)
	uid := strings.TrimPrefix(memo.Name, "memos/")
	doc, err := service.Store.GetJournalDocument(ctx, user.ID, "provenance", uid)
	require.NoError(t, err)
	var initial map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc.Payload, &initial))
	initial["modifiedTs"] = json.RawMessage(`1000`)
	initial["originalUID"] = json.RawMessage(`"kept-source"`)
	payload, err := json.Marshal(initial)
	require.NoError(t, err)
	_, err = service.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: user.ID, Kind: "provenance", Key: uid, Payload: payload}, doc.Version)
	require.NoError(t, err)
	beforeEditSec := time.Now().Unix()
	edited, err := service.UpdateMemo(ctx, &v1pb.UpdateMemoRequest{Memo: &v1pb.Memo{Name: memo.Name, Content: "after", UpdateTime: selected}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"content", "update_time"}}})
	require.NoError(t, err)
	require.Equal(t, selected.Seconds, edited.UpdateTime.Seconds)
	require.Equal(t, selected.Seconds, edited.CreateTime.Seconds)
	actual, err := service.Store.GetJournalDocument(ctx, user.ID, "provenance", uid)
	require.NoError(t, err)
	var values struct {
		EnteredTs   int64  `json:"enteredTs"`
		ModifiedTs  int64  `json:"modifiedTs"`
		OriginalUID string `json:"originalUID"`
	}
	require.NoError(t, json.Unmarshal(actual.Payload, &values))
	var enteredTs int64
	require.NoError(t, json.Unmarshal(initial["enteredTs"], &enteredTs))
	require.Equal(t, enteredTs, values.EnteredTs)
	require.GreaterOrEqual(t, values.ModifiedTs, beforeEditSec)
	require.Equal(t, "kept-source", values.OriginalUID)
	// A delayed provenance update from an earlier commit must not move time back.
	require.NoError(t, service.markJournalRecordModified(ctx, user.ID, uid, values.ModifiedTs-1))
	finalDoc, err := service.Store.GetJournalDocument(ctx, user.ID, "provenance", uid)
	require.NoError(t, err)
	require.Equal(t, actual.Payload, finalDoc.Payload)
}
