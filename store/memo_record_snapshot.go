package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	storepb "github.com/EgoSay/kairos/proto/gen/store"
)

// MemoRecordSnapshot describes all editable record data. Pinned status and
// reference relations are independent controls and are not part of this token.
// Its v1 canonical tuple is UTF-8 byte-length-prefixed strings in this order:
// version, content, created seconds, updated seconds, visibility, space UID,
// row state, location presence, placeholder, latitude IEEE754 big-endian hex,
// longitude hex, attachment count, then lexicographically sorted attachment UIDs.
// Float bits avoid language-specific decimal serialization differences.
// Keep web/src/lib/journal-record-version.ts and the shared vector in sync.
type MemoRecordSnapshot struct {
	Content              string
	CreatedTs, UpdatedTs int64
	Visibility           Visibility
	SpaceUID             string
	RowStatus            RowStatus
	Location             *storepb.MemoPayload_Location
	AttachmentUIDs       []string
}

// Hash returns the version token for the canonical record snapshot.
func (s MemoRecordSnapshot) Hash() string {
	fields := []string{"journal-record-v1", s.Content, strconv.FormatInt(s.CreatedTs, 10), strconv.FormatInt(s.UpdatedTs, 10), string(s.Visibility), s.SpaceUID, string(s.RowStatus), "0", "", "", ""}
	if s.Location != nil {
		fields[7], fields[8] = "1", s.Location.Placeholder
		fields[9] = fmt.Sprintf("%016x", math.Float64bits(s.Location.Latitude))
		fields[10] = fmt.Sprintf("%016x", math.Float64bits(s.Location.Longitude))
	}
	attachments := slices.Clone(s.AttachmentUIDs)
	slices.Sort(attachments)
	fields = append(fields, strconv.Itoa(len(attachments)))
	fields = append(fields, attachments...)
	var canonical strings.Builder
	for _, field := range fields {
		canonical.WriteString(strconv.Itoa(len([]byte(field))))
		canonical.WriteByte(':')
		canonical.WriteString(field)
	}
	digest := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(digest[:])
}
