package v1

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/usememos/memos/core/access"
	"github.com/usememos/memos/core/partition"
	"github.com/usememos/memos/internal/webhook"
	storepb "github.com/usememos/memos/proto/gen/store"
	"github.com/usememos/memos/store"
)

func (s *APIV1Service) registerJournalPartitionRoutes(group *echo.Group) {
	s.JournalPartitions = partition.New(s.Store, s.readPartitionMemo)
	group.GET("/partitions", s.journalListPartitions)
	group.POST("/partitions", s.journalSavePartition)
	group.PUT("/partitions/:id", s.journalSavePartition)
	group.DELETE("/partitions/:id", s.journalDeletePartition)
	group.GET("/partitions/:id/targets", s.journalListPartitionTargets)
	group.GET("/partitions/:id/memos", s.journalPartitionMemos)
	group.POST("/partitions/:id/send", s.journalPartitionBackfill)
	group.POST("/partitions/:id/targets", s.journalSavePartitionTarget)
	group.PUT("/targets/:id", s.journalSavePartitionTarget)
	group.DELETE("/targets/:id", s.journalDeletePartitionTarget)
	group.POST("/targets/:id/test", s.journalTestPartitionTarget)
	group.GET("/memos/:uid/partition", s.journalGetMemoPartition)
	group.PUT("/memos/:uid/partition", s.journalAssignMemoPartition)
	group.POST("/memos/:uid/send", s.journalSendPartitionMemo)
	group.GET("/deliveries", s.journalListPartitionDeliveries)
	group.POST("/deliveries/:id/retry", s.journalRetryPartitionDelivery)
}

func partitionError(c *echo.Context, err error) error {
	if errors.Is(err, partition.ErrNotFound) {
		return journalError(c, status.Error(codes.NotFound, "记录或分区不存在"))
	}
	return journalError(c, err)
}

func (s *APIV1Service) journalListPartitions(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	values, err := s.JournalPartitions.ListPartitions(c.Request().Context(), u.ID)
	if err != nil {
		return partitionError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"partitions": values})
}
func (s *APIV1Service) journalSavePartition(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	var request struct {
		Name    string `json:"name"`
		Version int64  `json:"version"`
	}
	if err := journalBind(c, &request); err != nil {
		return journalError(c, err)
	}
	p, err := s.JournalPartitions.SavePartition(c.Request().Context(), u.ID, &partition.Partition{ID: c.Param("id"), Name: request.Name, Version: request.Version})
	if err != nil {
		return partitionError(c, err)
	}
	return c.JSON(http.StatusOK, p)
}
func (s *APIV1Service) journalDeletePartition(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	version, err := strconv.ParseInt(c.QueryParam("version"), 10, 64)
	if err != nil || version < 1 {
		return journalError(c, errors.New("version is required"))
	}
	if err := s.JournalPartitions.DeletePartition(c.Request().Context(), u.ID, c.Param("id"), version); err != nil {
		return partitionError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
func redactPartitionTarget(t *partition.Target) *partition.Target {
	copy := *t
	copy.HasSigningSecret = t.SigningSecret != ""
	copy.SigningSecret = ""
	return &copy
}
func (s *APIV1Service) journalListPartitionTargets(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	values, err := s.JournalPartitions.ListTargets(c.Request().Context(), u.ID, c.Param("id"))
	if err != nil {
		return partitionError(c, err)
	}
	for i, t := range values {
		values[i] = redactPartitionTarget(t)
	}
	return c.JSON(http.StatusOK, map[string]any{"targets": values})
}
func (s *APIV1Service) journalSavePartitionTarget(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	var request struct {
		PartitionID   string           `json:"partitionId"`
		Name          string           `json:"name"`
		URL           string           `json:"url"`
		SigningSecret string           `json:"signingSecret"`
		Enabled       bool             `json:"enabled"`
		AutoUpdate    bool             `json:"autoUpdate"`
		Retract       bool             `json:"retract"`
		Fields        partition.Fields `json:"fields"`
		Version       int64            `json:"version"`
	}
	if err := journalBind(c, &request); err != nil {
		return journalError(c, err)
	}
	id := c.Param("id")
	if c.Request().Method == http.MethodPost {
		request.PartitionID = id
		id = ""
	}
	t := &partition.Target{ID: id, PartitionID: request.PartitionID, Name: request.Name, URL: request.URL, SigningSecret: request.SigningSecret, Enabled: request.Enabled, AutoUpdate: request.AutoUpdate, Retract: request.Retract, Fields: request.Fields, Version: request.Version}
	value, err := s.JournalPartitions.SaveTarget(c.Request().Context(), u.ID, t)
	if err != nil {
		return partitionError(c, err)
	}
	// A newly generated secret is shown once to configure the receiver. GET
	// responses never repeat it, and browser query caches never retain it.
	if id != "" {
		value = redactPartitionTarget(value)
	}
	return c.JSON(http.StatusOK, value)
}
func (s *APIV1Service) journalDeletePartitionTarget(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	version, err := strconv.ParseInt(c.QueryParam("version"), 10, 64)
	if err != nil || version < 1 {
		return journalError(c, errors.New("version is required"))
	}
	if err := s.JournalPartitions.DeleteTarget(c.Request().Context(), u.ID, c.Param("id"), version); err != nil {
		return partitionError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
func (s *APIV1Service) journalTestPartitionTarget(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	t, err := s.JournalPartitions.GetTarget(c.Request().Context(), u.ID, c.Param("id"))
	if err != nil {
		return partitionError(c, err)
	}
	id := "sample-" + uuid.NewV4().String()
	_, err = webhook.Deliver(c.Request().Context(), &webhook.Request{URL: t.URL, SigningSecret: t.SigningSecret, MessageID: id, Payload: map[string]any{"eventId": id, "event": "test", "content": "这是一条连接测试，不包含任何私人记录。"}})
	if err != nil {
		return journalError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "delivered", "message": "测试已送达，不代表社媒已发布"})
}
func (s *APIV1Service) journalGetMemoPartition(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	if _, err := s.readPartitionMemo(c.Request().Context(), u.ID, c.Param("uid"), false); err != nil {
		return partitionError(c, err)
	}
	value, err := s.JournalPartitions.GetMapping(c.Request().Context(), u.ID, c.Param("uid"))
	if err != nil {
		return partitionError(c, err)
	}
	return c.JSON(http.StatusOK, value)
}
func (s *APIV1Service) journalAssignMemoPartition(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	var request struct {
		PartitionID string `json:"partitionId"`
		Version     int64  `json:"version"`
	}
	if err := journalBind(c, &request); err != nil {
		return journalError(c, err)
	}
	value, err := s.JournalPartitions.Assign(c.Request().Context(), u.ID, c.Param("uid"), request.PartitionID, request.Version)
	if err != nil {
		return partitionError(c, err)
	}
	return c.JSON(http.StatusOK, value)
}
func (s *APIV1Service) journalSendPartitionMemo(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	var request struct {
		Manual bool `json:"manual"`
	}
	if err := journalBind(c, &request); err != nil {
		return journalError(c, err)
	}
	values, err := s.JournalPartitions.Commit(c.Request().Context(), u.ID, c.Param("uid"), request.Manual)
	if err != nil {
		return partitionError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"deliveries": values})
}
func (s *APIV1Service) journalListPartitionDeliveries(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	values, err := s.JournalPartitions.ListDeliveries(c.Request().Context(), u.ID)
	if err != nil {
		return partitionError(c, err)
	}
	slices.SortFunc(values, func(a, b *partition.Delivery) int { return cmp.Compare(b.CreatedTs, a.CreatedTs) })
	if len(values) > 200 {
		values = values[:200]
	}
	return c.JSON(http.StatusOK, map[string]any{"deliveries": values})
}
func (s *APIV1Service) journalRetryPartitionDelivery(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	value, err := s.JournalPartitions.Retry(c.Request().Context(), u.ID, c.Param("id"))
	if err != nil {
		return partitionError(c, err)
	}
	return c.JSON(http.StatusOK, value)
}

func (s *APIV1Service) readPartitionMemo(ctx context.Context, owner int32, uid string, withMedia bool) (*partition.Memo, error) {
	m, err := s.Store.GetMemo(ctx, &store.FindMemo{UID: &uid, CreatorID: &owner, ExcludeComments: true})
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, partition.ErrNotFound
	}
	user, err := s.Store.GetUser(ctx, &store.FindUser{ID: &owner})
	if err != nil {
		return nil, err
	}
	if user == nil || user.RowStatus != store.Normal {
		return nil, partition.ErrNotFound
	}
	readContext, err := s.buildMemoReadContextForViewer(ctx, m, user, false, nil)
	if err != nil {
		return nil, err
	}
	if !access.CheckMemoReadContext(readContext).Allowed() {
		return nil, partition.ErrNotFound
	}
	trash, err := s.Store.GetJournalDocument(ctx, owner, "trash", uid)
	if err != nil {
		return nil, err
	}
	if trash != nil {
		return nil, partition.ErrNotFound
	}
	attachments, err := s.Store.ListAttachments(ctx, &store.FindAttachment{MemoID: &m.ID, SkipDefaultLimit: true})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(attachments, func(a, b *store.Attachment) int { return cmp.Compare(a.ID, b.ID) })
	revision := struct {
		Content          string
		Created, Updated int64
		Attachments      []string
	}{m.Content, m.CreatedTs, m.UpdatedTs, []string{}}
	for _, a := range attachments {
		revision.Attachments = append(revision.Attachments, a.UID+":"+strconv.FormatInt(a.UpdatedTs, 10)+":"+strconv.FormatInt(a.Size, 10))
	}
	encoded, _ := json.Marshal(revision)
	digest := sha256.Sum256(encoded)
	result := &partition.Memo{UID: uid, Content: m.Content, RecordTime: m.CreatedTs, Revision: hex.EncodeToString(digest[:]), Active: m.RowStatus == store.Normal}
	if !withMedia {
		return result, nil
	}
	var total int64
	for _, a := range attachments {
		if a.StorageType == storepb.AttachmentStorageType_EXTERNAL {
			return nil, errors.New("external attachments are not fetched for automatic delivery")
		}
		if a.CreatorID != owner {
			return nil, errors.New("attachment is not owned by the journal owner")
		}
		total += a.Size
		if total > 45<<20 {
			return nil, errors.New("media exceeds delivery limit")
		}
		data, err := s.GetAttachmentBlob(ctx, a)
		if err != nil {
			return nil, err
		}
		mimeType := a.Type
		strip, err := shouldStripExifContent(bytes.NewReader(data), mimeType)
		if err != nil {
			return nil, err
		}
		if strip {
			data, err = stripImageExif(bytes.NewReader(data), mimeType)
			if err != nil {
				return nil, err
			}
			mimeType = http.DetectContentType(data)
		}
		// HTML/SVG documents may execute content at the receiver. The first
		// contract permits photo/audio/video only, with no external URL fetch.
		if (!strings.HasPrefix(mimeType, "image/") && !strings.HasPrefix(mimeType, "audio/") && !strings.HasPrefix(mimeType, "video/")) || mimeType == "image/svg+xml" {
			return nil, errors.New("this attachment type cannot be sent through the media contract")
		}
		result.Media = append(result.Media, partition.Media{Name: "attachment-" + strconv.Itoa(len(result.Media)+1), Type: mimeType, Content: data})
	}
	return result, nil
}

func (s *APIV1Service) cancelPartitionMemoDeliveries(ctx context.Context, owner int32, uid string) error {
	if s.JournalPartitions == nil {
		return nil
	}
	return s.JournalPartitions.CancelMemo(ctx, owner, uid)
}

func (s *APIV1Service) suspendPartitionMemoDeliveries(ctx context.Context, owner int32, uid string) error {
	service := s.JournalPartitions
	if service == nil {
		service = partition.New(s.Store, s.readPartitionMemo)
	}
	return service.SuspendMemo(ctx, owner, uid)
}
func (s *APIV1Service) enqueuePartitionMemo(ctx context.Context, owner int32, uid string, manual bool) error {
	if s.JournalPartitions == nil {
		return nil
	}
	_, err := s.JournalPartitions.Commit(ctx, owner, uid, manual)
	return err
}

// RunPartitionDeliveryWorker resumes due persisted events until shutdown.
func (s *APIV1Service) RunPartitionDeliveryWorker(ctx context.Context) {
	if s.JournalPartitions == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.JournalPartitions.ProcessDue(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("Partition delivery processing deferred; persistent queue retained")
			}
		}
	}
}

func (s *APIV1Service) journalPartitionMemos(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	ctx := c.Request().Context()
	if _, err := s.JournalPartitions.GetPartition(ctx, u.ID, c.Param("id")); err != nil {
		return partitionError(c, err)
	}
	docs, err := s.Store.ListJournalDocuments(ctx, &store.FindJournalDocument{OwnerID: &u.ID, Kind: partition.MappingKind})
	if err != nil {
		return journalError(c, err)
	}
	type item struct {
		UID        string `json:"uid"`
		Content    string `json:"content"`
		RecordTime int64  `json:"recordTime"`
	}
	items := []item{}
	for _, doc := range docs {
		var mapping partition.Mapping
		if err := json.Unmarshal(doc.Payload, &mapping); err != nil {
			return journalError(c, err)
		}
		if mapping.Suspended || mapping.PartitionID != c.Param("id") {
			continue
		}
		memo, err := s.readPartitionMemo(ctx, u.ID, mapping.MemoUID, false)
		if errors.Is(err, partition.ErrNotFound) {
			continue
		}
		if err != nil {
			return journalError(c, err)
		}
		if !memo.Active {
			continue
		}
		content := []rune(memo.Content)
		if len(content) > 180 {
			content = content[:180]
		}
		items = append(items, item{UID: memo.UID, Content: string(content), RecordTime: memo.RecordTime})
	}
	slices.SortFunc(items, func(a, b item) int { return cmp.Compare(b.RecordTime, a.RecordTime) })
	return c.JSON(http.StatusOK, map[string]any{"memos": items})
}
func (s *APIV1Service) journalPartitionBackfill(c *echo.Context) error {
	u, err := s.journalOwner(c)
	if err != nil {
		return journalError(c, err)
	}
	var request struct {
		MemoUIDs []string `json:"memoUids"`
	}
	if err := journalBind(c, &request); err != nil {
		return journalError(c, err)
	}
	if len(request.MemoUIDs) == 0 || len(request.MemoUIDs) > 100 {
		return journalError(c, errors.New("choose between 1 and 100 saved records"))
	}
	ctx := c.Request().Context()
	for _, uid := range request.MemoUIDs {
		m, err := s.JournalPartitions.GetMapping(ctx, u.ID, uid)
		if err != nil {
			return partitionError(c, err)
		}
		if m.PartitionID != c.Param("id") {
			return partitionError(c, partition.ErrNotFound)
		}
	}
	values := []*partition.Delivery{}
	for _, uid := range request.MemoUIDs {
		created, err := s.JournalPartitions.Commit(ctx, u.ID, uid, true)
		if err != nil {
			return partitionError(c, err)
		}
		values = append(values, created...)
	}
	return c.JSON(http.StatusOK, map[string]any{"deliveries": values})
}
