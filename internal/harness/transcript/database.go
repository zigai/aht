package transcript

import (
	"context"
	"encoding/json"
)

func TextRow(ctx context.Context, t *Decoder, row Row) error {
	if role, ok := RecognizedRole(row.Role); ok {
		t.Capture(ctx, role, row.Body.String, row.MessageID, 0, StoredTime(row.Timestamp))
	}
	return nil
}

func BlocksRow(ctx context.Context, t *Decoder, row Row) error {
	var blocks []Record
	if json.Unmarshal([]byte(row.Body.String), &blocks) != nil {
		return ErrInvalidRecord
	}
	role, ok := RecognizedRole(row.Role)
	if !ok {
		return nil
	}
	for _, part := range MessageBlockParts(blocks, role, true) {
		t.Capture(ctx, part.Role, part.Text, row.MessageID, 0, StoredTime(row.Timestamp))
	}
	return nil
}

func MessageRow(ctx context.Context, t *Decoder, row Row) error {
	r, err := RowRecord(row)
	if err != nil {
		return err
	}
	t.Message(ctx, r, row.MessageID, 0, StoredTime(row.Timestamp))
	return nil
}

func RowRecord(row Row) (Record, error) {
	var r Record
	if json.Unmarshal([]byte(row.Body.String), &r) != nil {
		return nil, ErrInvalidRecord
	}
	return r, nil
}
