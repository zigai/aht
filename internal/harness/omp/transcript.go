package omp

import (
	"context"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (ompHarness) Transcript() transcript.Reader {
	return transcript.Reader{
		Patterns:          []string{"*.jsonl"},
		Sources:           transcriptSources,
		Record:            transcriptRecord,
		FastRecord:        transcript.FastTreeRecord,
		LocalTitles:       true,
		RewritableHeaders: []string{"title"},
	}
}

func transcriptRecord(
	ctx context.Context,
	decoder *transcript.Decoder,
	record transcript.Record,
	line int,
) {
	if transcript.Str(record, "type") == "title" {
		decoder.Conversation.Title = transcript.Str(record, "title")
		return
	}

	transcript.TreeRecord(ctx, decoder, record, line)
}

func transcriptSources(home string) ([]string, error) {
	return []string{sessionsDirUnder(home)}, nil
}
