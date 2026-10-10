package v1

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
	"golang.org/x/crypto/bcrypt"

	"github.com/EgoSay/kairos/core/journal"
	"github.com/EgoSay/kairos/internal/ratelimit"
	"github.com/EgoSay/kairos/store"
)

type journalShareRequest struct {
	Title             string              `json:"title"`
	Mode              string              `json:"mode"`
	Filter            journal.ShareFilter `json:"filter"`
	ExcludedMemoNames []string            `json:"excludedMemoNames"`
	Passcode          string              `json:"passcode"`
	ClearPasscode     bool                `json:"clearPasscode"`
	ExpiresTs         int64               `json:"expiresTs"`
	Version           int64               `json:"version"`
	Paused            bool                `json:"paused"`
	PreviewDigest     string              `json:"previewDigest"`
}

func (s *APIV1Service) registerJournalShareRoutes(group *echo.Group, e *echo.Echo) {
	group.GET("/shares", s.journalListShares)
	group.GET("/shares/:id", s.journalGetShare)
	group.POST("/shares/preview", s.journalPreviewShare)
	group.POST("/shares", s.journalCreateShare)
	group.PUT("/shares/:id", s.journalUpdateShare)
	group.PATCH("/shares/:id", s.journalShareState)
	group.POST("/shares/:id/rotate", s.journalRotateShare)
	group.DELETE("/shares/:id", s.journalDeleteShare)
	e.POST(JournalPublicReadPath, s.journalReadShare)
}

func (s *APIV1Service) journalShareItems(c *echo.Context, owner int32, filter journal.ShareFilter, excluded []string) ([]journal.ShareItem, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	ctx := c.Request().Context()
	memos, err := journal.MemoNames(ctx, s.Store, owner, false)
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{}
	for _, name := range excluded {
		skip[name] = true
	}
	items := []journal.ShareItem{}
	size := 0
	for _, m := range memos {
		if skip["memos/"+m.UID] || !filter.Matches(m) {
			continue
		}
		if len(items) >= 200 {
			return nil, errors.New("范围超过 200 条，请缩小范围")
		}
		item := journal.ShareItem{MemoName: "memos/" + m.UID, Content: journal.ShareText(m.Content), CreatedTs: m.CreatedTs, Media: []journal.ShareMedia{}}
		attachments, err := s.Store.ListAttachments(ctx, &store.FindAttachment{MemoID: &m.ID, CreatorID: &owner, GetBlob: true, SkipDefaultLimit: true})
		if err != nil {
			return nil, err
		}
		for _, attachment := range attachments {
			if attachment.Size > 20<<20 {
				return nil, errors.New("分享中有超过 20 MiB 的附件，请移除该记录后重试")
			}
			data, err := s.GetAttachmentBlob(ctx, attachment)
			if err != nil {
				return nil, errors.New("分享媒体读取失败，请先修复附件")
			}
			mime := attachment.Type
			if strings.HasPrefix(mime, "image/") {
				data, err = stripImageExif(bytes.NewReader(data), mime)
				if err != nil {
					return nil, errors.New("图片无法生成安全的分享版本")
				}
				if mime != "image/png" {
					mime = "image/jpeg"
				}
			}
			size += len(data)
			if size > 50<<20 {
				return nil, errors.New("分享媒体超过 50 MiB，请缩小范围")
			}
			item.Media = append(item.Media, journal.ShareMedia{ID: attachment.UID, Filename: attachment.Filename, Type: mime, Content: data})
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *APIV1Service) journalPreviewShare(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	var request journalShareRequest
	if err := journalBind(c, &request); err != nil {
		return journalError(c, err)
	}
	items, err := s.journalShareItems(c, u.ID, request.Filter, request.ExcludedMemoNames)
	if err != nil {
		return journalError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items, "previewDigest": shareDigest(items)})
}

func sharePublicSummary(share journal.Share) map[string]any {
	return map[string]any{"id": share.ID, "title": share.Title, "mode": share.Mode, "filter": share.Filter, "excludedMemoNames": share.ExcludedMemoNames, "expiresTs": share.ExpiresTs, "createdTs": share.CreatedTs, "paused": share.Paused, "version": share.Version, "hasPasscode": share.PasscodeHash != "", "url": "/s/" + share.Token}
}

func (s *APIV1Service) journalGetShare(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	doc, err := s.Store.GetJournalDocument(c.Request().Context(), u.ID, "share", c.Param("id"))
	if err != nil {
		return journalError(c, err)
	}
	if doc == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"message": "分享不存在"})
	}
	var share journal.Share
	if err := json.Unmarshal(doc.Payload, &share); err != nil {
		return journalError(c, err)
	}
	share.Version = doc.Version
	items := share.Items
	if share.Mode == "dynamic" {
		items, err = s.journalShareItems(c, u.ID, share.Filter, share.ExcludedMemoNames)
		if err != nil {
			return journalError(c, err)
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"share": sharePublicSummary(share), "items": items})
}

func (s *APIV1Service) journalListShares(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	docs, err := s.Store.ListJournalDocuments(c.Request().Context(), &store.FindJournalDocument{OwnerID: &u.ID, Kind: "share"})
	if err != nil {
		return journalError(c, err)
	}
	rows := []map[string]any{}
	for _, doc := range docs {
		var share journal.Share
		if err := json.Unmarshal(doc.Payload, &share); err != nil {
			return journalError(c, err)
		}
		share.Version = doc.Version
		rows = append(rows, sharePublicSummary(share))
	}
	return c.JSON(http.StatusOK, rows)
}

func (s *APIV1Service) journalCreateShare(c *echo.Context) error {
	return s.journalWriteShare(c, false)
}
func (s *APIV1Service) journalUpdateShare(c *echo.Context) error { return s.journalWriteShare(c, true) }

func (s *APIV1Service) journalWriteShare(c *echo.Context, updating bool) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	var req journalShareRequest
	if err := journalBind(c, &req); err != nil {
		return journalError(c, err)
	}
	if req.Mode == "" {
		req.Mode = "fixed"
	}
	if req.Mode != "fixed" && req.Mode != "dynamic" {
		return journalError(c, errors.New("invalid share mode"))
	}
	if len(req.Title) > 200 || len(req.Passcode) > 72 {
		return journalError(c, errors.New("名称或提取码过长"))
	}
	if req.ExpiresTs != 0 && req.ExpiresTs <= time.Now().Unix() {
		return journalError(c, errors.New("到期时间应在将来"))
	}
	var share journal.Share
	version := int64(0)
	if updating {
		doc, err := s.Store.GetJournalDocument(ctx, u.ID, "share", c.Param("id"))
		if err != nil {
			return journalError(c, err)
		}
		if doc == nil {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "分享不存在"})
		}
		if err := json.Unmarshal(doc.Payload, &share); err != nil {
			return journalError(c, err)
		}
		version = doc.Version
		if req.Version != version {
			return journalError(c, store.ErrJournalConflict)
		}
	} else {
		token := make([]byte, 32)
		if _, err := rand.Read(token); err != nil {
			return journalError(c, err)
		}
		share.Token = hex.EncodeToString(token)
		hash := sha256.Sum256([]byte(share.Token))
		share.ID = hex.EncodeToString(hash[:])
		share.CreatedTs = time.Now().Unix()
	}
	items, err := s.journalShareItems(c, u.ID, req.Filter, req.ExcludedMemoNames)
	if err != nil {
		return journalError(c, err)
	}
	if len(items) == 0 {
		return journalError(c, errors.New("当前范围没有可分享的记录，请重新选择。"))
	}
	if req.PreviewDigest == "" || req.PreviewDigest != shareDigest(items) {
		return c.JSON(http.StatusConflict, map[string]string{"message": "记录在预览后发生了变化，请重新预览后再分享。"})
	}
	share.Title = req.Title
	if share.Title == "" {
		share.Title = "一段记录"
	}
	share.Mode = req.Mode
	share.Filter = req.Filter
	share.ExcludedMemoNames = req.ExcludedMemoNames
	share.ExpiresTs = req.ExpiresTs
	share.Paused = req.Paused
	share.Items = items
	share.ApprovedImportedMemoNames = []string{}
	for _, item := range items {
		share.ApprovedImportedMemoNames = append(share.ApprovedImportedMemoNames, item.MemoName)
	}
	if share.Mode == "dynamic" {
		share.Items = nil
	}
	if req.ClearPasscode {
		share.PasscodeHash = ""
	}
	if req.Passcode != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Passcode), bcrypt.DefaultCost)
		if err != nil {
			return journalError(c, err)
		}
		share.PasscodeHash = string(hash)
	}
	payload, _ := json.Marshal(share)
	doc, err := s.Store.PutJournalDocument(ctx, &store.JournalDocument{OwnerID: u.ID, Kind: "share", Key: share.ID, Payload: payload}, version)
	if err != nil {
		return journalError(c, err)
	}
	share.Version = doc.Version
	return c.JSON(http.StatusOK, sharePublicSummary(share))
}

func (s *APIV1Service) journalDeleteShare(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	if err := s.Store.DeleteJournalDocument(c.Request().Context(), u.ID, "share", c.Param("id"), -1); err != nil {
		return journalError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"deleted": true})
}

func (s *APIV1Service) journalReadShare(c *echo.Context) error {
	if err := s.throttleAndCharge(ratelimit.ScopeAnonymous, c.RealIP(), 1); err != nil {
		return journalError(c, err)
	}
	setAPIResponseNoStoreHeaders(c.Response().Header())
	c.Response().Header().Set("X-Robots-Tag", "noindex, nofollow")
	c.Response().Header().Set("Referrer-Policy", "no-referrer")
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 4096)
	var req struct {
		Passcode string `json:"passcode"`
	}
	if err := journalBind(c, &req); err != nil {
		return journalError(c, err)
	}
	denied := func() error {
		return c.JSON(http.StatusNotFound, map[string]string{"message": "链接不可用，或提取码不正确。"})
	}
	token := c.Param("token")
	if len(token) != 64 {
		return denied()
	}
	if _, err := hex.DecodeString(token); err != nil {
		return denied()
	}
	hash := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(hash[:])
	ctx := c.Request().Context()
	docs, err := s.Store.ListJournalDocuments(ctx, &store.FindJournalDocument{Kind: "share"})
	if err != nil {
		return journalError(c, err)
	}
	var doc *store.JournalDocument
	for _, d := range docs {
		var candidate journal.Share
		if json.Unmarshal(d.Payload, &candidate) != nil {
			continue
		}
		candidateHash := sha256.Sum256([]byte(candidate.Token))
		if hex.EncodeToString(candidateHash[:]) == key {
			doc = d
			break
		}
	}
	if doc == nil {
		return denied()
	}
	var share journal.Share
	if err := json.Unmarshal(doc.Payload, &share); err != nil {
		return denied()
	}
	if share.Paused || share.ExpiresTs != 0 && share.ExpiresTs <= time.Now().Unix() {
		return denied()
	}
	if share.PasscodeHash != "" && bcrypt.CompareHashAndPassword([]byte(share.PasscodeHash), []byte(req.Passcode)) != nil {
		return denied()
	}
	owner, err := s.Store.GetUser(ctx, &store.FindUser{ID: &doc.OwnerID})
	if err != nil || owner == nil || owner.RowStatus != store.Normal {
		return denied()
	}
	items := share.Items
	if share.Mode == "dynamic" {
		items, err = s.journalShareItems(c, doc.OwnerID, share.Filter, share.ExcludedMemoNames)
		if err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"message": "这份分享暂时无法打开。"})
		}
	}
	excluded := map[string]bool{}
	for _, name := range share.ExcludedMemoNames {
		excluded[name] = true
	}
	visible := []map[string]any{}
	for _, item := range items {
		if share.Mode == "dynamic" {
			imported, err := s.Store.GetJournalDocument(ctx, doc.OwnerID, "provenance", strings.TrimPrefix(item.MemoName, "memos/"))
			if err != nil {
				return journalError(c, err)
			}
			var provenance struct {
				Imported bool `json:"imported"`
			}
			if imported != nil {
				if err := json.Unmarshal(imported.Payload, &provenance); err != nil {
					return journalError(c, err)
				}
			}
			if provenance.Imported {
				approved := false
				for _, name := range share.ApprovedImportedMemoNames {
					if name == item.MemoName {
						approved = true
						break
					}
				}
				if !approved {
					continue
				}
			}
		}
		if excluded[item.MemoName] {
			continue
		}
		memo, err := journal.OwnedMemo(ctx, s.Store, doc.OwnerID, item.MemoName)
		if err != nil || memo.RowStatus != store.Normal {
			continue
		}
		visible = append(visible, map[string]any{"content": item.Content, "createdTs": item.CreatedTs, "media": item.Media})
	}
	current, err := s.Store.GetJournalDocument(ctx, doc.OwnerID, "share", doc.Key)
	if err != nil {
		return journalError(c, err)
	}
	if current == nil || current.Version != doc.Version {
		return denied()
	}
	return c.JSON(http.StatusOK, map[string]any{"title": share.Title, "timezone": share.Filter.Timezone, "items": visible})
}

// shareDigest binds approval to exact text, recorded dates and sanitized media.
func shareDigest(items []journal.ShareItem) string {
	copyItems := append([]journal.ShareItem{}, items...)
	slices.SortFunc(copyItems, func(a, b journal.ShareItem) int { return strings.Compare(a.MemoName, b.MemoName) })
	data, _ := json.Marshal(copyItems)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *APIV1Service) journalShareState(c *echo.Context) error {
	return s.journalChangeShareState(c, false)
}
func (s *APIV1Service) journalRotateShare(c *echo.Context) error {
	return s.journalChangeShareState(c, true)
}

// State changes never reselect or recapture a fixed snapshot.
func (s *APIV1Service) journalChangeShareState(c *echo.Context, rotate bool) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	var req struct {
		Version int64 `json:"version"`
		Paused  *bool `json:"paused"`
	}
	if err := journalBind(c, &req); err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	doc, err := s.Store.GetJournalDocument(ctx, u.ID, "share", c.Param("id"))
	if err != nil {
		return journalError(c, err)
	}
	if doc == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"message": "分享不存在"})
	}
	if doc.Version != req.Version {
		return journalError(c, store.ErrJournalConflict)
	}
	var share journal.Share
	if err := json.Unmarshal(doc.Payload, &share); err != nil {
		return journalError(c, err)
	}
	if rotate {
		token := make([]byte, 32)
		if _, err := rand.Read(token); err != nil {
			return journalError(c, err)
		}
		share.Token = hex.EncodeToString(token)
	} else {
		if req.Paused == nil {
			return journalError(c, errors.New("请指定分享状态"))
		}
		share.Paused = *req.Paused
	}
	doc.Payload, _ = json.Marshal(share)
	saved, err := s.Store.PutJournalDocument(ctx, doc, req.Version)
	if err != nil {
		return journalError(c, err)
	}
	share.Version = saved.Version
	return c.JSON(http.StatusOK, sharePublicSummary(share))
}
