package partition

import (
	"cmp"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/pkg/errors"

	"github.com/usememos/memos/internal/webhook"
	"github.com/usememos/memos/store"
)

// Commit records authorization to send the current complete version. No import,
// restore, configuration update or page view calls this method implicitly.
func (s *Service) Commit(ctx context.Context, owner int32, memoUID string, manual bool) ([]*Delivery, error) {
	m, err := s.GetMapping(ctx, owner, memoUID)
	if err != nil {
		return nil, err
	}
	if m.PartitionID == "" || m.Suspended {
		return []*Delivery{}, nil
	}
	memo, err := s.ReadMemo(ctx, owner, memoUID, false)
	if err != nil {
		return nil, err
	}
	if memo == nil || !memo.Active {
		return nil, ErrNotFound
	}
	targets, err := s.ListTargets(ctx, owner, m.PartitionID)
	if err != nil {
		return nil, err
	}
	existing, err := s.ListDeliveries(ctx, owner)
	if err != nil {
		return nil, err
	}
	created := []*Delivery{}
	for _, t := range targets {
		if !t.Enabled {
			continue
		}
		id := stableID(t.ID, memoUID, memo.Revision, strconv.FormatInt(t.Epoch, 10), strconv.FormatInt(m.Version, 10), "upsert")
		already := false
		hasPrior := false
		for _, previous := range existing {
			if previous.MemoUID != memoUID || previous.TargetID != t.ID || previous.Event != "upsert" {
				continue
			}
			hasPrior = true
			if previous.ID == id {
				if manual && previous.Status == "local_changed" {
					previous.Status = "pending"
					previous.NextAttemptTs = s.Now().Unix()
					doc, err := s.put(ctx, owner, DeliveryKind, id, previous, previous.Version)
					if err != nil {
						return nil, err
					}
					updated, err := decode[Delivery](doc)
					if err != nil {
						return nil, err
					}
					created = append(created, updated)
				}
				already = true
			}
		}
		if already {
			continue
		}
		status := "pending"
		if hasPrior && !manual && !t.AutoUpdate {
			status = "local_changed"
		}
		sequence, err := s.nextSequence(ctx, owner, t.ID, memoUID)
		if err != nil {
			return nil, err
		}
		d := &Delivery{MappingVersion: m.Version, Sequence: sequence, ID: id, MemoUID: memoUID, PartitionID: m.PartitionID, TargetID: t.ID, TargetName: t.Name, Revision: memo.Revision, Epoch: t.Epoch, Event: "upsert", Status: status, CreatedTs: s.Now().Unix(), UpdatedTs: s.Now().Unix(), NextAttemptTs: s.Now().Unix()}
		doc, err := s.put(ctx, owner, DeliveryKind, id, d, 0)
		if errors.Is(err, store.ErrJournalConflict) {
			continue
		}
		if err != nil {
			return nil, err
		}
		value, err := decode[Delivery](doc)
		if err != nil {
			return nil, err
		}
		created = append(created, value)
	}
	return created, nil
}

// CancelMemo removes pending bodies. Optional retraction contains identifiers
// only and requires an existing receiver mapping, never a deleted original.
func (s *Service) CancelMemo(ctx context.Context, owner int32, memoUID string) error {
	if err := s.SuspendMemo(ctx, owner, memoUID); err != nil {
		return err
	}
	return s.queueRetractions(ctx, owner, memoUID, "")
}

// SuspendMemo makes imports and restoration inert: stop pending work and require
// a new explicit assignment, without generating even a retraction event.
func (s *Service) SuspendMemo(ctx context.Context, owner int32, memoUID string) error {
	doc, err := s.Repo.GetJournalDocument(ctx, owner, MappingKind, memoUID)
	if err != nil {
		return err
	}
	if doc != nil {
		m, err := decode[Mapping](doc)
		if err != nil {
			return err
		}
		m.Suspended = true
		if _, err := s.put(ctx, owner, MappingKind, memoUID, m, doc.Version); err != nil {
			return err
		}
	}
	return s.cancelDeliveries(ctx, owner, memoUID, "")
}

func (s *Service) queueRetractions(ctx context.Context, owner int32, memoUID, partitionID string) error {
	docs, err := s.documents(ctx, owner, ExternalKind)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		external, err := decode[External](doc)
		if err != nil {
			return err
		}
		if external.MemoUID != memoUID {
			continue
		}
		t, err := s.GetTarget(ctx, owner, external.TargetID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if partitionID != "" && t.PartitionID != partitionID {
			continue
		}
		s.cancelInflight(owner, t.ID)
		if !t.Enabled || !t.Retract || external.ExternalID == "" {
			continue
		}
		id := stableID(t.ID, memoUID, external.ExternalID, "retract", strconv.FormatInt(t.Epoch, 10))
		sequence, err := s.nextSequence(ctx, owner, t.ID, memoUID)
		if err != nil {
			return err
		}
		d := &Delivery{Sequence: sequence, ID: id, MemoUID: memoUID, PartitionID: t.PartitionID, TargetID: t.ID, TargetName: t.Name, Epoch: t.Epoch, Event: "retract", ExternalID: external.ExternalID, Status: "pending", CreatedTs: s.Now().Unix(), UpdatedTs: s.Now().Unix(), NextAttemptTs: s.Now().Unix()}
		if _, err := s.put(ctx, owner, DeliveryKind, id, d, 0); err != nil && !errors.Is(err, store.ErrJournalConflict) {
			return err
		}
	}
	return nil
}

func (s *Service) Retry(ctx context.Context, owner int32, id string) (*Delivery, error) {
	doc, err := s.Repo.GetJournalDocument(ctx, owner, DeliveryKind, id)
	if err != nil {
		return nil, err
	}
	d, err := decode[Delivery](doc)
	if err != nil {
		return nil, err
	}
	if d.Status != "failed" && d.Status != "local_changed" {
		return nil, errors.Wrap(ErrInvalid, "only failed or locally changed events may be sent")
	}
	if _, _, err := s.authorize(ctx, owner, d, false); err != nil {
		return nil, err
	}
	d.Status = "pending"
	d.Attempts = 0
	d.NextAttemptTs = s.Now().Unix()
	d.LastError = ""
	doc, err = s.put(ctx, owner, DeliveryKind, id, d, d.Version)
	if err != nil {
		return nil, err
	}
	return decode[Delivery](doc)
}

func (*Service) inflightKey(owner int32, id string) string {
	return strconv.FormatInt(int64(owner), 10) + ":" + id
}
func (s *Service) cancelInflight(owner int32, targetID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel := s.inflight[s.inflightKey(owner, targetID)]; cancel != nil {
		cancel()
	}
}

func (s *Service) authorize(ctx context.Context, owner int32, d *Delivery, media bool) (*Target, *Memo, error) {
	t, err := s.GetTarget(ctx, owner, d.TargetID)
	if err != nil {
		return nil, nil, err
	}
	if !t.Enabled || t.Epoch != d.Epoch || t.PartitionID != d.PartitionID {
		return nil, nil, ErrNotFound
	}
	if _, err := s.GetPartition(ctx, owner, d.PartitionID); err != nil {
		return nil, nil, err
	}
	if d.Event == "retract" {
		if !t.Retract || d.ExternalID == "" {
			return nil, nil, ErrNotFound
		}
		return t, nil, nil
	}
	m, err := s.GetMapping(ctx, owner, d.MemoUID)
	if err != nil {
		return nil, nil, err
	}
	if m.Suspended || m.PartitionID != d.PartitionID || m.Version != d.MappingVersion {
		return nil, nil, ErrNotFound
	}
	memo, err := s.ReadMemo(ctx, owner, d.MemoUID, media && t.Fields.Media)
	if err != nil {
		return nil, nil, err
	}
	if memo == nil || !memo.Active || memo.Revision != d.Revision {
		return nil, nil, ErrNotFound
	}
	return t, memo, nil
}

// ProcessDue claims persisted work using a lease. A crash retries with the same
// event ID; receivers must deduplicate before creating an external post.
func (s *Service) ProcessDue(ctx context.Context) error {
	docs, err := s.Repo.ListJournalDocuments(ctx, &store.FindJournalDocument{Kind: DeliveryKind})
	if err != nil {
		return err
	}
	slices.SortFunc(docs, func(a, b *store.JournalDocument) int { return cmp.Compare(a.UpdatedTs, b.UpdatedTs) })
	for _, doc := range docs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		d, err := decode[Delivery](doc)
		if err != nil {
			return err
		}
		if d.Status == "delivering" && d.LeaseUntilTs > s.Now().Unix() {
			continue
		}
		if d.Status != "pending" && d.Status != "delivering" {
			continue
		}
		if d.NextAttemptTs > s.Now().Unix() {
			continue
		}
		d.Status = "delivering"
		d.LeaseUntilTs = s.Now().Add(2 * time.Minute).Unix()
		d.Attempts++
		claimed, err := s.put(ctx, doc.OwnerID, DeliveryKind, d.ID, d, d.Version)
		if errors.Is(err, store.ErrJournalConflict) {
			continue
		}
		if err != nil {
			return err
		}
		d.Version = claimed.Version
		if err := s.deliver(ctx, doc.OwnerID, d); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) deliver(parent context.Context, owner int32, d *Delivery) error {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	key := s.inflightKey(owner, d.TargetID)
	s.mu.Lock()
	s.inflight[key] = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.inflight, key); s.mu.Unlock() }()
	t, memo, err := s.authorize(ctx, owner, d, true)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			d.Status = "cancelled"
			d.LastError = ""
		} else {
			s.fail(d, "无法读取完整记录，请稍后重试")
		}
		return s.saveDelivery(ctx, owner, d)
	}
	// A pause can race with media preparation; verify the persisted delivery
	// and target again immediately before starting the network request.
	current, err := s.Repo.GetJournalDocument(ctx, owner, DeliveryKind, d.ID)
	if err != nil {
		return err
	}
	if current == nil || current.Version != d.Version {
		return nil
	}
	if _, _, err := s.authorize(ctx, owner, d, false); err != nil {
		d.Status = "cancelled"
		return s.saveDelivery(ctx, owner, d)
	}
	payload := Payload{TargetID: t.ID, Sequence: d.Sequence, EventID: d.ID, Event: d.Event, MemoUID: d.MemoUID, ExternalID: d.ExternalID}
	if memo != nil {
		payload.Revision = d.Revision
		if t.Fields.Content {
			payload.Content = &memo.Content
		}
		if t.Fields.Date {
			payload.RecordTime = &memo.RecordTime
		}
		if t.Fields.Media {
			payload.Media = memo.Media
		}
		mapping, err := s.Repo.GetJournalDocument(ctx, owner, ExternalKind, stableID(t.ID, d.MemoUID))
		if err != nil {
			return err
		}
		if mapping != nil {
			external, err := decode[External](mapping)
			if err != nil {
				return err
			}
			payload.ExternalID = external.ExternalID
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(body) > 64<<20 {
		d.Status = "failed"
		d.LastError = "内容与附件超过 64 MiB，请减少本次发送的媒体"
		return s.saveDelivery(ctx, owner, d)
	}
	receipt, err := s.Send(ctx, &webhook.Request{URL: t.URL, SigningSecret: t.SigningSecret, MessageID: d.ID, Label: d.Event, Payload: json.RawMessage(body)})
	if err != nil {
		s.fail(d, "投递失败或超时，可重试；对方可能已收到，请确保接收端支持事件去重")
	} else {
		d.Status = "delivered"
		d.LastError = ""
		d.PublishedURL = ""
		var result Receipt
		if receipt != nil && json.Unmarshal(receipt.Body, &result) == nil && result.EventID == d.ID && result.ExternalID != "" {
			d.ExternalID = result.ExternalID
			parsed, parseErr := url.Parse(result.PublishedURL)
			if result.Status == "published" && parseErr == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil {
				d.Status = "published"
				d.PublishedURL = result.PublishedURL
			}
			ext := &External{MemoUID: d.MemoUID, TargetID: t.ID, ExternalID: result.ExternalID, PublishedURL: d.PublishedURL}
			mapKey := stableID(t.ID, d.MemoUID)
			old, err := s.Repo.GetJournalDocument(ctx, owner, ExternalKind, mapKey)
			if err != nil {
				return err
			}
			version := int64(0)
			if old != nil {
				version = old.Version
			}
			if _, err := s.put(ctx, owner, ExternalKind, mapKey, ext, version); err != nil && !errors.Is(err, store.ErrJournalConflict) {
				return err
			}
		}
	}
	// A canceled request still needs durable state. A concurrent explicit pause
	// wins via CAS and its canceled row must never be overwritten.
	return s.saveDelivery(context.WithoutCancel(parent), owner, d)
}

func (s *Service) fail(d *Delivery, message string) {
	d.Status = "pending"
	d.LastError = message
	if d.Attempts >= 5 {
		d.Status = "failed"
	}
	d.NextAttemptTs = s.Now().Add(time.Duration(1<<min(d.Attempts, 5)) * 30 * time.Second).Unix()
}
func (s *Service) saveDelivery(ctx context.Context, owner int32, d *Delivery) error {
	d.UpdatedTs = s.Now().Unix()
	d.LeaseUntilTs = 0
	_, err := s.put(ctx, owner, DeliveryKind, d.ID, d, d.Version)
	if errors.Is(err, store.ErrJournalConflict) {
		return nil
	}
	return err
}

// PauseAll is used after backup restoration. It disables every destination
// before any background worker is allowed to resume.
func (s *Service) PauseAll(ctx context.Context) error {
	docs, err := s.Repo.ListJournalDocuments(ctx, &store.FindJournalDocument{Kind: TargetKind})
	if err != nil {
		return err
	}
	for _, doc := range docs {
		t, err := decode[Target](doc)
		if err != nil {
			return err
		}
		t.Enabled = false
		t.Epoch++
		if _, err := s.put(ctx, doc.OwnerID, TargetKind, doc.Key, t, doc.Version); err != nil {
			return err
		}
		s.cancelInflight(doc.OwnerID, t.ID)
		if err := s.cancelDeliveries(ctx, doc.OwnerID, "", t.ID); err != nil {
			return err
		}
	}
	return nil
}

// nextSequence supplies a durable monotonic revision for the receiver. A
// receiver must reject lower sequences to prevent delayed deliveries from
// overwriting newer content after an HTTP timeout.
func (s *Service) nextSequence(ctx context.Context, owner int32, targetID, memoUID string) (int64, error) {
	key := stableID(targetID, memoUID)
	for range 5 {
		doc, err := s.Repo.GetJournalDocument(ctx, owner, SequenceKind, key)
		if err != nil {
			return 0, err
		}
		version := int64(0)
		if doc != nil {
			version = doc.Version
		}
		sequence := version
		if doc != nil {
			var saved struct {
				Sequence int64 `json:"sequence"`
			}
			if err := json.Unmarshal(doc.Payload, &saved); err != nil {
				return 0, err
			}
			sequence = max(sequence, saved.Sequence)
		}
		_, err = s.put(ctx, owner, SequenceKind, key, map[string]any{"sequence": sequence + 1, "targetId": targetID, "memoUid": memoUID}, version)
		if errors.Is(err, store.ErrJournalConflict) {
			continue
		}
		if err != nil {
			return 0, err
		}
		return sequence + 1, nil
	}
	return 0, store.ErrJournalConflict
}

// PauseOwner disables only the restoring account's targets without resolving
// destination DNS or changing any other account's ongoing work.
func (s *Service) PauseOwner(ctx context.Context, owner int32) error {
	docs, err := s.documents(ctx, owner, TargetKind)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		t, err := decode[Target](doc)
		if err != nil {
			return err
		}
		t.Enabled = false
		t.Epoch++
		if _, err := s.put(ctx, owner, TargetKind, doc.Key, t, doc.Version); err != nil {
			return err
		}
		s.cancelInflight(owner, t.ID)
		if err := s.cancelDeliveries(ctx, owner, "", t.ID); err != nil {
			return err
		}
	}
	return nil
}

// RecordTargetKey is the durable identity used for a receiver's record mapping.
func RecordTargetKey(targetID, memoUID string) string { return stableID(targetID, memoUID) }
