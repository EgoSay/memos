package v1

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/EgoSay/kairos/core/journal"
	"github.com/EgoSay/kairos/core/journalbackup"
	"github.com/EgoSay/kairos/core/partition"
	"github.com/EgoSay/kairos/store"
)

type backupSequence struct {
	Sequence int64  `json:"sequence"`
	TargetID string `json:"targetId"`
	MemoUID  string `json:"memoUid"`
}

// backupPublicationDocument keeps organization and history without live grants.
// Destination URLs are credentials in many webhook products, so their complete
// value is deliberately removed alongside the explicit signing secret.
func backupPublicationDocument(doc *store.JournalDocument, memos map[string]bool) (*journalbackup.Document, bool, error) {
	var value any
	switch doc.Kind {
	case "share":
		var share journal.Share
		if err := json.Unmarshal(doc.Payload, &share); err != nil {
			return nil, true, err
		}
		share.Token = ""
		share.PasscodeHash = ""
		share.Paused = true
		share.Version = 0
		items := []journal.ShareItem{}
		for _, item := range share.Items {
			if memos[strings.TrimPrefix(item.MemoName, "memos/")] {
				item.Media = nil
				items = append(items, item)
			}
		}
		share.Items = items
		value = share
	case partition.TargetKind:
		var target partition.Target
		if err := json.Unmarshal(doc.Payload, &target); err != nil {
			return nil, true, err
		}
		target.URL = ""
		target.SigningSecret = ""
		target.HasSigningSecret = false
		target.Enabled = false
		value = target
	case partition.ExternalKind:
		var external partition.External
		if err := json.Unmarshal(doc.Payload, &external); err != nil {
			return nil, true, err
		}
		if !memos[external.MemoUID] {
			return nil, true, nil
		}
		value = external
	case partition.DeliveryKind:
		var delivery partition.Delivery
		if err := json.Unmarshal(doc.Payload, &delivery); err != nil {
			return nil, true, err
		}
		if !memos[delivery.MemoUID] {
			return nil, true, nil
		}
		if delivery.Status != "delivered" && delivery.Status != "published" {
			delivery.Status = "cancelled"
		}
		delivery.LastError = ""
		delivery.LeaseUntilTs = 0
		delivery.NextAttemptTs = 0
		value = delivery
	case partition.SequenceKind:
		var sequence backupSequence
		if err := json.Unmarshal(doc.Payload, &sequence); err != nil {
			return nil, true, err
		}
		if sequence.TargetID == "" || !memos[sequence.MemoUID] {
			return nil, true, nil
		}
		value = sequence
	default:
		return nil, false, nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, true, err
	}
	return &journalbackup.Document{Kind: doc.Kind, Key: doc.Key, Payload: payload}, true, nil
}

func (s *APIV1Service) restoreBackupPublication(ctx context.Context, owner int32, digest string, doc journalbackup.Document, memos map[string]string) (*journalbackup.Document, bool, error) {
	var key string
	var value any
	mapNames := func(names []string) []string {
		result := []string{}
		for _, name := range names {
			if uid := memos[strings.TrimPrefix(name, "memos/")]; uid != "" {
				result = append(result, "memos/"+uid)
			}
		}
		return result
	}
	mapTarget := func(id string) (string, error) { return s.backupUID(ctx, owner, digest, "target", id) }
	switch doc.Kind {
	case "share":
		var share journal.Share
		if err := json.Unmarshal(doc.Payload, &share); err != nil {
			return nil, true, err
		}
		if err := share.Filter.Validate(); err != nil {
			return nil, true, err
		}
		key = backupRestoredID(digest, doc.Kind, share.ID)
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, true, err
		}
		share.ID = key
		share.Token = hex.EncodeToString(secret)
		share.Paused = true
		share.PasscodeHash = ""
		share.Version = 0
		share.Filter.MemoNames = mapNames(share.Filter.MemoNames)
		share.ExcludedMemoNames = mapNames(share.ExcludedMemoNames)
		share.ApprovedImportedMemoNames = nil
		items := []journal.ShareItem{}
		for _, item := range share.Items {
			if uid := memos[strings.TrimPrefix(item.MemoName, "memos/")]; uid != "" {
				item.MemoName = "memos/" + uid
				item.Media = nil
				items = append(items, item)
			}
		}
		share.Items = items
		value = share
	case partition.TargetKind:
		var target partition.Target
		if err := json.Unmarshal(doc.Payload, &target); err != nil {
			return nil, true, err
		}
		id, err := mapTarget(target.ID)
		if err != nil {
			return nil, true, err
		}
		p, err := s.backupUID(ctx, owner, digest, "partition", target.PartitionID)
		if err != nil {
			return nil, true, err
		}
		target.ID = id
		target.PartitionID = p
		target.URL = ""
		target.SigningSecret = ""
		target.HasSigningSecret = false
		target.Enabled = false
		target.Epoch++
		target.Version = 0
		key = id
		value = target
	case partition.ExternalKind:
		var external partition.External
		if err := json.Unmarshal(doc.Payload, &external); err != nil {
			return nil, true, err
		}
		memo := memos[external.MemoUID]
		if memo == "" {
			return nil, true, nil
		}
		target, err := mapTarget(external.TargetID)
		if err != nil {
			return nil, true, err
		}
		external.MemoUID = memo
		external.TargetID = target
		key = partition.RecordTargetKey(target, memo)
		value = external
	case partition.DeliveryKind:
		var d partition.Delivery
		if err := json.Unmarshal(doc.Payload, &d); err != nil {
			return nil, true, err
		}
		memo := memos[d.MemoUID]
		if memo == "" {
			return nil, true, nil
		}
		target, err := mapTarget(d.TargetID)
		if err != nil {
			return nil, true, err
		}
		p, err := s.backupUID(ctx, owner, digest, "partition", d.PartitionID)
		if err != nil {
			return nil, true, err
		}
		d.MemoUID = memo
		d.TargetID = target
		d.PartitionID = p
		d.LeaseUntilTs = 0
		d.NextAttemptTs = 0
		d.LastError = ""
		d.Version = 0
		if d.Status != "published" && d.Status != "delivered" {
			d.Status = "cancelled"
		}
		key = backupRestoredID(digest, doc.Kind, d.ID)
		if d.SourceEventID == "" {
			d.SourceEventID = d.ID
		}
		d.ID = key
		value = d
	case partition.SequenceKind:
		var sequence backupSequence
		if err := json.Unmarshal(doc.Payload, &sequence); err != nil {
			return nil, true, err
		}
		memo := memos[sequence.MemoUID]
		if memo == "" {
			return nil, true, nil
		}
		target, err := mapTarget(sequence.TargetID)
		if err != nil {
			return nil, true, err
		}
		sequence.MemoUID = memo
		sequence.TargetID = target
		key = partition.RecordTargetKey(target, memo)
		value = sequence
	default:
		return nil, false, nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, true, err
	}
	return &journalbackup.Document{Kind: doc.Kind, Key: key, Payload: payload}, true, nil
}
