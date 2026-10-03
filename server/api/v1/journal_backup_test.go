package v1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/usememos/memos/core/journal"
	"github.com/usememos/memos/core/journalbackup"
	"github.com/usememos/memos/core/journalrecord"
	"github.com/usememos/memos/core/partition"
	v1pb "github.com/usememos/memos/proto/gen/api/v1"
	storepb "github.com/usememos/memos/proto/gen/store"
	"github.com/usememos/memos/store"
)

func putBackupTestDocument(t *testing.T, svc *APIV1Service, owner int32, kind, key string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	_, err = svc.Store.PutJournalDocument(context.Background(), &store.JournalDocument{OwnerID: owner, Kind: kind, Key: key, Payload: payload}, 0)
	require.NoError(t, err)
}

func TestJournalBackupNewInstanceRoundtripAndNoReplay(t *testing.T) {
	ctx := context.Background()
	source := newIntegrationService(t)
	owner := createSpaceTestUser(ctx, t, source, "archive-owner", store.RoleUser)
	other := createSpaceTestUser(ctx, t, source, "archive-other", store.RoleUser)
	body := "生活原文\r\n#生活/周末\n[关联](memos/second-record)"
	first, err := source.Store.CreateMemo(ctx, &store.Memo{UID: "first-record", CreatorID: owner.ID, Content: body, Visibility: store.Public, Payload: &storepb.MemoPayload{}})
	require.NoError(t, err)
	second, err := source.Store.CreateMemo(ctx, &store.Memo{UID: "second-record", CreatorID: owner.ID, Content: "另一段记忆", Visibility: store.Private, Payload: &storepb.MemoPayload{}})
	require.NoError(t, err)
	_, err = source.Store.CreateMemo(ctx, &store.Memo{UID: "someone-else", CreatorID: other.ID, Content: "NEVER_EXPORT_FOREIGN", Visibility: store.Public, Payload: &storepb.MemoPayload{}})
	require.NoError(t, err)
	created, updated := int64(1600000000), int64(1600000300)
	require.NoError(t, source.Store.UpdateMemo(ctx, &store.UpdateMemo{ID: first.ID, CreatedTs: &created, UpdatedTs: &updated}))
	archived := store.Archived
	require.NoError(t, source.Store.UpdateMemo(ctx, &store.UpdateMemo{ID: second.ID, RowStatus: &archived}))
	_, err = source.Store.UpsertMemoRelation(ctx, &store.MemoRelation{MemoID: first.ID, RelatedMemoID: second.ID, Type: store.MemoRelationReference})
	require.NoError(t, err)
	media := []byte("derived media bytes")
	original := []byte("ORIGINAL UPLOAD WITH METADATA\x00\xff")
	mediaPath := filepath.Join(source.Profile.Data, "original-test-media")
	require.NoError(t, os.WriteFile(mediaPath, media, 0600))
	_, err = source.Store.CreateAttachment(ctx, &store.Attachment{UID: "media-original", CreatorID: owner.ID, MemoID: &first.ID, Filename: "周末:照片.jpg", Type: "image/jpeg", Blob: media, Size: int64(len(media)), StorageType: storepb.AttachmentStorageType_LOCAL, Reference: mediaPath, CreatedTs: created, UpdatedTs: updated, Payload: &storepb.AttachmentPayload{Payload: &storepb.AttachmentPayload_S3Object_{S3Object: &storepb.AttachmentPayload_S3Object{S3Config: &storepb.StorageS3Config{AccessKeySecret: "SENSITIVE_S3_SECRET"}}}}})
	require.NoError(t, err)
	path, err := attachmentOriginalPath(source.Profile.Data, "media-original")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, original, 0600))
	put := func(kind, key string, value any) { putBackupTestDocument(t, source, owner.ID, kind, key, value) }
	put("provenance", first.UID, map[string]any{"modifiedTs": updated + 30, "enteredTs": created + 30})
	put("preferences", "main", journal.Preferences{Timezone: "Asia/Shanghai", CalendarVisible: true, CalendarColor: true, ExcludedMemoNames: []string{"memos/second-record"}})
	put(partition.PartitionKind, "private-zone", partition.Partition{ID: "private-zone", Name: "愿意公开的片段"})
	put(partition.MappingKind, first.UID, partition.Mapping{MemoUID: first.UID, PartitionID: "private-zone"})
	put(partition.TargetKind, "destination", partition.Target{ID: "destination", PartitionID: "private-zone", Name: "My receiver", URL: "https://secret.invalid/SENSITIVE_URL", SigningSecret: "SENSITIVE_SIGNING_SECRET", Enabled: true, Epoch: 1, Fields: partition.Fields{Content: true}})
	put("share", "shared-selection", journal.Share{ID: "shared-selection", Token: "SENSITIVE_SHARE_TOKEN", PasscodeHash: "SENSITIVE_PASSCODE", Filter: journal.ShareFilter{Timezone: "UTC", MemoNames: []string{"memos/first-record"}}, Mode: "fixed", Items: []journal.ShareItem{{MemoName: "memos/first-record", Content: body}}})
	put(partition.ExternalKind, partition.RecordTargetKey("destination", first.UID), partition.External{TargetID: "destination", MemoUID: first.UID, ExternalID: "platform-post-7", PublishedURL: "https://social.example/posts/7"})
	put(partition.DeliveryKind, "stable-event", partition.Delivery{ID: "stable-event", TargetID: "destination", PartitionID: "private-zone", MemoUID: first.UID, Status: "pending", Sequence: 7})
	put(partition.SequenceKind, partition.RecordTargetKey("destination", first.UID), backupSequence{Sequence: 7, TargetID: "destination", MemoUID: first.UID})
	put("insight", "saved-reflection", journalInsightResult{ID: "saved-reflection", Text: "原有感受，仅供参考", Sources: []journalInsightSource{{Name: "memos/first-record"}}, Citations: []string{"memos/first-record"}})
	revisionRaw, err := protojson.Marshal(&v1pb.Memo{Name: "memos/first-record", Creator: "users/archive-owner", Content: "修改前原文", Visibility: v1pb.Visibility_PUBLIC, Attachments: []*v1pb.Attachment{{Name: "attachments/media-original"}}})
	require.NoError(t, err)
	put("revision", "first-record_revision", journalrecord.Revision{ID: "revision", CreatedTs: time.Now().Unix(), Memo: revisionRaw})
	var zip bytes.Buffer
	require.NoError(t, source.writeJournalBackup(ctx, owner, &zip, true))
	for _, secret := range []string{"NEVER_EXPORT_FOREIGN", "SENSITIVE_URL", "SENSITIVE_SIGNING_SECRET", "SENSITIVE_SHARE_TOKEN", "SENSITIVE_PASSCODE", "SENSITIVE_S3_SECRET"} {
		require.NotContains(t, zip.String(), secret)
	}
	a, err := journalbackup.Read(bytes.NewReader(zip.Bytes()), int64(zip.Len()))
	require.NoError(t, err)
	require.NoError(t, validateJournalBackup(a))
	require.Len(t, a.Manifest.Memos, 2)
	require.Len(t, a.Manifest.Attachments, 1)
	digestBytes := sha256.Sum256(zip.Bytes())
	digest := hex.EncodeToString(digestBytes[:])

	// A separate SQLite store and data directory prove this is independently
	// restorable; the authenticated owner differs from the original numeric ID.
	dest := newIntegrationService(t)
	createSpaceTestUser(ctx, t, dest, "unrelated-destination-user", store.RoleUser)
	newOwner := createSpaceTestUser(ctx, t, dest, "restored-owner", store.RoleUser)
	report, err := dest.restoreJournalBackup(ctx, newOwner, a, digest)
	require.NoError(t, err)
	require.True(t, report.Complete)
	require.Equal(t, 2, report.Memos)
	require.Equal(t, 1, report.Attachments)
	require.Equal(t, 1, report.Originals)
	restored, err := dest.Store.GetMemo(ctx, &store.FindMemo{UID: &first.UID})
	require.NoError(t, err)
	require.Equal(t, body, restored.Content)
	require.Equal(t, created, restored.CreatedTs)
	require.Equal(t, updated, restored.UpdatedTs)
	require.Equal(t, newOwner.ID, restored.CreatorID)
	require.Equal(t, store.Private, restored.Visibility)
	require.Nil(t, restored.SpaceID)
	archivedMemo, err := dest.Store.GetMemo(ctx, &store.FindMemo{UID: &second.UID})
	require.NoError(t, err)
	require.Equal(t, store.Archived, archivedMemo.RowStatus)
	relations, err := dest.Store.ListMemoRelations(ctx, &store.FindMemoRelation{MemoID: &restored.ID})
	require.NoError(t, err)
	require.Len(t, relations, 1)
	require.Equal(t, archivedMemo.ID, relations[0].RelatedMemoID)
	uid := "media-original"
	attachment, err := dest.Store.GetAttachment(ctx, &store.FindAttachment{UID: &uid})
	require.NoError(t, err)
	require.Equal(t, created, attachment.CreatedTs)
	require.Equal(t, updated, attachment.UpdatedTs)
	require.Equal(t, newOwner.ID, attachment.CreatorID)
	actual, err := os.ReadFile(attachment.Reference)
	require.NoError(t, err)
	require.Equal(t, media, actual)
	path, err = attachmentOriginalPath(dest.Profile.Data, uid)
	require.NoError(t, err)
	actual, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, actual)
	docs, err := dest.Store.ListJournalDocuments(ctx, &store.FindJournalDocument{OwnerID: &newOwner.ID})
	require.NoError(t, err)
	for _, doc := range docs {
		switch doc.Kind {
		case "share":
			var share journal.Share
			require.NoError(t, json.Unmarshal(doc.Payload, &share))
			require.True(t, share.Paused)
			require.NotEmpty(t, share.Token)
			require.Empty(t, share.PasscodeHash)
		case partition.TargetKind:
			var target partition.Target
			require.NoError(t, json.Unmarshal(doc.Payload, &target))
			require.False(t, target.Enabled)
			require.Empty(t, target.URL)
			require.Empty(t, target.SigningSecret)
		case partition.MappingKind:
			var mapping partition.Mapping
			require.NoError(t, json.Unmarshal(doc.Payload, &mapping))
			require.True(t, mapping.Suspended)
		case partition.DeliveryKind:
			var delivery partition.Delivery
			require.NoError(t, json.Unmarshal(doc.Payload, &delivery))
			require.Equal(t, "cancelled", delivery.Status)
			require.Equal(t, "stable-event", delivery.SourceEventID)
		case partition.ExternalKind:
			var external partition.External
			require.NoError(t, json.Unmarshal(doc.Payload, &external))
			require.Equal(t, "platform-post-7", external.ExternalID)
		case partition.SequenceKind:
			var sequence backupSequence
			require.NoError(t, json.Unmarshal(doc.Payload, &sequence))
			require.Equal(t, int64(7), sequence.Sequence)
		case "insight":
			var result journalInsightResult
			require.NoError(t, json.Unmarshal(doc.Payload, &result))
			require.True(t, result.Stale)
			require.Equal(t, "原有感受，仅供参考", result.Text)
		case "revision":
			var revision journalrecord.Revision
			require.NoError(t, json.Unmarshal(doc.Payload, &revision))
			var memo v1pb.Memo
			require.NoError(t, protojson.Unmarshal(revision.Memo, &memo))
			require.Equal(t, v1pb.Visibility_PRIVATE, memo.Visibility)
			require.Equal(t, "users/restored-owner", memo.Creator)
			require.Equal(t, "修改前原文", memo.Content)
		default:
		}
	}
	prov, err := dest.Store.GetJournalDocument(ctx, newOwner.ID, "provenance", first.UID)
	require.NoError(t, err)
	require.NotNil(t, prov)
	var provenance struct {
		ModifiedTs int64 `json:"modifiedTs"`
		EnteredTs  int64 `json:"enteredTs"`
	}
	require.NoError(t, json.Unmarshal(prov.Payload, &provenance))
	require.Equal(t, updated+30, provenance.ModifiedTs)
	require.Equal(t, created+30, provenance.EnteredTs)
	service := partition.New(dest.Store, dest.readPartitionMemo)
	service.Send = nil // ProcessDue must find no runnable work at all.
	require.NoError(t, service.ProcessDue(ctx))
	_, err = dest.restoreJournalBackup(ctx, newOwner, a, digest)
	require.NoError(t, err)
	all, err := dest.Store.ListMemos(ctx, &store.FindMemo{CreatorID: &newOwner.ID})
	require.NoError(t, err)
	require.Len(t, all, 2, "retrying identical archive cannot duplicate records")
}

func TestJournalBackupPreviewIsolationAndValidationBeforeMutation(t *testing.T) {
	svc := newIntegrationService(t)
	ctx := context.Background()
	owner := createSpaceTestUser(ctx, t, svc, "preview-owner", store.RoleUser)
	other := createSpaceTestUser(ctx, t, svc, "preview-other", store.RoleUser)
	current := owner.ID
	e := echo.New()
	group := e.Group("/api/v1/journal")
	group.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.SetRequest(c.Request().WithContext(userCtx(c.Request().Context(), current)))
			return next(c)
		}
	})
	svc.registerJournalBackupRoutes(group)
	request := func(path string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/journal"+path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		return w
	}
	archive := func(docs []journalbackup.Document) []byte {
		var output bytes.Buffer
		w := journalbackup.NewWriter(&output)
		require.NoError(t, w.Add("memos/sample.md", strings.NewReader("my original")))
		require.NoError(t, w.Close(&journalbackup.Manifest{Memos: []journalbackup.Memo{{UID: "sample", ContentPath: "memos/sample.md", State: "NORMAL"}}, Documents: docs}))
		return output.Bytes()
	}
	bad := archive([]journalbackup.Document{{Kind: "preferences", Key: "main", Payload: json.RawMessage(`{"timezone":"Not/A/Timezone"}`)}})
	w := request("/backups/preview", bad)
	require.NotEqual(t, 200, w.Code)
	all, err := svc.Store.ListMemos(ctx, &store.FindMemo{CreatorID: &owner.ID})
	require.NoError(t, err)
	require.Empty(t, all)
	w = request("/backups/preview", archive(nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var preview backupPreview
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &preview))
	all, err = svc.Store.ListMemos(ctx, &store.FindMemo{CreatorID: &owner.ID})
	require.NoError(t, err)
	require.Empty(t, all, "preview cannot import")
	body, _ := json.Marshal(map[string]string{"digest": preview.Digest})
	current = other.ID
	w = request("/backups/"+preview.Token+"/restore", body)
	require.NotEqual(t, 200, w.Code)
	current = owner.ID
	w = request("/backups/"+preview.Token+"/restore", []byte(`{"digest":"changed"}`))
	require.NotEqual(t, 200, w.Code)
	w = request("/backups/"+preview.Token+"/restore", body)
	require.Equal(t, 200, w.Code, w.Body.String())
	w = request("/backups/"+preview.Token+"/restore", body)
	require.Equal(t, 200, w.Code, w.Body.String())
	all, err = svc.Store.ListMemos(ctx, &store.FindMemo{CreatorID: &owner.ID})
	require.NoError(t, err)
	require.Len(t, all, 1)
	staged, err := svc.backupStagePath(owner.ID, preview.Token)
	require.NoError(t, err)
	_, err = os.Stat(staged)
	require.True(t, os.IsNotExist(err))
}

func TestBackupLinkRemapDoesNotRewriteIDPrefixes(t *testing.T) {
	text := "memos/abc memos/abcdef file/image/a.jpg attachments/image2"
	require.Equal(t, "memos/new memos/abcdef file/restored/a.jpg attachments/image2", remapBackupLinks(text, map[string]string{"abc": "new"}, map[string]string{"image": "restored"}))
}

func TestJournalBackupExistingInstancePauseAndCollision(t *testing.T) {
	ctx := context.Background()
	svc := newIntegrationService(t)
	owner := createSpaceTestUser(ctx, t, svc, "existing-owner", store.RoleUser)
	other := createSpaceTestUser(ctx, t, svc, "still-active-owner", store.RoleUser)
	original, err := svc.Store.CreateMemo(ctx, &store.Memo{UID: "same-id", CreatorID: owner.ID, Content: "existing must survive", Visibility: store.Private, Payload: &storepb.MemoPayload{}})
	require.NoError(t, err)
	for _, user := range []*store.User{owner, other} {
		putBackupTestDocument(t, svc, user.ID, partition.TargetKind, "live-target", partition.Target{ID: "live-target", PartitionID: "zone", Enabled: true, Epoch: 2})
		putBackupTestDocument(t, svc, user.ID, "share", "live-share", journal.Share{ID: "live-share", Token: "original-live-token"})
	}
	var encoded bytes.Buffer
	w := journalbackup.NewWriter(&encoded)
	require.NoError(t, w.Add("memos/same.md", strings.NewReader("restored original [self](memos/same-id)")))
	require.NoError(t, w.Close(&journalbackup.Manifest{Memos: []journalbackup.Memo{{UID: "same-id", ContentPath: "memos/same.md", State: "NORMAL"}}}))
	a, err := journalbackup.Read(bytes.NewReader(encoded.Bytes()), int64(encoded.Len()))
	require.NoError(t, err)
	hash := sha256.Sum256(encoded.Bytes())
	report, err := svc.restoreJournalBackup(ctx, owner, a, hex.EncodeToString(hash[:]))
	require.NoError(t, err)
	require.True(t, report.Complete)
	kept, err := svc.Store.GetMemo(ctx, &store.FindMemo{ID: &original.ID})
	require.NoError(t, err)
	require.Equal(t, "existing must survive", kept.Content)
	memos, err := svc.Store.ListMemos(ctx, &store.FindMemo{CreatorID: &owner.ID})
	require.NoError(t, err)
	require.Len(t, memos, 2)
	for _, memo := range memos {
		if memo.ID != original.ID {
			require.Equal(t, "restored original [self](memos/"+memo.UID+")", memo.Content)
			require.Equal(t, store.Private, memo.Visibility)
		}
	}
	for _, user := range []*store.User{owner, other} {
		doc, err := svc.Store.GetJournalDocument(ctx, user.ID, partition.TargetKind, "live-target")
		require.NoError(t, err)
		var target partition.Target
		require.NoError(t, json.Unmarshal(doc.Payload, &target))
		require.Equal(t, user.ID != owner.ID, target.Enabled)
		doc, err = svc.Store.GetJournalDocument(ctx, user.ID, "share", "live-share")
		require.NoError(t, err)
		var share journal.Share
		require.NoError(t, json.Unmarshal(doc.Payload, &share))
		require.Equal(t, user.ID == owner.ID, share.Paused)
	}
}
