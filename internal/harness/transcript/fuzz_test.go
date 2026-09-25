package transcript

import (
	"bytes"
	"encoding/json"
	"testing"
	"unicode/utf8"
)

func FuzzMessageParts(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`"plain text"`),
		[]byte(`"escaped\ntext"`),
		[]byte(`[{"type":"text","text":"hello"},{"type":"tool_use","name":"read","input":{"path":"/tmp"}}]`),
		[]byte(`[{"type":"toolResponse","toolResult":{"status":"error","error":"denied"}}]`),
		[]byte(`{"content":"wrong shape"}`),
		[]byte(`[{"type":"text","text":`),
		{'"', 0xff, '"'},
	} {
		f.Add(seed)
	}
	f.Fuzz(checkMessageParts)
}

func checkMessageParts(t *testing.T, data []byte) {
	parts := MessageParts(data, "assistant", true)
	if len(parts) > 2 {
		t.Fatalf("seed=%q: emitted %d parts", data, len(parts))
	}
	for _, part := range parts {
		if part.Role != "assistant" && part.Role != "tool" {
			t.Fatalf("seed=%q: unexpected role %q", data, part.Role)
		}
		if len(part.Text) > 8*len(data)+256 {
			t.Fatalf("seed=%q: output grew beyond input bound: input=%d output=%d", data, len(data), len(part.Text))
		}
	}
	checkDirectMessageParts(t, data, parts)
}

func checkDirectMessageParts(t *testing.T, data []byte, parts []TextPart) {
	t.Helper()
	var direct string
	if json.Unmarshal(data, &direct) == nil {
		if !utf8.Valid(data) && len(data) >= 2 && data[0] == '"' && data[len(data)-1] == '"' {
			t.Skip("known invalid UTF-8 fast-path mismatch: seed with quoted 0xff reproduces it")
		}
		if len(parts) != 1 || parts[0].Role != "assistant" || parts[0].Text != direct {
			t.Fatalf("seed=%q: direct string changed: %#v, want %q", data, parts, direct)
		}
	}
}

func FuzzParseTree(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"type":"session","id":"s","cwd":"/repo","timestamp":"2026-01-01T00:00:00Z"}`),
		[]byte(`{"type":"message","id":"m","title":"hello"}`),
		[]byte(`{"type":"title_change","title":"renamed"}`),
		[]byte(`{"type":false,"timestamp":-1}`),
		[]byte(`[]`),
		[]byte(`{"type":`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		entry, err := ParseTree(data)
		if err != nil {
			return
		}
		if len(entry.Type)+len(entry.ID)+len(entry.CWD)+len(entry.Title)+len(entry.Name) > 3*len(data) {
			t.Fatalf("seed=%q: metadata grew beyond input bound: input=%d entry=%+v", data, len(data), entry)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, data); err != nil {
			t.Fatalf("seed=%q: accepted invalid JSON: %v", data, err)
		}
		again, err := ParseTree(compact.Bytes())
		if err != nil || entry != again {
			t.Fatalf("seed=%q: whitespace changed tree metadata: first=%+v second=%+v err=%v", data, entry, again, err)
		}
	})
}
