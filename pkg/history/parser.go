package history

import "context"

type transcriptSink interface {
	part(t *transcriptState, part Excerpt)
	conversation(ctx context.Context, t *transcriptState)
}
type parser struct {
	sourceMetadata map[string]string
	includeTools   bool
	writer         *indexWriter
	sink           transcriptSink
	selected       func(*transcriptState) bool
}

type discardTranscript struct{}

func (s *search) parser() *parser {
	return &parser{
		sourceMetadata: s.sourceMetadata,
		includeTools:   s.query.IncludeTools,
		writer:         nil,
		sink:           s,
		selected: func(t *transcriptState) bool {
			return s.issueConversationSelected(t.match.Conversation, t.qualifies(len(s.terms)))
		},
	}
}

func (s *search) part(t *transcriptState, part Excerpt) {
	if s.last != nil {
		if part.Role == s.last.role {
			t.keepLast(part)
		}

		return
	}

	s.matchText(t, part.Role, part.Text, part.MessageID, part.Line, part.Timestamp)
}
func (s *search) conversation(ctx context.Context, t *transcriptState) { s.add(ctx, t) }
func (writer *indexWriter) part(_ *transcriptState, part Excerpt)      { writer.append(part) }

func (writer *indexWriter) conversation(
	_ context.Context,
	t *transcriptState,
) {
	writer.finish(t.match.Conversation)
}

func (discardTranscript) part(t *transcriptState, part Excerpt)                {}
func (discardTranscript) conversation(ctx context.Context, t *transcriptState) {}
