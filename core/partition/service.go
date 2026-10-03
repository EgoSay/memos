package partition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"uuid"

	"github.com/pkg/errors"

	"github.com/usememos/memos/internal/webhook"
	"github.com/usememos/memos/store"
)

func (s *Service) ListPartitions(ctx context.Context, owner int32) ([]*Partition, error) {
	docs, err := s.documents(ctx, owner, PartitionKind)
	if err != nil {
		return nil, err
	}
	result := []*Partition{}
	for _, doc := range docs {
		value, err := decode[Partition](doc)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *Service) GetPartition(ctx context.Context, owner int32, id string) (*Partition, error) {
	doc, err := s.Repo.GetJournalDocument(ctx, owner, PartitionKind, id)
	if err != nil {
		return nil, err
	}
	return decode[Partition](doc)
}

func (s *Service) SavePartition(ctx context.Context, owner int32, p *Partition) (*Partition, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 120 {
		return nil, errors.Wrap(ErrInvalid, "name must contain 1–120 bytes")
	}
	if p.ID == "" {
		p.ID = uuid.NewV4().String()
		p.Version = 0
	} else if _, err := s.GetPartition(ctx, owner, p.ID); err != nil {
		return nil, err
	}
	doc, err := s.put(ctx, owner, PartitionKind, p.ID, p, p.Version)
	if err != nil {
		return nil, err
	}
	return decode[Partition](doc)
}

// DeletePartition invalidates the container first. Memo bodies and attachments
// are never touched; residual mappings fail authorization until cleaned up.
func (s *Service) DeletePartition(ctx context.Context, owner int32, id string, version int64) error {
	if _, err := s.GetPartition(ctx, owner, id); err != nil {
		return err
	}
	if err := s.Repo.DeleteJournalDocument(ctx, owner, PartitionKind, id, version); err != nil {
		return err
	}
	targets, err := s.ListTargets(ctx, owner, id)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if err := s.DeleteTarget(ctx, owner, target.ID, target.Version); err != nil {
			return err
		}
	}
	docs, err := s.documents(ctx, owner, MappingKind)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		m, err := decode[Mapping](doc)
		if err != nil {
			return err
		}
		if m.PartitionID == id {
			if err := s.Repo.DeleteJournalDocument(ctx, owner, MappingKind, doc.Key, doc.Version); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) ListTargets(ctx context.Context, owner int32, partitionID string) ([]*Target, error) {
	docs, err := s.documents(ctx, owner, TargetKind)
	if err != nil {
		return nil, err
	}
	result := []*Target{}
	for _, doc := range docs {
		t, err := decode[Target](doc)
		if err != nil {
			return nil, err
		}
		if partitionID == "" || t.PartitionID == partitionID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (s *Service) GetTarget(ctx context.Context, owner int32, id string) (*Target, error) {
	doc, err := s.Repo.GetJournalDocument(ctx, owner, TargetKind, id)
	if err != nil {
		return nil, err
	}
	return decode[Target](doc)
}

// SaveTarget never queues historical memos. Every edit invalidates old events.
func (s *Service) SaveTarget(ctx context.Context, owner int32, t *Target) (*Target, error) {
	if _, err := s.GetPartition(ctx, owner, t.PartitionID); err != nil {
		return nil, err
	}
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || len(t.Name) > 120 {
		return nil, errors.Wrap(ErrInvalid, "target name is required")
	}
	if err := webhook.ValidateURL(t.URL); err != nil {
		return nil, errors.Wrap(ErrInvalid, "webhook address is invalid or disallowed")
	}
	if !t.Fields.Content && !t.Fields.Date && !t.Fields.Media {
		return nil, errors.Wrap(ErrInvalid, "select at least one permitted field")
	}
	if t.ID == "" {
		t.ID = uuid.NewV4().String()
		t.Version = 0
		t.Epoch = 1
	} else {
		old, err := s.GetTarget(ctx, owner, t.ID)
		if err != nil {
			return nil, err
		}
		if old.PartitionID != t.PartitionID {
			return nil, errors.Wrap(ErrInvalid, "target partition is immutable")
		}
		if t.SigningSecret == "" {
			t.SigningSecret = old.SigningSecret
		}
		t.Epoch = old.Epoch + 1
	}
	if t.SigningSecret == "" {
		secret, err := webhook.GenerateSigningSecret()
		if err != nil {
			return nil, err
		}
		t.SigningSecret = secret
	}
	if err := webhook.ValidateSigningSecret(t.SigningSecret); err != nil {
		return nil, errors.Wrap(ErrInvalid, "invalid signing secret")
	}
	t.HasSigningSecret = true
	doc, err := s.put(ctx, owner, TargetKind, t.ID, t, t.Version)
	if err != nil {
		return nil, err
	}
	s.cancelInflight(owner, t.ID)
	if err := s.cancelDeliveries(ctx, owner, "", t.ID); err != nil {
		return nil, err
	}
	return decode[Target](doc)
}

func (s *Service) DeleteTarget(ctx context.Context, owner int32, id string, version int64) error {
	if err := s.Repo.DeleteJournalDocument(ctx, owner, TargetKind, id, version); err != nil {
		return err
	}
	s.cancelInflight(owner, id)
	return s.cancelDeliveries(ctx, owner, "", id)
}

func (s *Service) GetMapping(ctx context.Context, owner int32, memoUID string) (*Mapping, error) {
	doc, err := s.Repo.GetJournalDocument(ctx, owner, MappingKind, memoUID)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return &Mapping{MemoUID: memoUID}, nil
	}
	m, err := decode[Mapping](doc)
	if err != nil {
		return nil, err
	}
	if _, err := s.GetPartition(ctx, owner, m.PartitionID); errors.Is(err, ErrNotFound) {
		m.PartitionID = ""
	} else if err != nil {
		return nil, err
	}
	return m, nil
}

// Assign only changes private organization. Commit is a separate operation
// called after the saved body and every attachment are confirmed complete.
func (s *Service) Assign(ctx context.Context, owner int32, memoUID, partitionID string, version int64) (*Mapping, error) {
	memo, err := s.ReadMemo(ctx, owner, memoUID, false)
	if err != nil {
		return nil, err
	}
	if memo == nil || !memo.Active {
		return nil, ErrNotFound
	}
	if partitionID != "" {
		if _, err := s.GetPartition(ctx, owner, partitionID); err != nil {
			return nil, err
		}
	}
	old, err := s.GetMapping(ctx, owner, memoUID)
	if err != nil {
		return nil, err
	}
	m := &Mapping{MemoUID: memoUID, PartitionID: partitionID}
	doc, err := s.put(ctx, owner, MappingKind, memoUID, m, version)
	if err != nil {
		return nil, err
	}
	if err := s.cancelDeliveries(ctx, owner, memoUID, ""); err != nil {
		return nil, err
	}
	if old.PartitionID != "" && old.PartitionID != partitionID {
		if err := s.queueRetractions(ctx, owner, memoUID, old.PartitionID); err != nil {
			return nil, err
		}
	}
	return decode[Mapping](doc)
}

func stableID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (s *Service) ListDeliveries(ctx context.Context, owner int32) ([]*Delivery, error) {
	docs, err := s.documents(ctx, owner, DeliveryKind)
	if err != nil {
		return nil, err
	}
	result := []*Delivery{}
	for _, doc := range docs {
		value, err := decode[Delivery](doc)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func cancelable(status string) bool {
	return status == "pending" || status == "delivering" || status == "failed" || status == "local_changed"
}
func (s *Service) cancelDeliveries(ctx context.Context, owner int32, memoUID, targetID string) error {
	deliveries, err := s.ListDeliveries(ctx, owner)
	if err != nil {
		return err
	}
	for _, d := range deliveries {
		if memoUID != "" && d.MemoUID != memoUID || targetID != "" && d.TargetID != targetID || !cancelable(d.Status) {
			continue
		}
		s.cancelInflight(owner, d.TargetID)
		for attempt := 0; attempt < 5; attempt++ {
			d.Status = "cancelled"
			d.LastError = ""
			d.UpdatedTs = s.Now().Unix()
			_, err := s.put(ctx, owner, DeliveryKind, d.ID, d, d.Version)
			if err == nil {
				break
			}
			if !errors.Is(err, store.ErrJournalConflict) {
				return err
			}
			doc, err := s.Repo.GetJournalDocument(ctx, owner, DeliveryKind, d.ID)
			if err != nil {
				return err
			}
			if doc == nil {
				break
			}
			d, err = decode[Delivery](doc)
			if err != nil {
				return err
			}
			if !cancelable(d.Status) {
				break
			}
			if attempt == 4 {
				return store.ErrJournalConflict
			}
		}
	}
	return nil
}
