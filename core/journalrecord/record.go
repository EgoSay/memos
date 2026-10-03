// Package journalrecord defines revision and trash retention without transport details.
package journalrecord

import (
	"encoding/json"
	"time"
)

// Retention is the reversible window for edits and deletions.
const Retention = 30 * 24 * time.Hour

// Revision captures a readable memo before a successful edit attempt.
type Revision struct {
	ID        string          `json:"id"`
	CreatedTs int64           `json:"createdTs"`
	Memo      json.RawMessage `json:"memo"`
}

// Trash records deletion independently from ordinary archives.
type Trash struct {
	DeletedTs     int64  `json:"deletedTs"`
	PreviousState string `json:"previousState"`
}

// Expired reports when the documented recovery window has ended.
func Expired(timestamp int64, now time.Time) bool { return timestamp <= now.Add(-Retention).Unix() }
