package history

import "context"

type transcriptSink interface {
	part(t *transcript, part Excerpt)
	conversation(ctx context.Context, t *transcript)
}
type parser struct {
	sourceMetadata map[string]string
	includeTools   bool
	writer         *indexWriter
	sink           transcriptSink
	selected       func(*transcript) bool
}

type discardTranscript struct{}

func (s *search) parser() *parser {
	return &parser{
		sourceMetadata: s.sourceMetadata,
		includeTools:   s.query.IncludeTools,
		writer:         nil,
		sink:           s,
		selected: func(t *transcript) bool {
			return s.issueConversationSelected(t.match.Conversation, t.qualifies(len(s.terms)))
		},
	}
}

func (s *search) part(t *transcript, part Excerpt) {
	if s.last != nil {
		if part.Role == s.last.role {
			t.keepLast(part)
		}

		return
	}

	s.matchText(t, part.Role, part.Text, part.MessageID, part.Line, part.Timestamp)
}
func (s *search) conversation(ctx context.Context, t *transcript) { s.add(ctx, t) }
func (w *indexWriter) part(_ *transcript, part Excerpt)           { w.append(part) }

func (w *indexWriter) conversation(
	_ context.Context,
	t *transcript,
) {
	w.finish(t.match.Conversation)
}

func (discardTranscript) part(t *transcript, part Excerpt)                {}
func (discardTranscript) conversation(ctx context.Context, t *transcript) {}
