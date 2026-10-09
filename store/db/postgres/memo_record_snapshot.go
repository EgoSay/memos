package postgres

import (
	"context"
	"database/sql"

	"github.com/pkg/errors"
	"google.golang.org/protobuf/encoding/protojson"

	storepb "github.com/usememos/memos/proto/gen/store"
	"github.com/usememos/memos/store"
)

func readPostgresMemoRecordSnapshot(ctx context.Context, tx *sql.Tx, memoID int32) (*store.MemoRecordSnapshot, error) {
	snapshot := &store.MemoRecordSnapshot{}
	var payload string
	var spaceID sql.NullInt32
	if err := tx.QueryRowContext(ctx, `SELECT content, created_ts, updated_ts, visibility, row_status, space_id, payload FROM memo WHERE id = $1`, memoID).Scan(&snapshot.Content, &snapshot.CreatedTs, &snapshot.UpdatedTs, &snapshot.Visibility, &snapshot.RowStatus, &spaceID, &payload); err != nil {
		return nil, errors.Wrap(err, "failed to read record snapshot")
	}
	parsed := &storepb.MemoPayload{}
	if err := protojson.Unmarshal([]byte(payload), parsed); payload != "" && err != nil {
		return nil, errors.Wrap(err, "failed to read record location")
	}
	snapshot.Location = parsed.Location
	if spaceID.Valid {
		if err := tx.QueryRowContext(ctx, `SELECT uid FROM space WHERE id = $1`, spaceID.Int32).Scan(&snapshot.SpaceUID); err != nil {
			return nil, errors.Wrap(err, "failed to read record space")
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT uid FROM attachment WHERE memo_id = $1 FOR UPDATE`, memoID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read record attachments")
	}
	defer rows.Close()
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		snapshot.AttachmentUIDs = append(snapshot.AttachmentUIDs, uid)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return snapshot, nil
}
