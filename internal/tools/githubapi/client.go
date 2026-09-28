// Package githubapi is a small GitHub REST client for the repository's CI tools.
package githubapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// DefaultBaseURL is the public GitHub REST API root.
const DefaultBaseURL = "https://api.github.com"

const (
	apiVersion       = "2026-03-10"
	requestTimeout   = 30 * time.Second
	maxResponseBytes = 8 << 20
	maxPages         = 50
	maxAttempts      = 4
	defaultRetry     = 2 * time.Second
	maxErrorMessage  = 200
)

var (
	errGitHubAPI = errors.New("github api")
	errTransport = errors.New("request failed")
	nextLinkRE   = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)
)

// StatusError is a GitHub API response with an unsuccessful HTTP status.
type StatusError struct {
	Method  string
	URL     string
	Status  int
	Message string
}

// Client sends authenticated requests to one GitHub REST API root.
type Client struct {
	// RetryDelay is the delay before the first retry of a failed read; later
	// retries double it.
	RetryDelay time.Duration
	http       *http.Client
	baseURL    string
	token      string
}

// New returns a client for baseURL that authenticates with token when set.
func New(baseURL, token string) *Client {
	httpClient := *http.DefaultClient
	httpClient.Timeout = requestTimeout
	return &Client{
		RetryDelay: defaultRetry,
		http:       &httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
	}
}

func (e *StatusError) Error() string {
	text := fmt.Sprintf("GitHub API %s %s: HTTP %d", e.Method, e.URL, e.Status)
	if e.Message != "" {
		text += ": " + e.Message
	}
	return text
}

// HasStatus reports whether err is a GitHub API response with status.
func HasStatus(err error, status int) bool {
	var statusErr *StatusError
	return errors.As(err, &statusErr) && statusErr.Status == status
}

// Get decodes the response of a GET request for path into out.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

// Do sends body as JSON to path and decodes the response into out. A nil body
// sends no content and a nil out discards the response. Only GET requests are
// retried, so a mutation whose response is lost must be reconciled by the caller.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	_, err := c.send(ctx, method, c.baseURL+path, body, out)
	return err
}

// Paginate reads every page of a list endpoint. Key names the array field of
// endpoints that wrap results in an object; an empty key reads a bare array.
func Paginate[T any](ctx context.Context, c *Client, path, key string) ([]T, error) {
	var items []T
	target := c.baseURL + path
	for range maxPages {
		var page json.RawMessage
		next, err := c.send(ctx, http.MethodGet, target, nil, &page)
		if err != nil {
			return nil, err
		}
		pageItems, err := decodePage[T](page, key)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", target, err)
		}
		items = append(items, pageItems...)
		if next == "" {
			return items, nil
		}
		// Never forward the token to a host outside the configured API root.
		if !strings.HasPrefix(next, c.baseURL+"/") {
			return nil, fmt.Errorf("%w: next page %s is outside %s", errGitHubAPI, next, c.baseURL)
		}
		target = next
	}
	return nil, fmt.Errorf("%w: %s has more than %d pages", errGitHubAPI, path, maxPages)
}

func decodePage[T any](page json.RawMessage, key string) ([]T, error) {
	if key != "" {
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(page, &wrapper); err != nil {
			return nil, fmt.Errorf("decode page: %w", err)
		}
		field, ok := wrapper[key]
		if !ok {
			return nil, fmt.Errorf("%w: response has no %q field", errGitHubAPI, key)
		}
		page = field
	}
	var items []T
	if err := json.Unmarshal(page, &items); err != nil {
		return nil, fmt.Errorf("decode items: %w", err)
	}
	return items, nil
}

func (c *Client) send(ctx context.Context, method, target string, body, out any) (string, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return "", fmt.Errorf("encode %s %s: %w", method, target, err)
		}
	}
	attempts := 1
	if method == http.MethodGet {
		attempts = maxAttempts
	}
	delay := c.RetryDelay
	for attempt := 1; ; attempt++ {
		next, err := c.attempt(ctx, method, target, payload, out)
		if err == nil || attempt == attempts || ctx.Err() != nil || !retryable(err) {
			return next, err
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%s %s: %w", method, target, ctx.Err())
		case <-time.After(delay):
		}
		delay *= 2
	}
}

func (c *Client) attempt(ctx context.Context, method, target string, payload []byte, out any) (string, error) {
	req, err := c.newRequest(ctx, method, target, payload)
	if err != nil {
		return "", err
	}
	response, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %s %s: %w", errTransport, method, target, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s %s: %w", method, target, err)
	}
	if len(data) > maxResponseBytes {
		return "", fmt.Errorf("%w: response from %s exceeds size limit", errGitHubAPI, target)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", &StatusError{Method: method, URL: target, Status: response.StatusCode, Message: errorMessage(data)}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return "", fmt.Errorf("decode %s %s: %w", method, target, err)
		}
	}
	return nextLink(response.Header.Get("Link")), nil
}

func (c *Client) newRequest(ctx context.Context, method, target string, payload []byte) (*http.Request, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "aht-ci")
	req.Header.Set("X-Github-Api-Version", apiVersion)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}

func retryable(err error) bool {
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		return errors.Is(err, errTransport)
	}
	return statusErr.Status == http.StatusTooManyRequests || statusErr.Status >= http.StatusInternalServerError
}

func errorMessage(data []byte) string {
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &body) == nil && body.Message != "" {
		return body.Message
	}
	text := strings.TrimSpace(string(data))
	if len(text) > maxErrorMessage {
		text = text[:maxErrorMessage] + "..."
	}
	return text
}

func nextLink(header string) string {
	if match := nextLinkRE.FindStringSubmatch(header); match != nil {
		return match[1]
	}
	return ""
}
