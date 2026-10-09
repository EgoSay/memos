package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"uuid"

	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/usememos/memos/core/access"
	"github.com/usememos/memos/core/journal"
	"github.com/usememos/memos/core/journalbackup"
	"github.com/usememos/memos/core/journalrecord"
	"github.com/usememos/memos/core/partition"
	"github.com/usememos/memos/internal/identifier"
	v1pb "github.com/usememos/memos/proto/gen/api/v1"
	storepb "github.com/usememos/memos/proto/gen/store"
	"github.com/usememos/memos/store"
)

type backupPreview struct {
	Token       string        `json:"token"`
	Digest      string        `json:"digest"`
	CreatedTs   int64         `json:"createdTs"`
	ExpiresTs   int64         `json:"expiresTs"`
	Memos       int           `json:"memos"`
	Attachments int           `json:"attachments"`
	Originals   int           `json:"originals"`
	Warnings    []string      `json:"warnings"`
	Status      string        `json:"status"`
	StartedTs   int64         `json:"startedTs"`
	Report      *backupReport `json:"report,omitempty"`
}
type backupReport struct {
	Memos       int      `json:"memos"`
	Attachments int      `json:"attachments"`
	Originals   int      `json:"originals"`
	Documents   int      `json:"documents"`
	Warnings    []string `json:"warnings"`
	Complete    bool     `json:"complete"`
}

func (s *APIV1Service) registerJournalBackupRoutes(g *echo.Group) {
	g.GET("/backups/export", s.journalExportBackup)
	g.POST("/backups/preview", s.journalPreviewBackup)
	g.POST("/backups/:token/restore", s.journalRestoreBackup)
}

func (s *APIV1Service) journalExportBackup(c *echo.Context) error {
	owner, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	file, err := os.CreateTemp("", "journal-export-*.zip")
	if err != nil {
		return journalError(c, err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := s.writeJournalBackup(c.Request().Context(), owner, file, c.QueryParam("history") == "true"); err != nil {
		return journalError(c, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return journalError(c, err)
	}
	c.Response().Header().Set("Content-Disposition", `attachment; filename="personal-journal-`+time.Now().UTC().Format("20060102")+`.zip"`)
	return c.Stream(http.StatusOK, "application/zip", file)
}

func (s *APIV1Service) writeJournalBackup(ctx context.Context, owner *store.User, dest io.Writer, history bool) error {
	memos, err := journal.MemoNames(ctx, s.Store, owner.ID, true)
	if err != nil {
		return err
	}
	writer := journalbackup.NewWriter(dest)
	manifest := &journalbackup.Manifest{CreatedTs: time.Now().Unix(), GeneratorVersion: s.Profile.Version, IncludeHistory: history, Memos: []journalbackup.Memo{}, Attachments: []journalbackup.Attachment{}, Documents: []journalbackup.Document{}, Warnings: []string{}}
	memoIDs := map[int32]string{}
	memoUIDs := map[string]bool{}
	attachments := map[string]*store.Attachment{}
	memoAttachments := map[int32]map[string]bool{}
	readable := make([]*store.Memo, 0, len(memos))
	for _, memo := range memos {
		readContext, err := s.buildMemoReadContextForViewer(ctx, memo, owner, false, nil)
		if err != nil {
			return err
		}
		if !access.CheckMemoReadContext(readContext).Allowed() {
			manifest.Warnings = append(manifest.Warnings, "跳过当前没有读取权限的旧协作记录："+memo.UID)
			continue
		}
		readable = append(readable, memo)
		memoIDs[memo.ID] = memo.UID
		memoUIDs[memo.UID] = true
	}
	memos = readable
	for _, memo := range memos {
		contentPath := "memos/" + memo.UID + ".md"
		if err := writer.Add(contentPath, strings.NewReader(memo.Content)); err != nil {
			return err
		}
		payload, err := protojson.Marshal(memo.Payload)
		if err != nil {
			return err
		}
		record := journalbackup.Memo{UID: memo.UID, CreatedTs: memo.CreatedTs, UpdatedTs: memo.UpdatedTs, State: string(memo.RowStatus), ContentPath: contentPath, Payload: payload}
		provenance, err := s.Store.GetJournalDocument(ctx, owner.ID, "provenance", memo.UID)
		if err != nil {
			return err
		}
		if provenance != nil {
			var metadata struct {
				ModifiedTs int64 `json:"modifiedTs"`
				EnteredTs  int64 `json:"enteredTs"`
			}
			if err := json.Unmarshal(provenance.Payload, &metadata); err != nil {
				return err
			}
			record.ModifiedTs = metadata.ModifiedTs
			record.EnteredTs = metadata.EnteredTs
		}
		relations, err := s.Store.ListMemoRelations(ctx, &store.FindMemoRelation{MemoID: &memo.ID})
		if err != nil {
			return err
		}
		for _, relation := range relations {
			if relation.Type == store.MemoRelationReference && memoIDs[relation.RelatedMemoID] != "" {
				record.RelatedUIDs = append(record.RelatedUIDs, memoIDs[relation.RelatedMemoID])
			}
		}
		manifest.Memos = append(manifest.Memos, record)
		files, err := s.Store.ListAttachments(ctx, &store.FindAttachment{MemoID: &memo.ID, SkipDefaultLimit: true})
		if err != nil {
			return err
		}
		memoAttachments[memo.ID] = map[string]bool{}
		for _, a := range files {
			if a.CreatorID == owner.ID {
				attachments[a.UID] = a
				memoAttachments[memo.ID][a.UID] = true
			}
		}
	}
	docs, err := s.Store.ListJournalDocuments(ctx, &store.FindJournalDocument{OwnerID: &owner.ID})
	if err != nil {
		return err
	}
	for _, doc := range docs {
		keep := false
		if config, isConfig, err := backupPublicationDocument(doc, memoUIDs); err != nil {
			return err
		} else if isConfig {
			if config != nil {
				manifest.Documents = append(manifest.Documents, *config)
			}
			continue
		}
		switch doc.Kind {
		case "preferences", partition.PartitionKind:
			keep = true
		case partition.MappingKind:
			var m partition.Mapping
			if err := json.Unmarshal(doc.Payload, &m); err != nil {
				return err
			}
			keep = memoUIDs[m.MemoUID]
		case "insight":
			var result journalInsightResult
			if err := json.Unmarshal(doc.Payload, &result); err != nil {
				return err
			}
			keep = true
			for _, source := range result.Sources {
				if !memoUIDs[strings.TrimPrefix(source.Name, "memos/")] {
					keep = false
				}
			}
		case "revision":
			if !history {
				continue
			}
			var revision journalrecord.Revision
			if err := json.Unmarshal(doc.Payload, &revision); err != nil {
				return err
			}
			if journalrecord.Expired(revision.CreatedTs, time.Now()) {
				continue
			}
			var memo v1pb.Memo
			if err := protojson.Unmarshal(revision.Memo, &memo); err != nil {
				return err
			}
			keep = memoUIDs[strings.TrimPrefix(memo.Name, "memos/")]
			if keep {
				for _, ref := range memo.Attachments {
					uid := strings.TrimPrefix(ref.Name, "attachments/")
					a, err := s.Store.GetAttachment(ctx, &store.FindAttachment{UID: &uid, CreatorID: &owner.ID})
					if err != nil {
						return err
					}
					if a != nil {
						attachments[a.UID] = a
					} else {
						manifest.Warnings = append(manifest.Warnings, "编辑历史引用的附件已不存在："+uid)
					}
				}
			}
		default:
		}
		if keep {
			manifest.Documents = append(manifest.Documents, journalbackup.Document{Kind: doc.Kind, Key: doc.Key, Payload: doc.Payload})
		}
	}
	keys := make([]string, 0, len(attachments))
	for key := range attachments {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, uid := range keys {
		a := attachments[uid]
		record := journalbackup.Attachment{UID: a.UID, Filename: a.Filename, Type: a.Type, CreatedTs: a.CreatedTs, UpdatedTs: a.UpdatedTs}
		if a.MemoID != nil {
			record.MemoUID = memoIDs[*a.MemoID]
		}
		// Legacy S3 payloads embedded storage credentials. The archive owns the
		// bytes and media metadata, never an active storage connection.
		metadata := &storepb.AttachmentPayload{}
		if a.Payload != nil {
			metadata = proto.CloneOf(a.Payload)
		}
		metadata.Payload = nil
		payload, err := protojson.Marshal(metadata)
		if err != nil {
			return err
		}
		record.Payload = payload
		if a.StorageType == storepb.AttachmentStorageType_EXTERNAL {
			record.ExternalLink = a.Reference
			manifest.Warnings = append(manifest.Warnings, "外链附件未包含文件字节："+a.Filename)
		} else {
			record.Path = "media/" + a.UID + "/" + backupMediaFilename(a.Filename)
			stream, err := s.openAttachmentContent(ctx, a)
			if err != nil {
				return errors.Wrap(err, "backup cannot read a managed attachment")
			}
			err = writer.Add(record.Path, stream)
			closeErr := stream.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			originalPath, err := attachmentOriginalPath(s.Profile.Data, a.UID)
			if err != nil {
				return err
			}
			original, err := os.Open(originalPath)
			if errors.Is(err, os.ErrNotExist) {
				manifest.Warnings = append(manifest.Warnings, "旧附件仅有现存文件，未保存原始上传字节："+a.Filename)
			} else if err != nil {
				return err
			} else {
				record.OriginalPath = "originals/" + a.UID + "/" + backupMediaFilename(a.Filename)
				err = writer.Add(record.OriginalPath, original)
				closeErr := original.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
			}
		}
		manifest.Attachments = append(manifest.Attachments, record)
	}
	var index strings.Builder
	index.WriteString("# 私人生活记录备份\n\n正文位于 memos/，媒体位于 media/，原始上传文件（若存在）位于 originals/。manifest.json 保存日期、关系、校验和及恢复所需数据。\n\n此档案不包含访问令牌、分享授权、Webhook 密钥或待发送任务。导入后全部记录私密，不会自动发布。请另存到独立设备或你选择的备份位置；同一磁盘的副本不能应对磁盘损坏。\n\n")
	for _, m := range manifest.Memos {
		fmt.Fprintf(&index, "- [%s · %s](%s)\n", time.Unix(m.CreatedTs, 0).UTC().Format(time.RFC3339), m.UID, m.ContentPath)
	}
	if err := writer.Add("README.md", strings.NewReader(index.String())); err != nil {
		return err
	}
	// Fail visibly if originals changed during the streaming snapshot.
	for _, source := range memos {
		current, err := s.Store.GetMemo(ctx, &store.FindMemo{ID: &source.ID})
		if err != nil {
			return err
		}
		if current == nil || current.Content != source.Content || current.CreatedTs != source.CreatedTs || current.UpdatedTs != source.UpdatedTs || current.RowStatus != source.RowStatus || !proto.Equal(current.Payload, source.Payload) {
			return errors.New("备份期间记录发生变化，请重新导出")
		}
		trash, err := s.Store.GetJournalDocument(ctx, owner.ID, "trash", source.UID)
		if err != nil {
			return err
		}
		if trash != nil {
			return errors.New("备份期间记录已删除，请重新导出")
		}
		files, err := s.Store.ListAttachments(ctx, &store.FindAttachment{MemoID: &source.ID, CreatorID: &owner.ID, SkipDefaultLimit: true})
		if err != nil {
			return err
		}
		if len(files) != len(memoAttachments[source.ID]) {
			return errors.New("备份期间媒体发生变化，请重新导出")
		}
		for _, current := range files {
			old := attachments[current.UID]
			if !memoAttachments[source.ID][current.UID] || old == nil || current.UpdatedTs != old.UpdatedTs || current.Filename != old.Filename || current.Size != old.Size || current.Reference != old.Reference || !proto.Equal(current.Payload, old.Payload) {
				return errors.New("备份期间媒体发生变化，请重新导出")
			}
		}
	}
	return writer.Close(manifest)
}

func (s *APIV1Service) backupStagePath(owner int32, token string) (string, error) {
	if !identifier.UIDMatcher.MatchString(token) {
		return "", errors.New("invalid backup token")
	}
	return filepath.Join(s.Profile.Data, "backup-staging", strconv.FormatInt(int64(owner), 10)+"-"+token+".zip"), nil
}

// Keep extensions readable outside the app; the exact upload filename remains
// in the manifest even when a filesystem-reserved character is replaced.
func backupMediaFilename(filename string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/:\\`, r) {
			return '_'
		}
		return r
	}, filename)
}

func (s *APIV1Service) journalPreviewBackup(c *echo.Context) error {
	owner, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	if err := s.cleanExpiredBackupPreviews(c.Request().Context(), owner.ID); err != nil {
		return journalError(c, err)
	}
	token := uuid.NewV4().String()
	path, err := s.backupStagePath(owner.ID, token)
	if err != nil {
		return journalError(c, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return journalError(c, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return journalError(c, err)
	}
	defer file.Close()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(path)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(c.Request().Body, journalbackup.MaxArchiveBytes+1))
	if err != nil {
		return journalError(c, err)
	}
	if size > journalbackup.MaxArchiveBytes {
		return journalError(c, errors.New("备份文件超过 1 GiB"))
	}
	archive, err := journalbackup.Read(file, size)
	if err != nil {
		return journalError(c, err)
	}
	if err := validateJournalBackup(archive); err != nil {
		return journalError(c, err)
	}
	preview := backupPreview{Token: token, Digest: hex.EncodeToString(hash.Sum(nil)), CreatedTs: archive.Manifest.CreatedTs, ExpiresTs: time.Now().Add(2 * time.Hour).Unix(), Memos: len(archive.Manifest.Memos), Attachments: len(archive.Manifest.Attachments), Warnings: archive.Manifest.Warnings, Status: "preview"}
	for _, a := range archive.Manifest.Attachments {
		if a.OriginalPath != "" {
			preview.Originals++
		}
	}
	preview.Warnings = append(preview.Warnings, "恢复将暂停本账户现有分享与自动同步。重名记录保留为独立副本，不覆盖现有内容。")
	payload, _ := json.Marshal(preview)
	if _, err := s.Store.PutJournalDocument(c.Request().Context(), &store.JournalDocument{OwnerID: owner.ID, Kind: "backup-preview", Key: token, Payload: payload}, 0); err != nil {
		return journalError(c, err)
	}
	success = true
	return c.JSON(http.StatusOK, preview)
}

func validateJournalBackup(a *journalbackup.Archive) error {
	memos := map[string]bool{}
	attachments := map[string]bool{}
	for _, m := range a.Manifest.Memos {
		if !identifier.UIDMatcher.MatchString(m.UID) || memos[m.UID] || !a.Has(m.ContentPath) || (m.State != "NORMAL" && m.State != "ARCHIVED") {
			return errors.New("备份记录标识或状态无效")
		}
		memos[m.UID] = true
		if _, err := a.Text(m.ContentPath); err != nil {
			return err
		}
		payload := &storepb.MemoPayload{}
		if len(m.Payload) > 0 {
			if err := protojson.Unmarshal(m.Payload, payload); err != nil {
				return err
			}
		}
	}
	for _, m := range a.Manifest.Memos {
		for _, uid := range m.RelatedUIDs {
			if !memos[uid] {
				return errors.New("备份关系引用了档案之外的记录")
			}
		}
	}
	for _, file := range a.Manifest.Attachments {
		if !identifier.UIDMatcher.MatchString(file.UID) || attachments[file.UID] || !validateFilename(file.Filename) || (file.MemoUID != "" && !memos[file.MemoUID]) {
			return errors.New("备份附件标识无效")
		}
		attachments[file.UID] = true
		if file.ExternalLink != "" {
			if err := validateBackupExternalURL(file.ExternalLink); err != nil {
				return err
			}
		}
		if len(file.Payload) > 0 {
			if err := protojson.Unmarshal(file.Payload, &storepb.AttachmentPayload{}); err != nil {
				return err
			}
		}
		if file.ExternalLink == "" && !a.Has(file.Path) {
			return errors.New("备份缺少受管理媒体")
		}
		if file.OriginalPath != "" && !a.Has(file.OriginalPath) {
			return errors.New("备份缺少原始媒体")
		}
	}
	return validateBackupDocuments(a.Manifest.Documents, memos, attachments)
}

// Staged archives are private, temporary copies, not a permanent second backup.
func (s *APIV1Service) cleanExpiredBackupPreviews(ctx context.Context, owner int32) error {
	docs, err := s.Store.ListJournalDocuments(ctx, &store.FindJournalDocument{OwnerID: &owner, Kind: "backup-preview"})
	if err != nil {
		return err
	}
	for _, doc := range docs {
		var preview backupPreview
		if err := json.Unmarshal(doc.Payload, &preview); err != nil {
			return err
		}
		if preview.ExpiresTs >= time.Now().Unix() || preview.Status == "restoring" && preview.StartedTs > time.Now().Add(-30*time.Minute).Unix() {
			continue
		}
		path, err := s.backupStagePath(owner, doc.Key)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := s.Store.DeleteJournalDocument(ctx, owner, doc.Kind, doc.Key, doc.Version); err != nil {
			return err
		}
	}
	return nil
}
