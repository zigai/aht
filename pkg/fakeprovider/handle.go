package fakeprovider

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	maxRequestBytes  = 4 << 20
	abandonedMessage = "fake provider request was abandoned by reset or close"
)

var (
	errUnknownEndpoint  = errors.New("unknown model endpoint")
	errMethodNotAllowed = errors.New("method not allowed for model endpoint")
	errInvalidBody      = errors.New("invalid request body")
)

// ServeHTTP serves the fake model API.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.enter() {
		http.Error(w, errServerClosed.Error(), http.StatusServiceUnavailable)
		return
	}
	defer s.handlers.Done()

	if s.serveProbe(w, r) {
		return
	}

	protocol, ok := protocolOf(r.URL.Path)
	if !ok {
		s.reject(
			w,
			http.StatusNotFound,
			fmt.Errorf("%w: %s %s", errUnknownEndpoint, r.Method, r.URL.Path),
		)

		return
	}

	if r.Method != http.MethodPost {
		s.reject(
			w,
			http.StatusMethodNotAllowed,
			fmt.Errorf("%w: %s %s", errMethodNotAllowed, r.Method, r.URL.Path),
		)

		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		s.reject(w, http.StatusBadRequest, fmt.Errorf("%w: %w", errInvalidBody, err))
		return
	}

	s.serveModel(w, r, protocol, body)
}

func (s *Server) serveProbe(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodHead, r.Method == http.MethodGet && path == "/":
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/models"):
		s.answerJSON(
			w,
			map[string]any{
				"object": "list",
				"data":   []any{map[string]any{"id": s.model, "object": "model"}},
			},
		)
	case r.Method == http.MethodGet && strings.Contains(path, "/models/"):
		s.answerJSON(w, map[string]any{"id": s.model, "object": "model"})
	case r.Method == http.MethodPost && path == "/api/show":
		s.answerJSON(
			w,
			map[string]any{"model_info": map[string]any{}, "capabilities": []string{"tools"}},
		)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/messages/count_tokens"):
		s.answerJSON(w, map[string]any{"input_tokens": 1})
	default:
		return false
	}

	return true
}

func (s *Server) answerJSON(w http.ResponseWriter, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		s.reject(w, http.StatusInternalServerError, fmt.Errorf("encode probe response: %w", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Server) serveModel(
	w http.ResponseWriter,
	r *http.Request,
	protocol Protocol,
	body []byte,
) {
	value, err := decodeObject(body)
	if err != nil {
		s.reject(w, http.StatusBadRequest, fmt.Errorf("%w: %w", errInvalidBody, err))
		return
	}

	request := newRequest(protocol, r.Method, r.URL.Path, body, value)
	if s.isSideRequest(request, body, value) {
		s.serveSide(w, request, value)
		return
	}

	s.serveConversation(w, r, request)
}

func (s *Server) serveSide(w http.ResponseWriter, request Request, value map[string]any) {
	text, err := sideText(value)
	if err != nil {
		s.reject(w, http.StatusInternalServerError, err)
		return
	}

	gen, recorded := s.recordSide(request)
	s.answer(w, gen, recorded, textReply(text), nil)
}

func (s *Server) serveConversation(w http.ResponseWriter, r *http.Request, request Request) {
	ctx := r.Context()

	gen, recorded := s.recordConversation(request)
	if s.holdAll && !s.await(ctx, gen, newHold(recorded, StageBeforeReply)) {
		s.setOutcome(gen, recorded, OutcomeCanceled)
		http.Error(w, abandonedMessage, http.StatusServiceUnavailable)

		return
	}

	reply, err := gen.script.Reply(recorded)
	if err != nil {
		s.failRequest(w, gen, recorded, fmt.Errorf("script reply: %w", err))
		return
	}

	if err := validateReply(reply, recorded.Stream); err != nil {
		s.failRequest(w, gen, recorded, err)
		return
	}

	holdMidStream := func() bool {
		return s.await(ctx, gen, newHold(recorded, StageMidStream))
	}
	s.answer(w, gen, recorded, reply, holdMidStream)
}

func (s *Server) answer(
	w http.ResponseWriter,
	gen *session,
	request Request,
	reply Reply,
	holdMidStream func() bool,
) {
	outcome, err := s.writeReply(w, request, reply, holdMidStream)
	if err != nil {
		s.failRequest(w, gen, request, err)
		return
	}

	s.setOutcome(gen, request, outcome)

	if outcome == OutcomeDropped {
		panic(http.ErrAbortHandler)
	}
}

func (s *Server) writeReply(
	w http.ResponseWriter,
	request Request,
	reply Reply,
	holdMidStream func() bool,
) (Outcome, error) {
	if reply.Error != nil {
		body, err := errorBody(request.Protocol, reply.Error.Message)
		if err != nil {
			return OutcomeRejected, err
		}

		_ = writeBody(w, reply.Error.Status, "application/json", body)

		return OutcomeRejected, nil
	}

	if request.Stream {
		events, err := streamEvents(request.Protocol, s.model, reply)
		if err != nil {
			return OutcomeRejected, err
		}

		return writeEvents(w, events, reply, holdMidStream), nil
	}

	if reply.Disconnect {
		return OutcomeDropped, nil
	}

	body, err := completeBody(request.Protocol, s.model, reply)
	if err != nil {
		return OutcomeRejected, err
	}

	if !writeBody(w, http.StatusOK, "application/json", body) {
		return OutcomeCanceled, nil
	}

	return OutcomeCompleted, nil
}

func (s *Server) failRequest(w http.ResponseWriter, gen *session, request Request, err error) {
	s.fail(gen, err)
	s.setOutcome(gen, request, OutcomeRejected)
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func (s *Server) reject(w http.ResponseWriter, status int, err error) {
	s.fail(s.current(), err)
	http.Error(w, err.Error(), status)
}

func writeBody(w http.ResponseWriter, status int, contentType string, body []byte) bool {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, err := w.Write( //nolint:gosec // G705: the body is JSON served as application/json, not HTML.
		body,
	)

	return err == nil
}

func writeEvents(
	w http.ResponseWriter,
	events []sseEvent,
	reply Reply,
	holdMidStream func() bool,
) Outcome {
	w.Header().Set("Content-Type", "text/event-stream")

	for _, event := range events {
		if !writeEvent(w, event) {
			return OutcomeCanceled
		}

		if !event.content {
			continue
		}

		if http.NewResponseController(w).Flush() != nil {
			return OutcomeCanceled
		}

		if reply.Disconnect {
			return OutcomeDropped
		}

		if reply.HoldMidStream && !holdMidStream() {
			return OutcomeCanceled
		}
	}

	return OutcomeCompleted
}
