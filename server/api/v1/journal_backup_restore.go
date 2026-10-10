package v1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/EgoSay/kairos/core/journal"
	"github.com/EgoSay/kairos/core/journalbackup"
	"github.com/EgoSay/kairos/core/journalrecord"
	"github.com/EgoSay/kairos/core/memopayload"
	"github.com/EgoSay/kairos/core/partition"
	v1pb "github.com/EgoSay/kairos/proto/gen/api/v1"
	storepb "github.com/EgoSay/kairos/proto/gen/store"
	"github.com/EgoSay/kairos/store"
)

func (s *APIV1Service) journalRestoreBackup(c *echo.Context) error {
	owner, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	var request struct {
		Digest string `json:"digest"`
	}
	if err := journalBind(c, &request); err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	token := c.Param("token")
	doc, err := s.Store.GetJournalDocument(ctx, owner.ID, "backup-preview", token)
	if err != nil {
		return journalError(c, err)
	}
	if doc == nil {
		return journalError(c, errors.New("恢复预览已失效，请重新上传"))
	}
	var preview backupPreview
	if err := json.Unmarshal(doc.Payload, &preview); err != nil {
		return journalError(c, err)
	}
	if preview.Digest != request.Digest {
		return journalError(c, errors.New("备份摘要与预览不一致"))
	}
	if preview.Status == "complete" {
		return c.JSON(http.StatusOK, preview.Report)
	}
	if preview.ExpiresTs < time.Now().Unix() {
		return journalError(c, errors.New("恢复预览已过期，请重新上传"))
	}
	if preview.Status == "restoring" && preview.StartedTs > time.Now().Add(-30*time.Minute).Unix() {
		return journalError(c, errors.New("这份备份正在恢复，请稍后查看"))
	}
	path, err := s.backupStagePath(owner.ID, token)
	if err != nil {
		return journalError(c, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return journalError(c, errors.New("暂存备份已不可用，请重新上传"))
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return journalError(c, err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return journalError(c, err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != preview.Digest {
		return journalError(c, errors.New("暂存备份校验失败，请重新上传"))
	}
	archive, err := journalbackup.Read(file, stat.Size())
	if err != nil {
		return journalError(c, err)
	}
	if err := validateJournalBackup(archive); err != nil {
		return journalError(c, err)
	}
	preview.Status = "restoring"
	preview.StartedTs = time.Now().Unix()
	payload, _ := json.Marshal(preview)
	claimed, err := s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner.ID, Kind: "backup-preview", Key: token, Payload: payload}, doc.Version)
	if err != nil {
		return journalError(c, err)
	}
	report, restoreErr := s.restoreJournalBackup(ctx, owner, archive, preview.Digest)
	preview.Report = report
	preview.Status = "complete"
	if restoreErr != nil {
		preview.Status = "failed"
	}
	payload, _ = json.Marshal(preview)
	_, saveErr := s.Store.PutJournalDocument(context.WithoutCancel(ctx), &store.JournalDocument{OwnerID: owner.ID, Kind: "backup-preview", Key: token, Payload: payload}, claimed.Version)
	if restoreErr != nil {
		return c.JSON(http.StatusConflict, map[string]any{"message": "恢复尚未完成，已写入的内容会保留；可重新尝试，同一档案不会重复导入。", "report": report})
	}
	if saveErr != nil {
		return journalError(c, saveErr)
	}
	_ = os.Remove(path)
	return c.JSON(http.StatusOK, report)
}

func (s *APIV1Service) pauseJournalPublication(ctx context.Context, owner int32) error {
	service := s.JournalPartitions
	if service == nil {
		service = partition.New(s.Store, s.readPartitionMemo)
	}
	if err := service.PauseOwner(ctx, owner); err != nil {
		return err
	}
	docs, err := s.Store.ListJournalDocuments(ctx, &store.FindJournalDocument{OwnerID: &owner, Kind: "share"})
	if err != nil {
		return err
	}
	for _, doc := range docs {
		var share journal.Share
		if err := json.Unmarshal(doc.Payload, &share); err != nil {
			return err
		}
		share.Paused = true
		payload, err := json.Marshal(share)
		if err != nil {
			return err
		}
		if _, err := s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner, Kind: "share", Key: doc.Key, Payload: payload}, doc.Version); err != nil {
			return err
		}
	}
	return nil
}

func (s *APIV1Service) backupUID(ctx context.Context, owner int32, digest, kind, source string) (string, error) {
	key := backupRestoredID(digest, kind, source)
	doc, err := s.Store.GetJournalDocument(ctx, owner, "backup-restored-map", key)
	if err != nil {
		return "", err
	}
	if doc != nil {
		var uid string
		err := json.Unmarshal(doc.Payload, &uid)
		return uid, err
	}
	uid := source
	exists := false
	switch kind {
	case "memo":
		m, err := s.Store.GetMemo(ctx, &store.FindMemo{UID: &uid})
		if err != nil {
			return "", err
		}
		exists = m != nil
	case "attachment":
		a, err := s.Store.GetAttachment(ctx, &store.FindAttachment{UID: &uid})
		if err != nil {
			return "", err
		}
		exists = a != nil
	default:
	}
	if exists || kind == "partition" || kind == "target" {
		sum := sha256.Sum256([]byte(strconv.FormatInt(int64(owner), 10) + digest + kind + source))
		uid = hex.EncodeToString(sum[:])[:36]
	}
	payload, _ := json.Marshal(uid)
	_, err = s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner, Kind: "backup-restored-map", Key: key, Payload: payload}, 0)
	if errors.Is(err, store.ErrJournalConflict) {
		return s.backupUID(ctx, owner, digest, kind, source)
	}
	return uid, err
}

func (s *APIV1Service) restoreJournalBackup(ctx context.Context, owner *store.User, a *journalbackup.Archive, digest string) (*backupReport, error) {
	report := &backupReport{Warnings: append([]string{}, a.Manifest.Warnings...)}
	if err := validateJournalBackup(a); err != nil {
		return report, err
	}
	if err := s.pauseJournalPublication(ctx, owner.ID); err != nil {
		return report, err
	}
	memoUIDs := map[string]string{}
	attachmentUIDs := map[string]string{}
	memos := map[string]*store.Memo{}
	for _, record := range a.Manifest.Memos {
		uid, err := s.backupUID(ctx, owner.ID, digest, "memo", record.UID)
		if err != nil {
			return report, err
		}
		memoUIDs[record.UID] = uid
		if uid != record.UID {
			report.Warnings = append(report.Warnings, "重名记录已作为独立私密副本保留："+record.UID)
		}
	}
	for _, record := range a.Manifest.Attachments {
		uid, err := s.backupUID(ctx, owner.ID, digest, "attachment", record.UID)
		if err != nil {
			return report, err
		}
		attachmentUIDs[record.UID] = uid
	}
	for _, record := range a.Manifest.Memos {
		uid := memoUIDs[record.UID]
		// Exclude imports from existing dynamic shares before a memo can appear.
		metadata := map[string]any{"imported": true, "importedTs": time.Now().Unix(), "recordedTs": record.CreatedTs, "originalUID": record.UID, "sourceArchive": digest}
		if record.ModifiedTs != 0 {
			metadata["modifiedTs"] = record.ModifiedTs
		}
		if record.EnteredTs != 0 {
			metadata["enteredTs"] = record.EnteredTs
		}
		provenance, _ := json.Marshal(metadata)
		if _, err := s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner.ID, Kind: "provenance", Key: uid, Payload: provenance}, 0); err != nil && !errors.Is(err, store.ErrJournalConflict) {
			return report, err
		}
		existing, err := s.Store.GetMemo(ctx, &store.FindMemo{UID: &uid})
		if err != nil {
			return report, err
		}
		if existing != nil {
			if existing.CreatorID != owner.ID {
				return report, errors.New("restoration destination collision")
			}
			memos[record.UID] = existing
			report.Memos++
			continue
		}
		content, err := a.Text(record.ContentPath)
		if err != nil {
			return report, err
		}
		memo := &store.Memo{UID: uid, CreatorID: owner.ID, Content: remapBackupLinks(string(content), memoUIDs, attachmentUIDs), Visibility: store.Private, CreatedTs: record.CreatedTs, UpdatedTs: record.UpdatedTs, Payload: &storepb.MemoPayload{}}
		if len(record.Payload) > 0 {
			if err := protojson.Unmarshal(record.Payload, memo.Payload); err != nil {
				return report, err
			}
		}
		if err := memopayload.RebuildMemoPayload(ctx, memo, s.MarkdownService); err != nil {
			return report, err
		}
		memo, err = s.Store.CreateMemo(ctx, memo)
		if err != nil {
			return report, err
		}
		state := store.RowStatus(record.State)
		if err := s.Store.UpdateMemo(ctx, &store.UpdateMemo{ID: memo.ID, RowStatus: &state, CreatedTs: &record.CreatedTs, UpdatedTs: &record.UpdatedTs}); err != nil {
			return report, err
		}
		memos[record.UID] = memo
		report.Memos++
	}
	for _, record := range a.Manifest.Attachments {
		uid := attachmentUIDs[record.UID]
		existing, err := s.Store.GetAttachment(ctx, &store.FindAttachment{UID: &uid})
		if err != nil {
			return report, err
		}
		if existing != nil && existing.CreatorID != owner.ID {
			return report, errors.New("restoration attachment collision")
		}
		if existing == nil {
			attachment := &store.Attachment{UID: uid, CreatorID: owner.ID, Filename: record.Filename, Type: record.Type, CreatedTs: record.CreatedTs, UpdatedTs: record.UpdatedTs, Payload: &storepb.AttachmentPayload{}}
			if len(record.Payload) > 0 {
				if err := protojson.Unmarshal(record.Payload, attachment.Payload); err != nil {
					return report, err
				}
			}
			attachment.Payload.Payload = nil
			if record.MemoUID != "" {
				attachment.MemoID = &memos[record.MemoUID].ID
			}
			if record.ExternalLink != "" {
				attachment.StorageType = storepb.AttachmentStorageType_EXTERNAL
				attachment.Reference = record.ExternalLink
			} else {
				path := filepath.Join(s.Profile.Data, "assets", "restored", uid)
				size, err := restoreArchiveFile(a, record.Path, path)
				if err != nil {
					return report, err
				}
				attachment.StorageType = storepb.AttachmentStorageType_LOCAL
				attachment.Reference = path
				attachment.Size = size
			}
			if _, err := s.Store.CreateAttachment(ctx, attachment); err != nil {
				return report, err
			}
		}
		if record.OriginalPath != "" {
			path, err := attachmentOriginalPath(s.Profile.Data, uid)
			if err != nil {
				return report, err
			}
			if _, err := restoreArchiveFile(a, record.OriginalPath, path); err != nil {
				return report, err
			}
			report.Originals++
		}
		report.Attachments++
	}
	for _, record := range a.Manifest.Memos {
		for _, related := range record.RelatedUIDs {
			if _, err := s.Store.UpsertMemoRelation(ctx, &store.MemoRelation{MemoID: memos[record.UID].ID, RelatedMemoID: memos[related].ID, Type: store.MemoRelationReference}); err != nil {
				return report, err
			}
		}
	}
	for _, document := range a.Manifest.Documents {
		restored, err := s.restoreBackupDocument(ctx, owner, a, digest, document, memoUIDs, attachmentUIDs)
		if err != nil {
			return report, err
		}
		if restored {
			report.Documents++
		}
	}
	report.Complete = true
	return report, nil
}

func restoreArchiveFile(a *journalbackup.Archive, entry, destination string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return 0, err
	}
	if file, err := os.Open(destination); err == nil {
		defer file.Close()
		source, err := a.Open(entry)
		if err != nil {
			return 0, err
		}
		defer source.Close()
		left, right := sha256.New(), sha256.New()
		size, err := io.Copy(left, file)
		if err != nil {
			return 0, err
		}
		if _, err := io.Copy(right, source); err != nil {
			return 0, err
		}
		if !bytes.Equal(left.Sum(nil), right.Sum(nil)) {
			return 0, errors.New("existing restored media differs; it was not overwritten")
		}
		return size, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	source, err := a.Open(entry)
	if err != nil {
		return 0, err
	}
	defer source.Close()
	file, err := os.CreateTemp(filepath.Dir(destination), ".restore-")
	if err != nil {
		return 0, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	n, err := io.Copy(file, source)
	if err != nil {
		return 0, err
	}
	if err := file.Sync(); err != nil {
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	if err := os.Link(file.Name(), destination); err != nil {
		return 0, err
	}
	return n, nil
}

func remapBackupLinks(text string, memos, attachments map[string]string) string {
	// Only resource paths are rewritten when an existing instance already owns
	// the original ID. All other original wording stays unchanged.
	return backupResourcePath.ReplaceAllStringFunc(text, func(resource string) string {
		parts := strings.SplitN(resource, "/", 2)
		mapping := memos
		if parts[0] != "memos" {
			mapping = attachments
		}
		if uid := mapping[parts[1]]; uid != "" {
			return parts[0] + "/" + uid
		}
		return resource
	})
}

var backupResourcePath = regexp.MustCompile(`\b(?:memos|attachments|file)/[a-zA-Z0-9_-]+`)

func backupRestoredID(digest, kind, source string) string {
	sum := sha256.Sum256([]byte(digest + "\x00" + kind + "\x00" + source))
	return hex.EncodeToString(sum[:])
}

func (s *APIV1Service) restoreBackupDocument(ctx context.Context, owner *store.User, _ *journalbackup.Archive, digest string, doc journalbackup.Document, memoUIDs, attachmentUIDs map[string]string) (bool, error) {
	if publication, handled, err := s.restoreBackupPublication(ctx, owner.ID, digest, doc, memoUIDs); err != nil {
		return false, err
	} else if handled {
		if publication == nil {
			return false, nil
		}
		_, err := s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner.ID, Kind: publication.Kind, Key: publication.Key, Payload: publication.Payload}, 0)
		if errors.Is(err, store.ErrJournalConflict) {
			return false, nil
		}
		return err == nil, err
	}
	key := doc.Key
	var value any
	switch doc.Kind {
	case "preferences":
		var prefs journal.Preferences
		if err := json.Unmarshal(doc.Payload, &prefs); err != nil {
			return false, err
		}
		if _, err := time.LoadLocation(prefs.Timezone); err != nil {
			return false, err
		}
		names := []string{}
		for _, name := range prefs.ExcludedMemoNames {
			if uid := memoUIDs[strings.TrimPrefix(name, "memos/")]; uid != "" {
				names = append(names, "memos/"+uid)
			}
		}
		prefs.ExcludedMemoNames = names
		key = "main"
		value = prefs
	case partition.PartitionKind:
		var p partition.Partition
		if err := json.Unmarshal(doc.Payload, &p); err != nil {
			return false, err
		}
		uid, err := s.backupUID(ctx, owner.ID, digest, "partition", p.ID)
		if err != nil {
			return false, err
		}
		p.ID = uid
		p.Version = 0
		key = uid
		value = p
	case partition.MappingKind:
		var m partition.Mapping
		if err := json.Unmarshal(doc.Payload, &m); err != nil {
			return false, err
		}
		uid := memoUIDs[m.MemoUID]
		if uid == "" {
			return false, nil
		}
		p, err := s.backupUID(ctx, owner.ID, digest, "partition", m.PartitionID)
		if err != nil {
			return false, err
		}
		m.MemoUID = uid
		m.PartitionID = p
		m.Suspended = true
		m.Version = 0
		key = uid
		value = m
	case "insight":
		var insight journalInsightResult
		if err := json.Unmarshal(doc.Payload, &insight); err != nil {
			return false, err
		}
		for i, source := range insight.Sources {
			uid := memoUIDs[strings.TrimPrefix(source.Name, "memos/")]
			if uid == "" {
				return false, nil
			}
			insight.Sources[i].Name = "memos/" + uid
		}
		for i, name := range insight.Citations {
			uid := memoUIDs[strings.TrimPrefix(name, "memos/")]
			if uid == "" {
				return false, nil
			}
			insight.Citations[i] = "memos/" + uid
		}
		key = backupRestoredID(digest, doc.Kind, key)
		insight.ID = key
		insight.Stale = true
		value = insight
	case "revision":
		var revision journalrecord.Revision
		if err := json.Unmarshal(doc.Payload, &revision); err != nil {
			return false, err
		}
		if journalrecord.Expired(revision.CreatedTs, time.Now()) {
			return false, nil
		}
		var memo v1pb.Memo
		if err := protojson.Unmarshal(revision.Memo, &memo); err != nil {
			return false, err
		}
		uid := memoUIDs[strings.TrimPrefix(memo.Name, "memos/")]
		if uid == "" {
			return false, nil
		}
		memo.Name = "memos/" + uid
		memo.Creator = BuildUserName(owner.Username)
		memo.Visibility = v1pb.Visibility_PRIVATE
		memo.Space = nil
		memo.Parent = nil
		memo.Relations = nil
		memo.Reactions = nil
		memo.Content = remapBackupLinks(memo.Content, memoUIDs, attachmentUIDs)
		files := []*v1pb.Attachment{}
		for _, ref := range memo.Attachments {
			if mapped := attachmentUIDs[strings.TrimPrefix(ref.Name, "attachments/")]; mapped != "" {
				ref.Name = "attachments/" + mapped
				ref.Creator = BuildUserName(owner.Username)
				memoName := memo.Name
				ref.Memo = &memoName
				files = append(files, ref)
			}
		}
		memo.Attachments = files
		raw, err := protojson.Marshal(&memo)
		if err != nil {
			return false, err
		}
		revision.Memo = raw
		revision.ID = backupRestoredID(digest, doc.Kind, revision.ID)[:32]
		key = uid + "_" + revision.ID
		value = revision
	default:
		return false, errors.New("backup document type is not restorable")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	_, err = s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: owner.ID, Kind: doc.Kind, Key: key, Payload: payload}, 0)
	if errors.Is(err, store.ErrJournalConflict) {
		return false, nil
	}
	return err == nil, err
}
