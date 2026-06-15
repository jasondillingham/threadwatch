// SPDX-License-Identifier: Apache-2.0

package github_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasondillingham/threadwatch/internal/github"
)

// fakeRoutes records, for each request path, the canned response we want
// the test server to return. The same handler also captures If-None-Match
// for the ETag test.
type fakeRoutes struct {
	responses     map[string]fakeResp
	requestETags  map[string]string // last If-None-Match seen for that path
	requestCounts map[string]int
}

type fakeResp struct {
	status int
	body   string
	etag   string
	// If etagMatch is set, the handler returns 304 when the inbound
	// If-None-Match equals it (overriding status/body).
	etagMatch string
	// Override the X-RateLimit-* headers when non-zero.
	rateRemaining int
	rateReset     int64
}

func newServer(t *testing.T, routes *fakeRoutes) *httptest.Server {
	t.Helper()
	if routes.requestETags == nil {
		routes.requestETags = map[string]string{}
	}
	if routes.requestCounts == nil {
		routes.requestCounts = map[string]int{}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path + "?" + r.URL.RawQuery
		if r.URL.RawQuery == "" {
			key = r.URL.Path
		}
		routes.requestETags[key] = r.Header.Get("If-None-Match")
		routes.requestCounts[key]++

		resp, ok := routes.responses[key]
		if !ok {
			t.Errorf("unexpected request to %s", key)
			http.Error(w, "no canned response", http.StatusInternalServerError)
			return
		}

		if resp.etagMatch != "" && r.Header.Get("If-None-Match") == resp.etagMatch {
			w.Header().Set("ETag", resp.etagMatch)
			w.WriteHeader(http.StatusNotModified)
			return
		}

		if resp.etag != "" {
			w.Header().Set("ETag", resp.etag)
		}
		if resp.rateRemaining != 0 || resp.rateReset != 0 {
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(resp.rateRemaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resp.rateReset, 10))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchThread_Issue_HappyPath(t *testing.T) {
	t.Parallel()
	routes := &fakeRoutes{
		responses: map[string]fakeResp{
			"/repos/o/r/issues/1": {
				status: 200,
				body: `{
					"number": 1, "state": "open", "title": "T",
					"html_url": "https://x", "updated_at": "2026-01-02T00:00:00Z"
				}`,
				etag: `"issue-v1"`,
			},
			"/repos/o/r/issues/1/comments?per_page=100": {
				status: 200,
				body: `[{
					"id": 100, "user": {"login": "alice"},
					"body": "hello", "html_url": "https://c",
					"created_at": "2026-01-03T00:00:00Z"
				}]`,
				etag: `"comments-v1"`,
			},
			"/repos/o/r/issues/1/events?per_page=100": {
				status: 200,
				body: `[
					{"id": 1, "actor": {"login": "alice"}, "event": "labeled",
					 "label": {"name": "bug"}, "created_at": "2026-01-04T00:00:00Z"},
					{"id": 2, "actor": {"login": "alice"}, "event": "subscribed",
					 "created_at": "2026-01-05T00:00:00Z"}
				]`,
				etag: `"events-v1"`,
			},
		},
	}
	srv := newServer(t, routes)

	c := github.NewClient("", "threadwatch/test")
	c.BaseURL = srv.URL

	res, err := c.FetchThread(context.Background(), github.ThreadRef{Owner: "o", Repo: "r", Number: 1}, nil)
	if err != nil {
		t.Fatalf("FetchThread: %v", err)
	}

	if res.Snapshot.Kind != "issue" {
		t.Errorf("Kind: got %q, want %q", res.Snapshot.Kind, "issue")
	}
	if res.Snapshot.State != "open" {
		t.Errorf("State: got %q, want %q", res.Snapshot.State, "open")
	}
	if got := len(res.Snapshot.Comments); got != 1 {
		t.Fatalf("Comments len: got %d, want 1", got)
	}
	if res.Snapshot.Comments[0].Author != "alice" || res.Snapshot.Comments[0].Body != "hello" {
		t.Errorf("Comments[0]: got %+v", res.Snapshot.Comments[0])
	}
	if got := len(res.Snapshot.IssueEvents); got != 2 {
		t.Fatalf("IssueEvents len: got %d, want 2 (raw passes through; filter is the diff's job)", got)
	}
	// Reviews / ReviewComments NOT fetched for issues.
	if got := len(res.Snapshot.Reviews); got != 0 {
		t.Errorf("Reviews on issue: got %d, want 0", got)
	}

	wantETags := map[string]string{
		github.EndpointIssue:       `"issue-v1"`,
		github.EndpointComments:    `"comments-v1"`,
		github.EndpointIssueEvents: `"events-v1"`,
	}
	for k, v := range wantETags {
		if res.ETags[k] != v {
			t.Errorf("ETag[%s]: got %q, want %q", k, res.ETags[k], v)
		}
	}
}

func TestFetchThread_PullRequest_FetchesReviewEndpoints(t *testing.T) {
	t.Parallel()
	routes := &fakeRoutes{
		responses: map[string]fakeResp{
			"/repos/o/r/issues/2": {
				status: 200,
				body: `{
					"number": 2, "state": "open", "title": "PR",
					"html_url": "https://x", "updated_at": "2026-01-02T00:00:00Z",
					"pull_request": {"merged_at": null}
				}`,
			},
			"/repos/o/r/issues/2/comments?per_page=100":       {status: 200, body: `[]`},
			"/repos/o/r/issues/2/events?per_page=100":         {status: 200, body: `[]`},
			"/repos/o/r/pulls/2/reviews?per_page=100":         {status: 200, body: `[{"id": 10, "user": {"login": "rev"}, "state": "APPROVED", "body": "lgtm", "html_url": "https://rev", "submitted_at": "2026-01-05T00:00:00Z"}]`},
			"/repos/o/r/pulls/2/comments?per_page=100":        {status: 200, body: `[{"id": 100, "user": {"login": "rev"}, "body": "nit", "html_url": "https://rc", "created_at": "2026-01-06T00:00:00Z"}]`},
		},
	}
	srv := newServer(t, routes)

	c := github.NewClient("tok", "threadwatch/test")
	c.BaseURL = srv.URL

	res, err := c.FetchThread(context.Background(), github.ThreadRef{Owner: "o", Repo: "r", Number: 2}, nil)
	if err != nil {
		t.Fatalf("FetchThread: %v", err)
	}
	if res.Snapshot.Kind != "pr" {
		t.Fatalf("Kind: got %q, want %q", res.Snapshot.Kind, "pr")
	}
	if got := len(res.Snapshot.Reviews); got != 1 {
		t.Errorf("Reviews len: got %d, want 1", got)
	}
	if got := len(res.Snapshot.ReviewComments); got != 1 {
		t.Errorf("ReviewComments len: got %d, want 1", got)
	}

	// PR-specific paths must have been hit.
	for _, p := range []string{
		"/repos/o/r/pulls/2/reviews?per_page=100",
		"/repos/o/r/pulls/2/comments?per_page=100",
	} {
		if routes.requestCounts[p] == 0 {
			t.Errorf("expected request to %s", p)
		}
	}
}

func TestFetchThread_MergedPR_StateDerivedFromMergedAt(t *testing.T) {
	t.Parallel()
	routes := &fakeRoutes{
		responses: map[string]fakeResp{
			"/repos/o/r/issues/3": {
				status: 200,
				body: `{
					"number": 3, "state": "closed", "title": "PR",
					"html_url": "https://x", "updated_at": "2026-01-02T00:00:00Z",
					"pull_request": {"merged_at": "2026-01-02T00:00:00Z"}
				}`,
			},
			"/repos/o/r/issues/3/comments?per_page=100":      {status: 200, body: `[]`},
			"/repos/o/r/issues/3/events?per_page=100":        {status: 200, body: `[]`},
			"/repos/o/r/pulls/3/reviews?per_page=100":        {status: 200, body: `[]`},
			"/repos/o/r/pulls/3/comments?per_page=100":       {status: 200, body: `[]`},
		},
	}
	srv := newServer(t, routes)
	c := github.NewClient("", "threadwatch/test")
	c.BaseURL = srv.URL

	res, err := c.FetchThread(context.Background(), github.ThreadRef{Owner: "o", Repo: "r", Number: 3}, nil)
	if err != nil {
		t.Fatalf("FetchThread: %v", err)
	}
	if res.Snapshot.State != "merged" {
		t.Errorf("State: got %q, want %q", res.Snapshot.State, "merged")
	}
}

func TestFetchThread_NotModified_SendsIfNoneMatch(t *testing.T) {
	t.Parallel()
	routes := &fakeRoutes{
		responses: map[string]fakeResp{
			"/repos/o/r/issues/4": {
				etagMatch: `"issue-v1"`, // returns 304 if the request matches
			},
			"/repos/o/r/issues/4/comments?per_page=100": {
				etagMatch: `"comments-v1"`,
			},
			"/repos/o/r/issues/4/events?per_page=100": {
				etagMatch: `"events-v1"`,
			},
		},
	}
	srv := newServer(t, routes)
	c := github.NewClient("", "threadwatch/test")
	c.BaseURL = srv.URL

	prev := map[string]string{
		github.EndpointIssue:       `"issue-v1"`,
		github.EndpointComments:    `"comments-v1"`,
		github.EndpointIssueEvents: `"events-v1"`,
	}
	res, err := c.FetchThread(context.Background(), github.ThreadRef{Owner: "o", Repo: "r", Number: 4}, prev)
	if err != nil {
		t.Fatalf("FetchThread: %v", err)
	}
	if got := res.EndpointStatuses[github.EndpointIssue]; got != 304 {
		t.Errorf("issue status: got %d, want 304", got)
	}
	if got := res.EndpointStatuses[github.EndpointComments]; got != 304 {
		t.Errorf("comments status: got %d, want 304", got)
	}

	// We should have sent If-None-Match for each.
	for k, v := range routes.requestETags {
		if v == "" {
			t.Errorf("request %s had no If-None-Match", k)
		}
	}

	// On 304, ETags should carry forward from prev (not be cleared).
	if got := res.ETags[github.EndpointIssue]; got != `"issue-v1"` {
		t.Errorf("ETag carry-forward: got %q, want %q", got, `"issue-v1"`)
	}
}

func TestFetchThread_NotFound(t *testing.T) {
	t.Parallel()
	routes := &fakeRoutes{
		responses: map[string]fakeResp{
			"/repos/o/r/issues/9999": {status: 404, body: `{"message":"Not Found"}`},
		},
	}
	srv := newServer(t, routes)
	c := github.NewClient("", "threadwatch/test")
	c.BaseURL = srv.URL

	_, err := c.FetchThread(context.Background(), github.ThreadRef{Owner: "o", Repo: "r", Number: 9999}, nil)
	if !errors.Is(err, github.ErrNotFound) {
		t.Fatalf("err: got %v, want wrap of ErrNotFound", err)
	}
}

func TestFetchThread_RateLimited_OnIssue(t *testing.T) {
	t.Parallel()
	reset := time.Now().Add(10 * time.Minute).Unix()
	routes := &fakeRoutes{
		responses: map[string]fakeResp{
			"/repos/o/r/issues/5": {
				status:        http.StatusForbidden,
				rateRemaining: 0,
				rateReset:     reset,
				body:          `{"message":"API rate limit exceeded"}`,
			},
		},
	}
	srv := newServer(t, routes)
	c := github.NewClient("", "threadwatch/test")
	c.BaseURL = srv.URL

	res, err := c.FetchThread(context.Background(), github.ThreadRef{Owner: "o", Repo: "r", Number: 5}, nil)
	if !errors.Is(err, github.ErrRateLimited) {
		t.Fatalf("err: got %v, want wrap of ErrRateLimited", err)
	}
	// Rate-limit information should still be surfaced.
	if got := res.RateLimit.Reset.Unix(); got != reset {
		t.Errorf("RateLimit.Reset: got %d, want %d", got, reset)
	}
}

func TestFetchThread_Unauthorized_BubblesUp(t *testing.T) {
	t.Parallel()
	routes := &fakeRoutes{
		responses: map[string]fakeResp{
			"/repos/o/r/issues/6": {status: http.StatusUnauthorized, body: `{"message":"Bad credentials"}`},
		},
	}
	srv := newServer(t, routes)
	c := github.NewClient("bad-token", "threadwatch/test")
	c.BaseURL = srv.URL

	_, err := c.FetchThread(context.Background(), github.ThreadRef{Owner: "o", Repo: "r", Number: 6}, nil)
	if !errors.Is(err, github.ErrUnauthorized) {
		t.Fatalf("err: got %v, want wrap of ErrUnauthorized", err)
	}
}

func TestFetchThread_SendsExpectedHeaders(t *testing.T) {
	t.Parallel()
	// Capture the headers seen on the first request synchronously inside the
	// handler. Use a sync.Once to record only the first request's headers.
	var (
		mu       sync.Mutex
		captured http.Header
	)
	routes := &fakeRoutes{
		responses: map[string]fakeResp{
			"/repos/o/r/issues/7": {
				status: 200,
				body:   `{"number": 7, "state": "open", "title": "T", "html_url": "https://x", "updated_at": "2026-01-02T00:00:00Z"}`,
			},
			"/repos/o/r/issues/7/comments?per_page=100": {status: 200, body: `[]`},
			"/repos/o/r/issues/7/events?per_page=100":   {status: 200, body: `[]`},
		},
	}
	srv := newServerWithCapture(t, routes, &mu, &captured)

	c := github.NewClient("super-secret", "threadwatch/test")
	c.BaseURL = srv.URL

	if _, err := c.FetchThread(context.Background(), github.ThreadRef{Owner: "o", Repo: "r", Number: 7}, nil); err != nil {
		t.Fatalf("FetchThread: %v", err)
	}

	mu.Lock()
	got := captured.Clone()
	mu.Unlock()

	if v := got.Get("Accept"); !strings.Contains(v, "application/vnd.github+json") {
		t.Errorf("Accept: got %q", v)
	}
	if v := got.Get("Authorization"); v != "Bearer super-secret" {
		t.Errorf("Authorization: got %q, want %q", v, "Bearer super-secret")
	}
	if v := got.Get("User-Agent"); v != "threadwatch/test" {
		t.Errorf("User-Agent: got %q", v)
	}
	if v := got.Get("X-GitHub-Api-Version"); v != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version: got %q", v)
	}
}

// newServerWithCapture wraps the canned-route handler with a sync-guarded
// header capture so the test goroutine can read what the request looked
// like after FetchThread returns.
func newServerWithCapture(t *testing.T, routes *fakeRoutes, mu *sync.Mutex, dst *http.Header) *httptest.Server {
	t.Helper()
	if routes.requestETags == nil {
		routes.requestETags = map[string]string{}
	}
	if routes.requestCounts == nil {
		routes.requestCounts = map[string]int{}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if *dst == nil {
			*dst = r.Header.Clone()
		}
		mu.Unlock()

		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key = key + "?" + r.URL.RawQuery
		}
		resp, ok := routes.responses[key]
		if !ok {
			http.Error(w, "no canned response for "+key, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}
