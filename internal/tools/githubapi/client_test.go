package githubapi

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := New(server.URL+"/", "fixture-token")
	client.RetryDelay = 0
	return client
}

func TestRequestsAuthenticateAndEncodeJSON(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" || r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("headers = %v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/o/r/issues/5" || string(body) != `{"state":"closed"}` {
			t.Errorf("request = %s %s %s", r.Method, r.URL.Path, body)
		}
		_, _ = fmt.Fprint(w, `{"number":5}`)
	})
	var issue struct {
		Number int `json:"number"`
	}
	if err := client.Do(t.Context(), http.MethodPatch, "/repos/o/r/issues/5", map[string]string{"state": "closed"}, &issue); err != nil || issue.Number != 5 {
		t.Fatalf("Do = %+v, %v", issue, err)
	}
}

func TestStatusErrorsCarryStatusAndMessage(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"message":"Not Found"}`)
	})
	err := client.Get(t.Context(), "/repos/o/r/releases/tags/v1", nil)
	if !HasStatus(err, http.StatusNotFound) || HasStatus(err, http.StatusForbidden) {
		t.Fatalf("error = %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "Not Found") {
		t.Fatalf("message = %q", got)
	}
}

func TestOnlyReadsRetryTransientFailures(t *testing.T) {
	tests := []struct {
		name, method string
		status       int
		wantCalls    int32
	}{
		{"read retries server errors", http.MethodGet, http.StatusBadGateway, maxAttempts},
		{"read retries rate limits", http.MethodGet, http.StatusTooManyRequests, maxAttempts},
		{"read does not retry client errors", http.MethodGet, http.StatusForbidden, 1},
		{"mutation is never retried", http.MethodPost, http.StatusBadGateway, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tt.status)
			})
			if err := client.Do(t.Context(), tt.method, "/x", nil, nil); !HasStatus(err, tt.status) {
				t.Fatalf("error = %v", err)
			}
			if calls.Load() != tt.wantCalls {
				t.Fatalf("calls = %d, want %d", calls.Load(), tt.wantCalls)
			}
		})
	}
}

func TestReadRecoversAfterTransientFailure(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":1}`)
	})
	var out struct {
		ID int `json:"id"`
	}
	if err := client.Get(t.Context(), "/x", &out); err != nil || out.ID != 1 {
		t.Fatalf("Get = %+v, %v", out, err)
	}
}

func TestPaginateFollowsNextLinks(t *testing.T) {
	type item struct {
		ID int `json:"id"`
	}
	tests := []struct {
		name, key string
		pages     []string
	}{
		{"bare arrays", "", []string{`[{"id":1},{"id":2}]`, `[{"id":3}]`}},
		{"wrapped arrays", "jobs", []string{`{"total_count":3,"jobs":[{"id":1},{"id":2}]}`, `{"total_count":3,"jobs":[{"id":3}]}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var serverURL string
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page") == "2" {
					_, _ = fmt.Fprint(w, tt.pages[1])
					return
				}
				w.Header().Set("Link", fmt.Sprintf(`<%s/list?page=2>; rel="next", <%s/list?page=2>; rel="last"`, serverURL, serverURL))
				_, _ = fmt.Fprint(w, tt.pages[0])
			})
			serverURL = client.baseURL
			items, err := Paginate[item](t.Context(), client, "/list?per_page=2", tt.key)
			if err != nil || !reflect.DeepEqual(items, []item{{1}, {2}, {3}}) {
				t.Fatalf("Paginate = %+v, %v", items, err)
			}
		})
	}
}

func TestPaginateRejectsForeignNextLink(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `<https://attacker.invalid/list?page=2>; rel="next"`)
		_, _ = fmt.Fprint(w, `[]`)
	})
	if _, err := Paginate[struct{}](t.Context(), client, "/list", ""); err == nil {
		t.Fatal("followed a next link outside the API root")
	}
}

func TestPaginateRequiresWrappedField(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"message":"unexpected"}`)
	})
	if _, err := Paginate[struct{}](t.Context(), client, "/list", "jobs"); err == nil {
		t.Fatal("accepted a response without the listed field")
	}
}
