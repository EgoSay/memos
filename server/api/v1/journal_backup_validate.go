package v1

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/pkg/errors"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/usememos/memos/core/journal"
	"github.com/usememos/memos/core/journalbackup"
	"github.com/usememos/memos/core/journalrecord"
	"github.com/usememos/memos/core/partition"
	v1pb "github.com/usememos/memos/proto/gen/api/v1"
)

// Validate all restorable documents before any existing grant is paused or any
// record is written. References to absent historic destinations are allowed:
// those documents describe terminal delivery history and cannot be replayed.
func validateBackupDocuments(documents []journalbackup.Document, memos, attachments map[string]bool) error {
	validID := func(id string) bool {
		return id != "" && len(id) <= 128 && strings.IndexFunc(id, unicode.IsControl) < 0
	}
	containsMemo := func(name string) bool {
		return strings.HasPrefix(name, "memos/") && memos[strings.TrimPrefix(name, "memos/")]
	}
	seen := map[string]bool{}
	for _, d := range documents {
		if !validID(d.Key) || seen[d.Kind+"\x00"+d.Key] || len(d.Payload) == 0 || d.Payload[0] != '{' {
			return errors.New("invalid or duplicate backup document")
		}
		seen[d.Kind+"\x00"+d.Key] = true
		var dest any
		switch d.Kind {
		case "preferences":
			dest = &journal.Preferences{}
		case partition.PartitionKind:
			dest = &partition.Partition{}
		case partition.MappingKind:
			dest = &partition.Mapping{}
		case "revision":
			dest = &journalrecord.Revision{}
		case "insight":
			dest = &journalInsightResult{}
		case "share":
			dest = &journal.Share{}
		case partition.TargetKind:
			dest = &partition.Target{}
		case partition.ExternalKind:
			dest = &partition.External{}
		case partition.DeliveryKind:
			dest = &partition.Delivery{}
		case partition.SequenceKind:
			dest = &backupSequence{}
		default:
			return errors.New("备份包含不允许恢复的授权或配置类型")
		}
		if err := json.Unmarshal(d.Payload, dest); err != nil {
			return err
		}
		switch value := dest.(type) {
		case *journal.Preferences:
			if _, err := time.LoadLocation(value.Timezone); err != nil {
				return err
			}
		case *partition.Partition:
			if !validID(value.ID) || value.ID != d.Key || strings.TrimSpace(value.Name) == "" {
				return errors.New("invalid backup partition")
			}
		case *partition.Mapping:
			if !memos[value.MemoUID] || value.MemoUID != d.Key || !validID(value.PartitionID) {
				return errors.New("invalid backup partition mapping")
			}
		case *partition.Target:
			if !validID(value.ID) || value.ID != d.Key || !validID(value.PartitionID) {
				return errors.New("invalid backup target")
			}
		case *partition.External:
			if !memos[value.MemoUID] || !validID(value.TargetID) {
				return errors.New("invalid external mapping")
			}
		case *partition.Delivery:
			if !validID(value.ID) || !memos[value.MemoUID] || !validID(value.TargetID) || !validID(value.PartitionID) {
				return errors.New("invalid delivery history")
			}
		case *backupSequence:
			if !memos[value.MemoUID] || !validID(value.TargetID) || value.Sequence < 0 {
				return errors.New("invalid delivery sequence")
			}
		case *journal.Share:
			if value.Mode != "fixed" && value.Mode != "dynamic" {
				return errors.New("invalid share mode")
			}
			if !validID(value.ID) {
				return errors.New("invalid backup share")
			}
			if err := value.Filter.Validate(); err != nil {
				return err
			}
		case *journalInsightResult:
			if !validID(value.ID) {
				return errors.New("invalid saved insight")
			}
			for _, source := range value.Sources {
				if !containsMemo(source.Name) {
					return errors.New("insight references missing original")
				}
			}
			for _, name := range value.Citations {
				if !containsMemo(name) {
					return errors.New("insight cites missing original")
				}
			}
		case *journalrecord.Revision:
			if !validID(value.ID) {
				return errors.New("invalid backup revision")
			}
			var memo v1pb.Memo
			if err := protojson.Unmarshal(value.Memo, &memo); err != nil {
				return err
			}
			if !containsMemo(memo.Name) {
				return errors.New("revision references missing original")
			}
			// Old detached history may reference media already purged. Restore drops
			// those references while preserving the original text and date.
			_ = attachments
		default:
		}
	}
	return nil
}

func validateBackupExternalURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" {
		return errors.New("invalid external media URL")
	}
	return nil
}
