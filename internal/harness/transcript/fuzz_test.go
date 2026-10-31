package transcript

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
)

func FuzzMessageParts(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`"plain text"`),
		[]byte(`"escaped\ntext"`),
		[]byte(`[{"type":"text","text":"hello"},{"type":"tool_use","name":"read","input":{"path":"/tmp"}}]`),
		[]byte(`[{"type":"toolResponse","toolResult":{"status":"error","error":"denied"}}]`),
		[]byte(`{"content":"wrong shape"}`),
		[]byte(`[{"type":"text","text":`),
		[]byte(`[{"type":"text","text":"old","text":null}]`),
		[]byte(`[{"Type":"text","Text":"case-sensitive"}]`),
		[]byte(`[{"type":"text","text":""},{"type":"tool_result","content":[{"type":"text","text":"nested"}]}]`),
		[]byte(`[null,{"type":"text","text":7},{"type":"image","data":"base64"}]`),
		{'"', 0xff, '"'},
		{'"', 0x1f, '"'},
		[]byte(`"a"b"`),
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
			t.Fatalf(
				"seed=%q: output grew beyond input bound: input=%d output=%d",
				data,
				len(data),
				len(part.Text),
			)
		}
	}

	checkDirectMessageParts(t, data, parts)

	var direct string
	if json.Unmarshal(data, &direct) != nil {
		var blocks []Record
		if json.Unmarshal(data, &blocks) == nil {
			if want := MessageBlockParts(blocks, "assistant", true); !slices.Equal(parts, want) {
				t.Fatalf("seed=%q: message parts = %#v, ordinary reader = %#v", data, parts, want)
			}
		}
	}
}

func checkDirectMessageParts(t *testing.T, data []byte, parts []TextPart) {
	t.Helper()

	var direct string
	if err := json.Unmarshal(data, &direct); err == nil {
		if len(parts) != 1 || parts[0].Role != "assistant" || parts[0].Text != direct {
			t.Fatalf("seed=%q: direct string changed: %#v, want %q", data, parts, direct)
		}
	} else if len(data) >= 2 && data[0] == '"' && data[len(data)-1] == '"' && len(parts) != 0 {
		t.Fatalf("seed=%q: accepted invalid quoted string: %#v", data, parts)
	}
}

func TestMessagePartsQuotedString(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{name: "plain", raw: []byte(`"hello"`)},
		{name: "unicode", raw: []byte(`"Grüße"`)},
		{name: "escaped", raw: []byte(`"line\nnext"`)},
		{name: "invalid UTF-8", raw: []byte{'"', 0xff, '"'}},
		{name: "control", raw: []byte{'"', 0x1f, '"'}},
		{name: "unescaped quote", raw: []byte(`"a"b"`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var decoded string

			err := json.Unmarshal(tc.raw, &decoded)

			var want []TextPart
			if err == nil {
				want = []TextPart{{Role: "assistant", Text: decoded}}
			}

			if got := MessageParts(tc.raw, "assistant", true); !slices.Equal(got, want) {
				t.Fatalf(
					"raw=%q: MessageParts = %#v, slow path = %#v (error: %v)",
					tc.raw,
					got,
					want,
					err,
				)
			}

			if got := Str(Record{"value": tc.raw}, "value"); got != decoded {
				t.Fatalf("raw=%q: Str = %q, slow path = %q (error: %v)", tc.raw, got, decoded, err)
			}
		})
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

		if len(
			entry.Type,
		)+len(
			entry.ID,
		)+len(
			entry.CWD,
		)+len(
			entry.Title,
		)+len(
			entry.Name,
		) > 3*len(
			data,
		) {
			t.Fatalf(
				"seed=%q: metadata grew beyond input bound: input=%d entry=%+v",
				data,
				len(data),
				entry,
			)
		}

		var compact bytes.Buffer
		if err := json.Compact(&compact, data); err != nil {
			t.Fatalf("seed=%q: accepted invalid JSON: %v", data, err)
		}

		again, err := ParseTree(compact.Bytes())
		if err != nil || entry != again {
			t.Fatalf(
				"seed=%q: whitespace changed tree metadata: first=%+v second=%+v err=%v",
				data,
				entry,
				again,
				err,
			)
		}
	})
}
